//go:build windows

package windowsclient

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"neproto.local/chameleon/internal/clientcore"
	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/onboarding"
)

type recoveryRouteManager struct {
	err   error
	calls int
}

func (m *recoveryRouteManager) Recover(context.Context) error {
	m.calls++
	return m.err
}
func (*recoveryRouteManager) PrepareEndpoints(context.Context, []string) error  { return nil }
func (*recoveryRouteManager) ActivateTunnel(context.Context, string, int) error { return nil }
func (*recoveryRouteManager) Cleanup(context.Context) error                     { return nil }

func TestWindowsBackendRetriesDeferredStartupRecovery(t *testing.T) {
	routes := &recoveryRouteManager{}
	backend := newWindowsBackend(routes, errors.New("startup recovery timed out"))

	if err := backend.ensureRoutesRecovered(context.Background()); err != nil {
		t.Fatal(err)
	}
	if routes.calls != 1 || backend.recoveryErr != nil {
		t.Fatalf("calls=%d recoveryErr=%v", routes.calls, backend.recoveryErr)
	}
}

func TestWindowsBackendBlocksConnectWhileDeferredRecoveryStillFails(t *testing.T) {
	routes := &recoveryRouteManager{err: errors.New("rollback failed")}
	backend := newWindowsBackend(routes, errors.New("startup recovery timed out"))

	if err := backend.ensureRoutesRecovered(context.Background()); err == nil {
		t.Fatal("expected route recovery failure")
	}
	if routes.calls != 1 || backend.recoveryErr == nil {
		t.Fatalf("calls=%d recoveryErr=%v", routes.calls, backend.recoveryErr)
	}
}

type blockingCleanupRouteManager struct {
	started chan struct{}
	release chan struct{}
}

func (*blockingCleanupRouteManager) Recover(context.Context) error                     { return nil }
func (*blockingCleanupRouteManager) PrepareEndpoints(context.Context, []string) error  { return nil }
func (*blockingCleanupRouteManager) ActivateTunnel(context.Context, string, int) error { return nil }
func (m *blockingCleanupRouteManager) Cleanup(ctx context.Context) error {
	close(m.started)
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type orderedRouteManager struct {
	events      *[]string
	activateErr error
}

func (*orderedRouteManager) Recover(context.Context) error { return nil }
func (m *orderedRouteManager) PrepareEndpoints(context.Context, []string) error {
	*m.events = append(*m.events, "prepare")
	return nil
}
func (m *orderedRouteManager) ActivateTunnel(context.Context, string, int) error {
	*m.events = append(*m.events, "activate")
	return m.activateErr
}
func (m *orderedRouteManager) Cleanup(context.Context) error {
	*m.events = append(*m.events, "cleanup")
	return nil
}

type prepareFailureRouteManager struct {
	events     *[]string
	cleanupErr error
}

func (*prepareFailureRouteManager) Recover(context.Context) error { return nil }
func (m *prepareFailureRouteManager) PrepareEndpoints(context.Context, []string) error {
	*m.events = append(*m.events, "prepare")
	return errors.New("prepare failed")
}
func (m *prepareFailureRouteManager) ActivateTunnel(context.Context, string, int) error {
	*m.events = append(*m.events, "activate")
	return nil
}
func (m *prepareFailureRouteManager) Cleanup(context.Context) error {
	*m.events = append(*m.events, "cleanup")
	return m.cleanupErr
}

func TestWindowsBackendPreparesEndpointBeforeDialAndRollsBackDialFailure(t *testing.T) {
	events := []string{}
	backend := newWindowsBackend(&orderedRouteManager{events: &events}, nil)
	backend.newCore = func() (windowsClientCore, error) {
		return &fakeWindowsCore{connect: func(context.Context, clientcore.ConnectRequest) error {
			return errors.New("dial failed")
		}, events: &events}, nil
	}
	secret := base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x42))
	_, profile, secret, err := profileFromOnboarding(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x24)),
		Name: "Primary", ServerIdentity: "nepopus.lyntragram.ru", ServerAddresses: []string{"37.252.23.223"},
		HTTPSPath: "/" + strings.Repeat("1", 48), WebRTCPath: "/" + strings.Repeat("2", 48), HTTP3Path: "/" + strings.Repeat("3", 48),
		Secret: secret,
	}, "00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := backend.Connect(context.Background(), profile, secret); err == nil {
		t.Fatal("expected dial failure")
	} else if err.Error() != "dial failed" {
		t.Fatalf("unexpected pre-dial failure: %v", err)
	}
	want := []string{"prepare", "dial", "close-core", "cleanup"}
	if len(events) != len(want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events=%v want=%v", events, want)
		}
	}
}

