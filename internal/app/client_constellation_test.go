package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func TestDesktopConstellationRuntimeWarmsAndRecoversCarrierPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	hub, err := constellation.NewHub(constellation.HubConfig{
		MaxConstellations: 2, MaxLeases: 3, MaxDraining: 3,
		InactiveTTL:  time.Minute,
		TicketConfig: continuity.TicketRegistryConfig{MaxTickets: 8, TTL: 30 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	serverControl, err := constellation.NewServerControl(constellation.ServerControlConfig{Hub: hub})
	if err != nil {
		t.Fatal(err)
	}
	factory := &desktopConstellationTestFactory{
		t: t, ctx: ctx, serverControl: serverControl, serverErrors: make(chan error, 8),
	}
	initial, err := factory.connect(ctx, config.Client{})
	if err != nil {
		t.Fatal(err)
	}
	clientConfig := config.Client{
		EnableConstellation: true, MaxParallelCarriers: 2, MaxStreams: 8,
	}
	runtime, err := newDesktopConstellationRuntime(ctx, clientConfig, initial, factory.connect)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		factory.close()
		_ = hub.Close()
	})

	waitDesktopRouteCount(t, ctx, runtime, 2)
	if factory.calls() != 2 {
		t.Fatalf("authenticated carrier calls=%d, want 2", factory.calls())
	}
	_ = initial.Mux.Close()
	waitDesktopPoolReplacement(t, ctx, runtime, factory)
	select {
	case <-runtime.done:
		t.Fatalf("one failed carrier terminated healthy runtime: %v", runtime.Wait(context.Background()))
	default:
	}
	select {
	case err := <-factory.serverErrors:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("server constellation control: %v", err)
		}
	default:
	}
}

func TestDesktopConstellationRuntimeRotatesExpiredGrammarLeaseWithoutStopping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	hub, err := constellation.NewHub(constellation.HubConfig{
		MaxConstellations: 2, MaxLeases: 3, MaxDraining: 3,
		InactiveTTL:  time.Minute,
		TicketConfig: continuity.TicketRegistryConfig{MaxTickets: 8, TTL: 30 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	serverControl, err := constellation.NewServerControl(constellation.ServerControlConfig{Hub: hub})
	if err != nil {
		t.Fatal(err)
	}
	factory := &desktopConstellationTestFactory{
		t: t, ctx: ctx, serverControl: serverControl, serverErrors: make(chan error, 8),
	}
	initial, err := factory.connect(ctx, config.Client{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := newDesktopConstellationRuntime(ctx, config.Client{
		EnableConstellation: true, MaxParallelCarriers: 2, MaxStreams: 8,
	}, initial, factory.connect)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close()
		factory.close()
		_ = hub.Close()
	})
	waitDesktopRouteCount(t, ctx, runtime, 2)
	now := time.Now()
	runtime.mu.Lock()
	runtime.routes[1].grammarLease.ExpiresAt = now.Add(-time.Second)
	runtime.routes[1].lastActivity = now
	runtime.mu.Unlock()
	if !runtime.rotateExpired(now) {
		t.Fatal("expired grammar lease was not rotated")
	}
	waitDesktopPoolReplacement(t, ctx, runtime, factory)
	select {
	case <-runtime.done:
		t.Fatalf("lease rotation stopped runtime: %v", runtime.Wait(context.Background()))
	default:
	}
}

func waitDesktopPoolReplacement(
	t *testing.T,
	ctx context.Context,
	runtime *desktopConstellationRuntime,
	factory *desktopConstellationTestFactory,
) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.mu.Lock()
		count := len(runtime.routes)
		runtime.mu.Unlock()
		calls := factory.calls()
		if count == 2 && calls >= 3 {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("replacement routes=%d calls=%d: %v", count, calls, ctx.Err())
		}
	}
}

func waitDesktopRouteCount(
	t *testing.T,
	ctx context.Context,
	runtime *desktopConstellationRuntime,
	want int,
) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.mu.Lock()
		count := len(runtime.routes)
		runtime.mu.Unlock()
		if count == want {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("route count=%d, want %d: %v", count, want, ctx.Err())
		}
	}
}

type desktopConstellationTestFactory struct {
	t             *testing.T
	ctx           context.Context
	serverControl *constellation.ServerControl
	serverErrors  chan error

	mu       sync.Mutex
	count    int
	servers  []*session.Authenticated
	controls []*constellation.ControlChannel
	leases   []*constellation.Attachment
}

