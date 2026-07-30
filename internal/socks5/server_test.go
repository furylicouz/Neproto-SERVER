package socks5

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestServeConnNegotiatesConnectAndRelays(t *testing.T) {
	client, serverSide := net.Pipe()
	upstream, targetSide := net.Pipe()
	defer client.Close()
	defer targetSide.Close()
	requests := make(chan Request, 1)
	server := Server{Connect: func(_ context.Context, request Request) (io.ReadWriteCloser, error) {
		requests <- request
		return upstream, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- server.serveConn(ctx, serverSide) }()

	if _, err := client.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(client, method); err != nil || !bytes.Equal(method, []byte{5, 0}) {
		t.Fatalf("method response=%x err=%v", method, err)
	}
	request := append([]byte{5, 1, 0, 3, 11}, []byte("example.com")...)
	request = append(request, 0x01, 0xbb)
	if _, err := client.Write(request); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read CONNECT reply: %v", err)
	}
	if !bytes.Equal(reply, []byte{5, ReplySucceeded, 0, 1, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("unexpected CONNECT reply: %x", reply)
	}
	if got := <-requests; got != (Request{Host: "example.com", Port: 443}) {
		t.Fatalf("request mismatch: %#v", got)
	}

	clientPayload := []byte("through-socks")
	go func() { _, _ = client.Write(clientPayload) }()
	gotClientPayload := make([]byte, len(clientPayload))
	if _, err := io.ReadFull(targetSide, gotClientPayload); err != nil || !bytes.Equal(gotClientPayload, clientPayload) {
		t.Fatalf("target read=%q err=%v", gotClientPayload, err)
	}
	targetPayload := []byte("target-response")
	go func() { _, _ = targetSide.Write(targetPayload) }()
	gotTargetPayload := make([]byte, len(targetPayload))
	if _, err := io.ReadFull(client, gotTargetPayload); err != nil || !bytes.Equal(gotTargetPayload, targetPayload) {
		t.Fatalf("client read=%q err=%v", gotTargetPayload, err)
	}
	_ = client.Close()
	_ = targetSide.Close()
	select {
	case err := <-served:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("serve connection: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("serve connection did not stop")
	}
}

func TestServeConnRejectsUnsupportedAuthentication(t *testing.T) {
	client, serverSide := net.Pipe()
	defer client.Close()
	called := false
	server := Server{Connect: func(context.Context, Request) (io.ReadWriteCloser, error) {
		called = true
		return nil, errors.New("unexpected")
	}}
	served := make(chan error, 1)
	go func() { served <- server.serveConn(context.Background(), serverSide) }()
	if _, err := client.Write([]byte{5, 2, 1, 2}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(client, response); err != nil || !bytes.Equal(response, []byte{5, 0xff}) {
		t.Fatalf("response=%x err=%v", response, err)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve connection: %v", err)
	}
	if called {
		t.Fatal("connector called without supported authentication")
	}
}

func TestServeConnMapsCommandAndConnectorFailures(t *testing.T) {
	tests := []struct {
		name       string
		command    byte
		connectErr error
		wantReply  byte
	}{
		{name: "BIND unsupported", command: 2, wantReply: ReplyCommandNotSupported},
		{name: "policy denied", command: 1, connectErr: &ReplyError{Code: ReplyNotAllowed}, wantReply: ReplyNotAllowed},
		{name: "generic failure", command: 1, connectErr: errors.New("dial failed"), wantReply: ReplyGeneralFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, serverSide := net.Pipe()
			defer client.Close()
			server := Server{Connect: func(context.Context, Request) (io.ReadWriteCloser, error) {
				return nil, test.connectErr
			}}
			served := make(chan error, 1)
			go func() { served <- server.serveConn(context.Background(), serverSide) }()
			if _, err := client.Write([]byte{5, 1, 0}); err != nil {
				t.Fatalf("write greeting: %v", err)
			}
			method := make([]byte, 2)
			_, _ = io.ReadFull(client, method)
			if _, err := client.Write([]byte{5, test.command, 0, 1, 1, 1, 1, 1, 0, 80}); err != nil {
				t.Fatalf("write request: %v", err)
			}
			reply := make([]byte, 10)
			if _, err := io.ReadFull(client, reply); err != nil {
				t.Fatalf("read reply: %v", err)
			}
			if reply[1] != test.wantReply {
				t.Fatalf("reply=%d want=%d", reply[1], test.wantReply)
			}
			if err := <-served; err != nil {
				t.Fatalf("serve connection: %v", err)
			}
		})
	}
}

func TestServeRequiresLoopbackListener(t *testing.T) {
	listener := &stubListener{address: &net.TCPAddr{IP: net.IPv4zero, Port: 1080}}
	err := (Server{Connect: func(context.Context, Request) (io.ReadWriteCloser, error) {
		return nil, nil
	}}).Serve(context.Background(), listener)
	if !errors.Is(err, ErrUnsafeBind) {
		t.Fatalf("expected unsafe bind error, got %v", err)
	}
}

type stubListener struct {
	address net.Addr
}

func (l *stubListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *stubListener) Close() error              { return nil }
func (l *stubListener) Addr() net.Addr            { return l.address }
