package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestGeoDataScheduleWritesSafeSystemdDropIn(t *testing.T) {
	root := t.TempDir()
	for _, preset := range []string{"daily", "weekly", "monthly", "off"} {
		if err := setGeoDataSchedule(root, preset); err != nil {
			t.Fatalf("set %s: %v", preset, err)
		}
		if got := geoDataSchedule(root); got != preset {
			t.Fatalf("schedule=%q want=%q", got, preset)
		}
		raw, err := os.ReadFile(filepath.Join(root, "etc", "systemd", "system", "neproto-geodata-update.timer.d", "schedule.conf"))
		if err != nil {
			t.Fatal(err)
		}
		if preset == "off" && strings.Count(string(raw), "OnCalendar=") != 1 {
			t.Fatalf("off drop-in=%q", raw)
		}
		if preset != "off" && !strings.Contains(string(raw), "Persistent=true") {
			t.Fatalf("active drop-in=%q", raw)
		}
	}
	if err := setGeoDataSchedule(root, "@reboot; rm -rf /"); err == nil {
		t.Fatal("unsafe schedule was accepted")
	}
}

func TestRoutesWorkspaceQueuesClusterGeoDataUpdate(t *testing.T) {
	model := constellationTUIModel{view: tuiViewRoutes}
	handleConstellationTUIKey(tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone), &model)
	if model.dialog == nil || model.dialog.operation != tuiOperationGeoDataUpdate || model.dialog.kind != tuiDialogConfirm {
		t.Fatalf("update dialog=%+v", model.dialog)
	}
}

func TestGeoDataOrchestrationLockRejectsConcurrentUpdate(t *testing.T) {
	directory := t.TempDir()
	release, err := acquireGeoDataOrchestrationLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := acquireGeoDataOrchestrationLock(directory); err == nil {
		t.Fatal("concurrent cluster update acquired the same lock")
	}
	release()
	secondRelease, err := acquireGeoDataOrchestrationLock(directory)
	if err != nil {
		t.Fatalf("lock was not reusable after release: %v", err)
	}
	secondRelease()
}
