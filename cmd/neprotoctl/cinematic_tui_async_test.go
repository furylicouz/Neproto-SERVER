package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"neproto.local/chameleon/internal/admin"
)

func TestConstellationTUIBackgroundOperationDoesNotBlockAndShowsProgress(t *testing.T) {
	model := constellationTUIModel{now: time.Now(), view: tuiViewCluster}
	events := make(chan tuiBackgroundEvent, 8)
	release := make(chan struct{})
	started := make(chan struct{})

	before := time.Now()
	if !startTUIBackgroundOperation(
		&model,
		events,
		tuiOperationClusterEnrollConfirm,
		"CLUSTER NODE ENROLMENT",
		func(report func(int, string)) tuiBackgroundEvent {
			close(started)
			report(35, "Uploading signed server bundle")
			<-release
			return tuiBackgroundEvent{
				operation: tuiOperationClusterEnrollConfirm,
				done:      true,
				lines:     []string{"Node edge-fi enrolled."},
			}
		},
	) {
		t.Fatal("background operation did not start")
	}
	if elapsed := time.Since(before); elapsed > 100*time.Millisecond {
		t.Fatalf("starting background operation blocked for %v", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background worker did not start")
	}
	if model.dialog == nil || model.dialog.kind != tuiDialogProgress {
		t.Fatalf("progress dialog not shown: %+v", model.dialog)
	}

	progress := <-events
	applyTUIBackgroundEvent(&model, progress)
	if model.dialog.progress != 35 || model.dialog.prompt != "Uploading signed server bundle" {
		t.Fatalf("progress not applied: %+v", model.dialog)
	}

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(110, 34)
	renderConstellationTUI(screen, &model)
	content := simulationText(screen)
	for _, expected := range []string{"CLUSTER NODE ENROLMENT", "Uploading signed server bundle", "35%", "INSTALLATION CONTINUES"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("progress screen missing %q:\n%s", expected, content)
		}
	}

	dialogBefore := model.dialog
	if quit, invoke := handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), &model); quit || invoke || model.dialog != dialogBefore {
		t.Fatalf("escape interrupted running operation: quit=%t invoke=%t dialog=%+v", quit, invoke, model.dialog)
	}

	close(release)
	completion := <-events
	applyTUIBackgroundEvent(&model, completion)
	if model.backgroundOperation != tuiOperationNone || model.dialog == nil || model.dialog.kind != tuiDialogInfo {
		t.Fatalf("completion not applied: background=%v dialog=%+v", model.backgroundOperation, model.dialog)
	}
	if got := strings.Join(model.dialog.lines, "\n"); !strings.Contains(got, "edge-fi enrolled") {
		t.Fatalf("completion output missing: %q", got)
	}
}

func TestConstellationTUIBackgroundFailureRemainsVisible(t *testing.T) {
	model := constellationTUIModel{now: time.Now(), view: tuiViewCluster}
	model.backgroundOperation = tuiOperationClusterEnrollConfirm
	model.dialog = progressDialog("CLUSTER NODE ENROLMENT", model.now)
	model.dialog.lines = []string{"Connecting to the pinned SSH host", "Checking Linux, root access and systemd"}

	applyTUIBackgroundEvent(&model, tuiBackgroundEvent{
		operation: tuiOperationClusterEnrollConfirm,
		done:      true,
		err:       errors.New("process failed\nremote output:\nERROR: requested paths differ"),
		lines:     []string{"remote installer failed"},
	})

	if model.status != "ACTION FAILED" || model.dialog == nil || model.dialog.kind != tuiDialogInfo {
		t.Fatalf("failure was not retained in TUI: status=%q dialog=%+v", model.status, model.dialog)
	}
	if got := strings.Join(model.dialog.lines, "\n"); !strings.Contains(got, "Checking Linux") || !strings.Contains(got, "remote installer failed") || !strings.Contains(got, "ERROR: requested paths differ") {
		t.Fatalf("failure details missing: %q", got)
	}
	for _, line := range model.dialog.lines {
		if strings.ContainsAny(line, "\r\n") {
			t.Fatalf("failure output was not split into renderable lines: %q", model.dialog.lines)
		}
	}
}

