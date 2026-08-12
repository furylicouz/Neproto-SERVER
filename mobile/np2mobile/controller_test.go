package np2mobile

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/cover"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/tunstack"
)

func TestControllerConnectsStartsTunnelOnceAndStops(t *testing.T) {
	runtime := newStubRuntime()
	controller := newController(func(context.Context, config.Client) (mobileRuntime, error) {
		return runtime, nil
	})

	if err := controller.start(validProfile(), validSecret()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if got := controller.stateName(); got != "connected" {
		t.Fatalf("state=%q, want connected", got)
	}
	if got := controller.serverAddresses(); got != "104.171.136.10" {
		t.Fatalf("server routes=%q", got)
	}
	if err := controller.startTunnel(42); err != nil {
		t.Fatalf("start TUN: %v", err)
	}
	if got := controller.stateName(); got != "running" {
		t.Fatalf("state=%q, want running", got)
	}
	if runtime.startedFD != 42 {
		t.Fatalf("TUN fd=%d, want 42", runtime.startedFD)
	}
	if err := controller.startTunnel(43); !errors.Is(err, ErrTunnelAlreadyStarted) {
		t.Fatalf("duplicate TUN start error=%v", err)
	}
	if err := controller.start(validProfile(), validSecret()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate connect error=%v", err)
	}

	controller.stop()
	select {
	case <-runtime.closed:
	case <-time.After(time.Second):
		t.Fatal("runtime was not closed")
	}
	if got := controller.stateName(); got != "stopped" {
		t.Fatalf("state=%q, want stopped", got)
	}
}

func TestControllerStopReturnsWithoutWaitingForRuntimeCleanup(t *testing.T) {
	runtime := newBlockingCloseRuntime()
	controller := newController(func(context.Context, config.Client) (mobileRuntime, error) {
		return runtime, nil
	})
	if err := controller.start(validProfile(), validSecret()); err != nil {
		t.Fatal(err)
	}

	returned := make(chan struct{})
	go func() {
		controller.stop()
		close(returned)
	}()

	select {
	case <-runtime.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("runtime cleanup was not started")
	}
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		close(runtime.releaseClose)
		<-returned
		t.Fatal("stop waited for runtime cleanup")
	}
	if got := controller.stateName(); got != "stopped" {
		t.Fatalf("state=%q, want stopped", got)
	}
	close(runtime.releaseClose)
}

func TestControllerReportsLiveTrafficOnlyForActiveRuntime(t *testing.T) {
	runtime := newStubRuntime()
	runtime.uploadBytesPerSecond = 128_000
	runtime.downloadBytesPerSecond = 512_000
	runtime.uploadTotalBytes = 1_024_000
	runtime.downloadTotalBytes = 4_096_000
	runtime.udpMode = "reliable-stream-quic-fallback"
	runtime.quicFallbacks = 7
	runtime.carrierPoolTarget = 3
	runtime.carrierPoolHealthy = 2
	runtime.carrierPoolAssignments = 19
	runtime.carrierPoolScaleUps = 1
	runtime.carrierPoolFailures = 0
	runtime.dnsAttributionQueries = 8
	runtime.dnsAttributionResponses = 7
	runtime.dnsAttributionHits = 5
	runtime.dnsAttributionMisses = 3
	runtime.dnsAttributionCached = 6
	runtime.firstFlightDomainHits = 4
	runtime.firstFlightFallbacks = 2
	runtime.tcpStreamAttempts = 14
	runtime.tcpStreamSuccesses = 12
	runtime.tcpStreamFailures = 2
	runtime.activeStreams = 9
	runtime.flowControlStalls = 11
	runtime.protocolErrors = 1
	runtime.sentCells = 1_200
	runtime.receivedCells = 4_800
	runtime.sentCellPayloadBytes = 600_000
	runtime.receivedPayloadBytes = 8_000_000
	runtime.windowUpdatesSent = 31
	runtime.windowUpdatesReceived = 17
	runtime.coverRealWireBytes = 8_500_000
	runtime.coverPaddingBytes = 250_000
	runtime.coverDummyWireBytes = 50_000
	runtime.coverProfileTransitions = 6
	runtime.coverWebSessions = 1
	runtime.coverRealtimeSessions = 2
	runtime.coverStreamSessions = 3
	controller := newController(func(context.Context, config.Client) (mobileRuntime, error) {
		return runtime, nil
	})

	if got := controller.trafficStats(); got != (trafficStats{}) {
		t.Fatalf("stopped stats=%+v", got)
	}
	if err := controller.start(validProfile(), validSecret()); err != nil {
		t.Fatal(err)
	}
	got := controller.trafficStats()
	if got.UploadBytesPerSecond != 128_000 || got.DownloadBytesPerSecond != 512_000 ||
		got.UploadTotalBytes != 1_024_000 || got.DownloadTotalBytes != 4_096_000 ||
		got.UDPMode != "reliable-stream-quic-fallback" || got.QUICFallbacks != 7 ||
		got.CarrierPoolTarget != 3 || got.CarrierPoolHealthy != 2 ||
		got.CarrierPoolAssignments != 19 || got.CarrierPoolScaleUps != 1 ||
		got.CarrierPoolFailures != 0 || got.DNSAttributionQueries != 8 ||
		got.DNSAttributionResponses != 7 || got.DNSAttributionHits != 5 ||
		got.DNSAttributionMisses != 3 || got.DNSAttributionCached != 6 ||
		got.FirstFlightDomainHits != 4 || got.FirstFlightFallbacks != 2 ||
		got.TCPStreamAttempts != 14 || got.TCPStreamSuccesses != 12 || got.TCPStreamFailures != 2 ||
		got.ActiveStreams != 9 || got.FlowControlStalls != 11 || got.ProtocolErrors != 1 ||
		got.SentCells != 1_200 || got.ReceivedCells != 4_800 ||
		got.SentCellPayloadBytes != 600_000 || got.ReceivedPayloadBytes != 8_000_000 ||
		got.WindowUpdatesSent != 31 || got.WindowUpdatesReceived != 17 ||
		got.CoverRealWireBytes != 8_500_000 || got.CoverPaddingBytes != 250_000 ||
		got.CoverDummyWireBytes != 50_000 || got.CoverProfileTransitions != 6 ||
		got.CoverWebSessions != 1 || got.CoverRealtimeSessions != 2 || got.CoverStreamSessions != 3 {
		t.Fatalf("active stats=%+v", got)
	}
	controller.stop()
	if got := controller.trafficStats(); got != (trafficStats{}) {
		t.Fatalf("stopped stats=%+v", got)
	}
}

