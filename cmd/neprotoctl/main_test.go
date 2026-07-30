package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"neproto.local/chameleon/internal/admin"
)

type fakeController struct {
	actions        []string
	snapshot       serviceSnapshot
	validateError  error
	probeError     error
	provisioned    []string
	provisionError error
}

func (controller *fakeController) Action(action string, _, _ io.Writer) error {
	controller.actions = append(controller.actions, action)
	return nil
}

func (controller *fakeController) Logs(bool, io.Writer, io.Writer) error { return nil }

func (controller *fakeController) Snapshot() serviceSnapshot { return controller.snapshot }
func (controller *fakeController) Validate(io.Writer, io.Writer) error {
	return controller.validateError
}
func (controller *fakeController) PublicProbe(admin.Installation) error {
	return controller.probeError
}
func (controller *fakeController) ProvisionCertificate(domain string, _, _ io.Writer) error {
	controller.provisioned = append(controller.provisioned, domain)
	return controller.provisionError
}

func TestDoctorReportsConfigurationServicesAndPublicHTTPS(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active"}}
	var output, errors bytes.Buffer

	code := execute([]string{"doctor"}, root, &output, &errors, controller)
	if code != 0 {
		t.Fatalf("doctor code=%d stderr=%s", code, errors.String())
	}
	for _, expected := range []string{
		"[OK] installation state", "[OK] NP/2 configuration", "[OK] NP/2 service",
		"[OK] Caddy ingress", "[OK] public HTTPS + HTTP/3",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("doctor output missing %q:\n%s", expected, output.String())
		}
	}
}

func TestInteractiveDashboardShowsNodeSummaryAndASCIIWordmark(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active"}}
	var output, errors bytes.Buffer

	code := executeWithInput(
		nil,
		root,
		strings.NewReader("0\n"),
		&output,
		&errors,
		controller,
	)
	if code != 0 {
		t.Fatalf("menu code=%d stderr=%s", code, errors.String())
	}
	for _, expected := range []string{
		"NeProto", "NP/2 CONSTELLATION SERVER CONTROL", "vpn.example.com", "bare-metal",
		"NP/2 service : active", "Caddy ingress : active", "Active users  : 0",
		"CONSTELLATION NETWORK", "SERVER NODE", "SAFE STORAGE",
		"/etc/neproto/server.json", "1. Status and diagnostics", "2. Users and client QR",
		"7. NeProto storage and events", "0. Exit",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("dashboard missing %q:\n%s", expected, output.String())
		}
	}
}

