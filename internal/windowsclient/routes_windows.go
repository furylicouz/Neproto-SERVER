//go:build windows

package windowsclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

const (
	routeJournalFileName       = "active-routes.json"
	failedApplyRollbackTimeout = 10 * time.Second
)

const powerShellPreamble = "$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue';"

type powerShellRunner interface {
	Run(context.Context, string) ([]byte, error)
}

type nativePowerShellRunner struct{}

func (nativePowerShellRunner) Run(ctx context.Context, script string) ([]byte, error) {
	encoded := encodePowerShell(script)
	command := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	command.Env = append(os.Environ(), "POWERSHELL_TELEMETRY_OPTOUT=1")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Windows network command timed out: %w", ctx.Err())
		}
		message := sanitizePowerShellError(stderr.Bytes())
		if message == "" {
			message = sanitizePowerShellError(stdout.Bytes())
		}
		if message == "" {
			message = "PowerShell command failed"
		}
		if len(message) > 512 {
			message = message[:512]
		}
		return nil, fmt.Errorf("Windows network command failed: %s", message)
	}
	return bytes.TrimSpace(stdout.Bytes()), nil
}

func sanitizePowerShellError(raw []byte) string {
	message := strings.TrimSpace(string(raw))
	if message == "" || !strings.Contains(message, "#< CLIXML") {
		return message
	}
	start := strings.Index(message, "<Objs")
	if start < 0 {
		return "PowerShell command failed"
	}
	decoder := xml.NewDecoder(strings.NewReader(message[start:]))
	var errorsFound []string
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		element, ok := token.(xml.StartElement)
		if !ok || element.Name.Local != "S" {
			continue
		}
		isError := false
		for _, attribute := range element.Attr {
			if attribute.Name.Local == "S" && attribute.Value == "Error" {
				isError = true
				break
			}
		}
		if !isError {
			continue
		}
		var value string
		if err := decoder.DecodeElement(&value, &element); err == nil {
			value = strings.ReplaceAll(value, "_x000D__x000A_", "\n")
			value = strings.ReplaceAll(value, "_x000A_", "\n")
			if value = strings.TrimSpace(value); value != "" {
				errorsFound = append(errorsFound, value)
			}
		}
	}
	if len(errorsFound) == 0 {
		return "PowerShell command failed"
	}
	return strings.Join(errorsFound, "; ")
}

