package observability

import (
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"neproto.local/chameleon/internal/cover"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
)

const (
	carrierHTTPS = iota
	carrierWebRTC
	carrierHTTP3
	carrierCount
)

var (
	carrierNames  = [carrierCount]string{"https", "webrtc", "http3"}
	authBuckets   = [...]float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	terminalNames = [...]TerminalReason{
		TerminalNormal,
		TerminalAuthentication,
		TerminalProtocol,
		TerminalTransport,
		TerminalShutdown,
	}
)

// TerminalReason is a bounded, destination-free reason label. Arbitrary error
// strings are never used as metric labels, which prevents both cardinality
// growth and accidental disclosure of traffic metadata.
type TerminalReason string

const (
	TerminalNormal         TerminalReason = "normal"
	TerminalAuthentication TerminalReason = "authentication"
	TerminalProtocol       TerminalReason = "protocol"
	TerminalTransport      TerminalReason = "transport"
	TerminalShutdown       TerminalReason = "shutdown"
)

type durationHistogram struct {
	buckets [len(authBuckets)]atomic.Uint64
	count   atomic.Uint64
	sumNS   atomic.Uint64
}

func (h *durationHistogram) observe(duration time.Duration) {
	if duration < 0 {
		duration = 0
	}
	seconds := duration.Seconds()
	for index, upperBound := range authBuckets {
		if seconds <= upperBound {
			h.buckets[index].Add(1)
		}
	}
	h.count.Add(1)
	h.sumNS.Add(uint64(duration))
}

// ServerMetrics owns process-lifetime, lock-free aggregate counters. It never
// stores credentials, destinations, private paths, payloads, or peer addresses.
type ServerMetrics struct {
	carrierAttempts         [carrierCount]atomic.Uint64
	authFailures            [carrierCount]atomic.Uint64
	selectedCarrier         [carrierCount]atomic.Uint64
	authDuration            [carrierCount]durationHistogram
	activeSessions          atomic.Int64
	completed               atomic.Uint64
	rejected                atomic.Uint64
	streamsOpened           atomic.Uint64
	streamsRetired          atomic.Uint64
	receivedPayload         atomic.Uint64
	sentPayload             atomic.Uint64
	flowStalls              atomic.Uint64
	protocolErrors          atomic.Uint64
	coverRealWire           atomic.Uint64
	coverPadding            atomic.Uint64
	coverDummy              atomic.Uint64
	terminal                [len(terminalNames)]atomic.Uint64
	networkChanges          atomic.Uint64
	reconnects              atomic.Uint64
	migrations              atomic.Uint64
	constellationAdmissions atomic.Uint64
	constellationRejections atomic.Uint64
	constellationActive     atomic.Int64
	resourceLimiter         atomic.Pointer[proxy.ResourceLimiter]
	continuityRuntime       atomic.Pointer[proxy.ContinuityRuntime]
	UDP                     proxy.UDPStatistics
}

// ServerSnapshot is intended for tests and in-process diagnostics. Arrays use
// the fixed carrier order HTTPS, WebRTC, HTTP/3.
type ServerSnapshot struct {
	CarrierAttempts           [carrierCount]uint64
	AuthenticationErr         [carrierCount]uint64
	SelectedCarrier           [carrierCount]uint64
	ActiveSessions            int64
	CompletedSessions         uint64
	RejectedSessions          uint64
	StreamsOpened             uint64
	StreamsRetired            uint64
	ReceivedPayload           uint64
	SentPayload               uint64
	FlowControlStalls         uint64
	ProtocolErrors            uint64
	CoverRealWire             uint64
	CoverPadding              uint64
	CoverDummy                uint64
	NetworkChanges            uint64
	Reconnects                uint64
	Migrations                uint64
	ConstellationAdmissions   uint64
	ConstellationRejections   uint64
	ConstellationActiveLeases int64
	ConstellationActiveFlows  int
	UDP                       proxy.UDPStatsSnapshot
	Resources                 proxy.ResourceLimitSnapshot
}

func NewServerMetrics() *ServerMetrics { return &ServerMetrics{} }

func (m *ServerMetrics) AttachResourceLimiter(limiter *proxy.ResourceLimiter) {
	if m != nil {
		m.resourceLimiter.Store(limiter)
	}
}

