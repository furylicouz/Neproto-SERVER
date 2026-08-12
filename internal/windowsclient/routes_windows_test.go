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

type failingApplyPowerShellRunner struct {
	calls              int
	cancel             context.CancelFunc
	rollbackErr        error
	rollbackContextErr error
}

func (r *failingApplyPowerShellRunner) Run(ctx context.Context, _ string) ([]byte, error) {
	r.calls++
	switch r.calls {
	case 1:
		return []byte(`[{"class":"MSFT_NetRoute","interface_index":6,"next_hop":"10.0.0.1","hardware_interface":true,"status":"Up","interface_alias":"Ethernet"}]`), nil
	case 2:
		if r.cancel != nil {
			r.cancel()
		}
		return nil, errors.New("apply failed")
	default:
		r.rollbackContextErr = ctx.Err()
		if r.rollbackContextErr != nil {
			return nil, r.rollbackContextErr
		}
		return nil, r.rollbackErr
	}
}

func TestApplyRunsDiscoveryAndOnePowerShellBatch(t *testing.T) {
	runner := &recordingPowerShellRunner{output: []byte(`[{"class":"MSFT_NetRoute","interface_index":7,"next_hop":"192.168.1.1","hardware_interface":true,"status":"Up","interface_alias":"Ethernet"}]`)}
	manager := &WindowsRouteManager{directory: t.TempDir(), runner: runner}

	if err := manager.Apply(context.Background(), "NeProto", 42, []string{"104.171.136.10"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.scripts) != 3 {
		t.Fatalf("PowerShell processes=%d, want discovery, endpoint preparation, and tunnel activation", len(runner.scripts))
	}
	apply := runner.scripts[1] + runner.scripts[2]
	for _, required := range []string{"New-NetIPAddress", "New-NetRoute", "0.0.0.0/1", "128.0.0.0/1"} {
		if !strings.Contains(apply, required) {
			t.Fatalf("apply script lacks %q: %s", required, apply)
		}
	}
	if strings.Contains(apply, "Remove-NetRoute") || strings.Contains(apply, "Remove-NetIPAddress") {
		t.Fatalf("clean route plan repeats expensive rollback work during apply: %s", apply)
	}
}

func TestPrepareEndpointsRunsBeforeTunnelActivation(t *testing.T) {
	runner := &recordingPowerShellRunner{output: []byte(`[{"class":"MSFT_NetRoute","interface_index":6,"next_hop":"10.0.0.1","hardware_interface":true,"status":"Up","interface_alias":"Ethernet"}]`)}
	manager := &WindowsRouteManager{directory: t.TempDir(), runner: runner}

	if err := manager.PrepareEndpoints(context.Background(), []string{"37.252.23.223"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.scripts) != 2 {
		t.Fatalf("PowerShell processes=%d, want discovery and endpoint apply", len(runner.scripts))
	}
	prepared := runner.scripts[1]
	if !strings.Contains(prepared, "37.252.23.223/32") || strings.Contains(prepared, "0.0.0.0/1") || strings.Contains(prepared, "New-NetIPAddress") {
		t.Fatalf("unsafe endpoint preparation: %s", prepared)
	}

	if err := manager.ActivateTunnel(context.Background(), "NeProto", 42); err != nil {
		t.Fatal(err)
	}
	if len(runner.scripts) != 3 {
		t.Fatalf("PowerShell processes=%d, want one tunnel activation", len(runner.scripts))
	}
	activated := runner.scripts[2]
	for _, required := range []string{"New-NetIPAddress", "0.0.0.0/1", "128.0.0.0/1"} {
		if !strings.Contains(activated, required) {
			t.Fatalf("tunnel activation lacks %q: %s", required, activated)
		}
	}
}

func TestPrepareEndpointsRollsBackWithFreshContextAfterCallerCancellation(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	runner := &failingApplyPowerShellRunner{cancel: cancel}
	manager := &WindowsRouteManager{directory: directory, runner: runner}

	if err := manager.PrepareEndpoints(ctx, []string{"37.252.23.223"}); err == nil {
		t.Fatal("expected endpoint apply failure")
	}
	if runner.calls != 3 {
		t.Fatalf("PowerShell processes=%d, want discovery, failed apply, and rollback", runner.calls)
	}
	if runner.rollbackContextErr != nil {
		t.Fatalf("rollback inherited canceled caller context: %v", runner.rollbackContextErr)
	}
	if _, err := os.Stat(filepath.Join(directory, routeJournalFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains after successful rollback: %v", err)
	}
}

func TestPrepareEndpointsRetainsJournalWhenRollbackFails(t *testing.T) {
	directory := t.TempDir()
	runner := &failingApplyPowerShellRunner{rollbackErr: errors.New("rollback failed")}
	manager := &WindowsRouteManager{directory: directory, runner: runner}

	if err := manager.PrepareEndpoints(context.Background(), []string{"37.252.23.223"}); err == nil {
		t.Fatal("expected endpoint apply failure")
	}
	if _, err := os.Stat(filepath.Join(directory, routeJournalFileName)); err != nil {
		t.Fatalf("recovery journal missing after failed rollback: %v", err)
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
