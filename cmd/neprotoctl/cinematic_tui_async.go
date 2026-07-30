package main

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/cluster"
)

func progressDialog(title string, started time.Time) *tuiDialog {
	return &tuiDialog{
		kind: tuiDialogProgress, title: title,
		prompt: "Preparing secure background operation", progress: 1, started: started,
	}
}

func startTUIBackgroundOperation(
	model *constellationTUIModel,
	events chan<- tuiBackgroundEvent,
	operation tuiOperation,
	title string,
	work tuiBackgroundWork,
) bool {
	if model == nil || events == nil || work == nil || operation == tuiOperationNone || model.backgroundOperation != tuiOperationNone {
		return false
	}
	model.backgroundOperation = operation
	model.dialog = progressDialog(title, time.Now())
	model.status = "BACKGROUND OPERATION RUNNING"
	go func() {
		var result tuiBackgroundEvent
		defer func() {
			if recover() != nil {
				result = tuiBackgroundEvent{err: errors.New("background operation failed internally")}
			}
			result.operation = operation
			result.done = true
			events <- result
		}()
		report := func(progress int, stage string) {
			update := tuiBackgroundEvent{operation: operation, progress: minInt(maxInt(progress, 1), 99), stage: stage}
			select {
			case events <- update:
			default:
			}
		}
		result = work(report)
	}()
	return true
}

func applyTUIBackgroundEvent(model *constellationTUIModel, event tuiBackgroundEvent) {
	if model == nil || event.operation == tuiOperationNone || event.operation != model.backgroundOperation {
		return
	}
	if !event.done {
		if model.dialog == nil || model.dialog.kind != tuiDialogProgress {
			model.dialog = progressDialog("BACKGROUND OPERATION", time.Now())
		}
		model.dialog.progress = minInt(maxInt(event.progress, 1), 99)
		if event.stage != "" {
			model.dialog.prompt = event.stage
			model.dialog.lines = append(model.dialog.lines, event.stage)
			if len(model.dialog.lines) > 8 {
				model.dialog.lines = append([]string(nil), model.dialog.lines[len(model.dialog.lines)-8:]...)
			}
		}
		model.status = "BACKGROUND // " + boundedDisplay(event.stage, 38)
		return
	}

	var progressLines []string
	if model.dialog != nil && model.dialog.kind == tuiDialogProgress {
		progressLines = append(progressLines, model.dialog.lines...)
	}
	model.backgroundOperation = tuiOperationNone
	model.lastFull = time.Time{}
	if event.clearDraft {
		model.clearClusterDraft()
	}
	if event.operation == tuiOperationClusterDiscoverHostKey && event.err == nil && event.fingerprint != "" {
		model.clusterDraft.fingerprint = event.fingerprint
		model.dialog = &tuiDialog{
			kind: tuiDialogConfirm, title: "VERIFY SSH HOST KEY",
			prompt: "CONFIRM " + event.fingerprint, operation: tuiOperationClusterEnrollConfirm,
		}
		model.status = "VERIFY HOST KEY"
		return
	}

	lines := append(progressLines, event.lines...)
	title := "ACTION COMPLETE"
	if event.operation == tuiOperationClusterEnrollConfirm {
		title = "CLUSTER NODE ENROLMENT"
	}
	if event.operation == tuiOperationClusterDiscoverHostKey {
		title = "SSH HOST KEY DISCOVERY"
	}
	if event.err != nil {
		lines = append(lines, splitTUIOutput(event.err.Error(), 400)...)
		title = "ACTION FAILED // REVIEW OUTPUT"
		model.status = "ACTION FAILED"
	} else {
		model.status = "ACTION COMPLETE"
	}
	if len(lines) == 0 {
		lines = []string{"Operation completed without additional output."}
	}
	model.output = append([]string(nil), lines...)
	model.dialog = infoDialog(title, lines)
}

func drainTUIBackgroundEvents(model *constellationTUIModel, events <-chan tuiBackgroundEvent) bool {
	applied := false
	for {
		select {
		case event := <-events:
			applyTUIBackgroundEvent(model, event)
			applied = true
		default:
			return applied
		}
	}
}

func startConstellationBackgroundOperation(
	manager *admin.Manager,
	controller serviceController,
	model *constellationTUIModel,
	events chan<- tuiBackgroundEvent,
) bool {
	if model == nil || events == nil || model.backgroundOperation != tuiOperationNone || model.pending.operation == tuiOperationNone {
		return false
	}
	operation := model.pending.operation
	switch operation {
	case tuiOperationClusterDiscoverHostKey:
		draft := model.clusterDraft
		draft.password = append([]byte(nil), model.clusterDraft.password...)
		model.pending = tuiPendingOperation{}
		return startTUIBackgroundOperation(model, events, operation, "DISCOVER SSH HOST KEY", func(report func(int, string)) tuiBackgroundEvent {
			defer zeroClusterEnrollmentDraft(&draft)
			report(20, "Connecting to the new server over SSH")
			fingerprint, err := discoverClusterEnrollmentHostKeyForDraft(draft)
			if err != nil {
				return tuiBackgroundEvent{err: err, clearDraft: true}
			}
			report(95, "SSH host key received; waiting for operator verification")
			return tuiBackgroundEvent{fingerprint: fingerprint}
		})
	case tuiOperationClusterEnrollConfirm:
		draft := model.takeClusterDraft()
		model.pending = tuiPendingOperation{}
		return startTUIBackgroundOperation(model, events, operation, "CLUSTER NODE ENROLMENT", func(report func(int, string)) tuiBackgroundEvent {
			var output, failures bytes.Buffer
			err := enrolClusterNodeDraft(manager, controller, draft, &output, &failures, report)
			lines := splitTUIOutput(output.String()+failures.String(), 400)
			return tuiBackgroundEvent{err: err, lines: lines, clearDraft: true}
		})
	case tuiOperationGeoDataUpdate:
		model.pending = tuiPendingOperation{}
		return startTUIBackgroundOperation(model, events, operation, "CLUSTER GEODATA UPDATE", func(report func(int, string)) tuiBackgroundEvent {
			var output, failures bytes.Buffer
			statuses, err := runGeoDataOperation(manager, controller, cluster.GeoDataUpdate, true, report, &output, &failures)
			for _, status := range statuses {
				fmt.Fprintf(&output, "%s  %s  geoip=%s  geosite=%s\n", status.NodeID, status.State, shortHash(status.GeoIPSHA256), shortHash(status.GeoSiteSHA256))
			}
			return tuiBackgroundEvent{err: err, lines: splitTUIOutput(output.String()+failures.String(), 400)}
		})
	default:
		return false
	}
}

func isConstellationBackgroundOperation(operation tuiOperation) bool {
	return operation == tuiOperationClusterDiscoverHostKey || operation == tuiOperationClusterEnrollConfirm || operation == tuiOperationGeoDataUpdate
}