func TestAggregateProtocolTrafficStatsIsPayloadAndDestinationFree(t *testing.T) {
	got := aggregateProtocolTrafficStats(
		[]session.Stats{
			{ActiveStreams: 2, SentCells: 10, ReceivedCells: 20, SentCellPayloadBytes: 1_000,
				ReceivedPayloadBytes: 2_000, WindowUpdatesSent: 3, WindowUpdatesReceived: 4,
				FlowControlStalls: 5, ProtocolErrors: 1},
			{ActiveStreams: 3, SentCells: 30, ReceivedCells: 40, SentCellPayloadBytes: 3_000,
				ReceivedPayloadBytes: 4_000, WindowUpdatesSent: 6, WindowUpdatesReceived: 7,
				FlowControlStalls: 8, ProtocolErrors: 2},
		},
		[]cover.TransportStats{
			{RealWireBytes: 2_100, PaddingBytes: 100, DummyWireBytes: 50,
				TrafficClass: cover.TrafficWeb, ProfileTransitions: 2},
			{RealWireBytes: 4_200, PaddingBytes: 200, DummyWireBytes: 75,
				TrafficClass: cover.TrafficStream, ProfileTransitions: 3},
			{TrafficClass: cover.TrafficRealtime, ProfileTransitions: 1},
		},
	)

	if got.ActiveStreams != 5 || got.SentCells != 40 || got.ReceivedCells != 60 ||
		got.SentCellPayloadBytes != 4_000 || got.ReceivedPayloadBytes != 6_000 ||
		got.WindowUpdatesSent != 9 || got.WindowUpdatesReceived != 11 ||
		got.FlowControlStalls != 13 || got.ProtocolErrors != 3 ||
		got.CoverRealWireBytes != 6_300 || got.CoverPaddingBytes != 300 ||
		got.CoverDummyWireBytes != 125 || got.CoverProfileTransitions != 6 ||
		got.CoverWebSessions != 1 || got.CoverRealtimeSessions != 1 || got.CoverStreamSessions != 1 {
		t.Fatalf("aggregate stats=%+v", got)
	}
}

func TestControllerMigratesRunningRuntimeWithoutStoppingTunnel(t *testing.T) {
	runtime := newStubRuntime()
	runtime.migrationStarted = make(chan struct{})
	runtime.releaseMigration = make(chan struct{})
	runtime.migratedCarrier = "http3"
	controller := newController(func(context.Context, config.Client) (mobileRuntime, error) {
		return runtime, nil
	})
	if err := controller.start(validProfile(), validSecret()); err != nil {
		t.Fatal(err)
	}
	if err := controller.startTunnel(42); err != nil {
		t.Fatal(err)
	}
	migrated := make(chan error, 1)
	go func() { migrated <- controller.networkChanged() }()
	select {
	case <-runtime.migrationStarted:
	case <-time.After(time.Second):
		t.Fatal("runtime migration was not started")
	}
	if got := controller.stateName(); got != "migrating" {
		t.Fatalf("state=%q, want migrating", got)
	}
	if err := controller.networkChanged(); !errors.Is(err, ErrMigrationInProgress) {
		t.Fatalf("concurrent migration error=%v", err)
	}
	close(runtime.releaseMigration)
	if err := <-migrated; err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := controller.stateName(); got != "running" {
		t.Fatalf("state=%q, want running", got)
	}
	if got := controller.carrierName(); got != "http3" {
		t.Fatalf("carrier=%q, want http3", got)
	}
	if controller.networkChangeCount() != 2 || controller.reconnectCount() != 1 ||
		controller.migrationCount() != 1 {
		t.Fatalf("migration counters changes=%d reconnects=%d migrations=%d",
			controller.networkChangeCount(), controller.reconnectCount(), controller.migrationCount())
	}
	if got := controller.trafficStats(); got != (trafficStats{}) {
		// The stub reports zeros, but it remains available through the same runtime.
		t.Fatalf("traffic stats after migration=%+v", got)
	}
}

func TestControllerReportsCarrierPromotedInsideRuntime(t *testing.T) {
	runtime := newStubRuntime()
	controller := newController(nil)
	controller.state = "running"
	controller.runtime = runtime
	controller.carrier = "webrtc"

	runtime.mu.Lock()
	runtime.carrier = "https"
	runtime.mu.Unlock()

	if got := controller.carrierName(); got != "https" {
		t.Fatalf("carrier=%q, want promoted runtime carrier https", got)
	}
}

func TestControllerFailedMigrationKeepsLiveRuntimeRunning(t *testing.T) {
	runtime := newStubRuntime()
	runtime.migrationErr = errors.New("new path unavailable")
	controller := newController(func(context.Context, config.Client) (mobileRuntime, error) {
		return runtime, nil
	})
	if err := controller.start(validProfile(), validSecret()); err != nil {
		t.Fatal(err)
	}
	if err := controller.startTunnel(42); err != nil {
		t.Fatal(err)
	}
	if err := controller.networkChanged(); !errors.Is(err, runtime.migrationErr) {
		t.Fatalf("migration error=%v", err)
	}
	if got := controller.stateName(); got != "running" {
		t.Fatalf("state=%q, want running", got)
	}
	if !strings.Contains(controller.lastError(), "new path unavailable") {
		t.Fatalf("last error=%q", controller.lastError())
	}
	select {
	case <-runtime.closed:
		t.Fatal("failed migration closed still-live runtime")
	default:
	}
}

