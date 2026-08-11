package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRootedKeepsRuntimePathsInsideTestRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	got := rooted(root, "/var/lib/neproto/update")
	want := filepath.Join(root, "var", "lib", "neproto", "update")
	if got != want {
		t.Fatalf("rooted = %q, want %q", got, want)
	}
}

func TestAcquireLockRejectsActiveOperationAndRecoversStaleLock(t *testing.T) {
	directory := t.TempDir()
	release, err := acquireLock(directory)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if _, err := acquireLock(directory); err == nil {
		t.Fatal("second updater acquired active lock")
	}
	release()

	lockPath := filepath.Join(directory, "updater.lock")
	if err := os.WriteFile(lockPath, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err = acquireLock(directory)
	if err != nil {
		t.Fatalf("recover stale lock: %v", err)
	}
	release()
}

func TestAcquireLockReportsStableBusyCategory(t *testing.T) {
	directory := t.TempDir()
	release, err := acquireLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := acquireLock(directory); !errors.Is(err, errUpdateBusy) {
		t.Fatalf("second lock error = %v, want errUpdateBusy", err)
	}
}

func TestScheduledCheckIsIdempotentlyConsumedWhileUpdaterIsBusy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NEPROTO_TEST_ROOT", root)
	stateDirectory := rooted(root, defaultStateDirectory)
	inbox := filepath.Join(stateDirectory, "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(inbox, "check")
	if err := os.WriteFile(marker, []byte("check\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := acquireLock(stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := run([]string{"check"}); err != nil {
		t.Fatalf("busy scheduled check: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("check marker was not consumed: %v", err)
	}
}

func TestUpdateServiceSandboxAllowsManagedSetgidDirectories(t *testing.T) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	unit, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "systemd", "neproto-update.service"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unit), "RestrictSUIDSGID=true") {
		t.Fatal("update sandbox blocks the installer's required setgid chmod operations")
	}
}

func TestManagedUpdateDoesNotRepeatExistingSetgidChmod(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	installer, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(installer)
	for _, expected := range []string{
		`ensure_mode 2770 "$etc_neproto/geodata"`,
		`ensure_mode 2750 "$update_dir"`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("managed update repeats a restricted chmod instead of using %s", expected)
		}
	}
	for _, forbidden := range []string{
		`chmod 2770 "$etc_neproto/geodata"`,
		`chmod 2750 "$update_dir"`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("managed update still performs unconditional restricted operation: %s", forbidden)
		}
	}
}

func TestWebStageDropsInheritedSetgidBeforeCopy(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate updater test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	installer, err := os.ReadFile(filepath.Join(repositoryRoot, "deploy", "package", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}

	script := string(installer)
	stageCreated := strings.Index(script, `web_stage=$(mktemp -d "$opt_neproto/.web.XXXXXX")`)
	stageNormalized := strings.Index(script, `chmod 0700 "$web_stage"`)
	stageCopied := strings.Index(script, `cp -R --no-preserve=ownership,mode,timestamps -- "$script_dir/web/." "$web_stage/"`)
	if stageCreated < 0 || stageNormalized < 0 || stageCopied < 0 ||
		stageCreated >= stageNormalized || stageNormalized >= stageCopied {
		t.Fatal("web staging must remove inherited SGID before recursive payload copy")
	}
}