func (m *ServerMetrics) AttachContinuityRuntime(runtime *proxy.ContinuityRuntime) {
	if m != nil {
		m.continuityRuntime.Store(runtime)
	}
}

func (m *ServerMetrics) CarrierAccepted(kind protocol.CarrierKind) {
	if m != nil {
		m.carrierAttempts[carrierIndex(kind)].Add(1)
	}
}

func (m *ServerMetrics) AuthenticationFailed(kind protocol.CarrierKind, duration time.Duration) {
	if m == nil {
		return
	}
	index := carrierIndex(kind)
	m.authFailures[index].Add(1)
	m.authDuration[index].observe(duration)
}

func (m *ServerMetrics) SessionStarted(kind protocol.CarrierKind, authenticationDuration time.Duration) {
	if m == nil {
		return
	}
	index := carrierIndex(kind)
	m.selectedCarrier[index].Add(1)
	m.authDuration[index].observe(authenticationDuration)
	m.activeSessions.Add(1)
}

func (m *ServerMetrics) SessionEnded(
	_ protocol.CarrierKind,
	stats session.Stats,
	coverStats cover.TransportStats,
	reason TerminalReason,
) {
	if m == nil {
		return
	}
	for {
		active := m.activeSessions.Load()
		if active <= 0 || m.activeSessions.CompareAndSwap(active, active-1) {
			break
		}
	}
	m.completed.Add(1)
	m.streamsOpened.Add(stats.RemotelyOpenedStreams)
	m.streamsRetired.Add(stats.RetiredStreams)
	m.receivedPayload.Add(stats.ReceivedPayloadBytes)
	m.sentPayload.Add(stats.SentCellPayloadBytes)
	m.flowStalls.Add(stats.FlowControlStalls)
	m.protocolErrors.Add(stats.ProtocolErrors)
	m.coverRealWire.Add(coverStats.RealWireBytes)
	m.coverPadding.Add(coverStats.PaddingBytes)
	m.coverDummy.Add(coverStats.DummyWireBytes)
	m.terminal[terminalIndex(reason)].Add(1)
}

func (m *ServerMetrics) SessionRejected() {
	if m != nil {
		m.rejected.Add(1)
	}
}

func (m *ServerMetrics) NetworkChanged() {
	if m != nil {
		m.networkChanges.Add(1)
	}
}

func (m *ServerMetrics) Reconnected() {
	if m != nil {
		m.reconnects.Add(1)
	}
}

func (m *ServerMetrics) Migrated() {
	if m != nil {
		m.migrations.Add(1)
	}
}

func (m *ServerMetrics) ConstellationAdmitted() {
	if m != nil {
		m.constellationAdmissions.Add(1)
		m.constellationActive.Add(1)
	}
}

func (m *ServerMetrics) ConstellationRejected() {
	if m != nil {
		m.constellationRejections.Add(1)
	}
}

func (m *ServerMetrics) ConstellationDetached() {
	if m == nil {
		return
	}
	for {
		active := m.constellationActive.Load()
		if active <= 0 || m.constellationActive.CompareAndSwap(active, active-1) {
			return
		}
	}
}

func (m *ServerMetrics) Snapshot() ServerSnapshot {
	var snapshot ServerSnapshot
	if m == nil {
		return snapshot
	}
	for index := range carrierCount {
		snapshot.CarrierAttempts[index] = m.carrierAttempts[index].Load()
		snapshot.AuthenticationErr[index] = m.authFailures[index].Load()
		snapshot.SelectedCarrier[index] = m.selectedCarrier[index].Load()
	}
	snapshot.ActiveSessions = m.activeSessions.Load()
	snapshot.CompletedSessions = m.completed.Load()
	snapshot.RejectedSessions = m.rejected.Load()
	snapshot.StreamsOpened = m.streamsOpened.Load()
	snapshot.StreamsRetired = m.streamsRetired.Load()
	snapshot.ReceivedPayload = m.receivedPayload.Load()
	snapshot.SentPayload = m.sentPayload.Load()
	snapshot.FlowControlStalls = m.flowStalls.Load()
	snapshot.ProtocolErrors = m.protocolErrors.Load()
	snapshot.CoverRealWire = m.coverRealWire.Load()
	snapshot.CoverPadding = m.coverPadding.Load()
	snapshot.CoverDummy = m.coverDummy.Load()
	snapshot.NetworkChanges = m.networkChanges.Load()
	snapshot.Reconnects = m.reconnects.Load()
	snapshot.Migrations = m.migrations.Load()
	snapshot.ConstellationAdmissions = m.constellationAdmissions.Load()
	snapshot.ConstellationRejections = m.constellationRejections.Load()
	snapshot.ConstellationActiveLeases = nonNegative(m.constellationActive.Load())
	if runtime := m.continuityRuntime.Load(); runtime != nil {
		snapshot.ConstellationActiveFlows = runtime.Count()
	}
	snapshot.UDP = m.UDP.Snapshot()
	if limiter := m.resourceLimiter.Load(); limiter != nil {
		snapshot.Resources = limiter.Snapshot()
	}
	return snapshot
}

