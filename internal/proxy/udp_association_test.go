package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
)

func TestUDPAssociationPreservesDatagramBoundaries(t *testing.T) {
	leftConn, rightConn := net.Pipe()
	left, err := NewUDPAssociation(
		pipeAssociationStream{Conn: leftConn}, CommandUDPFixed,
		Target{Host: "1.1.1.1", Port: 53}, 4096,
	)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewUDPAssociation(
		pipeAssociationStream{Conn: rightConn}, CommandUDPFixed,
		Target{Host: "1.1.1.1", Port: 53}, 4096,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = leftConn.Close()
		_ = rightConn.Close()
	})
	sent := [][]byte{[]byte("first"), {}, bytes.Repeat([]byte{0x7a}, 4096)}
	writeDone := make(chan error, 1)
	go func() {
		for _, payload := range sent {
			if err := left.WriteDatagram(payload, nil); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- nil
	}()
	for index, payload := range sent {
		received, source, err := right.ReadDatagram()
		if err != nil {
			t.Fatalf("read datagram %d: %v", index, err)
		}
		if !bytes.Equal(received, payload) || source != (Target{Host: "1.1.1.1", Port: 53}) {
			t.Fatalf("datagram %d payload=%x source=%+v", index, received, source)
		}
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write datagrams: %v", err)
	}
}

func TestUDPAssociationUsesFastDatagramsAndReliableOversizeFallback(t *testing.T) {
	leftConn, rightConn := net.Pipe()
	leftFast, rightFast := newTestDatagramEndpointPair(64)
	target := Target{Host: "1.1.1.1", Port: 53}
	left, err := NewUDPAssociationWithDatagrams(
		pipeAssociationStream{Conn: leftConn}, CommandUDPFixed, target, 1200, leftFast,
	)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewUDPAssociationWithDatagrams(
		pipeAssociationStream{Conn: rightConn}, CommandUDPFixed, target, 1200, rightFast,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = left.Abort()
		_ = right.Abort()
	})

	if err := left.WriteDatagram([]byte("fast"), nil); err != nil {
		t.Fatalf("write fast datagram: %v", err)
	}
	payload, _, err := right.ReadDatagram()
	if err != nil || string(payload) != "fast" {
		t.Fatalf("fast payload=%q err=%v", payload, err)
	}
	large := bytes.Repeat([]byte{0x45}, 128)
	writeDone := make(chan error, 1)
	go func() { writeDone <- left.WriteDatagram(large, nil) }()
	payload, _, err = right.ReadDatagram()
	if err != nil || !bytes.Equal(payload, large) {
		t.Fatalf("fallback payload=%x err=%v", payload, err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write reliable fallback: %v", err)
	}
	if leftFast.sent != 1 {
		t.Fatalf("fast sends=%d, want only the small record", leftFast.sent)
	}
}

func TestUDPAssociationRequiresPerPacketTargetForUnboundMode(t *testing.T) {
	stream := &bufferAssociationStream{}
	association, err := NewUDPAssociation(stream, CommandUDPAssociate, Target{}, 1200)
	if err != nil {
		t.Fatal(err)
	}
	if err := association.WriteDatagram([]byte("dns"), nil); !errors.Is(err, ErrInvalidUDPRecord) {
		t.Fatalf("missing target error=%v", err)
	}
	target := Target{Host: "8.8.8.8", Port: 53}
	if err := association.WriteDatagram([]byte("dns"), &target); err != nil {
		t.Fatalf("write targeted datagram: %v", err)
	}
	receiver, err := NewUDPAssociation(stream, CommandUDPAssociate, Target{}, 1200)
	if err != nil {
		t.Fatal(err)
	}
	payload, source, err := receiver.ReadDatagram()
	if err != nil || string(payload) != "dns" || source != target {
		t.Fatalf("payload=%q source=%+v err=%v", payload, source, err)
	}
}

func TestUDPAssociationRejectsOversizeAndNonIncreasingPacketID(t *testing.T) {
	stream := &bufferAssociationStream{}
	association, err := NewUDPAssociation(
		stream, CommandUDPFixed, Target{Host: "1.1.1.1", Port: 53}, 1200,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := association.WriteDatagram(make([]byte, 1201), nil); !errors.Is(err, ErrInvalidUDPRecord) {
		t.Fatalf("oversized datagram error=%v", err)
	}
	record, err := EncodeUDPRecord(UDPRecord{Type: UDPRecordDatagram, PacketID: 1, Payload: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	stream.Buffer.Reset()
	for range 2 {
		stream.Buffer.Write(binary.AppendUvarint(nil, uint64(len(record))))
		stream.Buffer.Write(record)
	}
	if _, _, err := association.ReadDatagram(); err != nil {
		t.Fatalf("first record: %v", err)
	}
	if _, _, err := association.ReadDatagram(); !errors.Is(err, ErrInvalidUDPRecord) {
		t.Fatalf("replayed packet id error=%v", err)
	}
}

type pipeAssociationStream struct{ net.Conn }

func (pipeAssociationStream) CloseWrite() error { return nil }

type bufferAssociationStream struct{ bytes.Buffer }

func (*bufferAssociationStream) Close() error      { return nil }
func (*bufferAssociationStream) CloseWrite() error { return nil }

var _ io.ReadWriteCloser = (*bufferAssociationStream)(nil)

type testDatagramEndpoint struct {
	in     chan []byte
	out    chan []byte
	max    int
	sent   int
	closed chan struct{}
}

func newTestDatagramEndpointPair(maximum int) (*testDatagramEndpoint, *testDatagramEndpoint) {
	leftToRight := make(chan []byte, 16)
	rightToLeft := make(chan []byte, 16)
	return &testDatagramEndpoint{in: rightToLeft, out: leftToRight, max: maximum, closed: make(chan struct{})},
		&testDatagramEndpoint{in: leftToRight, out: rightToLeft, max: maximum, closed: make(chan struct{})}
}

func (e *testDatagramEndpoint) Send(ctx context.Context, raw []byte) error {
	if len(raw) > e.max {
		return errors.New("too large")
	}
	e.sent++
	select {
	case e.out <- append([]byte(nil), raw...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *testDatagramEndpoint) Receive(ctx context.Context) ([]byte, error) {
	select {
	case raw := <-e.in:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.closed:
		return nil, io.EOF
	}
}

func (e *testDatagramEndpoint) MaxPayload() int { return e.max }
func (e *testDatagramEndpoint) Close() error {
	select {
	case <-e.closed:
	default:
		close(e.closed)
	}
	return nil
}
