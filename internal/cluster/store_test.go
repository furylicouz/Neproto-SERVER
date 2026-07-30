package cluster

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStoreCreatesStableModeProtectedSigningKey(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := store.LoadOrCreateSigningKey(bytes.NewReader(bytes.Repeat([]byte{0x42}, ed25519.SeedSize)))
	if err != nil {
		t.Fatalf("LoadOrCreateSigningKey() error = %v", err)
	}
	loadedPublic, loadedPrivate, err := store.LoadOrCreateSigningKey(nil)
	if err != nil {
		t.Fatalf("LoadOrCreateSigningKey(second) error = %v", err)
	}
	if !bytes.Equal(publicKey, loadedPublic) || !bytes.Equal(privateKey, loadedPrivate) {
		t.Fatal("signing key changed across loads")
	}
	info, err := os.Stat(filepath.Join(root, signingKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("signing key mode = %o", info.Mode().Perm())
	}
}

func TestStoreSaveUsesRevisionCASAndKeepsLastGoodSnapshot(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	initial := testState(now)
	if err := store.Initialize(initial); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	next := initial
	next.Revision = 2
	next.UpdatedAt = now.Add(time.Minute)
	if err := store.Save(1, next); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Save(1, next); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Save(stale) error = %v", err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Revision != 2 {
		t.Fatalf("Load() state = %+v, error = %v", loaded, err)
	}

	info, err := os.Stat(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("cluster state mode = %o", info.Mode().Perm())
	}
	lastGood, err := store.LoadLastGood()
	if err != nil || lastGood.Revision != 1 {
		t.Fatalf("LoadLastGood() state = %+v, error = %v", lastGood, err)
	}
}

func TestOpenStorePreservesAdministratorAssignedServiceMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission mode test")
	}
	root := filepath.Join(t.TempDir(), "cluster")
	if _, err := OpenStore(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("service traversal mode was reset: mode=%v err=%v", info.Mode().Perm(), err)
	}
}
