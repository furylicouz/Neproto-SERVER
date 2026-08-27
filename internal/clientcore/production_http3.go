package clientcore

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core/device"

	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/carrier/http3wt"
	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/tunstack"
)

var ErrPacketTunnelAlreadyAttached = errors.New("client packet tunnel is already attached")

// NewProductionStrictHTTP3Core constructs the first-candidate production core.
// Its dependency graph contains one carrier dialer: HTTP/3 WebTransport.
func NewProductionStrictHTTP3Core() (*Core, error) {
	connector, err := NewStrictHTTP3Connector(StrictHTTP3Dependencies{
		DialHTTP3:    dialProductionHTTP3,
		Authenticate: authenticateProductionHTTP3,
	})
	if err != nil {
		return nil, err
	}
	return New(Options{Connect: connector})
}

func dialProductionHTTP3(ctx context.Context, clientConfig config.Client) (carrier.Carrier, error) {
	if ctx == nil || clientConfig.HTTP3Timeout.Duration <= 0 {
		return nil, ErrStrictHTTP3Required
	}
	attempt, cancel := context.WithTimeout(ctx, clientConfig.HTTP3Timeout.Duration)
	defer cancel()
	return http3wt.Dial(attempt, http3wt.DialConfig{
		URL:                  clientConfig.HTTP3URL,
		ServerAddresses:      clientConfig.ServerAddresses,
		HandshakeIdleTimeout: clientConfig.HTTP3Timeout.Duration,
		IdleTimeout:          boundedHTTP3IdleTimeout(clientConfig.HTTP3Timeout.Duration),
	})
}

func authenticateProductionHTTP3(
	ctx context.Context,
	clientConfig config.Client,
	connection carrier.Carrier,
) (Runtime, error) {
	if connection == nil || connection.Kind() != protocol.CarrierHTTP3 {
		return nil, ErrUnexpectedCarrier
	}
	var quicStats func() http3wt.ConnectionStats
	if provider, ok := connection.(interface {
		ConnectionStats() http3wt.ConnectionStats
	}); ok {
		quicStats = provider.ConnectionStats
	}
	authenticated, err := app.AuthenticateClientCarrier(ctx, clientConfig, connection)
	if err != nil {
		return nil, err
	}
	if authenticated == nil || authenticated.Mux == nil || authenticated.Carrier != protocol.CarrierHTTP3 {
		if authenticated != nil && authenticated.Mux != nil {
			_ = authenticated.Mux.Close()
		}
		return nil, ErrNoRuntime
	}
	return newAuthenticatedRuntime(clientConfig, authenticated, quicStats)
}

func boundedHTTP3IdleTimeout(handshakeTimeout time.Duration) time.Duration {
	idleTimeout := 6 * handshakeTimeout
	if idleTimeout < 30*time.Second {
		return 30 * time.Second
	}
	if idleTimeout > 3*time.Minute {
		return 3 * time.Minute
	}
	return idleTimeout
}

type authenticatedRuntime struct {
	mu            sync.Mutex
	authenticated *session.Authenticated
	router        *tunstack.SessionRouter
	stack         *tunstack.Stack
	addresses     []string
	quicStats     func() http3wt.ConnectionStats
	closed        bool
}

func newAuthenticatedRuntime(
	clientConfig config.Client,
	authenticated *session.Authenticated,
	quicStats func() http3wt.ConnectionStats,
) (*authenticatedRuntime, error) {
	if authenticated == nil || authenticated.Mux == nil || !supportedAuthenticatedCarrier(authenticated.Carrier) {
		return nil, ErrNoRuntime
	}
	maximumPayload, datagrams, err := authenticatedRoute(authenticated)
	if err != nil {
		_ = authenticated.Mux.Close()
		return nil, err
	}
	router, err := tunstack.NewSessionRouter(authenticated.Mux, maximumPayload, datagrams)
	if err != nil {
		_ = authenticated.Mux.Close()
		return nil, err
	}
	addresses, err := safeServerAddresses(clientConfig.ServerAddresses, authenticated.CarrierRemoteAddresses)
	if err != nil {
		_ = authenticated.Mux.Close()
		return nil, err
	}
	return &authenticatedRuntime{
		authenticated: authenticated,
		router:        router,
		addresses:     addresses,
		quicStats:     quicStats,
	}, nil
}

