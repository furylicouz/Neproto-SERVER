package http3wt

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

func TestCarrierReliableAndDatagramRoundTrip(t *testing.T) {
	server, client, accepted := newCarrierPair(t, 4)
	if client.Kind() != protocol.CarrierHTTP3 || accepted.Kind() != protocol.CarrierHTTP3 {
		t.Fatalf("carrier kinds client=%d server=%d", client.Kind(), accepted.Kind())
	}
	if _, ok := any(client).(carrier.DatagramCarrier); !ok {
		t.Fatal("HTTP/3 client does not implement DatagramCarrier")
	}
	if server.ActiveSessions() != 1 {
		t.Fatalf("active sessions=%d, want 1", server.ActiveSessions())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assertReliableRoundTrip(t, ctx, client, accepted, []byte("client reliable"))
	assertReliableRoundTrip(t, ctx, accepted, client, []byte("server reliable"))
	assertDatagramRoundTrip(t, ctx, client, accepted, []byte("client datagram"))
	assertDatagramRoundTrip(t, ctx, accepted, client, []byte("server datagram"))
}

func TestCarrierEnforcesMessageAndTLSBoundaries(t *testing.T) {
	_, client, _ := newCarrierPair(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Send(ctx, nil); !errors.Is(err, ErrEmptyMessage) {
		t.Fatalf("empty message error=%v", err)
	}
	if err := client.Send(ctx, make([]byte, protocol.MaxCellSize+1)); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversized message error=%v", err)
	}
	if _, err := Dial(ctx, DialConfig{
		URL:       "https://localhost:443/private",
		TLSConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec -- verifies rejection
	}); !errors.Is(err, ErrTLSVerificationRequired) {
		t.Fatalf("insecure TLS error=%v", err)
	}
	if _, err := Dial(ctx, DialConfig{
		URL:       "http://localhost:443/private",
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13},
	}); !errors.Is(err, ErrTLSRequired) {
		t.Fatalf("plaintext URL error=%v", err)
	}
}

func TestDialServerAddressesVerifiesURLHostname(t *testing.T) {
	serverTLS, clientTLS := testTLSConfigs(t)
	clientTLS.ServerName = ""
	server, err := NewServer(ServerConfig{
		Path: "/private-direct-address", TLSConfig: serverTLS, MaxSessions: 1,
		FirstStreamTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(packet) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = packet.Close()
		<-serveDone
	})

	address, err := netip.ParseAddr("127.0.0.1")
	if err != nil {
		t.Fatalf("parse direct address: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, DialConfig{
		URL:             "https://localhost:" + portOf(t, packet.LocalAddr()) + "/private-direct-address",
		TLSConfig:       clientTLS,
		ServerAddresses: []netip.Addr{address},
	})
	if err != nil {
		t.Fatalf("dial direct address with DNS certificate identity: %v", err)
	}
	stats := client.ConnectionStats()
	if stats.SmoothedRTT <= 0 || stats.PacketsSent == 0 || stats.PacketsReceived == 0 {
		t.Fatalf("QUIC connection stats were not captured: %+v", stats)
	}
	_ = client.Close()
}

func TestServerRejectsForeignOriginBeforeAccept(t *testing.T) {
	serverTLS, clientTLS := testTLSConfigs(t)
	server, err := NewServer(ServerConfig{
		Path: "/private-origin", TLSConfig: serverTLS, MaxSessions: 2,
		FirstStreamTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(packet) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = packet.Close()
		<-serveDone
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = Dial(ctx, DialConfig{
		URL:       "https://localhost:" + portOf(t, packet.LocalAddr()) + "/private-origin",
		TLSConfig: clientTLS,
		Header:    http.Header{"Origin": []string{"https://evil.example"}},
	})
	if err == nil {
		t.Fatal("foreign Origin unexpectedly connected")
	}
	acceptCtx, acceptCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer acceptCancel()
	if _, err := server.Accept(acceptCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("foreign Origin reached accept queue: %v", err)
	}
	if server.ActiveSessions() != 0 {
		t.Fatalf("foreign Origin allocated session: %d", server.ActiveSessions())
	}
}

func TestCloseReleasesSessionSlot(t *testing.T) {
	server, client, accepted := newCarrierPair(t, 1)
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	_ = accepted.Close()
	deadline := time.Now().Add(2 * time.Second)
	for server.ActiveSessions() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.ActiveSessions() != 0 {
		t.Fatalf("session slot leaked: %d", server.ActiveSessions())
	}
}

func BenchmarkCarrierReliableStream(b *testing.B) {
	_, client, accepted := newCarrierPair(b, 1)
	payload := bytes.Repeat([]byte{0xa5}, protocol.MaxCellPayloadSize)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	received := make(chan error, 1)
	go func() {
		for range b.N {
			raw, err := accepted.Receive(ctx)
			if err != nil {
				received <- err
				return
			}
			if len(raw) != len(payload) {
				received <- errors.New("unexpected benchmark payload size")
				return
			}
		}
		received <- nil
	}()
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := client.Send(ctx, payload); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := <-received; err != nil {
		b.Fatal(err)
	}
}

func newCarrierPair(t testing.TB, maxSessions int) (*Server, *Conn, *Conn) {
	t.Helper()
	serverTLS, clientTLS := testTLSConfigs(t)
	server, err := NewServer(ServerConfig{
		Path: "/private-webtransport", TLSConfig: serverTLS,
		MaxSessions: maxSessions, FirstStreamTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(packet) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = packet.Close()
		<-serveDone
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, DialConfig{
		URL:       "https://localhost:" + portOf(t, packet.LocalAddr()) + "/private-webtransport",
		TLSConfig: clientTLS,
	})
	if err != nil {
		t.Fatalf("dial carrier: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	acceptedCarrier, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("accept carrier: %v", err)
	}
	accepted, ok := acceptedCarrier.(*Conn)
	if !ok {
		t.Fatalf("accepted carrier type %T", acceptedCarrier)
	}
	t.Cleanup(func() { _ = accepted.Close() })
	return server, client, accepted
}

func assertReliableRoundTrip(t *testing.T, ctx context.Context, sender, receiver *Conn, payload []byte) {
	t.Helper()
	if err := sender.Send(ctx, payload); err != nil {
		t.Fatalf("send reliable: %v", err)
	}
	got, err := receiver.Receive(ctx)
	if err != nil {
		t.Fatalf("receive reliable: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("reliable payload=%q, want %q", got, payload)
	}
}

func assertDatagramRoundTrip(t *testing.T, ctx context.Context, sender, receiver *Conn, payload []byte) {
	t.Helper()
	if err := sender.SendDatagram(ctx, payload); err != nil {
		t.Fatalf("send datagram: %v", err)
	}
	got, err := receiver.ReceiveDatagram(ctx)
	if err != nil {
		t.Fatalf("receive datagram: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("datagram payload=%q, want %q", got, payload)
	}
}

func portOf(t testing.TB, address net.Addr) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address.String())
	if err != nil {
		t.Fatalf("split address %q: %v", address, err)
	}
	return port
}

func testTLSConfigs(t testing.TB) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	server := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
	client := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "localhost"}
	return server, client
}
