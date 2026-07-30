package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestSecureGeoDataFilesDoesNotMutateProvisionedDirectoryMode(t *testing.T) {
	directory := "/etc/neproto/geodata"
	var chowned, chmodded []string
	err := secureGeoDataFiles(
		directory,
		1234,
		func(path string, _, gid int) error {
			if gid != 1234 {
				t.Fatalf("gid=%d", gid)
			}
			chowned = append(chowned, path)
			return nil
		},
		func(path string, mode fs.FileMode) error {
			if path == directory {
				t.Fatal("runtime must not reset the installer-owned setgid directory mode")
			}
			if mode != 0o640 {
				t.Fatalf("mode=%#o", mode)
			}
			chmodded = append(chmodded, path)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(directory, "geoip.dat"), filepath.Join(directory, "geosite.dat")}
	if strings.Join(chowned, "|") != strings.Join(want, "|") || strings.Join(chmodded, "|") != strings.Join(want, "|") {
		t.Fatalf("chowned=%v chmodded=%v", chowned, chmodded)
	}
}

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
