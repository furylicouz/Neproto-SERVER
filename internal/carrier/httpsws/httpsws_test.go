package httpsws

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func TestTLSCarrierRoundTrip(t *testing.T) {
	acceptor, server, tlsConfig, extensionHeader := newTLSServer(t, "/assets/connect")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, DialConfig{URL: websocketURL(server.URL, "/assets/connect"), TLSConfig: tlsConfig})
	if err != nil {
		t.Fatalf("dial carrier: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	serverCarrier, err := acceptor.Accept(ctx)
	if err != nil {
		t.Fatalf("accept carrier: %v", err)
	}
	t.Cleanup(func() { _ = serverCarrier.Close() })

	if got := <-extensionHeader; got != "" {
		t.Fatalf("compression extension was offered: %q", got)
	}
	if client.Kind() != protocol.CarrierHTTPS || serverCarrier.Kind() != protocol.CarrierHTTPS {
		t.Fatal("unexpected carrier kind")
	}

	request := []byte("encrypted-np2-cell")
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

	response := []byte("encrypted-np2-response")
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
}

func TestDialUsesPinnedAddressWithoutDNSAndKeepsTLSIdentity(t *testing.T) {
	acceptor, server, tlsConfig, _ := newTLSServer(t, "/pinned/connect")
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	tlsConfig.ServerName = "127.0.0.1"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, DialConfig{
		URL:       "wss://must-not-resolve.invalid:" + parsed.Port() + "/pinned/connect",
		TLSConfig: tlsConfig, ServerAddresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
	})
	if err != nil {
		t.Fatalf("dial pinned carrier: %v", err)
	}
	defer client.Close()
	serverCarrier, err := acceptor.Accept(ctx)
	if err != nil {
		t.Fatalf("accept pinned carrier: %v", err)
	}
	defer serverCarrier.Close()
}

func TestTLSCarrierCarriesMultiplexedSession(t *testing.T) {
	acceptor, server, tlsConfig, _ := newTLSServer(t, "/assets/session")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientCarrier, err := Dial(ctx, DialConfig{URL: websocketURL(server.URL, "/assets/session"), TLSConfig: tlsConfig})
	if err != nil {
		t.Fatalf("dial carrier: %v", err)
	}
	serverCarrier, err := acceptor.Accept(ctx)
	if err != nil {
		t.Fatalf("accept carrier: %v", err)
	}
	secret := [protocol.RootSecretSize]byte{0x61, 0x92, 0xd4}
	authConfig := session.AuthenticatedConfig{
		RootSecret: secret, ServerIdentity: "edge.example.test",
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 64 * 1024, MaxStreams: 8,
		MaxCoverOverheadPercent: 30,
	}
	serverAuth := make(chan struct {
		session *session.Authenticated
		err     error
	}, 1)
	go func() {
		authenticated, authErr := session.AcceptServer(ctx, serverCarrier, authConfig)
		serverAuth <- struct {
			session *session.Authenticated
			err     error
		}{authenticated, authErr}
	}()
	clientSession, err := session.ConnectClient(ctx, clientCarrier, authConfig)
	if err != nil {
		t.Fatalf("authenticate client session: %v", err)
	}
	serverResult := <-serverAuth
	if serverResult.err != nil {
		t.Fatalf("authenticate server session: %v", serverResult.err)
	}
	t.Cleanup(func() {
		_ = clientSession.Mux.Close()
		_ = serverResult.session.Mux.Close()
	})

	serverDone := make(chan error, 1)
	go func() {
		incoming, acceptErr := serverResult.session.Mux.Accept(ctx)
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		if string(incoming.Metadata()) != "tcp|example.test|443" {
			serverDone <- errors.New("metadata mismatch")
			return
		}
		stream, acceptErr := incoming.Accept()
		if acceptErr == nil {
			_, acceptErr = io.Copy(stream, stream)
		}
		if acceptErr == nil {
			acceptErr = stream.CloseWrite()
		}
		serverDone <- acceptErr
	}()

	stream, err := clientSession.Mux.Open(ctx, []byte("tcp|example.test|443"))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	payload := bytes.Repeat([]byte("np2-over-real-tls"), 2048)
	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatalf("close stream write side: %v", err)
	}
	response, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if !bytes.Equal(response, payload) {
		t.Fatal("multiplexed TLS payload mismatch")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("serve stream: %v", err)
	}
}

