//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"neproto.local/chameleon/internal/windowscandidate"
)

const unifiedTestCommit = "0123456789abcdef0123456789abcdef01234567"

func TestSetupLayoutForUnifiedClient(t *testing.T) {
	layout, err := setupLayoutForMode("unified")
	if err != nil {
		t.Fatal(err)
	}
	if layout.application != `app\neproto_client.exe` {
		t.Fatalf("application=%q", layout.application)
	}
	if layout.service != `service\NeProto.Service.exe` {
		t.Fatalf("service=%q", layout.service)
	}
}

func TestSafePayloadPathAllowsNestedFilesAndRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	path, err := safePayloadPath(root, "app/data/flutter_assets/AssetManifest.bin")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "app", "data", "flutter_assets", "AssetManifest.bin")
	if path != want {
		t.Fatalf("path=%q want %q", path, want)
	}
	for _, name := range []string{"../escape.exe", "/absolute.exe", `C:\escape.exe`, "app/../../escape.exe"} {
		if _, err := safePayloadPath(root, name); err == nil {
			t.Fatalf("unsafe payload path %q was accepted", name)
		}
	}
}

func TestVerifyUnifiedPayloadRequiresVerifiedManifestAndRuntime(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"app/neproto_client.exe",
		"app/flutter_windows.dll",
		"app/data/icudtl.dat",
		"app/data/app.so",
		"app/data/flutter_assets/AssetManifest.bin",
		"service/NeProto.Service.exe",
		"service/wintun.dll",
		"NeProto.Uninstall.exe",
	} {
		writeSetupTestFile(t, root, name, name)
	}
	manifest, err := windowscandidate.Create(root, "np2-0.5.21", unifiedTestCommit)
	if err != nil {
		t.Fatal(err)
	}
	if err := windowscandidate.Write(root, manifest); err != nil {
		t.Fatal(err)
	}
	if err := verifyPayloadForMode(root, "unified", "np2-0.5.21"); err != nil {
		t.Fatalf("verify unified payload: %v", err)
	}

	writeSetupTestFile(t, root, "app/data/app.so", "tampered")
	if err := verifyPayloadForMode(root, "unified", "np2-0.5.21"); err == nil {
		t.Fatal("expected manifest integrity failure")
	}
}

func TestExtractArchiveForUnifiedClientSupportsNestedRuntime(t *testing.T) {
	reader := setupTestZIP(t, map[string]string{
		"app/":                   "",
		"app/neproto_client.exe": "runner",
		"app/data/flutter_assets/AssetManifest.bin": "manifest",
	})
	root := t.TempDir()
	if err := extractArchive(reader, root, "unified"); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "app", "neproto_client.exe")); err != nil || string(raw) != "runner" {
		t.Fatalf("extracted runner=%q err=%v", raw, err)
	}
}

func TestExtractArchiveRejectsTraversalBeforeWriting(t *testing.T) {
	reader := setupTestZIP(t, map[string]string{"../escape.exe": "escape"})
	root := t.TempDir()
	if err := extractArchive(reader, root, "unified"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := os.Stat(filepath.Join(root, "..", "escape.exe")); !os.IsNotExist(err) {
		t.Fatalf("escape file was written: %v", err)
	}
}

func setupTestZIP(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func writeSetupTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
