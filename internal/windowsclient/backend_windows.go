//go:build windows

package windowsclient

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"

	"neproto.local/chameleon/internal/clientcore"
	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
)

const (
	windowsAdapterName = "NeProto"
	windowsTunnelMTU   = 1500
)

type WindowsBackend struct {
	mu               sync.Mutex
	routes           windowsRouteManager
	endpoint         device.Device
	connecting       bool
	generation       uint64
	recoveryErr      error
	cleanupDone      chan struct{}
	cleanupErr       error
	core             windowsClientCore
	routePolicy      []byte
	newCore          func() (windowsClientCore, error)
	openTunnel       func(string, int) (device.Device, error)
	resolveInterface func(context.Context, string) (int, error)
}

type windowsClientCore interface {
	Connect(context.Context, clientcore.ConnectRequest) (clienthost.Snapshot, error)
	SetClientRoutesJSON([]byte) error
	AttachPacketDevice(context.Context, device.Device, uint32) error
	RuntimeSnapshot() clientcore.RuntimeSnapshot
	FetchCatalog(context.Context) ([]byte, error)
	Close(context.Context) error
}

type windowsRouteManager interface {
	Recover(context.Context) error
	PrepareEndpoints(context.Context, []string) error
	ActivateTunnel(context.Context, string, int) error
	Cleanup(context.Context) error
}

func NewWindowsBackend(routes *WindowsRouteManager) *WindowsBackend {
	return newWindowsBackend(routes, nil)
}

func NewWindowsBackendWithStartupRecovery(routes *WindowsRouteManager, recoveryErr error) *WindowsBackend {
	return newWindowsBackend(routes, recoveryErr)
}

func newWindowsBackend(routes windowsRouteManager, recoveryErr error) *WindowsBackend {
	return &WindowsBackend{
		routes: routes, recoveryErr: recoveryErr,
		newCore: func() (windowsClientCore, error) {
			return clientcore.NewProductionStrictHTTP3Core()
		},
		openTunnel:       func(name string, mtu int) (device.Device, error) { return tun.Open(name, uint32(mtu)) },
		resolveInterface: waitForInterface,
	}
}

func (b *WindowsBackend) SetRoutes(raw []byte) error {
	if err := clientcore.ValidateClientRoutesJSON(raw); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connecting || b.core != nil {
		return ErrControllerBusy
	}
	b.routePolicy = append(b.routePolicy[:0], raw...)
	return nil
}

func (b *WindowsBackend) Connect(ctx context.Context, profile []byte, secret string) (BackendStatus, error) {
	b.mu.Lock()
	if b.endpoint != nil || b.core != nil || b.connecting || b.routes == nil ||
		b.newCore == nil || b.openTunnel == nil || b.resolveInterface == nil {
		b.mu.Unlock()
		return BackendStatus{}, ErrControllerBusy
	}
	b.connecting = true
	b.generation++
	generation := b.generation
	routePolicy := append([]byte(nil), b.routePolicy...)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if b.generation == generation {
			b.connecting = false
		}
		b.mu.Unlock()
	}()
	if err := b.WaitForCleanup(ctx); err != nil {
		// The route journal is authoritative. ensureRoutesRecovered retries a
		// cleanup that failed in the background before any new side effect.
	}
	if err := ctx.Err(); err != nil {
		return BackendStatus{}, err
	}
	if err := b.ensureRoutesRecovered(ctx); err != nil {
		return BackendStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return BackendStatus{}, err
	}
	loaded, err := config.ParseMobileClientBytes(profile, secret)
	if err != nil {
		return BackendStatus{}, err
	}
	addresses := make([]string, 0, len(loaded.ServerAddresses))
	for _, address := range loaded.ServerAddresses {
		addresses = append(addresses, address.String())
	}
	cleanupPrepared := func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := b.routes.Cleanup(cleanupContext)
		cancel()
		if err != nil {
			b.mu.Lock()
			b.recoveryErr = err
			b.mu.Unlock()
		}
	}
	if err := b.routes.PrepareEndpoints(ctx, addresses); err != nil {
		cleanupPrepared()
		return BackendStatus{}, err
	}
	core, err := b.newCore()
	if err != nil || core == nil {
		cleanupPrepared()
		if err != nil {
			return BackendStatus{}, err
		}
		return BackendStatus{}, clientcore.ErrNoRuntime
	}
	closeCore := func() error {
		closeContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return core.Close(closeContext)
	}
	np2Started := time.Now()
	if _, err := core.Connect(ctx, clientcore.ConnectRequest{
		OperationID: "windows-connect", ProfileID: "windows-active", Profile: loaded,
	}); err != nil {
		_ = closeCore()
		cleanupPrepared()
		return BackendStatus{}, err
	}
	np2Duration := time.Since(np2Started)
	windowsStarted := time.Now()
	fail := func(endpoint device.Device, attached bool) {
		_ = closeCore()
		if endpoint != nil && !attached {
			endpoint.Close()
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cleanupErr := b.routes.Cleanup(cleanupContext)
		cancel()
		if cleanupErr != nil {
			b.mu.Lock()
			b.recoveryErr = cleanupErr
			b.mu.Unlock()
		}
	}
	if len(routePolicy) > 0 {
		if err := core.SetClientRoutesJSON(routePolicy); err != nil {
			fail(nil, false)
			return BackendStatus{}, err
		}
	}
	runtime := core.RuntimeSnapshot()
	connectedAddresses := runtime.ServerAddresses
	if runtime.Carrier != clienthost.CarrierHTTP3WebTransport || len(connectedAddresses) == 0 {
		fail(nil, false)
		return BackendStatus{}, errors.New("strict HTTP/3 core returned no route exclusions")
	}
	if !containsAllAddresses(addresses, connectedAddresses) {
		fail(nil, false)
		return BackendStatus{}, errors.New("strict HTTP/3 core returned an unprepared route exclusion")
	}
	if err := ctx.Err(); err != nil {
		fail(nil, false)
		return BackendStatus{}, err
	}
	endpoint, err := b.openTunnel(windowsAdapterName, windowsTunnelMTU)
	if err != nil {
		fail(nil, false)
		return BackendStatus{}, err
	}
	interfaceIndex, err := b.resolveInterface(ctx, windowsAdapterName)
	if err != nil {
		fail(endpoint, false)
		return BackendStatus{}, err
	}
	if err := b.routes.ActivateTunnel(ctx, windowsAdapterName, interfaceIndex); err != nil {
		fail(endpoint, false)
		return BackendStatus{}, err
	}
	if err := core.AttachPacketDevice(ctx, endpoint, windowsTunnelMTU); err != nil {
		fail(endpoint, false)
		return BackendStatus{}, err
	}
	b.mu.Lock()
	stale := b.generation != generation || !b.connecting
	if !stale {
		b.endpoint = endpoint
		b.core = core
	}
	b.mu.Unlock()
	if stale || ctx.Err() != nil {
		fail(endpoint, true)
		if ctx.Err() != nil {
			return BackendStatus{}, ctx.Err()
		}
		return BackendStatus{}, context.Canceled
	}
	status := b.Snapshot()
	status.NP2ConnectMilliseconds = np2Duration.Milliseconds()
	status.WindowsSetupMilliseconds = time.Since(windowsStarted).Milliseconds()
	return status, nil
}