func (m *ServerMetrics) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.Path != "/metrics" {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(m.prometheusSnapshot()))
}

func (m *ServerMetrics) prometheusSnapshot() string {
	if m == nil {
		m = NewServerMetrics()
	}
	var output strings.Builder
	writeMetricHeader(&output, "np2_server_carrier_attempts_total", "Accepted carrier connections before NP/2 authentication.", "counter")
	for index, name := range carrierNames {
		writeLabeled(&output, "np2_server_carrier_attempts_total", "carrier", name, m.carrierAttempts[index].Load())
	}
	writeMetricHeader(&output, "np2_server_auth_failures_total", "Failed NP/2 authentication attempts.", "counter")
	for index, name := range carrierNames {
		writeLabeled(&output, "np2_server_auth_failures_total", "carrier", name, m.authFailures[index].Load())
	}
	writeMetricHeader(&output, "np2_server_selected_carrier_total", "Authenticated sessions by carrier.", "counter")
	for index, name := range carrierNames {
		writeLabeled(&output, "np2_server_selected_carrier_total", "carrier", name, m.selectedCarrier[index].Load())
	}
	writeMetricHeader(&output, "np2_server_auth_duration_seconds", "NP/2 authentication latency.", "histogram")
	for carrierIndex, name := range carrierNames {
		histogram := &m.authDuration[carrierIndex]
		for bucketIndex, upperBound := range authBuckets {
			fmt.Fprintf(&output, "np2_server_auth_duration_seconds_bucket{carrier=%q,le=%q} %d\n",
				name, strconv.FormatFloat(upperBound, 'g', -1, 64), histogram.buckets[bucketIndex].Load())
		}
		count := histogram.count.Load()
		fmt.Fprintf(&output, "np2_server_auth_duration_seconds_bucket{carrier=%q,le=\"+Inf\"} %d\n", name, count)
		fmt.Fprintf(&output, "np2_server_auth_duration_seconds_sum{carrier=%q} %s\n", name,
			strconv.FormatFloat(float64(histogram.sumNS.Load())/float64(time.Second), 'g', -1, 64))
		fmt.Fprintf(&output, "np2_server_auth_duration_seconds_count{carrier=%q} %d\n", name, count)
	}
	writeGauge(&output, "np2_server_active_sessions", "Currently authenticated NP/2 sessions.", nonNegative(m.activeSessions.Load()))
	writeCounter(&output, "np2_server_completed_sessions_total", "Completed authenticated NP/2 sessions.", m.completed.Load())
	writeCounter(&output, "np2_server_rejected_sessions_total", "Carrier connections rejected by global or per-user session capacity limits.", m.rejected.Load())
	writeCounter(&output, "np2_server_streams_opened_total", "Remotely opened NP/2 streams.", m.streamsOpened.Load())
	writeCounter(&output, "np2_server_streams_retired_total", "Retired NP/2 streams.", m.streamsRetired.Load())
	writeCounter(&output, "np2_server_received_payload_bytes_total", "Received inner NP/2 cell payload bytes.", m.receivedPayload.Load())
	writeCounter(&output, "np2_server_sent_payload_bytes_total", "Sent inner NP/2 cell payload bytes.", m.sentPayload.Load())
	writeCounter(&output, "np2_server_flow_control_stalls_total", "NP/2 stream flow-control stalls.", m.flowStalls.Load())
	writeCounter(&output, "np2_server_protocol_errors_total", "NP/2 multiplexer protocol errors.", m.protocolErrors.Load())
	writeCounter(&output, "np2_server_cover_real_wire_bytes_total", "NP/2 real cell bytes before carrier framing.", m.coverRealWire.Load())
	writeCounter(&output, "np2_server_cover_padding_bytes_total", "NP/2 padding bytes inside real cells.", m.coverPadding.Load())
	writeCounter(&output, "np2_server_cover_dummy_wire_bytes_total", "NP/2 dummy cell bytes before carrier framing.", m.coverDummy.Load())
	writeMetricHeader(&output, "np2_server_terminal_total", "Sanitized authenticated-session terminal reasons.", "counter")
	for index, reason := range terminalNames {
		writeLabeled(&output, "np2_server_terminal_total", "reason", string(reason), m.terminal[index].Load())
	}
	writeCounter(&output, "np2_server_network_changes_total", "Observed client network changes.", m.networkChanges.Load())
	writeCounter(&output, "np2_server_reconnects_total", "Completed NP/2 reconnects.", m.reconnects.Load())
	writeCounter(&output, "np2_server_migrations_total", "Completed carrier migrations.", m.migrations.Load())
	writeCounter(&output, "np2_server_constellation_admissions_total", "Authenticated carrier leases admitted to NP/2 constellations.", m.constellationAdmissions.Load())
	writeCounter(&output, "np2_server_constellation_rejections_total", "Authenticated carrier leases rejected during bounded constellation admission.", m.constellationRejections.Load())
	writeGauge(&output, "np2_server_constellation_active_leases", "Currently admitted destination-free NP/2 carrier leases.", nonNegative(m.constellationActive.Load()))
	activeFlows := int64(0)
	if runtime := m.continuityRuntime.Load(); runtime != nil {
		activeFlows = int64(runtime.Count())
	}
	writeGauge(&output, "np2_server_constellation_active_flows", "Active logical continuity flows without destination labels.", activeFlows)
	m.writeUDPMetrics(&output)
	m.writeResourceMetrics(&output)
	writeGauge(&output, "go_goroutines", "Current Go goroutines.", int64(runtime.NumGoroutine()))
	writeGauge(&output, "process_resident_memory_bytes", "Best-effort process resident memory.", int64(residentMemoryBytes()))
	writeGauge(&output, "process_open_fds", "Best-effort process open file descriptors.", int64(openFileDescriptors()))
	return output.String()
}

