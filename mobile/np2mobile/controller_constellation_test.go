package np2mobile

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func TestNP2RuntimeCreatesConstellationAndAttachesWarmCarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hub, err := constellation.NewHub(constellation.HubConfig{
		MaxConstellations: 2, MaxLeases: 3, MaxDraining: 3, InactiveTTL: 10 * time.Second,
		TicketConfig: continuity.TicketRegistryConfig{MaxTickets: 6, TTL: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	serverControl, err := constellation.NewServerControl(constellation.ServerControlConfig{Hub: hub})
	if err != nil {
		t.Fatal(err)
	}

	primaryClient, primaryServer := newMobileConstellationAuthenticatedPair(t, ctx)
	primaryAdmission := admitMobileConstellation(t, ctx, serverControl, primaryServer)
	secondaryClient, secondaryServer := newMobileConstellationAuthenticatedPair(t, ctx)
	secondaryAdmission := admitMobileConstellation(t, ctx, serverControl, secondaryServer)
	connectCalls := 0
	clientConfig := config.Client{
		ServerAddresses: []netip.Addr{netip.MustParseAddr("104.171.136.10")},
		MaxStreams:      8, MaxParallelCarriers: 2, EnableConstellation: true,
		EnableForwardSecrecy: true,
	}
	runtime, err := newNP2Runtime(
		clientConfig,
		primaryClient,
		func(context.Context, config.Client) (*session.Authenticated, error) {
			connectCalls++
			return secondaryClient, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := receiveMobileAdmission(t, ctx, primaryAdmission)
	primaryControl := newMobileServerControlChannel(t, ctx, primaryServer, primary)
	t.Cleanup(func() {
		_ = primaryControl.Close()
		_ = primary.Close()
		_ = primaryServer.Mux.Close()
	})
	if runtime.constellationControl == nil || runtime.continuityRouter == nil || len(runtime.controls) != 1 {
		t.Fatal("primary mobile constellation was not installed")
	}

	if err := runtime.warmCarrierPool(ctx, 2); err != nil {
		t.Fatalf("attach warm carrier: %v", err)
	}
	secondary := receiveMobileAdmission(t, ctx, secondaryAdmission)
	secondaryControl := newMobileServerControlChannel(t, ctx, secondaryServer, secondary)
	t.Cleanup(func() {
		_ = secondaryControl.Close()
		_ = secondary.Close()
		_ = secondaryServer.Mux.Close()
	})
	runtime.mu.Lock()
	controlCount := len(runtime.controls)
	poolCount := len(runtime.poolSessions)
	runtime.mu.Unlock()
	if connectCalls != 1 || controlCount != 2 || poolCount != 1 {
		t.Fatalf("connects=%d controls=%d pool=%d", connectCalls, controlCount, poolCount)
	}
	if healthy, _ := runtime.router.PoolStats(); healthy != 2 {
		t.Fatalf("healthy routes=%d, want 2", healthy)
	}
	_ = primaryClient.Mux.Close()
	deadline := time.Now().Add(time.Second)
	for {
		runtime.mu.Lock()
		promoted := runtime.session == secondaryClient && len(runtime.poolSessions) == 0
		runtime.mu.Unlock()
		if promoted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("constellation warm carrier was not promoted")
		}
		time.Sleep(time.Millisecond)
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	if err := runtime.Wait(waitContext); err != context.DeadlineExceeded {
		t.Fatalf("constellation promotion stopped runtime: %v", err)
	}
}

type mobileAdmissionResult struct {
	attachment *constellation.Attachment
	err        error
}

type mobileAuthenticatedResult struct {
	authenticated *session.Authenticated
	err           error
}

func admitMobileConstellation(
	t *testing.T,
	ctx context.Context,
	control *constellation.ServerControl,
	authenticated *session.Authenticated,
) <-chan mobileAdmissionResult {
	t.Helper()
	result := make(chan mobileAdmissionResult, 1)
	go func() {
		attachment, err := control.Admit(ctx, authenticated)
		result <- mobileAdmissionResult{attachment: attachment, err: err}
	}()
	return result
}

func receiveMobileAdmission(
	t *testing.T,
	ctx context.Context,
	result <-chan mobileAdmissionResult,
) *constellation.Attachment {
	t.Helper()
	select {
	case admitted := <-result:
		if admitted.err != nil {
			t.Fatal(admitted.err)
		}
		return admitted.attachment
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return nil
	}
}

func newMobileServerControlChannel(
	t *testing.T,
	ctx context.Context,
	authenticated *session.Authenticated,
	attachment *constellation.Attachment,
) *constellation.ControlChannel {
	t.Helper()
	control, err := constellation.NewControlChannel(ctx, constellation.ControlChannelConfig{
		Mux: authenticated.Mux, ConstellationID: attachment.ConstellationID,
		FirstMessageID: attachment.ControlNextMessageID, MaxFlows: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	return control
}

func newMobileConstellationAuthenticatedPair(
	t *testing.T,
	ctx context.Context,
) (*session.Authenticated, *session.Authenticated) {
	t.Helper()
	leftToRight := make(chan []byte, 128)
	rightToLeft := make(chan []byte, 128)
	done := make(chan struct{})
	var closeOnce sync.Once
	closePair := func() { closeOnce.Do(func() { close(done) }) }
	clientCarrier := &migrationMemoryCarrier{
		in: rightToLeft, out: leftToRight, done: done, closePair: closePair,
		kind: protocol.CarrierHTTPS,
	}
	serverCarrier := &migrationMemoryCarrier{
		in: leftToRight, out: rightToLeft, done: done, closePair: closePair,
		kind: protocol.CarrierHTTPS,
	}
	extensions := protocol.ExtensionParameters{
		Capabilities:           protocol.CapabilityConstellationContinuity,
		MaxSessionReceiveBytes: 1024 * 1024, MaxStreamWindowBytes: 64 * 1024,
	}
	secret := [protocol.RootSecretSize]byte{0x83, 0x19, 0x47}
	serverConfig := session.AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 64 * 1024, MaxStreams: 8,
		ExtensionOffer: &extensions, ExtensionTimeout: time.Second,
		EnableForwardSecrecy: true,
	}
	clientConfig := serverConfig
	clientConfig.ExtensionOffer = nil
	request := extensions
	clientConfig.ExtensionRequest = &request
	clientConfig.RequiredExtensions = protocol.CapabilityConstellationContinuity
	serverResult := make(chan mobileAuthenticatedResult, 1)
	go func() {
		authenticated, err := session.AcceptServer(ctx, serverCarrier, serverConfig)
		serverResult <- mobileAuthenticatedResult{authenticated: authenticated, err: err}
	}()
	client, err := session.ConnectClient(ctx, clientCarrier, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	serverStatus := <-serverResult
	if serverStatus.err != nil {
		t.Fatal(serverStatus.err)
	}
	if serverStatus.authenticated == nil {
		t.Fatal("missing authenticated server session")
	}
	return client, serverStatus.authenticated
}
