package webrtc

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

func TestWebRTCCarrierRoundTrip(t *testing.T) {
	server, tlsServer, tlsConfig := newSignalingTLSServer(t, ServerConfig{
		Path: "/rtc/session", MaxPeers: 8,
		GatherTimeout: 5 * time.Second, ConnectTimeout: 8 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := Dial(ctx, DialConfig{
		SignalingURL: signalingURL(tlsServer.URL, "/rtc/session"),
		TLSConfig:    tlsConfig, GatherTimeout: 5 * time.Second, ConnectTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial WebRTC carrier: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	serverCarrier, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("accept WebRTC carrier: %v", err)
	}
	t.Cleanup(func() { _ = serverCarrier.Close() })
	if client.Kind() != protocol.CarrierWebRTC || serverCarrier.Kind() != protocol.CarrierWebRTC {
		t.Fatal("unexpected carrier kind")
	}
	if !client.dataChannel.Ordered() || client.dataChannel.MaxRetransmits() != nil || client.dataChannel.MaxPacketLifeTime() != nil {
		t.Fatal("data channel is not ordered and fully reliable")
	}
	if client.datagramChannel == nil || client.datagramChannel.Ordered() ||
		client.datagramChannel.MaxRetransmits() == nil || *client.datagramChannel.MaxRetransmits() != 0 {
		t.Fatal("datagram channel is not unordered with zero retransmissions")
	}
	remoteAddresses := client.RemoteAddresses()
	if len(remoteAddresses) != 1 || !remoteAddresses[0].IsValid() || remoteAddresses[0].IsUnspecified() {
		t.Fatalf("selected remote ICE addresses=%v, want one numeric address", remoteAddresses)
	}

	request := []byte("opaque-np2-over-dtls-sctp")
	if err := client.Send(ctx, request); err != nil {
		t.Fatalf("send request: %v", err)
	}
	gotRequest, err := serverCarrier.Receive(ctx)
	if err != nil {
		t.Fatalf("receive request: %v", err)
	}
	if !bytes.Equal(gotRequest, request) {
		t.Fatal("request mismatch")
	}
	response := []byte("opaque-response")
	if err := serverCarrier.Send(ctx, response); err != nil {
		t.Fatalf("send response: %v", err)
	}
	gotResponse, err := client.Receive(ctx)
	if err != nil {
		t.Fatalf("receive response: %v", err)
	}
	if !bytes.Equal(gotResponse, response) {
		t.Fatal("response mismatch")
	}
	if err := client.SendDatagram(ctx, []byte("realtime")); err != nil {
		t.Fatalf("send datagram: %v", err)
	}
	serverDatagrams, ok := serverCarrier.(carrier.DatagramCarrier)
	if !ok {
		t.Fatal("server WebRTC carrier does not expose datagrams")
	}
	gotDatagram, err := serverDatagrams.ReceiveDatagram(ctx)
	if err != nil || string(gotDatagram) != "realtime" {
		t.Fatalf("datagram=%q error=%v", gotDatagram, err)
	}
	if err := serverDatagrams.SendDatagram(ctx, []byte("realtime-response")); err != nil {
		t.Fatalf("send response datagram: %v", err)
	}
	gotDatagram, err = client.ReceiveDatagram(ctx)
	if err != nil || string(gotDatagram) != "realtime-response" {
		t.Fatalf("response datagram=%q error=%v", gotDatagram, err)
	}
}

func TestWebRTCCarrierRejectsTextAndOversizedMessages(t *testing.T) {
	server, tlsServer, tlsConfig := newSignalingTLSServer(t, ServerConfig{
		Path: "/rtc/messages", MaxPeers: 4,
		GatherTimeout: 5 * time.Second, ConnectTimeout: 8 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := Dial(ctx, DialConfig{
		SignalingURL: signalingURL(tlsServer.URL, "/rtc/messages"), TLSConfig: tlsConfig,
		GatherTimeout: 5 * time.Second, ConnectTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial carrier: %v", err)
	}
	defer client.Close()
	serverCarrier, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("accept carrier: %v", err)
	}
	defer serverCarrier.Close()

	if err := client.Send(ctx, make([]byte, protocol.MaxCellSize+1)); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("expected size rejection, got %v", err)
	}
	if err := client.dataChannel.SendText("not-binary"); err != nil {
		t.Fatalf("send text: %v", err)
	}
	if _, err := serverCarrier.Receive(ctx); !errors.Is(err, ErrUnexpectedMessage) {
		t.Fatalf("expected text rejection, got %v", err)
	}
}

func TestWebRTCCloseReleasesServerPeerSlot(t *testing.T) {
	server, tlsServer, tlsConfig := newSignalingTLSServer(t, ServerConfig{
		Path: "/rtc/close", MaxPeers: 1,
		GatherTimeout: 5 * time.Second, ConnectTimeout: 8 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := Dial(ctx, DialConfig{
		SignalingURL: signalingURL(tlsServer.URL, "/rtc/close"), TLSConfig: tlsConfig,
		GatherTimeout: 5 * time.Second, ConnectTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial carrier: %v", err)
	}
	peer, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("accept carrier: %v", err)
	}
	if server.ActivePeers() != 1 {
		t.Fatalf("active peers=%d", server.ActivePeers())
	}
	_ = client.Close()
	_ = peer.Close()
	deadline := time.Now().Add(2 * time.Second)
	for server.ActivePeers() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.ActivePeers() != 0 {
		t.Fatalf("peer slot leaked: %d", server.ActivePeers())
	}
}

func newSignalingTLSServer(t *testing.T, config ServerConfig) (*Server, *httptest.Server, *tls.Config) {
	t.Helper()
	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("create signaling server: %v", err)
	}
	tlsServer := httptest.NewUnstartedServer(server.Handler())
	tlsServer.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsServer.StartTLS()
	roots := x509.NewCertPool()
	roots.AddCert(tlsServer.Certificate())
	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}
	t.Cleanup(func() {
		_ = server.Close()
		tlsServer.Close()
	})
	return server, tlsServer, tlsConfig
}

func signalingURL(serverURL, route string) string {
	return "https" + strings.TrimPrefix(serverURL, "https") + route
}
