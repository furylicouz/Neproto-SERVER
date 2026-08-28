package np2mobile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"neproto.local/chameleon/internal/clientcore"
	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
)

var ErrClientCoreUnavailable = errors.New("NP/2 client core is unavailable")

type strictClientCoreAPI interface {
	Connect(context.Context, clientcore.ConnectRequest) (clienthost.Snapshot, error)
	SetClientRoutesJSON([]byte) error
	AttachPacketTunnel(context.Context, int, uint32) error
	NetworkChanged(context.Context, string) (clienthost.Snapshot, error)
	Snapshot() clienthost.Snapshot
	RuntimeSnapshot() clientcore.RuntimeSnapshot
	FetchCatalog(context.Context) ([]byte, error)
	Close(context.Context) error
}

// ClientCore is the gomobile-safe instance facade used by each Packet Tunnel
// provider. Unlike the legacy package functions, it owns no process-global
// mutable session state.
type ClientCore struct {
	mu         sync.Mutex
	core       strictClientCoreAPI
	routes     []byte
	cancel     context.CancelFunc
	generation uint64
	connected  bool
	closed     bool
}

func NewStrictHTTP3ClientCore() (*ClientCore, error) {
	core, err := clientcore.NewProductionStrictHTTP3Core()
	if err != nil {
		return nil, err
	}
	return newStrictClientCore(core), nil
}

func NewStrictHTTPSClientCore() (*ClientCore, error) {
	core, err := clientcore.NewProductionStrictHTTPSCore()
	if err != nil {
		return nil, err
	}
	return newStrictClientCore(core), nil
}

func newStrictClientCore(core strictClientCoreAPI) *ClientCore {
	return &ClientCore{core: core}
}

func (c *ClientCore) SetClientRoutesJSON(routesJSON string) error {
	raw := []byte(routesJSON)
	if err := clientcore.ValidateClientRoutesJSON(raw); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.connected || c.cancel != nil || c.core == nil {
		return ErrAlreadyRunning
	}
	c.routes = append(c.routes[:0], raw...)
	return nil
}

func (c *ClientCore) Connect(profileJSON, secret, operationID, profileID string) error {
	if c == nil {
		return ErrClientCoreUnavailable
	}
	loaded, err := config.ParseMobileClientBytes([]byte(profileJSON), secret)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	if c.closed || c.connected || c.cancel != nil || c.core == nil {
		c.mu.Unlock()
		cancel()
		return ErrAlreadyRunning
	}
	c.cancel = cancel
	c.generation++
	generation := c.generation
	core := c.core
	routes := append([]byte(nil), c.routes...)
	c.mu.Unlock()

	_, connectErr := core.Connect(ctx, clientcore.ConnectRequest{
		OperationID: operationID,
		ProfileID:   profileID,
		Profile:     loaded,
	})
	if connectErr == nil && len(routes) > 0 {
		connectErr = core.SetClientRoutesJSON(routes)
	}

	c.mu.Lock()
	if c.generation == generation {
		c.cancel = nil
	}
	if connectErr == nil && !c.closed {
		c.connected = true
	}
	closed := c.closed
	c.mu.Unlock()
	cancel()
	if connectErr == nil && closed {
		return context.Canceled
	}
	return connectErr
}

func (c *ClientCore) AttachPacketTunnel(fileDescriptor, mtu int64) error {
	if c == nil || fileDescriptor < 0 || fileDescriptor > int64(^uint(0)>>1) || mtu < 1280 || mtu > 9000 {
		return ErrInvalidTunnelFD
	}
	core, err := c.activeCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return core.AttachPacketTunnel(ctx, int(fileDescriptor), uint32(mtu))
}

func (c *ClientCore) NetworkChanged(operationID string) error {
	core, err := c.activeCore()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = core.NetworkChanged(ctx, operationID)
	return err
}

func (c *ClientCore) ServerAddresses() string {
	core, err := c.activeCore()
	if err != nil {
		return ""
	}
	return strings.Join(core.RuntimeSnapshot().ServerAddresses, ",")
}

