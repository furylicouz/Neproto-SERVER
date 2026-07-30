package observability

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cover"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
)

func TestMetricsExposeBoundedDestinationFreePrometheusSnapshot(t *testing.T) {
	metrics := NewServerMetrics()
	metrics.CarrierAccepted(protocol.CarrierHTTP3)
	metrics.AuthenticationFailed(protocol.CarrierHTTP3, 125*time.Millisecond)
	metrics.CarrierAccepted(protocol.CarrierWebRTC)
	metrics.SessionStarted(protocol.CarrierWebRTC, 250*time.Millisecond)
	metrics.SessionEnded(protocol.CarrierWebRTC, session.Stats{
		RemotelyOpenedStreams: 3,
		RetiredStreams:        2,
		ReceivedPayloadBytes:  1024,
		SentCellPayloadBytes:  2048,
		FlowControlStalls:     4,
		ProtocolErrors:        1,
	}, cover.TransportStats{RealWireBytes: 4096, PaddingBytes: 512, DummyWireBytes: 256}, TerminalProtocol)
	metrics.SessionRejected()
	metrics.NetworkChanged()
	metrics.Reconnected()
	metrics.Migrated()
	metrics.ConstellationAdmitted()
	metrics.ConstellationRejected()
	metrics.ConstellationDetached()

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/metrics", nil)
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`np2_server_carrier_attempts_total{carrier="http3"} 1`,
		`np2_server_auth_failures_total{carrier="http3"} 1`,
		`np2_server_selected_carrier_total{carrier="webrtc"} 1`,
		`np2_server_active_sessions 0`,
		`np2_server_completed_sessions_total 1`,
		`np2_server_rejected_sessions_total 1`,
		`np2_server_streams_opened_total 3`,
		`np2_server_streams_retired_total 2`,
		`np2_server_received_payload_bytes_total 1024`,
		`np2_server_sent_payload_bytes_total 2048`,
		`np2_server_flow_control_stalls_total 4`,
		`np2_server_protocol_errors_total 1`,
		`np2_server_cover_real_wire_bytes_total 4096`,
		`np2_server_cover_padding_bytes_total 512`,
		`np2_server_cover_dummy_wire_bytes_total 256`,
		`np2_server_terminal_total{reason="protocol"} 1`,
		`np2_server_network_changes_total 1`,
		`np2_server_reconnects_total 1`,
		`np2_server_migrations_total 1`,
		`np2_server_constellation_admissions_total 1`,
		`np2_server_constellation_rejections_total 1`,
		`np2_server_constellation_active_leases 0`,
		`np2_server_constellation_active_flows 0`,
		`np2_server_auth_duration_seconds_bucket{carrier="webrtc",le="0.5"} 1`,
		`np2_server_auth_duration_seconds_count{carrier="webrtc"} 1`,
		`np2_server_udp_active_associations 0`,
		`go_goroutines `,
		`process_resident_memory_bytes `,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, body)
		}
	}
	for _, forbidden := range []string{"private.example", "secret abc", "10.0.0.1", "payload=", "/private/"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("metrics expose forbidden value %q", forbidden)
		}
	}
}

func TestMetricsExposeResourceLimitStateWithoutUserLabels(t *testing.T) {
	limiter, err := proxy.NewResourceLimiter(proxy.ResourceLimitConfig{
		MaxSessionsPerUser: 1, MaxTCPConnectionsGlobal: 1, MaxTCPConnectionsPerUser: 1,
		MaxUDPAssociationsGlobal: 1, MaxUDPAssociationsPerUser: 1,
		UDPPacketsPerSecondGlobal: 1, UDPPacketsPerSecondPerUser: 1,
		UDPBytesPerSecondGlobal: 1, UDPBytesPerSecondPerUser: 1,
		DNSQueriesPerSecondGlobal: 1, DNSQueriesPerSecondPerUser: 1,
		TargetCreatesPerSecondGlobal: 1, TargetCreatesPerSecondPerUser: 1,
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	if !limiter.AcquireSession("private-user") || limiter.AcquireSession("private-user") {
		t.Fatal("session test setup failed")
	}
	if !limiter.AcquireTCP("private-user") || limiter.AcquireTCP("private-user") {
		t.Fatal("TCP test setup failed")
	}
	if !limiter.AcquireUDP("private-user") || limiter.AcquireUDP("private-user") {
		t.Fatal("UDP test setup failed")
	}
	if !limiter.AllowUDPPacket("private-user", 1, true, false) ||
		limiter.AllowUDPPacket("private-user", 1, false, false) {
		t.Fatal("UDP rate test setup failed")
	}

	metrics := NewServerMetrics()
	metrics.AttachResourceLimiter(limiter)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/metrics", nil)
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, request)
	body := response.Body.String()
	for _, expected := range []string{
		"np2_server_resource_active_sessions 1",
		"np2_server_resource_active_tcp_connections 1",
		"np2_server_resource_active_udp_associations 1",
		"np2_server_user_session_limit_rejects_total 1",
		"np2_server_tcp_limit_rejects_total 1",
		"np2_server_udp_global_user_limit_rejects_total 1",
		"np2_server_resource_udp_rate_limit_rejects_total 1",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("resource metrics missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "private-user") {
		t.Fatal("resource metrics exposed a credential ID")
	}
}

func TestMetricsAreConcurrencySafeAndNeverUnderflowActiveSessions(t *testing.T) {
	metrics := NewServerMetrics()
	const workers = 64
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			metrics.CarrierAccepted(protocol.CarrierHTTPS)
			metrics.SessionStarted(protocol.CarrierHTTPS, time.Millisecond)
			metrics.SessionEnded(protocol.CarrierHTTPS, session.Stats{}, cover.TransportStats{}, TerminalNormal)
		}()
	}
	wait.Wait()
	// A duplicate terminal callback is tolerated without wrapping the gauge.
	metrics.SessionEnded(protocol.CarrierHTTPS, session.Stats{}, cover.TransportStats{}, TerminalTransport)
	snapshot := metrics.Snapshot()
	if snapshot.ActiveSessions != 0 || snapshot.CompletedSessions != workers+1 ||
		snapshot.CarrierAttempts[carrierHTTPS] != workers {
		t.Fatalf("unexpected concurrent snapshot: %+v", snapshot)
	}
}

func TestTerminalReasonIsStableAndSanitized(t *testing.T) {
	tests := []struct {
		err  error
		want TerminalReason
	}{
		{nil, TerminalNormal},
		{errors.New("protocol violation: private.example:443"), TerminalProtocol},
		{errors.New("authentication failed for secret abc"), TerminalAuthentication},
		{errors.New("use of closed network connection"), TerminalTransport},
	}
	for _, test := range tests {
		if got := ClassifyTerminalError(test.err); got != test.want {
			t.Fatalf("error=%v reason=%q want=%q", test.err, got, test.want)
		}
	}
}
