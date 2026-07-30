package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/admin"
)

func TestStatusStoreRoundTrip(t *testing.T) {
	store := StatusStore{Directory: t.TempDir(), Now: func() time.Time { return time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC) }}
	want := Status{State: "downloading", CurrentVersion: "np2-0.4.0", AvailableVersion: "np2-0.4.1", UpdateAvailable: true, Progress: 15, Message: "Downloading update"}
	if err := store.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := store.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want.Schema = 1
	want.UpdatedAt = "2026-07-30T01:02:03Z"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status:\n got %+v\nwant %+v", got, want)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o007 != 0 {
		t.Fatalf("status is world accessible: %o", info.Mode().Perm())
	}
}

func TestStatusStoreRejectsTrailingJSON(t *testing.T) {
	store := StatusStore{Directory: t.TempDir()}
	payload := `{"schema":1,"state":"idle","current_version":"np2-0.4.0","update_available":false,"progress":0,"message":"ready","updated_at":"2026-07-30T01:02:03Z"}{}`
	if err := os.WriteFile(store.Path(), []byte(payload), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); err == nil {
		t.Fatal("status with trailing JSON was accepted")
	}
}

func TestInstalledTopologyRejectsTrailingJSON(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "etc", "neproto")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	installation := `{"version":1,"mode":"bare-metal","domain":"vpn.example.com","server_addresses":["203.0.113.10"],"https_path":"/111111111111111111111111111111111111111111111111","webrtc_path":"/222222222222222222222222222222222222222222222222","http3_path":"/333333333333333333333333333333333333333333333333","require_datagrams":false,"enable_constellation":true,"enable_forward_secrecy":true,"web_enabled":true,"web_domain":"admin.example.com","web_port":3000,"service_uid":65532,"service_gid":65532}{}`
	if err := os.WriteFile(filepath.Join(directory, "installation.json"), []byte(installation), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine("np2-0.4.0", root, filepath.Join(root, "var", "lib", "neproto", "update"))
	if _, _, err := engine.installedTopology(); err == nil {
		t.Fatal("installation state with trailing JSON was accepted")
	}
}

func TestEngineCheckUsesPinnedRepositoryAndPersistsAvailability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/furylicouz/Neproto-SERVER/releases/latest" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"tag_name":"np2-0.4.1","draft":false,"prerelease":false}`))
	}))
	defer server.Close()

	stateDirectory := t.TempDir()
	engine := NewEngine("np2-0.4.0", t.TempDir(), stateDirectory)
	engine.source = releaseSource{apiURL: server.URL + "/repos/furylicouz/Neproto-SERVER/releases/latest", repositoryURL: server.URL}
	engine.client = server.Client()
	status, err := engine.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !status.UpdateAvailable || status.AvailableVersion != "np2-0.4.1" || status.State != "idle" {
		t.Fatalf("unexpected status: %+v", status)
	}
	stored, err := engine.store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, status) {
		t.Fatalf("stored status differs: %+v", stored)
	}
}