func TestControllerReturnsConnectionFailure(t *testing.T) {
	want := errors.New("carrier unavailable")
	controller := newController(func(context.Context, config.Client) (mobileRuntime, error) {
		return nil, want
	})

	err := controller.start(validProfile(), validSecret())
	if !errors.Is(err, want) {
		t.Fatalf("start error=%v, want %v", err, want)
	}
	if got := controller.stateName(); got != "failed" {
		t.Fatalf("state=%q, want failed", got)
	}
	if got := controller.lastError(); got != want.Error() {
		t.Fatalf("last error=%q", got)
	}
}

func TestMobileConnectDeadlineCoversSequentialCarrierFallbacks(t *testing.T) {
	clientConfig := config.Client{
		HTTPSURL:           "wss://vpn.example.test/private/https/session",
		HTTPSTimeout:       config.Duration{Duration: 10 * time.Second},
		HTTP3URL:           "https://vpn.example.test/private/http3/session",
		HTTP3Timeout:       config.Duration{Duration: 8 * time.Second},
		WebRTCTimeout:      config.Duration{Duration: 5 * time.Second},
		CarrierCacheTTL:    config.Duration{Duration: 10 * time.Minute},
		ServerIdentity:     "vpn.example.test",
		WebRTCSignalingURL: "https://vpn.example.test/private/webrtc/session",
	}
	if got, want := mobileConnectDeadline(clientConfig), 26*time.Second; got != want {
		t.Fatalf("HTTP/3 deadline=%s, want %s", got, want)
	}
	clientConfig.CarrierPolicy = config.CarrierPolicyHTTP3Only
	if got, want := mobileConnectDeadline(clientConfig), 11*time.Second; got != want {
		t.Fatalf("HTTP/3-only deadline=%s, want %s", got, want)
	}
	clientConfig.CarrierPolicy = config.CarrierPolicyPerformance
	clientConfig.HTTP3URL = ""
	clientConfig.HTTP3Timeout.Duration = 0
	if got, want := mobileConnectDeadline(clientConfig), 18*time.Second; got != want {
		t.Fatalf("dual-carrier deadline=%s, want %s", got, want)
	}
}

func TestControllerClosesRuntimeWhenTunnelStartFails(t *testing.T) {
	runtime := newStubRuntime()
	runtime.startErr = errors.New("invalid utun")
	controller := newController(func(context.Context, config.Client) (mobileRuntime, error) {
		return runtime, nil
	})
	if err := controller.start(validProfile(), validSecret()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := controller.startTunnel(7); !errors.Is(err, runtime.startErr) {
		t.Fatalf("TUN start error=%v", err)
	}
	if got := controller.stateName(); got != "failed" {
		t.Fatalf("state=%q, want failed", got)
	}
	select {
	case <-runtime.closed:
	case <-time.After(time.Second):
		t.Fatal("failed runtime was not closed")
	}
}

func TestRuntimeClosesNP2SessionBeforeWaitingForTunnelStack(t *testing.T) {
	var mu sync.Mutex
	order := make([]string, 0, 2)
	session := closerFunc(func() error {
		mu.Lock()
		order = append(order, "session")
		mu.Unlock()
		return nil
	})
	stack := closerFunc(func() error {
		mu.Lock()
		order = append(order, "stack")
		mu.Unlock()
		return nil
	})

	if err := closeRuntimeResources(stack, session); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "session" || order[1] != "stack" {
		t.Fatalf("shutdown order=%v, want [session stack]", order)
	}
}

func TestNP2RuntimeUsesNativePathWhenAuthenticatedPingSurvives(t *testing.T) {
	clientMux, serverMux, _ := newMigrationMuxPair(t, protocol.CarrierHTTP3)
	connectorCalls := 0
	runtime, err := newNP2Runtime(
		config.Client{ServerAddresses: []netip.Addr{netip.MustParseAddr("104.171.136.10")}},
		&session.Authenticated{Mux: clientMux, Carrier: protocol.CarrierHTTP3},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			connectorCalls++
			return nil, errors.New("reconnect must not run")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = serverMux.Close()
	})
	result, err := runtime.NetworkChanged(context.Background())
	if err != nil || !result.NativePath || !result.Migrated || result.Reconnected {
		t.Fatalf("native migration result=%+v error=%v", result, err)
	}
	if connectorCalls != 0 {
		t.Fatalf("connector calls=%d", connectorCalls)
	}
}

func TestNP2RuntimeWarmsIndependentHTTPSCarrierPool(t *testing.T) {
	primaryClient, primaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	secondaryClient, secondaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	connectorCalls := 0
	runtime, err := newNP2Runtime(
		config.Client{
			ServerAddresses:     []netip.Addr{netip.MustParseAddr("104.171.136.10")},
			MaxParallelCarriers: 3,
		},
		&session.Authenticated{Mux: primaryClient, Carrier: protocol.CarrierHTTPS},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			connectorCalls++
			return &session.Authenticated{Mux: secondaryClient, Carrier: protocol.CarrierHTTPS}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = primaryServer.Close()
		_ = secondaryServer.Close()
	})
	if err := runtime.warmCarrierPool(context.Background(), 2); err != nil {
		t.Fatalf("warm carrier pool: %v", err)
	}
	target, healthy, assignments, scaleUps, failures := runtime.carrierPoolStats()
	if connectorCalls != 1 || target != 3 || healthy != 2 || assignments != 0 || scaleUps != 1 || failures != 0 {
		t.Fatalf("pool calls=%d target=%d healthy=%d assignments=%d scaleUps=%d failures=%d",
			connectorCalls, target, healthy, assignments, scaleUps, failures)
	}
}