func TestDialRequiresVerifiedTLS13(t *testing.T) {
	acceptor, server, _, _ := newTLSServer(t, "/connect")
	_ = acceptor
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tests := []struct {
		name   string
		config DialConfig
		want   error
	}{
		{name: "plain websocket", config: DialConfig{URL: "ws://example.test/connect"}, want: ErrTLSRequired},
		{
			name: "skip verification",
			config: DialConfig{
				URL:       websocketURL(server.URL, "/connect"),
				TLSConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec -- verifies rejection
			},
			want: ErrTLSVerificationRequired,
		},
		{
			name: "old minimum version",
			config: DialConfig{
				URL:       websocketURL(server.URL, "/connect"),
				TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
			want: ErrTLS13Required,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Dial(ctx, test.config)
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}

	if _, err := Dial(ctx, DialConfig{URL: websocketURL(server.URL, "/connect")}); err == nil {
		t.Fatal("untrusted test certificate was accepted")
	}
}

func TestInvalidRouteNeverEntersCarrierAcceptor(t *testing.T) {
	acceptor, server, tlsConfig, _ := newTLSServer(t, "/exact/connect")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := Dial(ctx, DialConfig{URL: websocketURL(server.URL, "/exact/connect/"), TLSConfig: tlsConfig})
	if err == nil {
		t.Fatal("invalid route was accepted")
	}

	acceptCtx, acceptCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer acceptCancel()
	if _, err := acceptor.Accept(acceptCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invalid route entered acceptor: %v", err)
	}
}

func TestAcceptorRejectsPlainHTTPUpgrade(t *testing.T) {
	acceptor, err := NewAcceptor(AcceptorConfig{Path: "/secure"})
	if err != nil {
		t.Fatalf("create acceptor: %v", err)
	}
	server := httptest.NewServer(acceptor.Handler())
	defer server.Close()
	defer acceptor.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	connection, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/secure", nil)
	if connection != nil {
		defer connection.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("plain HTTP upgrade was not hidden as 404: status=%v err=%v", responseStatus(response), err)
	}

	acceptCtx, acceptCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer acceptCancel()
	if _, err := acceptor.Accept(acceptCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("plain HTTP entered acceptor: %v", err)
	}
}

func TestAcceptorAllowsExplicitLoopbackTLSProxy(t *testing.T) {
	acceptor, err := NewAcceptor(AcceptorConfig{Path: "/proxied", AllowLoopbackProxy: true})
	if err != nil {
		t.Fatalf("create acceptor: %v", err)
	}
	defer acceptor.Close()
	server := httptest.NewServer(acceptor.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/proxied", nil)
	if err != nil {
		t.Fatalf("loopback proxy upgrade: %v", err)
	}
	defer connection.CloseNow()
	carrier, err := acceptor.Accept(ctx)
	if err != nil {
		t.Fatalf("accept proxied carrier: %v", err)
	}
	defer carrier.Close()
}

func TestAcceptorCloseClosesQueuedConnections(t *testing.T) {
	acceptor, server, tlsConfig, _ := newTLSServer(t, "/queued")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := Dial(ctx, DialConfig{URL: websocketURL(server.URL, "/queued"), TLSConfig: tlsConfig})
	if err != nil {
		t.Fatalf("dial queued carrier: %v", err)
	}
	defer client.Close()
	if err := acceptor.Close(); err != nil {
		t.Fatalf("close acceptor: %v", err)
	}
	if _, err := acceptor.Accept(ctx); !errors.Is(err, ErrAcceptorClosed) {
		t.Fatalf("accept after close: %v", err)
	}
	if _, err := client.Receive(ctx); err == nil {
		t.Fatal("queued connection remained open")
	}
}

func TestCarrierRejectsTextAndOversizedMessages(t *testing.T) {
	acceptor, server, tlsConfig, _ := newTLSServer(t, "/connect")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	raw, response, err := websocket.Dial(ctx, websocketURL(server.URL, "/connect"), &websocket.DialOptions{
		HTTPClient:      secureHTTPClient(tlsConfig),
		CompressionMode: websocket.CompressionDisabled,
	})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial raw websocket: %v", err)
	}
	defer raw.CloseNow()
	serverCarrier, err := acceptor.Accept(ctx)
	if err != nil {
		t.Fatalf("accept carrier: %v", err)
	}
	defer serverCarrier.Close()

	if err := raw.Write(ctx, websocket.MessageText, []byte("not-a-cell")); err != nil {
		t.Fatalf("write text frame: %v", err)
	}
	if _, err := serverCarrier.Receive(ctx); !errors.Is(err, ErrUnexpectedMessage) {
		t.Fatalf("expected text rejection, got %v", err)
	}

	client, err := Dial(ctx, DialConfig{URL: websocketURL(server.URL, "/connect"), TLSConfig: tlsConfig})
	if err != nil {
		t.Fatalf("dial second carrier: %v", err)
	}
	defer client.Close()
	if err := client.Send(ctx, make([]byte, protocol.MaxCellSize+1)); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func newTLSServer(t *testing.T, route string) (*Acceptor, *httptest.Server, *tls.Config, <-chan string) {
	t.Helper()
	acceptor, err := NewAcceptor(AcceptorConfig{Path: route})
	if err != nil {
		t.Fatalf("create acceptor: %v", err)
	}
	extensions := make(chan string, 4)
	handler := acceptor.Handler()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		extensions <- request.Header.Get("Sec-WebSocket-Extensions")
		handler.ServeHTTP(writer, request)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}
	t.Cleanup(func() {
		_ = acceptor.Close()
		server.Close()
	})
	return acceptor, server, tlsConfig, extensions
}

func secureHTTPClient(config *tls.Config) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:              http.ProxyFromEnvironment,
		TLSClientConfig:    config.Clone(),
		DisableCompression: true,
	}}
}

func websocketURL(serverURL, route string) string {
	return "wss" + strings.TrimPrefix(serverURL, "https") + route
}

func responseStatus(response *http.Response) any {
	if response == nil {
		return nil
	}
	return response.StatusCode
}