func TestEngineCheckAvailabilityReturnsFinalStatusWithoutWritingState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"tag_name":"np2-0.4.2","draft":false,"prerelease":false}`))
	}))
	defer server.Close()

	stateDirectory := t.TempDir()
	engine := NewEngine("np2-0.4.0", t.TempDir(), stateDirectory)
	engine.source = releaseSource{apiURL: server.URL + "/latest", repositoryURL: server.URL}
	engine.client = server.Client()
	engine.store.Now = func() time.Time { return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC) }

	status, err := engine.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability: %v", err)
	}
	if status.State != "idle" || !status.UpdateAvailable || status.AvailableVersion != "np2-0.4.2" || status.UpdatedAt != "2026-07-30T12:00:00Z" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if _, err := os.Stat(engine.store.Path()); !os.IsNotExist(err) {
		t.Fatalf("read-only availability check wrote status file: %v", err)
	}
}

func TestEngineApplyVerifiesBundleAndRunsInstallerWithInstalledTopology(t *testing.T) {
	const tag = "np2-0.4.1"
	archive := updateArchive(t, tag)
	digest := sha256.Sum256(archive)
	archiveName := ArchiveName(tag)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/furylicouz/Neproto-SERVER/releases/latest":
			_, _ = response.Write([]byte(`{"tag_name":"np2-0.4.1","draft":false,"prerelease":false}`))
		case "/releases/download/" + tag + "/" + archiveName:
			_, _ = response.Write(archive)
		case "/releases/download/" + tag + "/" + archiveName + ".sha256":
			_, _ = response.Write([]byte(hex.EncodeToString(digest[:]) + "  " + archiveName + "\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	stateDirectory := filepath.Join(root, "var", "lib", "neproto", "update")
	if err := os.MkdirAll(filepath.Join(root, "etc", "neproto"), 0o750); err != nil {
		t.Fatal(err)
	}
	installation := admin.Installation{
		Version: 1, Mode: admin.ModeBareMetal, Domain: "vpn.example.com",
		ServerAddresses: []string{"203.0.113.10"},
		HTTPSPath:       "/111111111111111111111111111111111111111111111111",
		WebRTCPath:      "/222222222222222222222222222222222222222222222222",
		HTTP3Path:       "/333333333333333333333333333333333333333333333333",
		WebEnabled:      true, WebDomain: "admin.example.com", WebPort: 3000,
	}
	encoded, _ := json.Marshal(installation)
	if err := os.WriteFile(filepath.Join(root, "etc", "neproto", "installation.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "neproto", "acme-email"), []byte("ops@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var installerPath string
	var installerArguments []string
	engine := NewEngine("np2-0.4.0", root, stateDirectory)
	engine.source = releaseSource{apiURL: server.URL + "/repos/furylicouz/Neproto-SERVER/releases/latest", repositoryURL: server.URL}
	engine.client = server.Client()
	engine.runInstaller = func(_ context.Context, path string, arguments []string) error {
		installerPath = path
		installerArguments = append([]string(nil), arguments...)
		return nil
	}

	status, err := engine.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if status.State != "succeeded" || status.Progress != 100 || status.CurrentVersion != tag || status.UpdateAvailable {
		t.Fatalf("unexpected final status: %+v", status)
	}
	if filepath.Base(installerPath) != "install.sh" {
		t.Fatalf("unexpected installer path: %s", installerPath)
	}
	joined := strings.Join(installerArguments, " ")
	for _, expected := range []string{"--mode bare-metal", "--domain vpn.example.com", "--web-domain admin.example.com", "--email ops@example.com", "--non-interactive"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in %q", expected, joined)
		}
	}
}

func TestEngineApplyRejectsChecksumMismatchBeforeInstaller(t *testing.T) {
	const tag = "np2-0.4.1"
	archive := updateArchive(t, tag)
	archiveName := ArchiveName(tag)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/latest"):
			_, _ = response.Write([]byte(`{"tag_name":"np2-0.4.1","draft":false,"prerelease":false}`))
		case strings.HasSuffix(request.URL.Path, ".sha256"):
			_, _ = response.Write([]byte(strings.Repeat("0", 64) + "  " + archiveName + "\n"))
		default:
			_, _ = response.Write(archive)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	writeMinimalInstallation(t, root)
	called := false
	engine := NewEngine("np2-0.4.0", root, filepath.Join(root, "state"))
	engine.source = releaseSource{apiURL: server.URL + "/latest", repositoryURL: server.URL}
	engine.client = server.Client()
	engine.runInstaller = func(context.Context, string, []string) error { called = true; return nil }
	status, err := engine.Apply(context.Background())
	if err == nil || status.State != "failed" || status.ErrorCode != "verification_failed" {
		t.Fatalf("expected verification failure, status=%+v err=%v", status, err)
	}
	if called {
		t.Fatal("installer ran for a bundle with an invalid checksum")
	}
}

func TestEngineApplyPreservesAdministratorSecretWhenInstallerReplacesIt(t *testing.T) {
	const tag = "np2-0.4.1"
	archive := updateArchive(t, tag)
	archiveName := ArchiveName(tag)
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/latest"):
			_, _ = response.Write([]byte(`{"tag_name":"np2-0.4.1","draft":false,"prerelease":false}`))
		case strings.HasSuffix(request.URL.Path, ".sha256"):
			_, _ = fmt.Fprintf(response, "%s  %s\n", hex.EncodeToString(digest[:]), archiveName)
		default:
			_, _ = response.Write(archive)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	writeMinimalInstallation(t, root)
	secretPath := filepath.Join(root, "etc", "neproto", "web-admin.secret")
	originalSecret := []byte(strings.Repeat("a", 64) + "\n")
	if err := os.WriteFile(secretPath, originalSecret, 0o640); err != nil {
		t.Fatal(err)
	}

	engine := NewEngine("np2-0.4.0", root, filepath.Join(root, "state"))
	engine.source = releaseSource{apiURL: server.URL + "/latest", repositoryURL: server.URL}
	engine.client = server.Client()
	engine.runInstaller = func(context.Context, string, []string) error {
		return os.WriteFile(secretPath, []byte(strings.Repeat("b", 64)+"\n"), 0o640)
	}

	status, err := engine.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if status.State != "succeeded" {
		t.Fatalf("unexpected final status: %+v", status)
	}
	preserved, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preserved, originalSecret) {
		t.Fatal("administrator secret changed during the update")
	}
}

func TestAdminSecretSnapshotRestoresDeletedFile(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "etc", "neproto")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(directory, "web-admin.secret")
	originalSecret := []byte(strings.Repeat("c", 64) + "\n")
	if err := os.WriteFile(secretPath, originalSecret, 0o640); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := captureAdminSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(secretPath); err != nil {
		t.Fatal(err)
	}
	restored, err := snapshot.restoreIfChanged()
	if err != nil {
		t.Fatal(err)
	}
	if !restored {
		t.Fatal("deleted administrator secret was not restored")
	}
	preserved, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(preserved, originalSecret) {
		t.Fatal("restored administrator secret differs from the original")
	}
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != originalInfo.Mode().Perm() {
		t.Fatalf("restored mode = %o, want %o", info.Mode().Perm(), originalInfo.Mode().Perm())
	}
}

func updateArchive(t *testing.T, tag string) []byte {
	t.Helper()
	root := strings.TrimSuffix(ArchiveName(tag), ".tar.gz")
	installer := []byte("#!/bin/sh\nexit 0\n")
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	for _, header := range []tar.Header{
		{Name: root + "/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: root + "/install.sh", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(installer))},
	} {
		header := header
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tw.Write(installer); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeMinimalInstallation(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "etc", "neproto"), 0o750); err != nil {
		t.Fatal(err)
	}
	installation := admin.Installation{
		Version: 1, Mode: admin.ModeBareMetal, Domain: "vpn.example.com", ServerAddresses: []string{"203.0.113.10"},
		HTTPSPath:  "/111111111111111111111111111111111111111111111111",
		WebRTCPath: "/222222222222222222222222222222222222222222222222",
		HTTP3Path:  "/333333333333333333333333333333333333333333333333",
		WebEnabled: true, WebPort: 3000,
	}
	encoded, _ := json.Marshal(installation)
	if err := os.WriteFile(filepath.Join(root, "etc", "neproto", "installation.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}