func TestNP2RuntimePromotesWarmHTTP3CarrierOverHTTPS(t *testing.T) {
	primaryClient, primaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	secondaryClient, secondaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTP3)
	compatibilityCalls := 0
	fastCalls := 0
	runtime, err := newNP2RuntimeWithConnectors(
		config.Client{
			ServerAddresses:     []netip.Addr{netip.MustParseAddr("104.171.136.10")},
			MaxParallelCarriers: 3,
		},
		&session.Authenticated{Mux: primaryClient, Carrier: protocol.CarrierHTTPS},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			compatibilityCalls++
			return nil, errors.New("compatibility connector must remain idle during fast promotion")
		},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			fastCalls++
			return &session.Authenticated{Mux: secondaryClient, Carrier: protocol.CarrierHTTP3}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = primaryServer.Close()
		_ = secondaryServer.Close()
	})
	if err := runtime.warmCarrierPool(context.Background(), 2); err != nil {
		t.Fatalf("promote warm HTTP/3 carrier: %v", err)
	}
	runtime.mu.Lock()
	promoted := runtime.session != nil && runtime.session.Mux == secondaryClient &&
		runtime.carrierName == "http3" && runtime.poolTarget == 2 &&
		len(runtime.poolSessions) == 1 && runtime.poolSessions[1] != nil &&
		runtime.poolSessions[1].Mux == primaryClient
	runtime.mu.Unlock()
	if !promoted {
		t.Fatal("authenticated HTTP/3 warm carrier was not promoted to primary")
	}
	if fastCalls != 1 || compatibilityCalls != 0 {
		t.Fatalf("fast calls=%d compatibility calls=%d", fastCalls, compatibilityCalls)
	}
	_, healthy, _, _, failures := runtime.carrierPoolStats()
	if healthy != 2 || failures != 0 {
		t.Fatalf("promoted carrier healthy=%d failures=%d", healthy, failures)
	}
}

func TestNP2RuntimeCloseCancelsPendingPoolDial(t *testing.T) {
	primaryClient, primaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	dialStarted := make(chan struct{})
	runtime, err := newNP2Runtime(
		config.Client{
			ServerAddresses:     []netip.Addr{netip.MustParseAddr("104.171.136.10")},
			MaxParallelCarriers: 3,
		},
		&session.Authenticated{Mux: primaryClient, Carrier: protocol.CarrierHTTPS},
		func(ctx context.Context, _ config.Client) (*session.Authenticated, error) {
			close(dialStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = primaryServer.Close() })
	warmDone := make(chan error, 1)
	go func() { warmDone <- runtime.warmCarrierPool(context.Background(), 2) }()
	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		t.Fatal("pool dial did not start")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-warmDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pool dial cancellation error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pool dial survived runtime close")
	}
}

func TestNP2RuntimeScalesThirdCarrierOnlyUnderLoadAndRetiresItWhenIdle(t *testing.T) {
	primaryClient, primaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	secondaryOne, secondaryServerOne, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	secondaryTwo, secondaryServerTwo, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	connections := []*session.Authenticated{
		{Mux: secondaryOne, Carrier: protocol.CarrierHTTPS},
		{Mux: secondaryTwo, Carrier: protocol.CarrierHTTPS},
	}
	connectorCalls := 0
	runtime, err := newNP2Runtime(
		config.Client{
			ServerAddresses:     []netip.Addr{netip.MustParseAddr("104.171.136.10")},
			MaxParallelCarriers: 3,
		},
		&session.Authenticated{Mux: primaryClient, Carrier: protocol.CarrierHTTPS},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			connected := connections[connectorCalls]
			connectorCalls++
			return connected, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = primaryServer.Close()
		_ = secondaryServerOne.Close()
		_ = secondaryServerTwo.Close()
	})
	if err := runtime.reconcileCarrierPool(context.Background(), poolActivity{}); err != nil {
		t.Fatal(err)
	}
	if _, healthy, _, _, _ := runtime.carrierPoolStats(); healthy != 2 || connectorCalls != 1 {
		t.Fatalf("warm pool healthy=%d connector calls=%d", healthy, connectorCalls)
	}
	if err := runtime.reconcileCarrierPool(context.Background(), poolActivity{
		BytesPerSecond: carrierPoolHighRate,
	}); err != nil {
		t.Fatal(err)
	}
	if _, healthy, _, _, _ := runtime.carrierPoolStats(); healthy != 3 || connectorCalls != 2 {
		t.Fatalf("scaled pool healthy=%d connector calls=%d", healthy, connectorCalls)
	}
	if !runtime.retireIdlePoolMember() {
		t.Fatal("idle third carrier was not retired")
	}
	if _, healthy, _, _, _ := runtime.carrierPoolStats(); healthy != 2 {
		t.Fatalf("retired pool healthy=%d, want 2", healthy)
	}
}

func TestNP2RuntimeSecondaryFailureDoesNotStopHealthyPrimary(t *testing.T) {
	primaryClient, primaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	secondaryClient, secondaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	runtime, err := newNP2Runtime(
		config.Client{
			ServerAddresses:     []netip.Addr{netip.MustParseAddr("104.171.136.10")},
			MaxParallelCarriers: 3,
		},
		&session.Authenticated{Mux: primaryClient, Carrier: protocol.CarrierHTTPS},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			return &session.Authenticated{Mux: secondaryClient, Carrier: protocol.CarrierHTTPS}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = primaryServer.Close()
		_ = secondaryServer.Close()
	})
	if err := runtime.warmCarrierPool(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	_ = secondaryClient.Close()
	deadline := time.Now().Add(time.Second)
	for {
		_, healthy, _, _, failures := runtime.carrierPoolStats()
		if healthy == 1 && failures >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("secondary failure was not removed: healthy=%d failures=%d", healthy, failures)
		}
		time.Sleep(time.Millisecond)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := runtime.Wait(waitContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("secondary failure stopped runtime: %v", err)
	}
}

