package clientcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/xjasonlyu/tun2socks/v2/core/device"

	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/tunstack"
)

var (
	ErrPlatformAdapterUnavailable = errors.New("client runtime platform adapter is unavailable")
	ErrInvalidPacketDevice        = errors.New("invalid packet tunnel device")
	ErrInvalidCatalogResponse     = errors.New("invalid client catalog response")
)

// RuntimeSnapshot contains bounded aggregate runtime data. It deliberately
// excludes destinations, payloads, credentials and raw profile material.
type RuntimeSnapshot struct {
	Carrier                 clienthost.Carrier
	ServerAddresses         []string
	UploadBytesPerSecond    int64
	DownloadBytesPerSecond  int64
	UploadTotalBytes        int64
	DownloadTotalBytes      int64
	UDPMode                 string
	CarrierPoolTarget       int64
	CarrierPoolHealthy      int64
	CarrierPoolAssignments  int64
	QUICMinRTTMS            int64
	QUICLatestRTTMS         int64
	QUICSmoothedRTTMS       int64
	QUICRTTDeviationMS      int64
	QUICBytesSent           uint64
	QUICPacketsSent         uint64
	QUICBytesReceived       uint64
	QUICPacketsReceived     uint64
	QUICBytesLost           uint64
	QUICPacketsLost         uint64
	DNSAttributionQueries   uint64
	DNSAttributionResponses uint64
	DNSAttributionHits      uint64
	DNSAttributionMisses    uint64
	DNSAttributionCached    uint64
	FirstFlightDomainHits   uint64
	FirstFlightFallbacks    uint64
	TCPStreamAttempts       uint64
	TCPStreamSuccesses      uint64
	TCPStreamFailures       uint64
	TCPStreamOpenLastMS     uint64
	TCPStreamOpenMaxMS      uint64
	ActiveStreams           uint64
	FlowControlStalls       uint64
	ProtocolErrors          uint64
}

type clientRouteRuntime interface {
	SetClientRoutes(*tunstack.ClientRoutePolicy) error
}

type packetDeviceRuntime interface {
	AttachPacketDevice(device.Device, uint32) error
}

type packetTunnelRuntime interface {
	AttachPacketTunnel(int, uint32) error
}

type snapshotRuntime interface {
	RuntimeSnapshot() RuntimeSnapshot
}

type catalogRuntime interface {
	FetchCatalog(context.Context) ([]byte, error)
}

// SetClientRoutesJSON validates one bounded immutable route snapshot before it
// reaches the active packet runtime.
func (c *Core) SetClientRoutesJSON(raw []byte) error {
	policy, err := parseClientRoutes(raw)
	if err != nil {
		return err
	}
	runtime, err := c.activeRuntime()
	if err != nil {
		return err
	}
	provider, ok := runtime.(clientRouteRuntime)
	if !ok {
		return ErrPlatformAdapterUnavailable
	}
	return provider.SetClientRoutes(policy)
}

// ValidateClientRoutesJSON validates a platform-provided route snapshot
// without requiring or mutating an active Core.
func ValidateClientRoutesJSON(raw []byte) error {
	_, err := parseClientRoutes(raw)
	return err
}

func parseClientRoutes(raw []byte) (*tunstack.ClientRoutePolicy, error) {
	if len(raw) == 0 || len(raw) > proxy.MaxCatalogPayload {
		return nil, tunstack.ErrInvalidClientRoutes
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var routes []cluster.Route
	if err := decoder.Decode(&routes); err != nil {
		return nil, tunstack.ErrInvalidClientRoutes
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, tunstack.ErrInvalidClientRoutes
	}
	return tunstack.NewClientRoutePolicy(routes)
}

// AttachPacketDevice transfers an already configured platform tunnel device
// to the active authenticated runtime. The platform host remains responsible
// for creating the device and installing system routes first.
func (c *Core) AttachPacketDevice(ctx context.Context, endpoint device.Device, mtu uint32) error {
	if ctx == nil || endpoint == nil {
		return ErrInvalidPacketDevice
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime, err := c.activeRuntime()
	if err != nil {
		return err
	}
	provider, ok := runtime.(packetDeviceRuntime)
	if !ok {
		return ErrPlatformAdapterUnavailable
	}
	if err := provider.AttachPacketDevice(endpoint, mtu); err != nil {
		return err
	}
	return ctx.Err()
}

// AttachPacketTunnel transfers a duplicated platform tunnel file descriptor to
// the active authenticated runtime. It is the NetworkExtension-facing twin of
// AttachPacketDevice.
func (c *Core) AttachPacketTunnel(ctx context.Context, fileDescriptor int, mtu uint32) error {
	if ctx == nil || fileDescriptor < 0 {
		return ErrInvalidPacketDevice
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime, err := c.activeRuntime()
	if err != nil {
		return err
	}
	provider, ok := runtime.(packetTunnelRuntime)
	if !ok {
		return ErrPlatformAdapterUnavailable
	}
	if err := provider.AttachPacketTunnel(fileDescriptor, mtu); err != nil {
		return err
	}
	return ctx.Err()
}

func (c *Core) RuntimeSnapshot() RuntimeSnapshot {
	runtime, err := c.activeRuntime()
	if err != nil {
		return RuntimeSnapshot{Carrier: clienthost.CarrierNone}
	}
	provider, ok := runtime.(snapshotRuntime)
	if !ok {
		return RuntimeSnapshot{Carrier: clienthost.CarrierUnknown}
	}
	result := provider.RuntimeSnapshot()
	result.ServerAddresses = append([]string(nil), result.ServerAddresses...)
	return result
}

func (c *Core) FetchCatalog(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, clienthost.ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime, err := c.activeRuntime()
	if err != nil {
		return nil, err
	}
	provider, ok := runtime.(catalogRuntime)
	if !ok {
		return nil, ErrPlatformAdapterUnavailable
	}
	raw, err := provider.FetchCatalog(ctx)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > proxy.MaxCatalogPayload {
		return nil, ErrInvalidCatalogResponse
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]byte(nil), raw...), nil
}

func (c *Core) activeRuntime() (Runtime, error) {
	if c == nil {
		return nil, ErrNotConnected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if c.snapshot.State != clienthost.StateConnected || c.runtime == nil || c.runtime.runtime == nil {
		return nil, ErrNotConnected
	}
	return c.runtime.runtime, nil
}