func TestWindowsBackendUsesStrictCoreBeforeWintunAndRoutes(t *testing.T) {
	events := []string{}
	routes := &orderedRouteManager{events: &events}
	backend := newWindowsBackend(routes, nil)
	core := &fakeWindowsCore{
		connect: func(_ context.Context, request clientcore.ConnectRequest) error {
			if request.Profile.CarrierPolicy != "http3-only" || request.Profile.MaxParallelCarriers != 1 {
				t.Fatalf("non-strict request = %+v", request.Profile)
			}
			return nil
		},
		snapshot: clientcore.RuntimeSnapshot{
			Carrier:         clienthost.CarrierHTTP3WebTransport,
			ServerAddresses: []string{"37.252.23.223"}, CarrierPoolTarget: 1, CarrierPoolHealthy: 1,
		},
		events: &events,
	}
	backend.newCore = func() (windowsClientCore, error) { return core, nil }
	endpoint := &fakeWindowsDevice{events: &events}
	backend.openTunnel = func(name string, mtu int) (device.Device, error) {
		events = append(events, "open-wintun")
		if name != windowsAdapterName || mtu != windowsTunnelMTU {
			t.Fatalf("tunnel name=%q mtu=%d", name, mtu)
		}
		return endpoint, nil
	}
	backend.resolveInterface = func(context.Context, string) (int, error) {
		events = append(events, "resolve-interface")
		return 42, nil
	}
	secret := base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x42))
	_, profile, secret, err := profileFromOnboarding(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x24)),
		Name: "Primary", ServerIdentity: "nepopus.lyntragram.ru", ServerAddresses: []string{"37.252.23.223"},
		HTTPSPath: "/" + strings.Repeat("1", 48), WebRTCPath: "/" + strings.Repeat("2", 48), HTTP3Path: "/" + strings.Repeat("3", 48),
		Secret: secret,
	}, "00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.SetRoutes([]byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	status, err := backend.Connect(context.Background(), profile, secret)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prepare", "dial", "set-client-routes", "open-wintun", "resolve-interface", "activate", "attach"}
	if len(events) != len(want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events=%v want=%v", events, want)
		}
	}
	if status.Carrier != "http3" || status.CarrierPoolTarget != 1 || core.endpoint != endpoint {
		t.Fatalf("status=%+v endpoint=%v", status, core.endpoint)
	}
}

func TestWindowsBackendRollsBackCoreDeviceAndRoutesWhenTunnelActivationFails(t *testing.T) {
	events := []string{}
	routes := &orderedRouteManager{events: &events, activateErr: errors.New("activate failed")}
	backend := newWindowsBackend(routes, nil)
	core := &fakeWindowsCore{
		snapshot: clientcore.RuntimeSnapshot{
			Carrier:         clienthost.CarrierHTTP3WebTransport,
			ServerAddresses: []string{"37.252.23.223"}, CarrierPoolTarget: 1,
		},
		events: &events,
	}
	backend.newCore = func() (windowsClientCore, error) { return core, nil }
	endpoint := &fakeWindowsDevice{events: &events}
	backend.openTunnel = func(string, int) (device.Device, error) {
		events = append(events, "open-wintun")
		return endpoint, nil
	}
	backend.resolveInterface = func(context.Context, string) (int, error) {
		events = append(events, "resolve-interface")
		return 42, nil
	}
	profile, secret := strictWindowsBackendProfile(t)

	if _, err := backend.Connect(context.Background(), profile, secret); err == nil || err.Error() != "activate failed" {
		t.Fatalf("connect error = %v", err)
	}
	want := []string{"prepare", "dial", "open-wintun", "resolve-interface", "activate", "close-core", "close-device", "cleanup"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events=%v want=%v", events, want)
	}
	if backend.core != nil || backend.endpoint != nil {
		t.Fatal("failed operation retained owned core or device")
	}
}

func TestWindowsBackendRejectsUnpreparedAuthenticatedEndpointBeforeWintun(t *testing.T) {
	events := []string{}
	backend := newWindowsBackend(&orderedRouteManager{events: &events}, nil)
	core := &fakeWindowsCore{
		snapshot: clientcore.RuntimeSnapshot{
			Carrier:         clienthost.CarrierHTTP3WebTransport,
			ServerAddresses: []string{"104.171.136.10"}, CarrierPoolTarget: 1,
		},
		events: &events,
	}
	backend.newCore = func() (windowsClientCore, error) { return core, nil }
	backend.openTunnel = func(string, int) (device.Device, error) {
		t.Fatal("Wintun opened for unprepared authenticated endpoint")
		return nil, errors.New("unreachable")
	}
	profile, secret := strictWindowsBackendProfile(t)

	if _, err := backend.Connect(context.Background(), profile, secret); err == nil ||
		!strings.Contains(err.Error(), "unprepared route exclusion") {
		t.Fatalf("connect error = %v", err)
	}
	want := []string{"prepare", "dial", "close-core", "cleanup"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events=%v want=%v", events, want)
	}
}