func TestNP2RuntimePromotesWarmCarrierWhenPrimaryFails(t *testing.T) {
	primaryClient, primaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	secondaryClient, secondaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	runtime, err := newNP2Runtime(
		config.Client{
			ServerAddresses:     []netip.Addr{netip.MustParseAddr("104.171.136.10")},
			MaxParallelCarriers: 2,
		},
		&session.Authenticated{Mux: primaryClient, Carrier: protocol.CarrierHTTPS},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			return &session.Authenticated{Mux: secondaryClient, Carrier: protocol.CarrierHTTPS}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = primaryServer.Close()
		_ = secondaryServer.Close()
	})
	if err := runtime.warmCarrierPool(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	_ = primaryClient.Close()
	deadline := time.Now().Add(time.Second)
	for {
		runtime.mu.Lock()
		promoted := runtime.session != nil && runtime.session.Mux == secondaryClient &&
			len(runtime.poolSessions) == 0
		runtime.mu.Unlock()
		if promoted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("warm carrier was not promoted after primary failure")
		}
		time.Sleep(time.Millisecond)
	}
	if healthy, _ := runtime.router.PoolStats(); healthy != 1 {
		t.Fatalf("healthy routes after promotion=%d, want 1", healthy)
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	if err := runtime.Wait(waitContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("primary failure stopped promoted runtime: %v", err)
	}
}

func TestNP2RuntimePromotesHTTPSStandbyWhenWebRTCPrimaryFails(t *testing.T) {
	primaryClient, primaryServer, _ := newMigrationMuxPair(t, protocol.CarrierWebRTC)
	secondaryClient, secondaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	runtime, err := newNP2Runtime(
		config.Client{
			ServerAddresses:     []netip.Addr{netip.MustParseAddr("104.171.136.10")},
			MaxParallelCarriers: 2,
		},
		&session.Authenticated{Mux: primaryClient, Carrier: protocol.CarrierWebRTC},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			return &session.Authenticated{Mux: secondaryClient, Carrier: protocol.CarrierHTTPS}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = primaryServer.Close()
		_ = secondaryServer.Close()
	})
	if err := runtime.warmCarrierPool(context.Background(), 2); err != nil {
		t.Fatalf("warm HTTPS compatibility standby: %v", err)
	}
	if _, healthy, _, _, failures := runtime.carrierPoolStats(); healthy != 2 || failures != 0 {
		t.Fatalf("compatibility pool healthy=%d failures=%d", healthy, failures)
	}

	_ = primaryClient.Close()
	deadline := time.Now().Add(time.Second)
	for runtime.CarrierName() != "https" {
		if time.Now().After(deadline) {
			t.Fatal("HTTPS standby was not promoted after WebRTC failure")
		}
		time.Sleep(time.Millisecond)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := runtime.Wait(waitContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WebRTC failure stopped runtime instead of promoting HTTPS: %v", err)
	}
}

func TestNP2RuntimeReconnectsNewFlowsAndDrainsExistingFlow(t *testing.T) {
	oldClient, oldServer, oldWire := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	newClient, newServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTP3)
	clientConfig := config.Client{ServerAddresses: []netip.Addr{netip.MustParseAddr("104.171.136.10")}}
	runtime, err := newNP2Runtime(
		clientConfig,
		&session.Authenticated{Mux: oldClient, Carrier: protocol.CarrierHTTPS},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			return &session.Authenticated{Mux: newClient, Carrier: protocol.CarrierHTTP3}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime.nativeProbeTimeout = 20 * time.Millisecond
	runtime.drainTimeout = 500 * time.Millisecond
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = oldServer.Close()
		_ = newServer.Close()
	})

	dialer, err := tunstack.NewDialerWithSessionRouter(runtime.router)
	if err != nil {
		t.Fatal(err)
	}
	oldAccepted := acceptMigrationStream(t, oldServer)
	metadata := &M.Metadata{
		Network: M.TCP, DstIP: netip.MustParseAddr("203.0.113.20"), DstPort: 443,
		SrcIP: netip.MustParseAddr("198.18.0.1"), SrcPort: 49000,
	}
	oldConnection, err := dialer.DialContext(context.Background(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	openTrigger := []byte{0x01}
	if _, err := oldConnection.Write(openTrigger); err != nil {
		t.Fatalf("open old flow: %v", err)
	}
	oldServerStream := <-oldAccepted
	if _, err := io.ReadFull(oldServerStream, make([]byte, len(openTrigger))); err != nil {
		t.Fatalf("read old flow trigger: %v", err)
	}
	oldWire.blockReceive.Store(true)

	result, err := runtime.NetworkChanged(context.Background())
	if err != nil || !result.Reconnected || !result.Migrated || result.NativePath {
		t.Fatalf("reconnect result=%+v error=%v", result, err)
	}
	if got := runtime.CarrierName(); got != "http3" {
		t.Fatalf("carrier=%q", got)
	}

	oldPayload := []byte("old flow survives")
	if _, err := oldConnection.Write(oldPayload); err != nil {
		t.Fatalf("write old flow after switch: %v", err)
	}
	oldReceived := make([]byte, len(oldPayload))
	if _, err := io.ReadFull(oldServerStream, oldReceived); err != nil || !bytes.Equal(oldReceived, oldPayload) {
		t.Fatalf("read old flow payload=%q error=%v", oldReceived, err)
	}

	newAccepted := acceptMigrationStream(t, newServer)
	newConnection, err := dialer.DialContext(context.Background(), metadata)
	if err != nil {
		t.Fatalf("open new flow: %v", err)
	}
	newPayload := []byte("new carrier")
	if _, err := newConnection.Write(newPayload); err != nil {
		t.Fatal(err)
	}
	newServerStream := <-newAccepted
	newReceived := make([]byte, len(newPayload))
	if _, err := io.ReadFull(newServerStream, newReceived); err != nil || !bytes.Equal(newReceived, newPayload) {
		t.Fatalf("read new flow payload=%q error=%v", newReceived, err)
	}
	_ = oldConnection.Close()
	_ = newConnection.Close()
}

func TestNP2RuntimeMigrationDrainsOldPoolAndKeepsHTTP3StandbyTarget(t *testing.T) {
	oldPrimary, oldPrimaryServer, oldWire := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	oldSecondary, oldSecondaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	replacement, replacementServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTP3)
	connections := []*session.Authenticated{
		{Mux: oldSecondary, Carrier: protocol.CarrierHTTPS},
		{Mux: replacement, Carrier: protocol.CarrierHTTP3},
	}
	connectorCalls := 0
	runtime, err := newNP2Runtime(
		config.Client{
			ServerAddresses:     []netip.Addr{netip.MustParseAddr("104.171.136.10")},
			MaxParallelCarriers: 3,
		},
		&session.Authenticated{Mux: oldPrimary, Carrier: protocol.CarrierHTTPS},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			connected := connections[connectorCalls]
			connectorCalls++
			return connected, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime.nativeProbeTimeout = 20 * time.Millisecond
	runtime.drainTimeout = time.Second
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = oldPrimaryServer.Close()
		_ = oldSecondaryServer.Close()
		_ = replacementServer.Close()
	})
	if err := runtime.warmCarrierPool(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	accepted := acceptMigrationStream(t, oldSecondaryServer)
	stream, err := oldSecondary.Open(context.Background(), []byte("pooled-flow"))
	if err != nil {
		t.Fatal(err)
	}
	serverStream := <-accepted
	oldWire.blockReceive.Store(true)

	result, err := runtime.NetworkChanged(context.Background())
	if err != nil || !result.Reconnected || !result.Migrated {
		t.Fatalf("migration result=%+v error=%v", result, err)
	}
	runtime.mu.Lock()
	poolTarget := runtime.poolTarget
	poolMembers := len(runtime.poolSessions)
	retiredSecondary := false
	for _, authenticated := range runtime.retired {
		if authenticated == connections[0] {
			retiredSecondary = true
		}
	}
	runtime.mu.Unlock()
	if poolTarget != 2 || poolMembers != 0 || !retiredSecondary {
		t.Fatalf("migrated pool target=%d members=%d retired_secondary=%v",
			poolTarget, poolMembers, retiredSecondary)
	}
	if oldSecondary.Stats().ActiveStreams == 0 {
		t.Fatal("pooled stream was closed instead of being drained")
	}
	_ = stream.Close()
	_ = serverStream.Close()
}