func (c *ClientCore) SnapshotJSON() string {
	if c == nil {
		return `{"state":"failed","carrier":"none"}`
	}
	c.mu.Lock()
	core := c.core
	c.mu.Unlock()
	if core == nil {
		return `{"state":"failed","carrier":"none"}`
	}
	snapshot := core.Snapshot()
	runtime := core.RuntimeSnapshot()
	result := struct {
		State                   clienthost.State        `json:"state"`
		ProfileID               string                  `json:"profile_id,omitempty"`
		Carrier                 clienthost.Carrier      `json:"carrier"`
		ConnectedAtUnixMS       int64                   `json:"connected_at_unix_ms"`
		UploadBytesPerSecond    int64                   `json:"upload_bytes_per_second"`
		DownloadBytesPerSecond  int64                   `json:"download_bytes_per_second"`
		UploadTotalBytes        int64                   `json:"upload_total_bytes"`
		DownloadTotalBytes      int64                   `json:"download_total_bytes"`
		Sequence                int64                   `json:"sequence"`
		LastError               *clienthost.PublicError `json:"last_error,omitempty"`
		UDPMode                 string                  `json:"udp_mode,omitempty"`
		CarrierPoolTarget       int64                   `json:"carrier_pool_target"`
		CarrierPoolHealthy      int64                   `json:"carrier_pool_healthy"`
		CarrierPoolAssignments  int64                   `json:"carrier_pool_assignments"`
		QUICMinRTTMS            int64                   `json:"quic_min_rtt_ms"`
		QUICLatestRTTMS         int64                   `json:"quic_latest_rtt_ms"`
		QUICSmoothedRTTMS       int64                   `json:"quic_smoothed_rtt_ms"`
		QUICRTTDeviationMS      int64                   `json:"quic_rtt_deviation_ms"`
		QUICBytesSent           uint64                  `json:"quic_bytes_sent"`
		QUICPacketsSent         uint64                  `json:"quic_packets_sent"`
		QUICBytesReceived       uint64                  `json:"quic_bytes_received"`
		QUICPacketsReceived     uint64                  `json:"quic_packets_received"`
		QUICBytesLost           uint64                  `json:"quic_bytes_lost"`
		QUICPacketsLost         uint64                  `json:"quic_packets_lost"`
		DNSAttributionQueries   uint64                  `json:"dns_attribution_queries"`
		DNSAttributionResponses uint64                  `json:"dns_attribution_responses"`
		DNSAttributionHits      uint64                  `json:"dns_attribution_hits"`
		DNSAttributionMisses    uint64                  `json:"dns_attribution_misses"`
		DNSAttributionCached    uint64                  `json:"dns_attribution_cached"`
		FirstFlightDomainHits   uint64                  `json:"first_flight_domain_hits"`
		FirstFlightFallbacks    uint64                  `json:"first_flight_fallbacks"`
		TCPStreamAttempts       uint64                  `json:"tcp_stream_attempts"`
		TCPStreamSuccesses      uint64                  `json:"tcp_stream_successes"`
		TCPStreamFailures       uint64                  `json:"tcp_stream_failures"`
		TCPStreamOpenLastMS     uint64                  `json:"tcp_stream_open_last_ms"`
		TCPStreamOpenMaxMS      uint64                  `json:"tcp_stream_open_max_ms"`
		ActiveStreams           uint64                  `json:"active_streams"`
		FlowControlStalls       uint64                  `json:"flow_control_stalls"`
		ProtocolErrors          uint64                  `json:"protocol_errors"`
		SentCells               uint64                  `json:"sent_cells"`
		ReceivedCells           uint64                  `json:"received_cells"`
		SentCellPayloadBytes    uint64                  `json:"sent_cell_payload_bytes"`
		ReceivedPayloadBytes    uint64                  `json:"received_payload_bytes"`
		WindowUpdatesSent       uint64                  `json:"window_updates_sent"`
		WindowUpdatesReceived   uint64                  `json:"window_updates_received"`
		CoverMode               string                  `json:"cover_mode"`
		CoverVariantID          uint8                   `json:"cover_variant_id"`
		CoverRealWireBytes      uint64                  `json:"cover_real_wire_bytes"`
		CoverPaddingBytes       uint64                  `json:"cover_padding_bytes"`
		CoverDummyWireBytes     uint64                  `json:"cover_dummy_wire_bytes"`
		CoverProfileTransitions uint64                  `json:"cover_profile_transitions"`
		CoverBursts             uint64                  `json:"cover_bursts"`
		CoverDummySelected      uint64                  `json:"cover_dummy_selected"`
		CoverDummyRejected      uint64                  `json:"cover_dummy_rejected"`
		CoverAddedDelayUS       uint64                  `json:"cover_added_delay_us"`
		CoverMaxDelayUS         uint64                  `json:"cover_max_delay_us"`
	}{
		State: snapshot.State, ProfileID: snapshot.ProfileID, Carrier: snapshot.Carrier,
		ConnectedAtUnixMS:    snapshot.ConnectedAtUnixMS,
		UploadBytesPerSecond: runtime.UploadBytesPerSecond, DownloadBytesPerSecond: runtime.DownloadBytesPerSecond,
		UploadTotalBytes: runtime.UploadTotalBytes, DownloadTotalBytes: runtime.DownloadTotalBytes,
		Sequence: snapshot.Sequence, LastError: snapshot.LastError, UDPMode: runtime.UDPMode,
		CarrierPoolTarget: runtime.CarrierPoolTarget, CarrierPoolHealthy: runtime.CarrierPoolHealthy,
		CarrierPoolAssignments: runtime.CarrierPoolAssignments,
		QUICMinRTTMS:           runtime.QUICMinRTTMS, QUICLatestRTTMS: runtime.QUICLatestRTTMS,
		QUICSmoothedRTTMS: runtime.QUICSmoothedRTTMS, QUICRTTDeviationMS: runtime.QUICRTTDeviationMS,
		QUICBytesSent: runtime.QUICBytesSent, QUICPacketsSent: runtime.QUICPacketsSent,
		QUICBytesReceived: runtime.QUICBytesReceived, QUICPacketsReceived: runtime.QUICPacketsReceived,
		QUICBytesLost: runtime.QUICBytesLost, QUICPacketsLost: runtime.QUICPacketsLost,
		DNSAttributionQueries: runtime.DNSAttributionQueries, DNSAttributionResponses: runtime.DNSAttributionResponses,
		DNSAttributionHits: runtime.DNSAttributionHits, DNSAttributionMisses: runtime.DNSAttributionMisses,
		DNSAttributionCached:  runtime.DNSAttributionCached,
		FirstFlightDomainHits: runtime.FirstFlightDomainHits, FirstFlightFallbacks: runtime.FirstFlightFallbacks,
		TCPStreamAttempts: runtime.TCPStreamAttempts, TCPStreamSuccesses: runtime.TCPStreamSuccesses,
		TCPStreamFailures: runtime.TCPStreamFailures, TCPStreamOpenLastMS: runtime.TCPStreamOpenLastMS,
		TCPStreamOpenMaxMS: runtime.TCPStreamOpenMaxMS, ActiveStreams: runtime.ActiveStreams,
		FlowControlStalls: runtime.FlowControlStalls, ProtocolErrors: runtime.ProtocolErrors,
		SentCells: runtime.SentCells, ReceivedCells: runtime.ReceivedCells,
		SentCellPayloadBytes: runtime.SentCellPayloadBytes, ReceivedPayloadBytes: runtime.ReceivedPayloadBytes,
		WindowUpdatesSent: runtime.WindowUpdatesSent, WindowUpdatesReceived: runtime.WindowUpdatesReceived,
		CoverMode: runtime.CoverMode, CoverVariantID: runtime.CoverVariantID,
		CoverRealWireBytes: runtime.CoverRealWireBytes, CoverPaddingBytes: runtime.CoverPaddingBytes,
		CoverDummyWireBytes: runtime.CoverDummyWireBytes, CoverProfileTransitions: runtime.CoverProfileTransitions,
		CoverBursts: runtime.CoverBursts, CoverDummySelected: runtime.CoverDummySelected,
		CoverDummyRejected: runtime.CoverDummyRejected, CoverAddedDelayUS: runtime.CoverAddedDelayUS,
		CoverMaxDelayUS: runtime.CoverMaxDelayUS,
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return `{"state":"failed","carrier":"none"}`
	}
	return string(raw)
}

func (c *ClientCore) CatalogJSON() (string, error) {
	core, err := c.activeCore()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	raw, err := core.FetchCatalog(ctx)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (c *ClientCore) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.connected = false
	c.generation++
	cancelOperation := c.cancel
	core := c.core
	c.mu.Unlock()
	if cancelOperation != nil {
		cancelOperation()
	}
	if core == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return core.Close(ctx)
}

func (c *ClientCore) activeCore() (strictClientCoreAPI, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.connected || c.core == nil {
		return nil, ErrNotConnected
	}
	return c.core, nil
}
