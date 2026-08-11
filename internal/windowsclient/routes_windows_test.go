//go:build windows

package windowsclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingPowerShellRunner struct {
	scripts []string
	output  []byte
}

func (r *recordingPowerShellRunner) Run(_ context.Context, script string) ([]byte, error) {
	r.scripts = append(r.scripts, script)
	return append([]byte(nil), r.output...), nil
}

func TestApplyRunsDiscoveryAndOnePowerShellBatch(t *testing.T) {
	runner := &recordingPowerShellRunner{output: []byte(`[{"class":"MSFT_NetRoute","interface_index":7,"next_hop":"192.168.1.1","hardware_interface":true,"status":"Up","interface_alias":"Ethernet"}]`)}
	manager := &WindowsRouteManager{directory: t.TempDir(), runner: runner}

	if err := manager.Apply(context.Background(), "NeProto", 42, []string{"104.171.136.10"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.scripts) != 2 {
		t.Fatalf("PowerShell processes=%d, want one discovery and one apply batch", len(runner.scripts))
	}
	apply := runner.scripts[1]
	for _, required := range []string{"New-NetIPAddress", "New-NetRoute", "0.0.0.0/1", "128.0.0.0/1"} {
		if !strings.Contains(apply, required) {
			t.Fatalf("apply script lacks %q: %s", required, apply)
		}
	}
	if strings.Contains(apply, "Remove-NetRoute") || strings.Contains(apply, "Remove-NetIPAddress") {
		t.Fatalf("clean route plan repeats expensive rollback work during apply: %s", apply)
	}
}

func TestRollbackRunsOneGuardedPowerShellBatch(t *testing.T) {
	plan, err := BuildRoutePlan("NeProto", 42, []EndpointRoute{{Address: "104.171.136.10", InterfaceIndex: 7, NextHop: "192.168.1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingPowerShellRunner{}
	manager := &WindowsRouteManager{directory: t.TempDir(), runner: runner}

	if err := manager.rollbackLocked(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(runner.scripts) != 1 {
		t.Fatalf("PowerShell processes=%d, want 1", len(runner.scripts))
	}
	script := runner.scripts[0]
	for _, required := range []string{"$ProgressPreference='SilentlyContinue'", "$_.Name -eq 'NeProto'", "-NextHop '192.168.1.1'"} {
		if !strings.Contains(script, required) {
			t.Fatalf("rollback script lacks %q: %s", required, script)
		}
	}
}

func TestSanitizePowerShellErrorRemovesProgressCLIXML(t *testing.T) {
	raw := []byte("#< CLIXML\r\n<Objs><Obj S=\"progress\"><MS><PR N=\"Record\"><AV>Loading module</AV></PR></MS></Obj><S S=\"Error\">Route_x000D__x000A_cleanup failed</S></Objs>")

	message := sanitizePowerShellError(raw)

	if strings.Contains(message, "CLIXML") || strings.Contains(message, "Loading module") {
		t.Fatalf("progress leaked into diagnostic: %q", message)
	}
	if !strings.Contains(message, "Route") || !strings.Contains(message, "cleanup failed") {
		t.Fatalf("real error missing: %q", message)
	}
}

func TestRecoverForStartupQuarantinesInvalidJournal(t *testing.T) {
	directory := t.TempDir()
	journal := filepath.Join(directory, routeJournalFileName)
	if err := os.WriteFile(journal, []byte(`{"invalid":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewWindowsRouteManager(directory)
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.RecoverForStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active journal still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, routeJournalFileName+".invalid")); err != nil {
		t.Fatalf("quarantined journal missing: %v", err)
	}
}

func TestNativePowerShellRunnerKeepsDiagnosticsOutOfSuccessfulOutput(t *testing.T) {
	raw, err := (nativePowerShellRunner{}).Run(context.Background(),
		`[Console]::Error.WriteLine('progress');[Console]::Out.Write('[{"class":"MSFT_NetRoute"}]')`)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `[{"class":"MSFT_NetRoute"}]` {
		t.Fatalf("output contains non-JSON diagnostics: %q", raw)
	}
}

func TestDecodeEndpointRouteSelectsRouteFromFindNetRouteResult(t *testing.T) {
	raw := []byte(`[
		{"class":"MSFT_NetIPAddress","interface_index":22,"next_hop":"","hardware_interface":false,"status":"Up","interface_alias":"happ-tun"},
		{"class":"MSFT_NetRoute","interface_index":22,"next_hop":"172.18.0.2","hardware_interface":false,"status":"Up","interface_alias":"happ-tun"},
		{"class":"MSFT_NetRoute","interface_index":6,"next_hop":"10.0.0.42","hardware_interface":true,"status":"Up","interface_alias":"Ethernet0"}
	]`)

	route, err := decodeEndpointRoute(raw, "37.252.23.223")
	if err != nil {
		t.Fatal(err)
	}
	if route.Address != "37.252.23.223" || route.InterfaceIndex != 6 || route.NextHop != "10.0.0.42" {
		t.Fatalf("route=%+v", route)
	}
}

func TestDecodeEndpointRouteRejectsFindNetRouteResultWithoutRoute(t *testing.T) {
	raw := []byte(`[{"class":"MSFT_NetIPAddress","interface_index":22,"next_hop":""}]`)

	if _, err := decodeEndpointRoute(raw, "37.252.23.223"); err == nil {
		t.Fatal("accepted Find-NetRoute output without an MSFT_NetRoute record")
	}
}