func TestNP2RuntimeMigrationKeepsConfiguredPoolAfterHTTPSFallback(t *testing.T) {
	oldPrimary, oldServer, oldWire := newMigrationMuxPair(t, protocol.CarrierHTTP3)
	replacement, replacementServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	secondary, secondaryServer, _ := newMigrationMuxPair(t, protocol.CarrierHTTPS)
	connections := []*session.Authenticated{
		{Mux: replacement, Carrier: protocol.CarrierHTTPS},
		{Mux: secondary, Carrier: protocol.CarrierHTTPS},
	}
	connectorCalls := 0
	runtime, err := newNP2Runtime(
		config.Client{
			ServerAddresses:     []netip.Addr{netip.MustParseAddr("104.171.136.10")},
			MaxParallelCarriers: 3,
		},
		&session.Authenticated{Mux: oldPrimary, Carrier: protocol.CarrierHTTP3},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			connected := connections[connectorCalls]
			connectorCalls++
			return connected, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime.nativeProbeTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		_ = runtime.Close()
		_ = oldServer.Close()
		_ = replacementServer.Close()
		_ = secondaryServer.Close()
	})
	if target, healthy, _, _, _ := runtime.carrierPoolStats(); target != 2 || healthy != 1 {
		t.Fatalf("initial HTTP/3 pool target=%d healthy=%d", target, healthy)
	}
	oldWire.blockReceive.Store(true)
	if _, err := runtime.NetworkChanged(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.reconcileCarrierPool(context.Background(), poolActivity{}); err != nil {
		t.Fatal(err)
	}
	if target, healthy, _, _, _ := runtime.carrierPoolStats(); target != 3 || healthy != 2 {
		t.Fatalf("HTTPS fallback pool target=%d healthy=%d", target, healthy)
	}
}

func TestRouteSetContainsOnlyPreviouslyExcludedAddresses(t *testing.T) {
	if !routeSetContains("104.171.136.10,2001:4860::1", "104.171.136.10") {
		t.Fatal("existing route was rejected")
	}
	if routeSetContains("104.171.136.10", "104.171.136.10,8.8.8.8") ||
		routeSetContains("104.171.136.10", "") {
		t.Fatal("unsafe replacement route was accepted")
	}
}

func acceptMigrationStream(t *testing.T, mux *session.Mux) <-chan *session.Stream {
	t.Helper()
	accepted := make(chan *session.Stream, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		incoming, err := mux.Accept(ctx)
		if err != nil {
			return
		}
		stream, err := incoming.Accept()
		if err == nil {
			accepted <- stream
		}
	}()
	return accepted
}

type migrationMemoryCarrier struct {
	in           <-chan []byte
	out          chan<- []byte
	done         <-chan struct{}
	closePair    func()
	closeOnce    sync.Once
	blockReceive atomic.Bool
	kind         protocol.CarrierKind
}

var _ carrier.Carrier = (*migrationMemoryCarrier)(nil)

func newMigrationMuxPair(
	t *testing.T,
	kind protocol.CarrierKind,
) (*session.Mux, *session.Mux, *migrationMemoryCarrier) {
	t.Helper()
	leftToRight := make(chan []byte, 128)
	rightToLeft := make(chan []byte, 128)
	done := make(chan struct{})
	var closePairOnce sync.Once
	closePair := func() { closePairOnce.Do(func() { close(done) }) }
	left := &migrationMemoryCarrier{
		in: rightToLeft, out: leftToRight, done: done, closePair: closePair, kind: kind,
	}
	right := &migrationMemoryCarrier{
		in: leftToRight, out: rightToLeft, done: done, closePair: closePair, kind: kind,
	}
	typeMap, err := protocol.NewTypeMap([32]byte{0x91, 0x22})
	if err != nil {
		t.Fatal(err)
	}
	client, err := session.New(session.Config{
		Role: session.RoleClient, Carrier: left, TypeMap: typeMap,
		InitialWindow: 64 * 1024, MaxStreams: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := session.New(session.Config{
		Role: session.RoleServer, Carrier: right, TypeMap: typeMap,
		InitialWindow: 64 * 1024, MaxStreams: 8,
	})
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	return client, server, left
}

func (c *migrationMemoryCarrier) Send(ctx context.Context, raw []byte) error {
	copyOfRaw := append([]byte(nil), raw...)
	select {
	case c.out <- copyOfRaw:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return io.EOF
	}
}

func (c *migrationMemoryCarrier) Receive(ctx context.Context) ([]byte, error) {
	if c.blockReceive.Load() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return nil, io.EOF
		}
	}
	select {
	case raw := <-c.in:
		if c.blockReceive.Load() {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-c.done:
				return nil, io.EOF
			}
		}
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *migrationMemoryCarrier) Close() error {
	c.closeOnce.Do(c.closePair)
	return nil
}