func TestInteractiveStorageBrowserExposesOnlyManagedNeProtoAreas(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active"}}
	var output, errors bytes.Buffer

	code := executeWithInput(nil, root, strings.NewReader("7\n0\n0\n"), &output, &errors, controller)
	if code != 0 {
		t.Fatalf("menu code=%d stderr=%s", code, errors.String())
	}
	for _, expected := range []string{
		"NEPROTO SAFE STORAGE", "[CONFIG]", "[USERS]", "[CERTIFICATES]", "[BACKUPS]", "[EVENTS]",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("storage browser missing %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "/etc/shadow") || strings.Contains(output.String(), "private_https_route") {
		t.Fatalf("storage browser exposed a system secret:\n%s", output.String())
	}
}

func TestInteractiveUserMenuCreatesUserAndRestartsServer(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active"}}
	var output, errors bytes.Buffer
	input := strings.NewReader("2\n2\nAlice iPhone\n2\nn\n\n0\n0\n")

	code := executeWithInput(nil, root, input, &output, &errors, controller)
	if code != 0 {
		t.Fatalf("menu code=%d stderr=%s", code, errors.String())
	}
	if !strings.Contains(output.String(), "Created user: Alice iPhone") {
		t.Fatalf("missing create result:\n%s", output.String())
	}
	if len(controller.actions) != 1 || controller.actions[0] != "restart" {
		t.Fatalf("service actions=%v, want restart", controller.actions)
	}
	manager, err := admin.Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	users, err := manager.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Name != "Alice iPhone" || users[0].Profile != "web" {
		t.Fatalf("users=%+v", users)
	}
}

func TestInteractiveServiceMenuRestartsNode(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active"}}
	var output, errors bytes.Buffer
	input := strings.NewReader("3\n4\n\n0\n0\n")

	code := executeWithInput(nil, root, input, &output, &errors, controller)
	if code != 0 {
		t.Fatalf("menu code=%d stderr=%s", code, errors.String())
	}
	if len(controller.actions) != 1 || controller.actions[0] != "restart" {
		t.Fatalf("service actions=%v, want restart", controller.actions)
	}
	if !strings.Contains(output.String(), "Server services restarted.") {
		t.Fatalf("missing restart confirmation:\n%s", output.String())
	}
}

func TestDomainSetCreatesBackupValidatesRestartsAndProbes(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active"}}
	previousResolver := resolveDomainAddresses
	resolveDomainAddresses = func(domain string) ([]string, error) {
		if domain != "new.example.com" {
			t.Fatalf("resolved domain=%q", domain)
		}
		return []string{"1.1.1.1"}, nil
	}
	t.Cleanup(func() { resolveDomainAddresses = previousResolver })
	var output, errors bytes.Buffer

	code := execute([]string{"domain", "set", "--domain", "new.example.com"}, root, &output, &errors, controller)
	if code != 0 {
		t.Fatalf("domain code=%d stderr=%s", code, errors.String())
	}
	if len(controller.actions) != 1 || controller.actions[0] != "restart" {
		t.Fatalf("actions=%v", controller.actions)
	}
	if len(controller.provisioned) != 1 || controller.provisioned[0] != "new.example.com" {
		t.Fatalf("provisioned domains=%v", controller.provisioned)
	}
	manager, err := admin.Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if manager.Installation().Domain != "new.example.com" {
		t.Fatalf("domain=%q", manager.Installation().Domain)
	}
	if !strings.Contains(output.String(), "Domain changed to new.example.com") ||
		!strings.Contains(output.String(), "Re-export every client profile") {
		t.Fatalf("domain output:\n%s", output.String())
	}
	backups, err := manager.ListBackups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
}

func TestDomainSetReadinessFailureRestoresOldDomainAndCertificate(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	controller := &fakeController{
		snapshot:   serviceSnapshot{NP2: "active", Ingress: "active"},
		probeError: errors.New("HTTP/3 blocked"),
	}
	manager := mustOpenManager(t, root)
	if _, err := manager.AddUser("Rollback admin", "web"); err != nil {
		t.Fatal(err)
	}
	var output, errorOutput bytes.Buffer
	code := performDomainSet(
		manager, controller, "new.example.com", []string{"1.1.1.1"},
		&output, &errorOutput,
	)
	if code == 0 {
		t.Fatal("failed public readiness was accepted")
	}
	manager = mustOpenManager(t, root)
	if manager.Installation().Domain != "vpn.example.com" {
		t.Fatalf("rolled-back domain=%q stderr=%s", manager.Installation().Domain, errorOutput.String())
	}
	wantProvisioned := []string{"new.example.com", "vpn.example.com"}
	if len(controller.provisioned) != len(wantProvisioned) {
		t.Fatalf("provisioned domains=%v", controller.provisioned)
	}
	for index := range wantProvisioned {
		if controller.provisioned[index] != wantProvisioned[index] {
			t.Fatalf("provisioned domains=%v", controller.provisioned)
		}
	}
	if len(controller.actions) != 2 || controller.actions[0] != "restart" || controller.actions[1] != "restart" {
		t.Fatalf("restart actions=%v", controller.actions)
	}
}

func TestFeatureSetCompatibilityBacksUpRestartsAndUpdatesRuntime(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	controller := &fakeController{snapshot: serviceSnapshot{NP2: "active", Ingress: "active"}}
	var output, errorOutput bytes.Buffer
	code := execute(
		[]string{"feature", "set", "--mode", "compatibility"},
		root, &output, &errorOutput, controller,
	)
	if code != 0 {
		t.Fatalf("feature code=%d stderr=%s", code, errorOutput.String())
	}
	manager := mustOpenManager(t, root)
	installation := manager.Installation()
	if installation.EnableConstellation || installation.EnableForwardSecrecy {
		t.Fatalf("compatibility policy not applied: %+v", installation)
	}
	if len(controller.actions) != 1 || controller.actions[0] != "restart" {
		t.Fatalf("feature restart actions=%v", controller.actions)
	}
	if !strings.Contains(output.String(), "Compatibility mode enabled") ||
		!strings.Contains(output.String(), "Re-export client profiles") {
		t.Fatalf("feature output:\n%s", output.String())
	}
}

func TestFeatureSetReadinessFailureRollsBackPolicy(t *testing.T) {
	root := t.TempDir()
	writeFeatureTestInstallation(t, root)
	controller := &fakeController{
		snapshot:   serviceSnapshot{NP2: "active", Ingress: "active"},
		probeError: errors.New("public probe failed"),
	}
	manager := mustOpenManager(t, root)
	if _, err := manager.AddUser("Feature rollback", "web"); err != nil {
		t.Fatal(err)
	}
	var output, errorOutput bytes.Buffer
	if code := performFeatureSet(manager, controller, false, &output, &errorOutput); code == 0 {
		t.Fatal("failed feature readiness was accepted")
	}
	manager = mustOpenManager(t, root)
	installation := manager.Installation()
	if !installation.EnableConstellation || !installation.EnableForwardSecrecy {
		t.Fatalf("feature rollback did not restore production policy: %+v stderr=%s",
			installation, errorOutput.String())
	}
	if len(controller.actions) != 2 {
		t.Fatalf("feature rollback restart actions=%v", controller.actions)
	}
}

func mustOpenManager(t *testing.T, root string) *admin.Manager {
	t.Helper()
	manager, err := admin.Open(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestUserLifecycleCommands(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	controller := &fakeController{}
	var output, errors bytes.Buffer
	if code := execute([]string{"user", "add", "--name", "Alice", "--profile", "web"}, root, &output, &errors, controller); code != 0 {
		t.Fatalf("add code=%d stderr=%s", code, errors.String())
	}
	if len(controller.actions) != 1 || controller.actions[0] != "restart" {
		t.Fatalf("actions=%v", controller.actions)
	}
	fields := strings.Fields(output.String())
	identifier := strings.Trim(fields[len(fields)-1], "()")
	output.Reset()
	if code := execute([]string{"user", "export", "--id", identifier, "--format", "uri"}, root, &output, &errors, controller); code != 0 {
		t.Fatalf("export code=%d stderr=%s", code, errors.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(output.String()), "np2://import/v1/") {
		t.Fatalf("unexpected export: %q", output.String())
	}
	output.Reset()
	errors.Reset()
	if code := execute([]string{"user", "export", "--id", identifier, "--format", "manual"}, root, &output, &errors, controller); code != 0 {
		t.Fatalf("manual export code=%d stderr=%s", code, errors.String())
	}
	for _, expected := range []string{
		"Server: vpn.example.com", "Addresses: 8.8.8.8", "Credential ID: " + identifier,
		"Secret: ", "Profile: web", "HTTPS path: /private_https_route_0123456789",
		"Import URI: np2://import/v1/",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("manual export missing %q:\n%s", expected, output.String())
		}
	}
	output.Reset()
	errors.Reset()
	if code := execute([]string{"user", "delete", "--id", identifier, "--confirm", "DELETE"}, root, &output, &errors, controller); code == 0 {
		t.Fatal("active user was permanently deleted without revocation")
	}
	if !strings.Contains(errors.String(), "user must be revoked") {
		t.Fatalf("active delete error=%q", errors.String())
	}
	output.Reset()
	errors.Reset()
	if code := execute([]string{"user", "revoke", "--id", identifier}, root, &output, &errors, controller); code != 0 {
		t.Fatalf("revoke code=%d stderr=%s", code, errors.String())
	}
	output.Reset()
	errors.Reset()
	if code := execute([]string{"user", "delete", "--id", identifier, "--confirm", "DELETE"}, root, &output, &errors, controller); code != 0 {
		t.Fatalf("delete code=%d stderr=%s", code, errors.String())
	}
	users, err := mustOpenManager(t, root).ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 0 {
		t.Fatalf("users after delete=%+v", users)
	}
	if !strings.Contains(output.String(), "Permanently deleted revoked user "+identifier) {
		t.Fatalf("delete output=%q", output.String())
	}
}

func writeTestInstallation(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, "etc", "neproto")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"version": 1, "mode": "bare-metal", "domain": "vpn.example.com",
		"server_addresses": []string{"8.8.8.8"},
		"https_path":       "/private_https_route_0123456789",
		"webrtc_path":      "/private_webrtc_route_0123456789",
	}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(directory, "installation.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	server := []byte(`{"server_identity":"vpn.example.com","listen":"127.0.0.1:9080"}`)
	if err := os.WriteFile(filepath.Join(directory, "server.json"), server, 0o640); err != nil {
		t.Fatal(err)
	}
	caddyDirectory := filepath.Join(root, "etc", "caddy")
	if err := os.MkdirAll(caddyDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(caddyDirectory, "Caddyfile"),
		[]byte("vpn.example.com {\n\treverse_proxy 127.0.0.1:9080\n}\n"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
}

func writeFeatureTestInstallation(t *testing.T, root string) {
	t.Helper()
	writeTestInstallation(t, root)
	directory := filepath.Join(root, "etc", "neproto")
	state := map[string]any{
		"version": 1, "mode": "bare-metal", "domain": "vpn.example.com",
		"server_addresses":       []string{"8.8.8.8"},
		"https_path":             "/private_https_route_0123456789",
		"webrtc_path":            "/private_webrtc_route_0123456789",
		"http3_path":             "/private_http3_route_01234567890",
		"require_datagrams":      true,
		"enable_constellation":   true,
		"enable_forward_secrecy": true,
	}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(directory, "installation.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	server := []byte(`{"server_identity":"vpn.example.com","listen":"127.0.0.1:9080","enable_constellation":true,"enable_forward_secrecy":true}`)
	if err := os.WriteFile(filepath.Join(directory, "server.json"), server, 0o640); err != nil {
		t.Fatal(err)
	}
}