func (b *WindowsBackend) ensureRoutesRecovered(ctx context.Context) error {
	b.mu.Lock()
	pending := b.recoveryErr
	routes := b.routes
	b.mu.Unlock()
	if pending == nil {
		return nil
	}
	if routes == nil {
		return errors.New("Windows route recovery unavailable")
	}
	err := routes.Recover(ctx)
	b.mu.Lock()
	b.recoveryErr = err
	b.mu.Unlock()
	if err != nil {
		return errors.New("Windows route recovery is incomplete: " + err.Error())
	}
	return nil
}

func (b *WindowsBackend) Disconnect(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	b.mu.Lock()
	b.generation++
	b.connecting = false
	b.endpoint = nil
	core := b.core
	b.core = nil
	b.mu.Unlock()
	var closeErr error
	if core != nil {
		closeContext, cancel := context.WithTimeout(ctx, 15*time.Second)
		closeErr = core.Close(closeContext)
		cancel()
	}
	b.startRouteCleanup()
	return closeErr
}

func (b *WindowsBackend) startRouteCleanup() {
	b.mu.Lock()
	if b.cleanupDone != nil {
		select {
		case <-b.cleanupDone:
		default:
			b.mu.Unlock()
			return
		}
	}
	done := make(chan struct{})
	b.cleanupDone = done
	b.cleanupErr = nil
	routes := b.routes
	b.mu.Unlock()

	go func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := routes.Cleanup(cleanupContext)
		cancel()
		b.mu.Lock()
		b.cleanupErr = err
		if err != nil {
			b.recoveryErr = err
		}
		close(done)
		b.mu.Unlock()
	}()
}

// WaitForCleanup is used by a new connection and by service shutdown. Normal
// UI disconnect remains immediate while the durable route journal is cleaned
// in the background.
func (b *WindowsBackend) WaitForCleanup(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	b.mu.Lock()
	done := b.cleanupDone
	b.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		b.mu.Lock()
		err := b.cleanupErr
		b.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *WindowsBackend) Snapshot() BackendStatus {
	b.mu.Lock()
	core := b.core
	b.mu.Unlock()
	if core == nil {
		return BackendStatus{}
	}
	runtime := core.RuntimeSnapshot()
	carrier := ""
	if runtime.Carrier == clienthost.CarrierHTTP3WebTransport {
		carrier = "http3"
	}
	return BackendStatus{
		Carrier: carrier, ServerAddresses: append([]string(nil), runtime.ServerAddresses...),
		UploadBytesPerSecond: runtime.UploadBytesPerSecond, DownloadBytesPerSecond: runtime.DownloadBytesPerSecond,
		UploadTotalBytes: runtime.UploadTotalBytes, DownloadTotalBytes: runtime.DownloadTotalBytes,
		UDPMode: runtime.UDPMode, CarrierPoolTarget: runtime.CarrierPoolTarget,
		CarrierPoolHealthy: runtime.CarrierPoolHealthy, CarrierPoolAssignments: runtime.CarrierPoolAssignments,
	}
}

func (b *WindowsBackend) FetchCatalog(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	core := b.core
	b.mu.Unlock()
	if core == nil {
		return nil, clientcore.ErrNotConnected
	}
	return core.FetchCatalog(ctx)
}

func containsAllAddresses(prepared, connected []string) bool {
	allowed := make(map[netip.Addr]struct{}, len(prepared))
	for _, value := range prepared {
		address, err := netip.ParseAddr(strings.TrimSpace(value))
		if err == nil {
			allowed[address.Unmap()] = struct{}{}
		}
	}
	if len(connected) == 0 {
		return false
	}
	for _, value := range connected {
		address, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil {
			return false
		}
		if _, ok := allowed[address.Unmap()]; !ok {
			return false
		}
	}
	return true
}

func waitForInterface(ctx context.Context, name string) (int, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		if networkInterface, err := net.InterfaceByName(name); err == nil && networkInterface.Index > 0 {
			return networkInterface.Index, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-timer.C:
			return 0, errors.New("Wintun interface did not become available")
		case <-ticker.C:
		}
	}
}