func (c *migrationMemoryCarrier) Kind() protocol.CarrierKind { return c.kind }

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func TestControllerRejectsInvalidInputsBeforeConnecting(t *testing.T) {
	called := false
	controller := newController(func(context.Context, config.Client) (mobileRuntime, error) {
		called = true
		return newStubRuntime(), nil
	})

	if err := controller.start([]byte(`{"server_identity":"bad"}`), validSecret()); err == nil {
		t.Fatal("invalid profile was accepted")
	}
	if called {
		t.Fatal("connector called for invalid profile")
	}
	if err := controller.start(validProfile(), "not-a-secret"); err == nil {
		t.Fatal("invalid secret was accepted")
	}
	if err := controller.startTunnel(-1); !errors.Is(err, ErrInvalidTunnelFD) {
		t.Fatalf("invalid TUN descriptor error=%v", err)
	}
}

func TestMobileCarrierNameSupportsAllProductionCarriers(t *testing.T) {
	tests := []struct {
		kind protocol.CarrierKind
		want string
	}{
		{protocol.CarrierHTTP3, "http3"},
		{protocol.CarrierWebRTC, "webrtc"},
		{protocol.CarrierHTTPS, "https"},
	}
	for _, test := range tests {
		got, err := mobileCarrierName(test.kind)
		if err != nil || got != test.want {
			t.Fatalf("carrier %d name=%q error=%v", test.kind, got, err)
		}
	}
	if _, err := mobileCarrierName(0xff); err == nil {
		t.Fatal("unknown carrier was accepted")
	}
}

func TestControllerAppliesStrictClientRouteSnapshotBeforeStart(t *testing.T) {
	runtime := newStubRuntime()
	controller := newController(func(context.Context, config.Client) (mobileRuntime, error) {
		return runtime, nil
	})
	routes := `[{
		"id":"local-media","name":"Media","priority":10,"enabled":true,
		"source":"client","match":{"cidrs":["203.0.113.0/24"],"protocols":["tcp","udp"]},
		"action":{"kind":"node","node_ids":["edge-01"]}
	}]`
	if err := controller.setClientRoutesJSON(routes); err != nil {
		t.Fatalf("set routes: %v", err)
	}
	if err := controller.start(validProfile(), validSecret()); err != nil {
		t.Fatalf("start with routes: %v", err)
	}
	runtime.mu.Lock()
	applied := runtime.clientRoutes != nil
	runtime.mu.Unlock()
	if !applied {
		t.Fatal("client route snapshot was not attached to runtime")
	}
	controller.stop()
	if err := controller.setClientRoutesJSON(routes + ` {}`); err == nil {
		t.Fatal("route snapshot accepted trailing JSON")
	}
}

type stubRuntime struct {
	mu                      sync.Mutex
	startedFD               int
	startErr                error
	closed                  chan struct{}
	closeOnce               sync.Once
	uploadBytesPerSecond    int64
	downloadBytesPerSecond  int64
	uploadTotalBytes        int64
	downloadTotalBytes      int64
	udpMode                 string
	quicFallbacks           int64
	carrierPoolTarget       int64
	carrierPoolHealthy      int64
	carrierPoolAssignments  int64
	carrierPoolScaleUps     int64
	carrierPoolFailures     int64
	dnsAttributionQueries   int64
	dnsAttributionResponses int64
	dnsAttributionHits      int64
	dnsAttributionMisses    int64
	dnsAttributionCached    int64
	firstFlightDomainHits   int64
	firstFlightFallbacks    int64
	tcpStreamAttempts       int64
	tcpStreamSuccesses      int64
	tcpStreamFailures       int64
	activeStreams           int64
	flowControlStalls       int64
	protocolErrors          int64
	sentCells               int64
	receivedCells           int64
	sentCellPayloadBytes    int64
	receivedPayloadBytes    int64
	windowUpdatesSent       int64
	windowUpdatesReceived   int64
	coverRealWireBytes      int64
	coverPaddingBytes       int64
	coverDummyWireBytes     int64
	coverProfileTransitions int64
	coverWebSessions        int64
	coverRealtimeSessions   int64
	coverStreamSessions     int64
	migrationStarted        chan struct{}
	releaseMigration        chan struct{}
	migrationOnce           sync.Once
	migrationErr            error
	migratedCarrier         string
	carrier                 string
	catalogJSON             string
	clientRoutes            *tunstack.ClientRoutePolicy
}

func TestControllerReturnsCatalogOnlyFromConnectedCatalogRuntime(t *testing.T) {
	runtime := newStubRuntime()
	runtime.catalogJSON = `{"version":1,"cluster_id":"cluster-01"}`
	controller := newController(nil)
	controller.state = "running"
	controller.runtime = runtime

	got, err := controller.catalogJSON(context.Background())
	if err != nil || got != runtime.catalogJSON {
		t.Fatalf("catalogJSON() = %q, %v", got, err)
	}
	controller.state = "stopped"
	if _, err := controller.catalogJSON(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("stopped catalogJSON() error = %v", err)
	}
}

func (r *stubRuntime) CatalogJSON(context.Context) (string, error) {
	if r.catalogJSON == "" {
		return "", errors.New("catalog unavailable")
	}
	return r.catalogJSON, nil
}

func (r *stubRuntime) SetClientRoutes(policy *tunstack.ClientRoutePolicy) error {
	r.mu.Lock()
	r.clientRoutes = policy
	r.mu.Unlock()
	return nil
}

