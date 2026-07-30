package tunstack

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
)

func TestClientContinuityRouterMigratesLiveFlowWithoutRedial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := proxy.NewContinuityRuntime(proxy.ContinuityRuntimeConfig{
		Context: ctx, MaxFlows: 8, MaxFlowsPerPrincipal: 4,
		JournalBytes: proxy.MinContinuityJournalBytes, AckEveryBytes: 1,
		MigrationTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	principal := continuity.PrincipalID{1, 2, 3}
	constellationID := protocol.ContinuityID{17, 18, 19}
	firstLeaseKey := protocol.ContinuityID{33, 34, 35}
	firstClientMux, firstServerMux := newContinuityRouterMuxPair(t)
	firstClientControl := newContinuityRouterControl(t, ctx, firstClientMux, constellationID)
	firstServerControl := newContinuityRouterControl(t, ctx, firstServerMux, constellationID)
	targetServer, targetPeer := net.Pipe()
	t.Cleanup(func() { _ = targetPeer.Close() })
	dialer := &continuityRouterDialer{connection: targetServer}
	go func() {
		_ = (proxy.Server{
			Mux: firstServerMux, Policy: proxy.DestinationPolicy{AllowPrivate: true}, Dialer: dialer,
			Continuity: runtime, ContinuityLease: proxy.ContinuityLease{
				Principal: principal, ConstellationID: constellationID,
				LeaseKey: firstLeaseKey, Control: firstServerControl, Mux: firstServerMux,
			},
		}).Serve(ctx)
	}()

	router, err := NewClientContinuityRouter(ClientContinuityRouterConfig{
		Context: ctx,
		Initial: ContinuityRoute{
			ID: 1, Mux: firstClientMux, Control: firstClientControl,
			ConstellationID: constellationID, LeaseKey: firstLeaseKey,
		},
		MaxFlows: 8, JournalBytes: proxy.MinContinuityJournalBytes, AckEveryBytes: 1,
		MigrationTimeout: 2 * time.Second, ControlTimeout: time.Second,
		Random: bytes.NewReader(bytes.Repeat([]byte{0x51}, 16)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close() })
	inner, err := proxy.EncodeOpenRequest(proxy.OpenRequest{
		Command: proxy.CommandTCPConnect, Target: proxy.Target{Host: "127.0.0.1", Port: 443},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := router.OpenStream(ctx, inner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	if _, err := stream.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	assertContinuityRouterRead(t, ctx, targetPeer, "first")
	if _, err := targetPeer.Write([]byte("reply")); err != nil {
		t.Fatal(err)
	}
	assertContinuityRouterRead(t, ctx, stream, "reply")

	_ = firstClientMux.Close()
	_ = firstServerMux.Close()
	pendingWrite := make(chan error, 1)
	go func() {
		_, writeErr := stream.Write([]byte("second"))
		pendingWrite <- writeErr
	}()

	secondLeaseKey := protocol.ContinuityID{49, 50, 51}
	secondClientMux, secondServerMux := newContinuityRouterMuxPair(t)
	secondClientControl := newContinuityRouterControl(t, ctx, secondClientMux, constellationID)
	secondServerControl := newContinuityRouterControl(t, ctx, secondServerMux, constellationID)
	go func() {
		_ = (proxy.Server{
			Mux: secondServerMux, Policy: proxy.DestinationPolicy{AllowPrivate: true}, Dialer: dialer,
			Continuity: runtime, ContinuityLease: proxy.ContinuityLease{
				Principal: principal, ConstellationID: constellationID,
				LeaseKey: secondLeaseKey, Control: secondServerControl, Mux: secondServerMux,
			},
		}).Serve(ctx)
	}()
	if err := router.AddRoute(ContinuityRoute{
		ID: 2, Mux: secondClientMux, Control: secondClientControl,
		ConstellationID: constellationID, LeaseKey: secondLeaseKey,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-pendingWrite:
		if err != nil {
			t.Fatalf("write during migration: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertContinuityRouterRead(t, ctx, targetPeer, "second")
	if calls := dialer.Calls(); calls != 1 {
		t.Fatalf("target dial calls=%d, want 1", calls)
	}
	if _, err := targetPeer.Write([]byte("again")); err != nil {
		t.Fatal(err)
	}
	assertContinuityRouterRead(t, ctx, stream, "again")
	if router.Count() != 1 || runtime.Count() != 1 {
		t.Fatalf("flow counts client=%d server=%d", router.Count(), runtime.Count())
	}

	_ = stream.Close()
	_ = secondClientMux.Close()
	_ = secondServerMux.Close()
	_ = firstClientControl.Close()
	_ = firstServerControl.Close()
	_ = secondClientControl.Close()
	_ = secondServerControl.Close()
}

func assertContinuityRouterRead(t *testing.T, ctx context.Context, reader io.Reader, want string) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		buffer := make([]byte, len(want))
		_, err := io.ReadFull(reader, buffer)
		if err == nil && string(buffer) != want {
			err = errors.New("unexpected continuity payload: " + string(buffer))
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func newContinuityRouterControl(
	t *testing.T,
	ctx context.Context,
	mux *session.Mux,
	constellationID protocol.ContinuityID,
) *constellation.ControlChannel {
	t.Helper()
	control, err := constellation.NewControlChannel(ctx, constellation.ControlChannelConfig{
		Mux: mux, ConstellationID: constellationID, FirstMessageID: 3, MaxFlows: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	return control
}

type continuityRouterDialer struct {
	mu         sync.Mutex
	connection net.Conn
	calls      int
}

func (d *continuityRouterDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.connection == nil {
		return nil, errors.New("continuity target dialed more than once")
	}
	connection := d.connection
	d.connection = nil
	return connection, nil
}

func (d *continuityRouterDialer) Calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func newContinuityRouterMuxPair(t *testing.T) (*session.Mux, *session.Mux) {
	t.Helper()
	left, right := newContinuityRouterCarrierPair()
	typeMap, err := protocol.NewTypeMap([32]byte{0x4a, 0x71})
	if err != nil {
		t.Fatal(err)
	}
	client, err := session.New(session.Config{
		Role: session.RoleClient, Carrier: left, TypeMap: typeMap,
		InitialWindow: 64 * 1024, MaxStreams: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := session.New(session.Config{
		Role: session.RoleServer, Carrier: right, TypeMap: typeMap,
		InitialWindow: 64 * 1024, MaxStreams: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}

type continuityRouterCarrier struct {
	in    <-chan []byte
	out   chan<- []byte
	done  <-chan struct{}
	close func()
	once  sync.Once
}

var _ carrier.Carrier = (*continuityRouterCarrier)(nil)

func newContinuityRouterCarrierPair() (*continuityRouterCarrier, *continuityRouterCarrier) {
	leftToRight := make(chan []byte, 128)
	rightToLeft := make(chan []byte, 128)
	done := make(chan struct{})
	var once sync.Once
	closePair := func() { once.Do(func() { close(done) }) }
	return &continuityRouterCarrier{in: rightToLeft, out: leftToRight, done: done, close: closePair},
		&continuityRouterCarrier{in: leftToRight, out: rightToLeft, done: done, close: closePair}
}

func (c *continuityRouterCarrier) Send(ctx context.Context, payload []byte) error {
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

func (c *continuityRouterCarrier) Receive(ctx context.Context) ([]byte, error) {
	select {
	case payload := <-c.in:
		return payload, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *continuityRouterCarrier) Close() error {
	c.once.Do(c.close)
	return nil
}

func (c *continuityRouterCarrier) Kind() protocol.CarrierKind { return protocol.CarrierHTTPS }