func (m *ServerMetrics) writeResourceMetrics(output *strings.Builder) {
	var snapshot proxy.ResourceLimitSnapshot
	if limiter := m.resourceLimiter.Load(); limiter != nil {
		snapshot = limiter.Snapshot()
	}
	writeGauge(output, "np2_server_resource_active_sessions", "Authenticated sessions accounted by the per-user limiter.", snapshot.ActiveSessions)
	writeGauge(output, "np2_server_resource_active_tcp_connections", "Active target TCP connections across authenticated users.", snapshot.ActiveTCPConnections)
	writeGauge(output, "np2_server_resource_active_udp_associations", "Active UDP associations across authenticated users.", snapshot.ActiveUDPAssociations)
	writeCounter(output, "np2_server_user_session_limit_rejects_total", "Authenticated sessions rejected by a per-user limit.", snapshot.SessionLimitRejects)
	writeCounter(output, "np2_server_tcp_limit_rejects_total", "TCP target connections rejected by global, per-user, or creation-rate limits.", snapshot.TCPLimitRejects)
	writeCounter(output, "np2_server_udp_global_user_limit_rejects_total", "UDP associations rejected by global or per-user limits.", snapshot.UDPAssociationLimitRejects)
	writeCounter(output, "np2_server_resource_udp_rate_limit_rejects_total", "UDP datagrams rejected by packet, byte, DNS, or target-creation rates.", snapshot.UDPRateLimitDrops)
	writeCounter(output, "np2_server_dns_rate_limit_rejects_total", "UDP DNS datagrams rejected by the DNS rate limit.", snapshot.DNSRateLimitDrops)
	writeCounter(output, "np2_server_target_creation_rate_limit_rejects_total", "TCP or UDP target creations rejected by rate limits.", snapshot.TargetRateLimitDrops)
}

