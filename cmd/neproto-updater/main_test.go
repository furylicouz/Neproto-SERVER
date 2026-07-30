package main

import (
	"errors"
	"os"
	"path/filepath"
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
