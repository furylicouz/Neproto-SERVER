package windowscandidate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func TestCreateAndVerifyCandidateManifest(t *testing.T) {
	root := t.TempDir()
	writeCandidateFile(t, root, "app/NeProto.Candidate.exe", "flutter runner")
	writeCandidateFile(t, root, "service/NeProto.Service.exe", "strict service")

	manifest, err := Create(root, "np2-0.5.19", testCommit)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if manifest.Platform != PlatformWindowsX64 || manifest.CarrierPolicy != CarrierPolicyHTTP3Only {
		t.Fatalf("unexpected manifest policy: %+v", manifest)
	}
	if len(manifest.Files) != 2 || manifest.Files[0].Path != "app/NeProto.Candidate.exe" {
		t.Fatalf("unexpected files: %+v", manifest.Files)
	}
	if err := Write(root, manifest); err != nil {
		t.Fatalf("Write: %v", err)
	}

	verified, err := LoadAndVerify(root)
	if err != nil {
		t.Fatalf("LoadAndVerify: %v", err)
	}
	if verified.Version != "np2-0.5.19" || verified.Commit != testCommit {
		t.Fatalf("unexpected verified identity: %+v", verified)
	}
}

func TestVerifyRejectsTamperedCandidateFile(t *testing.T) {
	root := validCandidate(t)
	writeCandidateFile(t, root, "app/NeProto.Candidate.exe", "tampered bytes")

	_, err := LoadAndVerify(root)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("expected digest error, got %v", err)
	}
}

func TestVerifyRejectsUnexpectedCandidateFile(t *testing.T) {
	root := validCandidate(t)
	writeCandidateFile(t, root, "unexpected.dll", "not listed")

	_, err := LoadAndVerify(root)
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("expected unexpected-file error, got %v", err)
	}
}

func TestVerifyRejectsTraversalPath(t *testing.T) {
	root := validCandidate(t)
	manifestPath := filepath.Join(root, ManifestName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "app/NeProto.Candidate.exe", "../escape.exe", 1))
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = LoadAndVerify(root)
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("expected path error, got %v", err)
	}
}

func TestVerifyRejectsWindowsDrivePathOnEveryBuildHost(t *testing.T) {
	root := validCandidate(t)
	manifestPath := filepath.Join(root, ManifestName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "app/NeProto.Candidate.exe", "C:/escape.exe", 1))
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = LoadAndVerify(root)
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("expected Windows drive path error, got %v", err)
	}
}

func TestCreateRejectsInvalidCandidateIdentity(t *testing.T) {
	root := t.TempDir()
	writeCandidateFile(t, root, "app/NeProto.Candidate.exe", "runner")

	if _, err := Create(root, "0.5.19", testCommit); err == nil {
		t.Fatal("expected invalid version error")
	}
	if _, err := Create(root, "np2-0.5.19", "not-a-commit"); err == nil {
		t.Fatal("expected invalid commit error")
	}
}

func validCandidate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeCandidateFile(t, root, "app/NeProto.Candidate.exe", "flutter runner")
	writeCandidateFile(t, root, "service/NeProto.Service.exe", "strict service")
	manifest, err := Create(root, "np2-0.5.19", testCommit)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(root, manifest); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeCandidateFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