func TestConstellationEnrollmentInvokeReturnsBeforeWorkerCompletes(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	model := constellationTUIModel{
		now: time.Now(), view: tuiViewCluster,
		pending: tuiPendingOperation{operation: tuiOperationClusterEnrollConfirm},
		clusterDraft: clusterEnrollmentDraft{
			host: "203.0.113.50", port: 22, user: "root", password: []byte("temporary"),
			nodeID: "edge-fi", name: "Finland", region: "Helsinki",
			domain: "edge.example.com", addresses: []string{"203.0.113.50"}, fingerprint: "SHA256:test",
		},
	}
	events := make(chan tuiBackgroundEvent, 32)
	before := time.Now()
	quit, err := invokeConstellationTUIAction(manager, &fakeController{}, &model, events)
	if err != nil || quit {
		t.Fatalf("invoke returned quit=%t err=%v", quit, err)
	}
	if elapsed := time.Since(before); elapsed > 100*time.Millisecond {
		t.Fatalf("enrolment invoke blocked the TUI for %v", elapsed)
	}
	if model.backgroundOperation != tuiOperationClusterEnrollConfirm || model.dialog == nil || model.dialog.kind != tuiDialogProgress {
		t.Fatalf("enrolment did not move to background: operation=%v dialog=%+v", model.backgroundOperation, model.dialog)
	}
	// The invocation itself is the latency assertion above. Allow the isolated
	// network worker enough wall time on a loaded CI host to report its expected
	// failure without turning scheduler contention into a false regression.
	deadline := time.After(10 * time.Second)
	for model.backgroundOperation != tuiOperationNone {
		select {
		case event := <-events:
			applyTUIBackgroundEvent(&model, event)
		case <-deadline:
			t.Fatal("background enrolment result did not return")
		}
	}
	if model.dialog == nil || model.dialog.kind != tuiDialogInfo || model.status != "ACTION FAILED" {
		t.Fatalf("background failure was not returned to the panel: status=%q dialog=%+v", model.status, model.dialog)
	}
}

func TestConstellationEnrollmentNeverFallsBackToBlockingExecution(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	model := constellationTUIModel{
		pending:      tuiPendingOperation{operation: tuiOperationClusterEnrollConfirm},
		clusterDraft: clusterEnrollmentDraft{password: []byte("temporary")},
	}
	before := time.Now()
	_, err = invokeConstellationTUIAction(manager, &fakeController{}, &model, nil)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(before); elapsed > 100*time.Millisecond {
		t.Fatalf("missing background queue triggered blocking execution: %v", elapsed)
	}
	if model.dialog == nil || model.dialog.kind != tuiDialogInfo || model.status != "ACTION FAILED" {
		t.Fatalf("missing background queue was not surfaced: status=%q dialog=%+v", model.status, model.dialog)
	}
	if got := strings.Join(model.dialog.lines, "\n"); !strings.Contains(got, "background executor unavailable") {
		t.Fatalf("unexpected unavailable message: %q", got)
	}
}

func TestConstellationBackgroundPanicReturnsFailureInsteadOfFreezingPanel(t *testing.T) {
	model := constellationTUIModel{now: time.Now()}
	events := make(chan tuiBackgroundEvent, 2)
	if !startTUIBackgroundOperation(&model, events, tuiOperationClusterEnrollConfirm, "CLUSTER NODE ENROLMENT", func(func(int, string)) tuiBackgroundEvent {
		panic("synthetic worker panic")
	}) {
		t.Fatal("background worker did not start")
	}
	select {
	case event := <-events:
		applyTUIBackgroundEvent(&model, event)
	case <-time.After(time.Second):
		t.Fatal("worker panic left the panel frozen")
	}
	if model.backgroundOperation != tuiOperationNone || model.status != "ACTION FAILED" || model.dialog == nil {
		t.Fatalf("panic was not converted to visible failure: background=%v status=%q dialog=%+v", model.backgroundOperation, model.status, model.dialog)
	}
	if got := strings.Join(model.dialog.lines, "\n"); !strings.Contains(got, "background operation failed internally") {
		t.Fatalf("panic failure missing: %q", got)
	}
}

type testBackgroundError string

func (err testBackgroundError) Error() string { return string(err) }

const errTestBackgroundFailure = testBackgroundError("synthetic background failure")
