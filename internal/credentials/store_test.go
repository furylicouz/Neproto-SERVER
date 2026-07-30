package credentials

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadActiveDirectoryReturnsStableCredentials(t *testing.T) {
	directory := t.TempDir()
	writeCredential(t, directory, "ABEiM0RVZneImaq7zN3u_w", 0x11, 0o600)
	writeCredential(t, directory, "_-7dzLuqmYh3ZlVEMyIRAA", 0x22, 0o600)

	loaded, err := LoadActiveDirectory(directory)
	if err != nil {
		t.Fatalf("load active credentials: %v", err)
	}
	if len(loaded) != 2 || loaded[0].ID != "ABEiM0RVZneImaq7zN3u_w" ||
		loaded[1].ID != "_-7dzLuqmYh3ZlVEMyIRAA" {
		t.Fatalf("unexpected stable credentials: %#v", loaded)
	}
	if loaded[0].Secret == loaded[1].Secret {
		t.Fatal("distinct credentials collapsed to one key")
	}
}

func TestLoadActiveDirectoryRejectsDuplicateSecrets(t *testing.T) {
	directory := t.TempDir()
	writeCredential(t, directory, "ABEiM0RVZneImaq7zN3u_w", 0x44, 0o600)
	writeCredential(t, directory, "_-7dzLuqmYh3ZlVEMyIRAA", 0x44, 0o600)
	if _, err := LoadActiveDirectory(directory); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate secret error=%v", err)
	}
}

func TestLoadActiveDirectoryRejectsMalformedAndOverPermissiveFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce Unix credential permission bits")
	}
	directory := t.TempDir()
	writeCredential(t, directory, "ABEiM0RVZneImaq7zN3u_w", 0x33, 0o644)
	if _, err := LoadActiveDirectory(directory); err == nil {
		t.Fatal("accepted group/world-readable credential")
	}

	directory = t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "not-an-id.secret"), []byte("bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadActiveDirectory(directory); err == nil {
		t.Fatal("accepted malformed credential filename")
	}
}

func TestLoadActiveDirectoryEnforcesCredentialLimit(t *testing.T) {
	directory := t.TempDir()
	for index := 0; index <= MaxActiveCredentials; index++ {
		rawID := make([]byte, IDSize)
		rawID[0] = byte(index)
		rawID[1] = byte(index >> 8)
		id := base64.RawURLEncoding.EncodeToString(rawID)
		writeCredential(t, directory, id, byte(index+1), 0o600)
	}
	if _, err := LoadActiveDirectory(directory); err == nil {
		t.Fatal("accepted too many active credentials")
	}
}

func writeCredential(t *testing.T, directory, id string, fill byte, mode os.FileMode) {
	t.Helper()
	secret := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat(string([]byte{fill}), 32)))
	if err := os.WriteFile(filepath.Join(directory, id+".secret"), []byte(secret+"\n"), mode); err != nil {
		t.Fatal(err)
	}
}