func (f *desktopConstellationTestFactory) connect(
	ctx context.Context,
	_ config.Client,
) (*session.Authenticated, error) {
	f.mu.Lock()
	f.count++
	f.mu.Unlock()
	clientCarrier, serverCarrier := newDesktopConstellationCarrierPair()
	secret := [protocol.RootSecretSize]byte{0x91, 0x27, 0x44}
	offer := productionExtensionParameters(8)
	offer.Capabilities |= protocol.CapabilityConstellationContinuity
	request := offer
	type serverResult struct {
		authenticated *session.Authenticated
		err           error
	}
	authenticatedServer := make(chan serverResult, 1)
	go func() {
		serverSession, acceptErr := session.AcceptServer(ctx, serverCarrier, session.AuthenticatedConfig{
			RootSecret: secret, ServerIdentity: "edge.example.test",
			Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
			InitialWindow: 64 * 1024, MaxStreams: 8,
			ExtensionOffer: &offer, ExtensionTimeout: time.Second,
		})
		authenticatedServer <- serverResult{authenticated: serverSession, err: acceptErr}
		if acceptErr != nil {
			return
		}
		attachment, admitErr := f.serverControl.Admit(f.ctx, serverSession)
		if admitErr != nil {
			f.serverErrors <- admitErr
			return
		}
		control, controlErr := constellation.NewControlChannel(f.ctx, constellation.ControlChannelConfig{
			Mux: serverSession.Mux, ConstellationID: attachment.ConstellationID,
			FirstMessageID: attachment.ControlNextMessageID, MaxFlows: 8,
		})
		if controlErr != nil {
			_ = attachment.Close()
			f.serverErrors <- controlErr
			return
		}
		f.mu.Lock()
		f.servers = append(f.servers, serverSession)
		f.controls = append(f.controls, control)
		f.leases = append(f.leases, attachment)
		f.mu.Unlock()
	}()
	clientSession, err := session.ConnectClient(ctx, clientCarrier, session.AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 64 * 1024, MaxStreams: 8,
		ExtensionRequest: &request, RequiredExtensions: protocol.CapabilityConstellationContinuity,
		ExtensionTimeout: time.Second,
	})
	server := <-authenticatedServer
	if err != nil {
		return nil, err
	}
	if server.err != nil {
		_ = clientSession.Mux.Close()
		return nil, server.err
	}
	return clientSession, nil
}

func (f *desktopConstellationTestFactory) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

func (f *desktopConstellationTestFactory) close() {
	f.mu.Lock()
	servers := append([]*session.Authenticated(nil), f.servers...)
	controls := append([]*constellation.ControlChannel(nil), f.controls...)
	leases := append([]*constellation.Attachment(nil), f.leases...)
	f.mu.Unlock()
	for _, control := range controls {
		_ = control.Close()
	}
	for _, lease := range leases {
		_ = lease.Close()
	}
	for _, server := range servers {
		_ = server.Mux.Close()
	}
}

type desktopConstellationCarrier struct {
	in    <-chan []byte
	out   chan<- []byte
	done  <-chan struct{}
	close func()
	once  sync.Once
}

var _ carrier.Carrier = (*desktopConstellationCarrier)(nil)

func newDesktopConstellationCarrierPair() (*desktopConstellationCarrier, *desktopConstellationCarrier) {
	leftToRight := make(chan []byte, 256)
	rightToLeft := make(chan []byte, 256)
	done := make(chan struct{})
	var once sync.Once
	closePair := func() { once.Do(func() { close(done) }) }
	return &desktopConstellationCarrier{in: rightToLeft, out: leftToRight, done: done, close: closePair},
		&desktopConstellationCarrier{in: leftToRight, out: rightToLeft, done: done, close: closePair}
}

func (c *desktopConstellationCarrier) Send(ctx context.Context, payload []byte) error {
	copyOfPayload := append([]byte(nil), payload...)
	select {
	case c.out <- copyOfPayload:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return io.EOF
	}
}

func (c *desktopConstellationCarrier) Receive(ctx context.Context) ([]byte, error) {
	select {
	case payload := <-c.in:
		return payload, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *desktopConstellationCarrier) Close() error {
	c.once.Do(c.close)
	return nil
}

func (c *desktopConstellationCarrier) Kind() protocol.CarrierKind {
	return protocol.CarrierHTTPS
}