func (m *ServerMetrics) writeUDPMetrics(output *strings.Builder) {
	snapshot := m.UDP.Snapshot()
	writeGauge(output, "np2_server_udp_active_associations", "Current UDP associations.", snapshot.ActiveAssociations)
	writeCounter(output, "np2_server_udp_opened_associations_total", "Opened UDP associations.", snapshot.OpenedAssociations)
	writeCounter(output, "np2_server_udp_association_limit_rejects_total", "UDP associations rejected by limits.", snapshot.AssociationLimitRejects)
	writeCounter(output, "np2_server_udp_client_datagrams_total", "UDP datagrams received from clients.", snapshot.ClientDatagrams)
	writeCounter(output, "np2_server_udp_target_datagrams_total", "UDP datagrams received from targets.", snapshot.TargetDatagrams)
	writeCounter(output, "np2_server_udp_client_bytes_total", "UDP bytes received from clients.", snapshot.ClientBytes)
	writeCounter(output, "np2_server_udp_target_bytes_total", "UDP bytes received from targets.", snapshot.TargetBytes)
	writeCounter(output, "np2_server_udp_policy_drops_total", "UDP datagrams dropped by traffic policy.", snapshot.PolicyDrops)
	writeCounter(output, "np2_server_udp_target_limit_drops_total", "UDP datagrams dropped by target limits.", snapshot.TargetLimitDrops)
	writeCounter(output, "np2_server_udp_unexpected_source_drops_total", "UDP datagrams dropped for unexpected source.", snapshot.UnexpectedSourceDrops)
	writeCounter(output, "np2_server_udp_oversized_drops_total", "Oversized UDP datagrams dropped.", snapshot.OversizedDrops)
	writeCounter(output, "np2_server_udp_idle_expirations_total", "UDP associations closed by idle timeout.", snapshot.IdleExpirations)
	writeCounter(output, "np2_server_udp_relay_errors_total", "UDP relay errors.", snapshot.RelayErrors)
	writeCounter(output, "np2_server_udp_rate_limit_drops_total", "UDP datagrams dropped by resource rate limits.", snapshot.RateLimitDrops)
	writeCounter(output, "np2_server_udp_amplification_drops_total", "First UDP replies dropped by anti-amplification accounting.", snapshot.AmplificationDrops)
}

func ClassifyTerminalError(err error) TerminalReason {
	if err == nil {
		return TerminalNormal
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "auth"), strings.Contains(message, "credential"):
		return TerminalAuthentication
	case strings.Contains(message, "protocol"), strings.Contains(message, "unknown stream"),
		strings.Contains(message, "invalid cell"), strings.Contains(message, "replay"):
		return TerminalProtocol
	case strings.Contains(message, "context canceled"), strings.Contains(message, "shutdown"):
		return TerminalShutdown
	default:
		return TerminalTransport
	}
}

func carrierIndex(kind protocol.CarrierKind) int {
	switch kind {
	case protocol.CarrierWebRTC:
		return carrierWebRTC
	case protocol.CarrierHTTP3:
		return carrierHTTP3
	default:
		return carrierHTTPS
	}
}

func terminalIndex(reason TerminalReason) int {
	for index, candidate := range terminalNames {
		if reason == candidate {
			return index
		}
	}
	return terminalIndex(TerminalTransport)
}

func writeMetricHeader(output *strings.Builder, name, help, kind string) {
	fmt.Fprintf(output, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

func writeCounter(output *strings.Builder, name, help string, value uint64) {
	writeMetricHeader(output, name, help, "counter")
	fmt.Fprintf(output, "%s %d\n", name, value)
}

func writeGauge(output *strings.Builder, name, help string, value int64) {
	writeMetricHeader(output, name, help, "gauge")
	fmt.Fprintf(output, "%s %d\n", name, value)
}

func writeLabeled(output *strings.Builder, name, label, value string, count uint64) {
	fmt.Fprintf(output, "%s{%s=%q} %d\n", name, label, value, count)
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