func encodePowerShell(script string) string {
	encoded := utf16.Encode([]rune(script))
	raw := make([]byte, len(encoded)*2)
	for index, value := range encoded {
		binary.LittleEndian.PutUint16(raw[index*2:], value)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

type WindowsRouteManager struct {
	mu        sync.Mutex
	directory string
	runner    powerShellRunner
	active    *RoutePlan
}

func NewWindowsRouteManager(directory string) (*WindowsRouteManager, error) {
	if directory == "" || !filepath.IsAbs(directory) {
		return nil, ErrInvalidRoutePlan
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	return &WindowsRouteManager{directory: directory, runner: nativePowerShellRunner{}}, nil
}

func (m *WindowsRouteManager) Recover(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, err := os.ReadFile(filepath.Join(m.directory, routeJournalFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(raw) == 0 || len(raw) > MaxIPCMessageBytes {
		return ErrInvalidRoutePlan
	}
	var plan RoutePlan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || len(plan.Rollback) == 0 {
		return ErrInvalidRoutePlan
	}
	err = m.rollbackLocked(ctx, plan)
	if err == nil {
		_ = os.Remove(filepath.Join(m.directory, routeJournalFileName))
		m.active = nil
	}
	return err
}

// RecoverForStartup quarantines only structurally invalid journals. A journal
// with valid rollback commands is retained when execution fails so a later
// connect attempt can retry the cleanup instead of overwriting it.
func (m *WindowsRouteManager) RecoverForStartup(ctx context.Context) error {
	err := m.Recover(ctx)
	if !errors.Is(err, ErrInvalidRoutePlan) {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	journal := filepath.Join(m.directory, routeJournalFileName)
	quarantine := journal + ".invalid"
	_ = os.Remove(quarantine)
	if renameErr := os.Rename(journal, quarantine); renameErr != nil {
		return renameErr
	}
	m.active = nil
	return nil
}

func (m *WindowsRouteManager) Apply(ctx context.Context, adapterName string, adapterIndex int, addresses []string) error {
	if err := m.PrepareEndpoints(ctx, addresses); err != nil {
		return err
	}
	if err := m.ActivateTunnel(ctx, adapterName, adapterIndex); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), failedApplyRollbackTimeout)
		cleanupErr := m.Cleanup(cleanupContext)
		cancel()
		return errors.Join(err, cleanupErr)
	}
	return nil
}

// PrepareEndpoints installs only the physical-uplink host routes needed for
// the carrier handshake. It deliberately runs before Wintun exists.
func (m *WindowsRouteManager) PrepareEndpoints(ctx context.Context, addresses []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil {
		return ErrControllerBusy
	}
	endpoints := make([]EndpointRoute, 0, len(addresses))
	for _, address := range addresses {
		route, err := m.discoverLocked(ctx, address)
		if err != nil {
			return err
		}
		endpoints = append(endpoints, route)
	}
	plan, err := BuildEndpointRoutePlan(endpoints)
	if err != nil {
		return err
	}
	if err := m.writeJournalLocked(plan); err != nil {
		return err
	}
	script, err := applyPlanScript(plan)
	if err != nil {
		_ = os.Remove(filepath.Join(m.directory, routeJournalFileName))
		return err
	}
	if _, err := m.runner.Run(ctx, script); err != nil {
		return m.rollbackFailedApplyLocked(plan, err)
	}
	m.active = &plan
	return nil
}

// ActivateTunnel extends the prepared endpoint transaction with adapter and
// default routes after the NP/2 carrier has authenticated.
func (m *WindowsRouteManager) ActivateTunnel(ctx context.Context, adapterName string, adapterIndex int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || len(m.active.Apply) == 0 {
		return ErrInvalidRoutePlan
	}
	for _, command := range m.active.Apply {
		if command.Kind != RouteCommandEndpointExclusion {
			return ErrControllerBusy
		}
	}
	tunnel, err := buildTunnelRoutePlan(adapterName, adapterIndex)
	if err != nil {
		return err
	}
	combined := combineRoutePlans(*m.active, tunnel)
	// Journal the complete rollback before applying tunnel side effects. The
	// rollback commands are idempotent if a crash happens before all apply
	// commands finish.
	if err := m.writeJournalLocked(combined); err != nil {
		return err
	}
	script, err := applyPlanScript(tunnel)
	if err != nil {
		return err
	}
	if _, err := m.runner.Run(ctx, script); err != nil {
		return m.rollbackFailedApplyLocked(combined, err)
	}
	m.active = &combined
	return nil
}

func (m *WindowsRouteManager) rollbackFailedApplyLocked(plan RoutePlan, applyErr error) error {
	rollbackContext, cancel := context.WithTimeout(context.Background(), failedApplyRollbackTimeout)
	rollbackErr := m.rollbackLocked(rollbackContext, plan)
	cancel()
	if rollbackErr != nil {
		// Keep both the in-memory plan and the durable journal so the backend or
		// startup recovery can retry after a partial PowerShell transaction.
		m.active = &plan
		return errors.Join(applyErr, fmt.Errorf("rollback Windows route plan: %w", rollbackErr))
	}
	m.active = nil
	journal := filepath.Join(m.directory, routeJournalFileName)
	if err := os.Remove(journal); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(applyErr, fmt.Errorf("remove Windows route journal: %w", err))
	}
	return applyErr
}

func (m *WindowsRouteManager) Cleanup(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		raw, err := os.ReadFile(filepath.Join(m.directory, routeJournalFileName))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		var plan RoutePlan
		if err := json.Unmarshal(raw, &plan); err != nil {
			return ErrInvalidRoutePlan
		}
		m.active = &plan
	}
	err := m.rollbackLocked(ctx, *m.active)
	if err == nil {
		m.active = nil
		_ = os.Remove(filepath.Join(m.directory, routeJournalFileName))
	}
	return err
}

func (m *WindowsRouteManager) discoverLocked(ctx context.Context, address string) (EndpointRoute, error) {
	script := powerShellPreamble + "$address=[System.Net.IPAddress]::Parse('" + quotePowerShell(address) +
		"');$family=if($address.AddressFamily -eq [System.Net.Sockets.AddressFamily]::InterNetwork){'IPv4'}else{'IPv6'};" +
		"$prefix=if($family -eq 'IPv4'){'0.0.0.0/0'}else{'::/0'};" +
		"$items=@(Get-NetRoute -AddressFamily $family -DestinationPrefix $prefix -PolicyStore ActiveStore -ErrorAction Stop);" +
		"$records=@($items|ForEach-Object{$route=$_;$adapter=@(Get-NetAdapter -IncludeHidden -InterfaceIndex $route.InterfaceIndex -ErrorAction SilentlyContinue);" +
		"$ipif=@(Get-NetIPInterface -AddressFamily $family -InterfaceIndex $route.InterfaceIndex -ErrorAction SilentlyContinue);" +
		"[pscustomobject]@{class=[string]$route.CimClass.CimClassName;interface_index=[int]$route.InterfaceIndex;" +
		"next_hop=[string]$route.NextHop;hardware_interface=[bool]($adapter.Count -gt 0 -and $adapter[0].HardwareInterface);" +
		"status=if($adapter.Count -gt 0){[string]$adapter[0].Status}else{''};" +
		"interface_alias=if($adapter.Count -gt 0){[string]$adapter[0].Name}else{''};" +
		"route_metric=[int]$route.RouteMetric;interface_metric=if($ipif.Count -gt 0){[int]$ipif[0].InterfaceMetric}else{[int]::MaxValue}}});" +
		"ConvertTo-Json -InputObject $records -Compress"
	raw, err := m.runner.Run(ctx, script)
	if err != nil {
		return EndpointRoute{}, err
	}
	return decodeEndpointRoute(raw, address)
}

type findNetRouteRecord struct {
	Class             string `json:"class"`
	InterfaceIndex    int    `json:"interface_index"`
	NextHop           string `json:"next_hop"`
	HardwareInterface bool   `json:"hardware_interface"`
	Status            string `json:"status"`
	InterfaceAlias    string `json:"interface_alias"`
	RouteMetric       int    `json:"route_metric"`
	InterfaceMetric   int    `json:"interface_metric"`
}

func decodeEndpointRoute(raw []byte, address string) (EndpointRoute, error) {
	if len(raw) == 0 || len(raw) > MaxIPCMessageBytes {
		return EndpointRoute{}, ErrInvalidRoutePlan
	}
	var records []findNetRouteRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil || len(records) == 0 || len(records) > 32 {
		return EndpointRoute{}, ErrInvalidRoutePlan
	}
	bestCost := int(^uint(0) >> 1)
	var selected EndpointRoute
	for _, record := range records {
		if record.Class != "MSFT_NetRoute" || !record.HardwareInterface ||
			!strings.EqualFold(record.Status, "Up") ||
			strings.EqualFold(strings.TrimSpace(record.InterfaceAlias), windowsAdapterName) {
			continue
		}
		if record.InterfaceIndex <= 0 || strings.TrimSpace(record.NextHop) == "" {
			return EndpointRoute{}, ErrInvalidRoutePlan
		}
		cost := record.RouteMetric + record.InterfaceMetric
		if cost < 0 || cost >= bestCost {
			continue
		}
		bestCost = cost
		selected = EndpointRoute{Address: address, InterfaceIndex: record.InterfaceIndex, NextHop: record.NextHop}
	}
	if selected.InterfaceIndex <= 0 {
		return EndpointRoute{}, ErrInvalidRoutePlan
	}
	return selected, nil
}

func (m *WindowsRouteManager) executeLocked(ctx context.Context, command RouteCommand) error {
	script, err := routeCommandScript(command)
	if err != nil {
		return err
	}
	_, err = m.runner.Run(ctx, script)
	return err
}

func (m *WindowsRouteManager) rollbackLocked(ctx context.Context, plan RoutePlan) error {
	script, err := rollbackPlanScript(plan)
	if err != nil {
		return err
	}
	_, err = m.runner.Run(ctx, script)
	return err
}

func (m *WindowsRouteManager) writeJournalLocked(plan RoutePlan) error {
	raw, err := json.Marshal(plan)
	if err != nil || len(raw) > MaxIPCMessageBytes {
		return ErrInvalidRoutePlan
	}
	temporary, err := os.CreateTemp(m.directory, ".routes-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFile(name, filepath.Join(m.directory, routeJournalFileName))
}

func routeCommandScript(command RouteCommand) (string, error) {
	body, err := routeCommandBody(command)
	if err != nil {
		return "", err
	}
	return powerShellPreamble + body, nil
}

func rollbackPlanScript(plan RoutePlan) (string, error) {
	if len(plan.Rollback) == 0 || len(plan.Rollback) > 64 {
		return "", ErrInvalidRoutePlan
	}
	var script strings.Builder
	script.WriteString(powerShellPreamble)
	for _, command := range plan.Rollback {
		body, err := routeCommandBody(command)
		if err != nil || command.Add {
			return "", ErrInvalidRoutePlan
		}
		script.WriteString(body)
		script.WriteByte(';')
	}
	return script.String(), nil
}

func applyPlanScript(plan RoutePlan) (string, error) {
	if len(plan.Apply) == 0 || len(plan.Apply) > 64 {
		return "", ErrInvalidRoutePlan
	}
	var script strings.Builder
	script.WriteString(powerShellPreamble)
	for _, command := range plan.Apply {
		body, err := routeCommandBody(command)
		if err != nil || !command.Add {
			return "", ErrInvalidRoutePlan
		}
		script.WriteString(body)
		script.WriteByte(';')
	}
	return script.String(), nil
}

func routeCommandBody(command RouteCommand) (string, error) {
	if command.InterfaceIndex <= 0 {
		return "", ErrInvalidRoutePlan
	}
	if command.Kind == RouteCommandConfigureAdapter {
		if !adapterNamePattern.MatchString(command.AdapterName) {
			return "", ErrInvalidRoutePlan
		}
		if !command.Add {
			return fmt.Sprintf("$adapter=@(Get-NetAdapter -IncludeHidden -ErrorAction SilentlyContinue|Where-Object{$_.ifIndex -eq %d -and $_.Name -eq '%s'});if($adapter.Count -gt 0){Set-DnsClientServerAddress -InterfaceIndex %d -ResetServerAddresses -ErrorAction SilentlyContinue;Get-NetIPAddress -InterfaceIndex %d -ErrorAction SilentlyContinue|Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue}", command.InterfaceIndex, quotePowerShell(command.AdapterName), command.InterfaceIndex, command.InterfaceIndex), nil
		}
		return fmt.Sprintf("New-NetIPAddress -InterfaceIndex %d -IPAddress '198.18.0.1' -PrefixLength 15|Out-Null;New-NetIPAddress -InterfaceIndex %d -IPAddress 'fd00:6e70:2::1' -PrefixLength 64|Out-Null;Set-NetIPInterface -InterfaceIndex %d -AddressFamily IPv4 -AutomaticMetric Disabled -InterfaceMetric 5 -NlMtuBytes 1500;Set-NetIPInterface -InterfaceIndex %d -AddressFamily IPv6 -AutomaticMetric Disabled -InterfaceMetric 5 -NlMtuBytes 1500;Set-DnsClientServerAddress -InterfaceIndex %d -ServerAddresses @('1.1.1.1','1.0.0.1','2606:4700:4700::1111','2606:4700:4700::1001')", command.InterfaceIndex, command.InterfaceIndex, command.InterfaceIndex, command.InterfaceIndex, command.InterfaceIndex), nil
	}
	if command.Kind != RouteCommandEndpointExclusion && command.Kind != RouteCommandTunnelRoute {
		return "", ErrInvalidRoutePlan
	}
	if command.Destination == "" || command.NextHop == "" {
		return "", ErrInvalidRoutePlan
	}
	remove := fmt.Sprintf("Get-NetRoute -DestinationPrefix '%s' -InterfaceIndex %d -NextHop '%s' -ErrorAction SilentlyContinue|Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue", quotePowerShell(command.Destination), command.InterfaceIndex, quotePowerShell(command.NextHop))
	if !command.Add {
		if command.Kind == RouteCommandTunnelRoute {
			return fmt.Sprintf("$adapter=@(Get-NetAdapter -IncludeHidden -ErrorAction SilentlyContinue|Where-Object{$_.ifIndex -eq %d -and $_.Name -eq '%s'});if($adapter.Count -gt 0){%s}", command.InterfaceIndex, windowsAdapterName, remove), nil
		}
		return remove, nil
	}
	return fmt.Sprintf("New-NetRoute -PolicyStore ActiveStore -DestinationPrefix '%s' -InterfaceIndex %d -NextHop '%s' -RouteMetric 1|Out-Null", quotePowerShell(command.Destination), command.InterfaceIndex, quotePowerShell(command.NextHop)), nil
}

func quotePowerShell(value string) string { return strings.ReplaceAll(value, "'", "''") }
