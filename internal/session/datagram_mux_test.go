package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

func TestDatagramMuxEncryptsRoutesAndPreservesBothDirections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientWire := newDatagramMemoryCarrier(1150)
	serverWire := newDatagramMemoryCarrier(1150)
	clientWire.peer = serverWire
	serverWire.peer = clientWire
	keys := datagramTestKeys()
	client, err := newDatagramMux(ctx, clientWire, RoleClient, keys)
	if err != nil {
		t.Fatalf("create client mux: %v", err)
	}
	server, err := newDatagramMux(ctx, serverWire, RoleServer, keys)
	if err != nil {
		t.Fatalf("create server mux: %v", err)
	}
	client.Enable(1024)
	server.Enable(1024)
	clientEndpoint, err := client.OpenEndpoint(17)
	if err != nil {
		t.Fatalf("open client endpoint: %v", err)
	}
	serverEndpoint, err := server.OpenEndpoint(17)
	if err != nil {
		t.Fatalf("open server endpoint: %v", err)
	}

	assertEndpointRoundTrip(t, clientEndpoint, serverEndpoint, []byte("client datagram"))
	assertEndpointRoundTrip(t, serverEndpoint, clientEndpoint, []byte("server datagram"))
	deadline := time.Now().Add(time.Second)
	for {
		clientStats, serverStats := client.Stats(), server.Stats()
		if clientStats.Sent == 1 && clientStats.Received == 1 &&
			serverStats.Sent == 1 && serverStats.Received == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unexpected stats client=%+v server=%+v", clientStats, serverStats)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDatagramMuxDropsTamperingReplayAndUnknownAssociations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientWire := newDatagramMemoryCarrier(1150)
	serverWire := newDatagramMemoryCarrier(1150)
	keys := datagramTestKeys()
	client, err := newDatagramMux(ctx, clientWire, RoleClient, keys)
	if err != nil {
		t.Fatal(err)
	}
	server, err := newDatagramMux(ctx, serverWire, RoleServer, keys)
	if err != nil {
		t.Fatal(err)
	}
	client.Enable(1024)
	server.Enable(1024)
	sender, _ := client.OpenEndpoint(19)
	receiver, _ := server.OpenEndpoint(19)
	unknown, _ := client.OpenEndpoint(21)

	if err := sender.Send(ctx, []byte("protected")); err != nil {
		t.Fatalf("send protected: %v", err)
	}
	raw := <-clientWire.outbound
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-1] ^= 0x80
	serverWire.inbound <- tampered
	serverWire.inbound <- append([]byte(nil), raw...)
	serverWire.inbound <- append([]byte(nil), raw...)
	if err := unknown.Send(ctx, []byte("unknown")); err != nil {
		t.Fatalf("send unknown: %v", err)
	}
	serverWire.inbound <- <-clientWire.outbound

	receiveCtx, receiveCancel := context.WithTimeout(ctx, time.Second)
	got, err := receiver.Receive(receiveCtx)
	receiveCancel()
	if err != nil || !bytes.Equal(got, []byte("protected")) {
		t.Fatalf("received=%q err=%v", got, err)
	}
	quietCtx, quietCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer quietCancel()
	if _, err := receiver.Receive(quietCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("tamper/replay/unknown reached endpoint: %v", err)
	}
	stats := server.Stats()
	if stats.AuthenticationDrops != 1 || stats.ReplayDrops != 1 || stats.UnknownAssociationDrops != 1 {
		t.Fatalf("drop stats=%+v", stats)
	}
}