func (r *authenticatedRuntime) Carrier() clienthost.Carrier {
	if r == nil {
		return clienthost.CarrierUnknown
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.authenticated == nil {
		return clienthost.CarrierUnknown
	}
	return hostCarrier(r.authenticated.Carrier)
}

func supportedAuthenticatedCarrier(kind protocol.CarrierKind) bool {
	return kind == protocol.CarrierHTTP3 || kind == protocol.CarrierHTTPS
}

func hostCarrier(kind protocol.CarrierKind) clienthost.Carrier {
	switch kind {
	case protocol.CarrierHTTP3:
		return clienthost.CarrierHTTP3WebTransport
	case protocol.CarrierHTTPS:
		return clienthost.CarrierHTTPSWebSocket
	default:
		return clienthost.CarrierUnknown
	}
}

func (r *authenticatedRuntime) Wait(ctx context.Context) error {
	if r == nil {
		return ErrNoRuntime
	}
	r.mu.Lock()
	authenticated := r.authenticated
	closed := r.closed
	r.mu.Unlock()
	if closed || authenticated == nil || authenticated.Mux == nil {
		return ErrNoRuntime
	}
	return authenticated.Mux.Wait(ctx)
}

func (r *authenticatedRuntime) Probe(ctx context.Context) error {
	if r == nil {
		return ErrNoRuntime
	}
	r.mu.Lock()
	authenticated := r.authenticated
	closed := r.closed
	r.mu.Unlock()
	if closed || authenticated == nil || authenticated.Mux == nil {
		return ErrNoRuntime
	}
	return authenticated.Mux.Ping(ctx)
}

func (r *authenticatedRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	stack := r.stack
	r.stack = nil
	authenticated := r.authenticated
	r.authenticated = nil
	r.mu.Unlock()
	var muxErr error
	if authenticated != nil && authenticated.Mux != nil {
		muxErr = authenticated.Mux.Close()
	}
	var stackErr error
	if stack != nil {
		stackErr = stack.Close()
	}
	return errors.Join(stackErr, muxErr)
}

// HandoverPacketPathTo keeps the platform-owned TUN device and its bounded
// packet-processing goroutines alive while atomically switching new flows to
// an authenticated replacement using the same strict carrier. The replacement then owns the router
// and stack; closing the old runtime only closes the obsolete carrier mux.
func (r *authenticatedRuntime) HandoverPacketPathTo(replacement Runtime) error {
	next, ok := replacement.(*authenticatedRuntime)
	if r == nil || !ok || next == nil || r == next {
		return ErrNoRuntime
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	next.mu.Lock()
	defer next.mu.Unlock()
	if r.closed || next.closed || r.authenticated == nil || next.authenticated == nil ||
		r.router == nil || next.router == nil ||
		r.authenticated.Carrier != next.authenticated.Carrier {
		return ErrNotConnected
	}
	maximumPayload, datagrams, err := authenticatedRoute(next.authenticated)
	if err != nil {
		return err
	}
	if err := r.router.Switch(next.authenticated.Mux, maximumPayload, datagrams); err != nil {
		return err
	}
	next.router = r.router
	next.stack = r.stack
	r.router = nil
	r.stack = nil
	return nil
}

func (r *authenticatedRuntime) SetClientRoutes(policy *tunstack.ClientRoutePolicy) error {
	if r == nil || policy == nil {
		return tunstack.ErrInvalidClientRoutes
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.router == nil {
		return ErrNotConnected
	}
	return r.router.SetClientRoutes(policy)
}

func (r *authenticatedRuntime) AttachPacketDevice(endpoint device.Device, mtu uint32) error {
	if r == nil || endpoint == nil {
		return ErrInvalidPacketDevice
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.authenticated == nil || r.router == nil {
		return ErrNotConnected
	}
	if r.stack != nil {
		return ErrPacketTunnelAlreadyAttached
	}
	stack, err := tunstack.StartWithSessionRouterDevice(endpoint, mtu, r.router)
	if err != nil {
		return err
	}
	r.stack = stack
	return nil
}

func (r *authenticatedRuntime) AttachPacketTunnel(fileDescriptor int, mtu uint32) error {
	if r == nil || fileDescriptor < 0 {
		return ErrInvalidPacketDevice
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.authenticated == nil || r.router == nil {
		return ErrNotConnected
	}
	if r.stack != nil {
		return ErrPacketTunnelAlreadyAttached
	}
	stack, err := tunstack.StartWithSessionRouter(fileDescriptor, mtu, r.router)
	if err != nil {
		return err
	}
	r.stack = stack
	return nil
}

func (r *authenticatedRuntime) RuntimeSnapshot() RuntimeSnapshot {
	if r == nil {
		return RuntimeSnapshot{Carrier: clienthost.CarrierNone}
	}
	r.mu.Lock()
	stack := r.stack
	router := r.router
	closed := r.closed
	var carrierKind protocol.CarrierKind
	if r.authenticated != nil {
		carrierKind = r.authenticated.Carrier
	}
	addresses := append([]string(nil), r.addresses...)
	quicStats := r.quicStats
	r.mu.Unlock()
	if closed || router == nil {
		return RuntimeSnapshot{Carrier: clienthost.CarrierNone}
	}
	result := RuntimeSnapshot{
		Carrier: hostCarrier(carrierKind), ServerAddresses: addresses,
		CarrierPoolTarget: 1,
	}
	healthy, assignments := router.PoolStats()
	result.CarrierPoolHealthy = int64(healthy)
	result.CarrierPoolAssignments = int64(assignments)
	result.UDPMode = router.UDPMode()
	if stack != nil {
		result.UploadBytesPerSecond, result.DownloadBytesPerSecond,
			result.UploadTotalBytes, result.DownloadTotalBytes = stack.TrafficStats()
	}
	if quicStats != nil {
		stats := quicStats()
		result.QUICMinRTTMS = stats.MinRTT.Milliseconds()
		result.QUICLatestRTTMS = stats.LatestRTT.Milliseconds()
		result.QUICSmoothedRTTMS = stats.SmoothedRTT.Milliseconds()
		result.QUICRTTDeviationMS = stats.MeanDeviation.Milliseconds()
		result.QUICBytesSent = stats.BytesSent
		result.QUICPacketsSent = stats.PacketsSent
		result.QUICBytesReceived = stats.BytesReceived
		result.QUICPacketsReceived = stats.PacketsReceived
		result.QUICBytesLost = stats.BytesLost
		result.QUICPacketsLost = stats.PacketsLost
	}
	return result
}

func (r *authenticatedRuntime) FetchCatalog(ctx context.Context) ([]byte, error) {
	if r == nil || ctx == nil {
		return nil, ErrNotConnected
	}
	r.mu.Lock()
	authenticated := r.authenticated
	closed := r.closed
	r.mu.Unlock()
	if closed || authenticated == nil || authenticated.Mux == nil {
		return nil, ErrNotConnected
	}
	return proxy.FetchCatalog(ctx, authenticated.Mux)
}

func authenticatedRoute(authenticated *session.Authenticated) (uint64, *session.DatagramMux, error) {
	if authenticated == nil || authenticated.Mux == nil {
		return 0, nil, ErrNoRuntime
	}
	extensions, negotiated := authenticated.Extensions()
	if !negotiated || extensions.Capabilities&protocol.CapabilityReliableUDP == 0 {
		return 0, nil, nil
	}
	if extensions.MaxUDPPayload < 1200 {
		return 0, nil, session.ErrInvalidConfig
	}
	if extensions.Capabilities&protocol.CapabilityUnreliableDatagrams != 0 {
		if authenticated.Datagrams == nil || !authenticated.Datagrams.Enabled() {
			return 0, nil, session.ErrInvalidConfig
		}
		return extensions.MaxUDPPayload, authenticated.Datagrams, nil
	}
	return extensions.MaxUDPPayload, nil, nil
}

func safeServerAddresses(configured, connected []netip.Addr) ([]string, error) {
	addresses := append(append([]netip.Addr(nil), configured...), connected...)
	unique := make(map[string]struct{}, len(addresses))
	benchmarkIPv4 := netip.MustParsePrefix("198.18.0.0/15")
	documentationIPv6 := netip.MustParsePrefix("2001:db8::/32")
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() ||
			address.IsMulticast() || address.IsLinkLocalUnicast() || address.IsPrivate() ||
			benchmarkIPv4.Contains(address) || documentationIPv6.Contains(address) {
			continue
		}
		unique[address.String()] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for address := range unique {
		result = append(result, address)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil, errors.New("NP/2 server has no safe route exclusions")
	}
	return result, nil
}
