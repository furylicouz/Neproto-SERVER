package cover

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

func TestTransportPadsRealCellsAndSchedulesValidDummyCells(t *testing.T) {
	typeMap := transportTypeMap(t)
	engine, err := NewEngine(Config{
		Profile: ProfileWeb, MaxOverheadPercent: 100, MaxBudgetBytes: 65_535,
		Seed: [32]byte{0x41, 0x83},
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	underlying := newRecordingCarrier()
	transport, err := NewTransport(TransportConfig{
		Carrier: underlying, TypeMap: typeMap, Engine: engine, PaddingSeed: [32]byte{0x72, 0x19},
	})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer transport.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	payload := bytes.Repeat([]byte{0xa5}, protocol.MaxCellPayloadSize)
	raw, err := protocol.EncodeCell(typeMap, protocol.Cell{
		Kind: protocol.CellData, StreamID: 1, Sequence: 1, Payload: payload,
	})
	if err != nil {
		t.Fatalf("encode real cell: %v", err)
	}
	if err := transport.Send(ctx, raw); err != nil {
		t.Fatalf("send real cell: %v", err)
	}

	gotReal := <-underlying.sent
	realCell, err := protocol.DecodeCell(typeMap, gotReal)
	if err != nil {
		t.Fatalf("decode padded cell: %v", err)
	}
	if !bytes.Equal(realCell.Payload, payload) || len(realCell.Padding) == 0 {
		t.Fatalf("real cell payload/padding mismatch: payload=%d padding=%d", len(realCell.Payload), len(realCell.Padding))
	}

	select {
	case gotDummy := <-underlying.sent:
		dummyCell, err := protocol.DecodeCell(typeMap, gotDummy)
		if err != nil {
			t.Fatalf("decode dummy cell: %v", err)
		}
		if dummyCell.Kind != protocol.CellDummy || dummyCell.StreamID != 0 {
			t.Fatalf("invalid dummy cell: %#v", dummyCell)
		}
	case <-ctx.Done():
		t.Fatal("earned cover budget did not produce a dummy cell")
	}

	stats := transport.Stats()
	statsDeadline := time.Now().Add(time.Second)
	for stats.DummyMessages == 0 && time.Now().Before(statsDeadline) {
		time.Sleep(time.Millisecond)
		stats = transport.Stats()
	}
	if stats.RealMessages != 1 || stats.DummyMessages == 0 || stats.PaddingBytes == 0 {
		t.Fatalf("unexpected transport stats: %#v", stats)
	}
	if stats.PaddingBytes+stats.DummyWireBytes > stats.RealWireBytes {
		t.Fatalf("actual overhead exceeded 100%% budget: %#v", stats)
	}
}

func TestQuietTransportPreservesCellAndNeverSendsDummy(t *testing.T) {
	typeMap := transportTypeMap(t)
	engine, err := NewEngine(Config{
		Profile: ProfileQuiet, MaxOverheadPercent: 100, Seed: [32]byte{0x51},
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	underlying := newRecordingCarrier()
	transport, err := NewTransport(TransportConfig{
		Carrier: underlying, TypeMap: typeMap, Engine: engine, PaddingSeed: [32]byte{0x52},
	})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer transport.Close()
	raw, err := protocol.EncodeCell(typeMap, protocol.Cell{
		Kind: protocol.CellPing, StreamID: 0, Payload: []byte("ping"),
	})
	if err != nil {
		t.Fatalf("encode cell: %v", err)
	}
	if err := transport.Send(context.Background(), raw); err != nil {
		t.Fatalf("send cell: %v", err)
	}
	if got := <-underlying.sent; !bytes.Equal(got, raw) {
		t.Fatal("quiet profile changed real cell")
	}
	select {
	case unexpected := <-underlying.sent:
		t.Fatalf("quiet profile sent dummy: %x", unexpected)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestTransportReceivePassesCarrierMessagesThrough(t *testing.T) {
	typeMap := transportTypeMap(t)
	engine, _ := NewEngine(Config{Profile: ProfileQuiet, Seed: [32]byte{1}})
	underlying := newRecordingCarrier()
	transport, err := NewTransport(TransportConfig{
		Carrier: underlying, TypeMap: typeMap, Engine: engine, PaddingSeed: [32]byte{2},
	})
	if err != nil {
		t.Fatalf("create transport: %v", err)
	}
	defer transport.Close()
	want := []byte("carrier-message")
	underlying.received <- want
	got, err := transport.Receive(context.Background())
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("receive=%x err=%v", got, err)
	}
}

func transportTypeMap(t *testing.T) protocol.TypeMap {
	t.Helper()
	typeMap, err := protocol.NewTypeMap([32]byte{0x91, 0x31})
	if err != nil {
		t.Fatalf("create type map: %v", err)
	}
	return typeMap
}

type recordingCarrier struct {
	sent      chan []byte
	received  chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

var _ carrier.Carrier = (*recordingCarrier)(nil)

func newRecordingCarrier() *recordingCarrier {
	return &recordingCarrier{
		sent: make(chan []byte, 256), received: make(chan []byte, 16), done: make(chan struct{}),
	}
}

func (c *recordingCarrier) Send(ctx context.Context, raw []byte) error {
	copyOfRaw := append([]byte(nil), raw...)
	select {
	case c.sent <- copyOfRaw:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return io.EOF
	}
}

func (c *recordingCarrier) Receive(ctx context.Context) ([]byte, error) {
	select {
	case raw := <-c.received:
		return append([]byte(nil), raw...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *recordingCarrier) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return nil
}

func (c *recordingCarrier) Kind() protocol.CarrierKind { return protocol.CarrierHTTPS }
