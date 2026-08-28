package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/carrier/httpsws"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/socks5"
)

func TestSOCKSConnectOverAuthenticatedHTTPSCarrier(t *testing.T) {
	runHTTPSCarrierSOCKSE2E(t, false)
}

func TestSOCKSConnectOverPulseClientAndOffHTTPSServer(t *testing.T) {
	runHTTPSCarrierSOCKSE2E(t, true)
}

func runHTTPSCarrierSOCKSE2E(t *testing.T, pulseClient bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	acceptor, err := httpsws.NewAcceptor(httpsws.AcceptorConfig{Path: "/static/chunks/connect"})
	if err != nil {
		t.Fatalf("create HTTPS acceptor: %v", err)
	}
	defer acceptor.Close()
	tlsServer := httptest.NewUnstartedServer(acceptor.Handler())
	tlsServer.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsServer.StartTLS()
	defer tlsServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(tlsServer.Certificate())
	clientCarrier, err := httpsws.Dial(ctx, httpsws.DialConfig{
		URL: "wss" + strings.TrimPrefix(tlsServer.URL, "https") + "/static/chunks/connect",
		TLSConfig: &tls.Config{
			RootCAs: roots, MinVersion: tls.VersionTLS13,
		},
	})
	if err != nil {
		t.Fatalf("dial HTTPS carrier: %v", err)
	}
	serverCarrier, err := acceptor.Accept(ctx)
	if err != nil {
		t.Fatalf("accept HTTPS carrier: %v", err)
	}
	defer clientCarrier.Close()
	defer serverCarrier.Close()
	if !pulseClient {
		runSOCKSE2E(t, ctx, clientCarrier, serverCarrier)
		return
	}
	features := protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb
	serverConfig := session.AuthenticatedConfig{
		RootSecret: [protocol.RootSecretSize]byte{0x8a, 0x4f, 0x11, 0xd3}, ServerIdentity: "edge.example.test",
		Features: features, InitialWindow: 128 * 1024, MaxStreams: 32,
		DisableCover: true,
	}
	clientConfig := serverConfig
	clientConfig.DisableCover = false
	clientConfig.EnablePulse = true
	clientConfig.MaxCoverOverheadPercent = 5
	runSOCKSE2EWithConfig(t, ctx, clientCarrier, serverCarrier, clientConfig, serverConfig)
}

func runSOCKSE2E(t *testing.T, parent context.Context, clientCarrier, serverCarrier carrier.Carrier) {
	t.Helper()
	authConfig := session.AuthenticatedConfig{
		RootSecret: [protocol.RootSecretSize]byte{0x8a, 0x4f, 0x11, 0xd3}, ServerIdentity: "edge.example.test",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 128 * 1024, MaxStreams: 32,
		MaxCoverOverheadPercent: 30,
	}
	runSOCKSE2EWithConfig(t, parent, clientCarrier, serverCarrier, authConfig, authConfig)
}

func runSOCKSE2EWithConfig(
	t *testing.T,
	parent context.Context,
	clientCarrier, serverCarrier carrier.Carrier,
	clientConfig, serverConfig session.AuthenticatedConfig,
) {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer targetListener.Close()
	targetDone := make(chan error, 1)
	go func() {
		connection, acceptErr := targetListener.Accept()
		if acceptErr == nil {
			_, acceptErr = io.Copy(connection, connection)
			_ = connection.Close()
		}
		targetDone <- acceptErr
	}()

	serverAuth := make(chan struct {
		session *session.Authenticated
		err     error
	}, 1)
	go func() {
		authenticated, authErr := session.AcceptServer(ctx, serverCarrier, serverConfig)
		serverAuth <- struct {
			session *session.Authenticated
			err     error
		}{authenticated, authErr}
	}()
	clientSession, err := session.ConnectClient(ctx, clientCarrier, clientConfig)
	if err != nil {
		t.Fatalf("authenticate client: %v", err)
	}
	serverResult := <-serverAuth
	if serverResult.err != nil {
		t.Fatalf("authenticate server: %v", serverResult.err)
	}
	defer clientSession.Mux.Close()
	defer serverResult.session.Mux.Close()

	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- (proxy.Server{
			Mux: serverResult.session.Mux, Policy: proxy.DestinationPolicy{AllowPrivate: true},
		}).Serve(ctx)
	}()
	socksListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen SOCKS: %v", err)
	}
	connector := proxy.Connector{Mux: clientSession.Mux}
	socksDone := make(chan error, 1)
	go func() {
		socksDone <- (socks5.Server{Connect: connector.Connect}).Serve(ctx, socksListener)
	}()

	local, err := net.DialTimeout("tcp", socksListener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS: %v", err)
	}
	if err := local.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set SOCKS deadline: %v", err)
	}
	if _, err := local.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("write SOCKS greeting: %v", err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(local, method); err != nil || !bytes.Equal(method, []byte{5, 0}) {
		t.Fatalf("SOCKS method=%x err=%v", method, err)
	}
	targetAddress := targetListener.Addr().(*net.TCPAddr)
	request := []byte{5, 1, 0, 1, 127, 0, 0, 1}
	request = binary.BigEndian.AppendUint16(request, uint16(targetAddress.Port))
	if _, err := local.Write(request); err != nil {
		t.Fatalf("write SOCKS CONNECT: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(local, reply); err != nil {
		t.Fatalf("read SOCKS reply: %v", err)
	}
	if reply[1] != socks5.ReplySucceeded {
		t.Fatalf("SOCKS CONNECT reply=%d", reply[1])
	}

	payload := bytes.Repeat([]byte("unique-np2-e2e"), 4096)
	if _, err := local.Write(payload); err != nil {
		t.Fatalf("write E2E payload: %v", err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(local, response); err != nil {
		t.Fatalf("read E2E payload: %v", err)
	}
	if !bytes.Equal(response, payload) {
		t.Fatal("E2E payload mismatch")
	}
	_ = local.Close()
	cancel()

	waitResult(t, "SOCKS server", socksDone)
	waitResult(t, "proxy server", proxyDone)
	waitResult(t, "target", targetDone)
}

func waitResult(t *testing.T, name string, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not stop", name)
	}
}
