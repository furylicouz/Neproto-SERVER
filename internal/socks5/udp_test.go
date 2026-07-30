package socks5

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestServerUDPAssociateRelaysRFC1928Datagrams(t *testing.T) {
	association := newStubUDPAssociation()
	server := Server{
		Connect: func(context.Context, Request) (io.ReadWriteCloser, error) {
			return nil, errors.New("CONNECT not expected")
		},
		AssociateUDP: func(context.Context) (UDPAssociation, error) { return association, nil },
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	control, relay := establishUDPAssociate(t, listener.Addr().String())
	defer control.Close()
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	payload := []byte("dns request")
	request := encodeSOCKSUDPDatagram(t, Request{Host: "1.1.1.1", Port: 53}, payload)
	if _, err := client.WriteToUDP(request, relay); err != nil {
		t.Fatal(err)
	}
	select {
	case datagram := <-association.writes:
		if datagram.Request != (Request{Host: "1.1.1.1", Port: 53}) ||
			!bytes.Equal(datagram.Payload, payload) {
			t.Fatalf("association datagram=%+v", datagram)
		}
	case <-time.After(time.Second):
		t.Fatal("SOCKS UDP request was not relayed")
	}

	association.reads <- stubUDPDatagram{
		Request: Request{Host: "8.8.8.8", Port: 53}, Payload: []byte("dns response"),
	}
	buffer := make([]byte, maxSOCKSUDPDatagram)
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	length, _, err := client.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	responseTarget, responsePayload, err := decodeSOCKSUDPDatagram(buffer[:length])
	if err != nil || responseTarget != (Request{Host: "8.8.8.8", Port: 53}) ||
		!bytes.Equal(responsePayload, []byte("dns response")) {
		t.Fatalf("response target=%+v payload=%q error=%v", responseTarget, responsePayload, err)
	}

	_ = control.Close()
	select {
	case <-association.closed:
	case <-time.After(time.Second):
		t.Fatal("closing TCP control did not close UDP association")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("SOCKS server did not stop")
	}
}

func TestServerUDPAssociateDropsFragments(t *testing.T) {
	association := newStubUDPAssociation()
	server := Server{
		Connect:      func(context.Context, Request) (io.ReadWriteCloser, error) { return nil, nil },
		AssociateUDP: func(context.Context) (UDPAssociation, error) { return association, nil },
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx, listener) }()
	control, relay := establishUDPAssociate(t, listener.Addr().String())
	defer control.Close()
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	fragmented := encodeSOCKSUDPDatagram(t, Request{Host: "1.1.1.1", Port: 53}, []byte("drop"))
	fragmented[2] = 1
	if _, err := client.WriteToUDP(fragmented, relay); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-association.writes:
		t.Fatalf("fragment was relayed: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func establishUDPAssociate(t *testing.T, address string) (net.Conn, *net.UDPAddr) {
	t.Helper()
	control, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(control, method); err != nil || !bytes.Equal(method, []byte{5, 0}) {
		t.Fatalf("method=%x error=%v", method, err)
	}
	if _, err := control.Write([]byte{5, commandUDPAssociate, 0, addressIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	replyHeader := make([]byte, 4)
	if _, err := io.ReadFull(control, replyHeader); err != nil || replyHeader[1] != ReplySucceeded {
		t.Fatalf("reply header=%x error=%v", replyHeader, err)
	}
	relay, err := readReplyUDPAddress(control, replyHeader[3])
	if err != nil {
		t.Fatal(err)
	}
	if !relay.IP.IsLoopback() || relay.Port == 0 {
		t.Fatalf("unsafe relay=%v", relay)
	}
	_ = control.SetDeadline(time.Time{})
	return control, relay
}

func readReplyUDPAddress(reader io.Reader, addressType byte) (*net.UDPAddr, error) {
	var ip net.IP
	switch addressType {
	case addressIPv4:
		ip = make(net.IP, net.IPv4len)
	case addressIPv6:
		ip = make(net.IP, net.IPv6len)
	default:
		return nil, ErrProtocol
	}
	if _, err := io.ReadFull(reader, ip); err != nil {
		return nil, err
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(reader, port); err != nil {
		return nil, err
	}
	return &net.UDPAddr{IP: ip, Port: int(binary.BigEndian.Uint16(port))}, nil
}

func encodeSOCKSUDPDatagram(t *testing.T, target Request, payload []byte) []byte {
	t.Helper()
	raw, err := encodeUDPDatagram(target, payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type stubUDPDatagram struct {
	Request Request
	Payload []byte
}

type stubUDPAssociation struct {
	writes chan stubUDPDatagram
	reads  chan stubUDPDatagram
	closed chan struct{}
	once   sync.Once
}

func newStubUDPAssociation() *stubUDPAssociation {
	return &stubUDPAssociation{
		writes: make(chan stubUDPDatagram, 4), reads: make(chan stubUDPDatagram, 4),
		closed: make(chan struct{}),
	}
}

func (a *stubUDPAssociation) WriteDatagram(payload []byte, target Request) error {
	select {
	case a.writes <- stubUDPDatagram{Request: target, Payload: append([]byte(nil), payload...)}:
		return nil
	case <-a.closed:
		return net.ErrClosed
	}
}

func (a *stubUDPAssociation) ReadDatagram() ([]byte, Request, error) {
	select {
	case datagram := <-a.reads:
		return append([]byte(nil), datagram.Payload...), datagram.Request, nil
	case <-a.closed:
		return nil, Request{}, net.ErrClosed
	}
}

func (a *stubUDPAssociation) Close() error {
	a.once.Do(func() { close(a.closed) })
	return nil
}