func TestWindowsBackendCleansUpFailedEndpointPreparation(t *testing.T) {
	events := []string{}
	cleanupErr := errors.New("cleanup failed")
	backend := newWindowsBackend(&prepareFailureRouteManager{events: &events, cleanupErr: cleanupErr}, nil)
	secret := base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x42))
	_, profile, secret, err := profileFromOnboarding(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x24)),
		Name: "Primary", ServerIdentity: "nepopus.lyntragram.ru", ServerAddresses: []string{"37.252.23.223"},
		HTTPSPath: "/" + strings.Repeat("1", 48), WebRTCPath: "/" + strings.Repeat("2", 48), HTTP3Path: "/" + strings.Repeat("3", 48),
		Secret: secret,
	}, "00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := backend.Connect(context.Background(), profile, secret); err == nil {
		t.Fatal("expected endpoint preparation failure")
	}
	if want := []string{"prepare", "cleanup"}; len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("events=%v want=%v", events, want)
	}
	backend.mu.Lock()
	recoveryErr := backend.recoveryErr
	backend.mu.Unlock()
	if !errors.Is(recoveryErr, cleanupErr) {
		t.Fatalf("recovery error=%v want %v", recoveryErr, cleanupErr)
	}
}

func TestWindowsBackendDisconnectReturnsWhileRouteCleanupContinues(t *testing.T) {
	routes := &blockingCleanupRouteManager{started: make(chan struct{}), release: make(chan struct{})}
	backend := newWindowsBackend(routes, nil)

	started := time.Now()
	if err := backend.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("disconnect waited for route cleanup: %v", elapsed)
	}
	select {
	case <-routes.started:
	case <-time.After(time.Second):
		t.Fatal("route cleanup was not started")
	}

	waited := make(chan error, 1)
	go func() { waited <- backend.WaitForCleanup(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("cleanup wait returned early: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(routes.release)
	if err := <-waited; err != nil {
		t.Fatal(err)
	}
}

type fakeWindowsCore struct {
	connect  func(context.Context, clientcore.ConnectRequest) error
	snapshot clientcore.RuntimeSnapshot
	events   *[]string
	endpoint device.Device
}

func (c *fakeWindowsCore) Connect(ctx context.Context, request clientcore.ConnectRequest) (clienthost.Snapshot, error) {
	if c.events != nil {
		*c.events = append(*c.events, "dial")
	}
	if c.connect != nil {
		if err := c.connect(ctx, request); err != nil {
			return clienthost.Snapshot{}, err
		}
	}
	return clienthost.Snapshot{State: clienthost.StateConnected, Carrier: clienthost.CarrierHTTP3WebTransport}, nil
}

func (c *fakeWindowsCore) SetClientRoutesJSON([]byte) error {
	if c.events != nil {
		*c.events = append(*c.events, "set-client-routes")
	}
	return nil
}

func (c *fakeWindowsCore) AttachPacketDevice(_ context.Context, endpoint device.Device, _ uint32) error {
	if c.events != nil {
		*c.events = append(*c.events, "attach")
	}
	c.endpoint = endpoint
	return nil
}

func (c *fakeWindowsCore) RuntimeSnapshot() clientcore.RuntimeSnapshot { return c.snapshot }
func (*fakeWindowsCore) FetchCatalog(context.Context) ([]byte, error)  { return []byte(`{}`), nil }
func (c *fakeWindowsCore) Close(context.Context) error {
	if c.events != nil {
		*c.events = append(*c.events, "close-core")
	}
	return nil
}

type fakeWindowsDevice struct {
	stack.LinkEndpoint
	events *[]string
}

func (*fakeWindowsDevice) Name() string { return "fake-wintun" }
func (*fakeWindowsDevice) Type() string { return "fake" }
func (d *fakeWindowsDevice) Close() {
	if d.events != nil {
		*d.events = append(*d.events, "close-device")
	}
}

func strictWindowsBackendProfile(t *testing.T) ([]byte, string) {
	t.Helper()
	secret := base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x42))
	_, profile, secret, err := profileFromOnboarding(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x24)),
		Name: "Primary", ServerIdentity: "nepopus.lyntragram.ru", ServerAddresses: []string{"37.252.23.223"},
		HTTPSPath: "/" + strings.Repeat("1", 48), WebRTCPath: "/" + strings.Repeat("2", 48), HTTP3Path: "/" + strings.Repeat("3", 48),
		Secret: secret,
	}, "00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	return profile, secret
}