func TestDatagramEndpointEnforcesNegotiatedLimitAndLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wire := newDatagramMemoryCarrier(1150)
	mux, err := newDatagramMux(ctx, wire, RoleClient, datagramTestKeys())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mux.OpenEndpoint(7); !errors.Is(err, ErrDatagramUnavailable) {
		t.Fatalf("disabled endpoint error=%v", err)
	}
	mux.Enable(512)
	endpoint, err := mux.OpenEndpoint(7)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.MaxPayload() != 512 {
		t.Fatalf("max payload=%d", endpoint.MaxPayload())
	}
	if err := endpoint.Send(ctx, make([]byte, 513)); !errors.Is(err, ErrDatagramTooLarge) {
		t.Fatalf("oversized error=%v", err)
	}
	if _, err := mux.OpenEndpoint(7); !errors.Is(err, ErrDatagramAssociationExists) {
		t.Fatalf("duplicate endpoint error=%v", err)
	}
	if err := endpoint.Close(); err != nil {
		t.Fatalf("close endpoint: %v", err)
	}
	if _, err := endpoint.Receive(ctx); !errors.Is(err, ErrDatagramEndpointClosed) {
		t.Fatalf("closed receive error=%v", err)
	}
	cancel()
}

func TestDatagramMuxRekeysAtForwardSecretBarrier(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientWire := newDatagramMemoryCarrier(1150)
	serverWire := newDatagramMemoryCarrier(1150)
	clientWire.peer = serverWire
	serverWire.peer = clientWire
	client, err := newDatagramMux(ctx, clientWire, RoleClient, datagramTestKeys())
	if err != nil {
		t.Fatal(err)
	}
	server, err := newDatagramMux(ctx, serverWire, RoleServer, datagramTestKeys())
	if err != nil {
		t.Fatal(err)
	}
	client.Enable(1024)
	server.Enable(1024)
	clientEndpoint, _ := client.OpenEndpoint(29)
	serverEndpoint, _ := server.OpenEndpoint(29)
	assertEndpointRoundTrip(t, clientEndpoint, serverEndpoint, []byte("before rekey"))
	if client.sendCounter == 0 {
		t.Fatal("missing pre-rekey datagram counter")
	}
	forwardSecretKeys := protocol.SessionKeys{Control: [32]byte{0xf1, 0xe2, 0xd3, 0xc4}}
	if err := client.rekey(forwardSecretKeys); err != nil {
		t.Fatal(err)
	}
	if err := server.rekey(forwardSecretKeys); err != nil {
		t.Fatal(err)
	}
	if client.sendCounter != 0 {
		t.Fatalf("datagram counter was not reset at fresh key barrier: %d", client.sendCounter)
	}
	assertEndpointRoundTrip(t, clientEndpoint, serverEndpoint, []byte("after rekey"))
	assertEndpointRoundTrip(t, serverEndpoint, clientEndpoint, []byte("reverse after rekey"))
}

func assertEndpointRoundTrip(t *testing.T, sender, receiver *DatagramEndpoint, payload []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sender.Send(ctx, payload); err != nil {
		t.Fatalf("send: %v", err)
	}
	got, err := receiver.Receive(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload=%q want=%q", got, payload)
	}
}

func datagramTestKeys() protocol.SessionKeys {
	return protocol.SessionKeys{Control: [32]byte{1, 2, 3, 4}}
}

type datagramMemoryCarrier struct {
	inbound  chan []byte
	outbound chan []byte
	peer     *datagramMemoryCarrier
	maximum  int
}

func newDatagramMemoryCarrier(maximum int) *datagramMemoryCarrier {
	return &datagramMemoryCarrier{
		inbound: make(chan []byte, 16), outbound: make(chan []byte, 16), maximum: maximum,
	}
}

func (c *datagramMemoryCarrier) Send(context.Context, []byte) error { return nil }
func (c *datagramMemoryCarrier) Receive(context.Context) ([]byte, error) {
	return nil, io.EOF
}
func (c *datagramMemoryCarrier) Close() error               { return nil }
func (c *datagramMemoryCarrier) Kind() protocol.CarrierKind { return protocol.CarrierHTTP3 }
func (c *datagramMemoryCarrier) MaxDatagramPayload() int    { return c.maximum }
func (c *datagramMemoryCarrier) SendDatagram(ctx context.Context, raw []byte) error {
	copyOfRaw := append([]byte(nil), raw...)
	if c.peer != nil {
		select {
		case c.peer.inbound <- copyOfRaw:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case c.outbound <- copyOfRaw:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *datagramMemoryCarrier) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case raw := <-c.inbound:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