type blockingCloseRuntime struct {
	closeStarted chan struct{}
	releaseClose chan struct{}
	closeOnce    sync.Once
}

func newBlockingCloseRuntime() *blockingCloseRuntime {
	return &blockingCloseRuntime{
		closeStarted: make(chan struct{}),
		releaseClose: make(chan struct{}),
	}
}

func (*blockingCloseRuntime) StartTunnel(int) error { return nil }

func (r *blockingCloseRuntime) Wait(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (r *blockingCloseRuntime) Close() error {
	r.closeOnce.Do(func() {
		close(r.closeStarted)
		<-r.releaseClose
	})
	return nil
}

func (*blockingCloseRuntime) ServerAddresses() string { return "104.171.136.10" }

func (*blockingCloseRuntime) CarrierName() string { return "https" }

func (*blockingCloseRuntime) TrafficStats() trafficStats { return trafficStats{} }

func (*blockingCloseRuntime) NetworkChanged(context.Context) (migrationResult, error) {
	return migrationResult{}, errors.New("unavailable")
}

func newStubRuntime() *stubRuntime {
	return &stubRuntime{startedFD: -1, closed: make(chan struct{}), carrier: "webrtc"}
}

func (r *stubRuntime) StartTunnel(fileDescriptor int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return r.startErr
	}
	r.startedFD = fileDescriptor
	return nil
}

func (r *stubRuntime) Wait(ctx context.Context) error {
	select {
	case <-r.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *stubRuntime) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func (*stubRuntime) ServerAddresses() string { return "104.171.136.10" }

func (r *stubRuntime) CarrierName() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.carrier
}

func (r *stubRuntime) NetworkChanged(ctx context.Context) (migrationResult, error) {
	if r.migrationStarted != nil {
		r.migrationOnce.Do(func() { close(r.migrationStarted) })
	}
	if r.releaseMigration != nil {
		select {
		case <-r.releaseMigration:
		case <-ctx.Done():
			return migrationResult{}, ctx.Err()
		}
	}
	if r.migrationErr != nil {
		return migrationResult{}, r.migrationErr
	}
	r.mu.Lock()
	if r.migratedCarrier != "" {
		r.carrier = r.migratedCarrier
	}
	r.mu.Unlock()
	return migrationResult{Reconnected: true, Migrated: true}, nil
}

func (r *stubRuntime) TrafficStats() trafficStats {
	return trafficStats{
		UploadBytesPerSecond:    r.uploadBytesPerSecond,
		DownloadBytesPerSecond:  r.downloadBytesPerSecond,
		UploadTotalBytes:        r.uploadTotalBytes,
		DownloadTotalBytes:      r.downloadTotalBytes,
		UDPMode:                 r.udpMode,
		QUICFallbacks:           r.quicFallbacks,
		CarrierPoolTarget:       r.carrierPoolTarget,
		CarrierPoolHealthy:      r.carrierPoolHealthy,
		CarrierPoolAssignments:  r.carrierPoolAssignments,
		CarrierPoolScaleUps:     r.carrierPoolScaleUps,
		CarrierPoolFailures:     r.carrierPoolFailures,
		DNSAttributionQueries:   r.dnsAttributionQueries,
		DNSAttributionResponses: r.dnsAttributionResponses,
		DNSAttributionHits:      r.dnsAttributionHits,
		DNSAttributionMisses:    r.dnsAttributionMisses,
		DNSAttributionCached:    r.dnsAttributionCached,
		FirstFlightDomainHits:   r.firstFlightDomainHits,
		FirstFlightFallbacks:    r.firstFlightFallbacks,
		TCPStreamAttempts:       r.tcpStreamAttempts,
		TCPStreamSuccesses:      r.tcpStreamSuccesses,
		TCPStreamFailures:       r.tcpStreamFailures,
		ActiveStreams:           r.activeStreams,
		FlowControlStalls:       r.flowControlStalls,
		ProtocolErrors:          r.protocolErrors,
		SentCells:               r.sentCells,
		ReceivedCells:           r.receivedCells,
		SentCellPayloadBytes:    r.sentCellPayloadBytes,
		ReceivedPayloadBytes:    r.receivedPayloadBytes,
		WindowUpdatesSent:       r.windowUpdatesSent,
		WindowUpdatesReceived:   r.windowUpdatesReceived,
		CoverRealWireBytes:      r.coverRealWireBytes,
		CoverPaddingBytes:       r.coverPaddingBytes,
		CoverDummyWireBytes:     r.coverDummyWireBytes,
		CoverProfileTransitions: r.coverProfileTransitions,
		CoverWebSessions:        r.coverWebSessions,
		CoverRealtimeSessions:   r.coverRealtimeSessions,
		CoverStreamSessions:     r.coverStreamSessions,
	}
}

func TestCanonicalRouteAddressesFiltersAndSorts(t *testing.T) {
	got, err := canonicalRouteAddresses([]netip.Addr{
		netip.MustParseAddr("2001:4860:4860::8888"),
		netip.MustParseAddr("104.171.136.10"),
		netip.MustParseAddr("::ffff:104.171.136.10"),
		netip.MustParseAddr("127.0.0.1"),
		netip.MustParseAddr("192.168.1.1"),
		netip.MustParseAddr("198.18.1.233"),
	})
	if err != nil {
		t.Fatalf("canonical routes: %v", err)
	}
	if want := "104.171.136.10,2001:4860:4860::8888"; got != want {
		t.Fatalf("routes=%q, want %q", got, want)
	}
}

func validProfile() []byte {
	return []byte(`{
  "server_identity":"vpn.example.com","secret_file":"keychain",
	"server_addresses":["104.171.136.10"],
  "https_url":"wss://vpn.example.com/private/https/session",
  "webrtc_signaling_url":"https://vpn.example.com/private/webrtc/offer","profile":"interactive",
  "max_cover_overhead_percent":30,"initial_window_bytes":262144,"max_streams":128,
  "webrtc_timeout":"5s","https_timeout":"10s","carrier_cache_ttl":"10m"
}`)
}

func validSecret() string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x3c}, 32))
}
