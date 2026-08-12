//go:build windows

package windowsclient

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"

	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/mobile/np2mobile"
)

const (
	windowsAdapterName = "NeProto"
	windowsTunnelMTU   = 1500
)

type WindowsBackend struct {
	mu          sync.Mutex
	routes      windowsRouteManager
	endpoint    device.Device
	connecting  bool
	generation  uint64
	recoveryErr error
	cleanupDone chan struct{}
	cleanupErr  error
	startNP2    func(string, string) error
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
	return &WindowsBackend{routes: routes, recoveryErr: recoveryErr, startNP2: np2mobile.Start}
}

func (*WindowsBackend) SetRoutes(raw []byte) error {
	return np2mobile.SetClientRoutesJSON(string(raw))
}

func (b *WindowsBackend) Connect(ctx context.Context, profile []byte, secret string) (BackendStatus, error) {
	b.mu.Lock()
	if b.endpoint != nil || b.connecting || b.routes == nil {
		b.mu.Unlock()
		return BackendStatus{}, ErrControllerBusy
	}
	b.connecting = true
	b.generation++
	generation := b.generation
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
	np2Started := time.Now()
	if err := b.startNP2(string(profile), secret); err != nil {
		cleanupPrepared()
		return BackendStatus{}, err
	}
	np2Duration := time.Since(np2Started)
	windowsStarted := time.Now()
	fail := func(endpoint device.Device, attached bool) {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = b.routes.Cleanup(cleanupContext)
		cancel()
		np2mobile.Stop()
		if endpoint != nil && !attached {
			endpoint.Close()
		}
	}
	connectedAddresses := splitAddresses(np2mobile.ServerAddresses())
	if len(connectedAddresses) == 0 {
		fail(nil, false)
		return BackendStatus{}, errors.New("NP/2 carrier returned no route exclusions")
	}
	if err := ctx.Err(); err != nil {
		fail(nil, false)
		return BackendStatus{}, err
	}
	endpoint, err := tun.Open(windowsAdapterName, windowsTunnelMTU)
	if err != nil {
		fail(nil, false)
		return BackendStatus{}, err
	}
	interfaceIndex, err := waitForInterface(ctx, windowsAdapterName)
	if err != nil {
		fail(endpoint, false)
		return BackendStatus{}, err
	}
	if err := b.routes.ActivateTunnel(ctx, windowsAdapterName, interfaceIndex); err != nil {
		fail(endpoint, false)
		return BackendStatus{}, err
	}
	if err := np2mobile.StartWindowsPacketTunnel(endpoint, windowsTunnelMTU); err != nil {
		fail(endpoint, false)
		return BackendStatus{}, err
	}
	b.mu.Lock()
	stale := b.generation != generation || !b.connecting
	if !stale {
		b.endpoint = endpoint
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
	b.mu.Lock()
	b.generation++
	b.connecting = false
	b.endpoint = nil
	b.mu.Unlock()
	np2mobile.Stop()
	b.startRouteCleanup()
	return nil
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
	return BackendStatus{
		Carrier: np2mobile.Carrier(), ServerAddresses: splitAddresses(np2mobile.ServerAddresses()),
		UploadBytesPerSecond: np2mobile.UploadBytesPerSecond(), DownloadBytesPerSecond: np2mobile.DownloadBytesPerSecond(),
		UploadTotalBytes: np2mobile.UploadTotalBytes(), DownloadTotalBytes: np2mobile.DownloadTotalBytes(),
		UDPMode: np2mobile.UDPMode(), CarrierPoolTarget: np2mobile.CarrierPoolTarget(),
		CarrierPoolHealthy: np2mobile.CarrierPoolHealthy(), CarrierPoolAssignments: np2mobile.CarrierPoolAssignments(),
	}
}

func (*WindowsBackend) FetchCatalog(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := np2mobile.CatalogJSON()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

func splitAddresses(value string) []string {
	var result []string
	for _, address := range strings.Split(value, ",") {
		address = strings.TrimSpace(address)
		if address != "" {
			result = append(result, address)
		}
	}
	return result
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
