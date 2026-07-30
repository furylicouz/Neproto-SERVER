package webrtc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"
)

func TestSignalingRejectsInvalidRouteMalformedAndOversizedRequests(t *testing.T) {
	server, tlsServer, tlsConfig := newSignalingTLSServer(t, ServerConfig{
		Path: "/hidden/offer", MaxPeers: 2,
		GatherTimeout: 3 * time.Second, ConnectTimeout: 3 * time.Second,
	})
	client := secureSignalingHTTPClient(tlsConfig)
	tests := []struct {
		name        string
		url         string
		body        []byte
		contentType string
	}{
		{name: "wrong route", url: tlsServer.URL + "/hidden/offer/", body: []byte(`{}`)},
		{name: "malformed", url: tlsServer.URL + "/hidden/offer", body: []byte(`{"type":"offer"`)},
		{name: "oversized", url: tlsServer.URL + "/hidden/offer", body: bytes.Repeat([]byte{'x'}, MaxSignalingBody+1)},
		{name: "trailing JSON", url: tlsServer.URL + "/hidden/offer", body: []byte(`{"type":"offer","sdp":"x"}{}`)},
		{name: "JSON prefix MIME", url: tlsServer.URL + "/hidden/offer", body: []byte(`{}`), contentType: "application/jsonp"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, test.url, bytes.NewReader(test.body))
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			contentType := test.contentType
			if contentType == "" {
				contentType = "application/json"
			}
			request.Header.Set("Content-Type", contentType)
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("request signaling: %v", err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("status=%d", response.StatusCode)
			}
		})
	}
	if server.ActivePeers() != 0 {
		t.Fatalf("invalid signaling allocated %d peers", server.ActivePeers())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := server.Accept(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invalid signaling entered carrier acceptor: %v", err)
	}
}

func TestSignalingCapsOutstandingPeerConnections(t *testing.T) {
	server, tlsServer, tlsConfig := newSignalingTLSServer(t, ServerConfig{
		Path: "/offer", MaxPeers: 1,
		GatherTimeout: 5 * time.Second, ConnectTimeout: 5 * time.Second,
	})
	offer, peer := createGatheredOffer(t)
	defer peer.Close()
	body, err := json.Marshal(offer)
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	httpClient := secureSignalingHTTPClient(tlsConfig)
	post := func() *http.Response {
		request, requestErr := http.NewRequest(http.MethodPost, tlsServer.URL+"/offer", bytes.NewReader(body))
		if requestErr != nil {
			t.Fatalf("create request: %v", requestErr)
		}
		request.Header.Set("Content-Type", "application/json")
		response, requestErr := httpClient.Do(request)
		if requestErr != nil {
			t.Fatalf("post offer: %v", requestErr)
		}
		return response
	}
	first := post()
	_, _ = io.Copy(io.Discard, first.Body)
	_ = first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status=%d", first.StatusCode)
	}
	if server.ActivePeers() != 1 {
		t.Fatalf("active peers=%d", server.ActivePeers())
	}
	second := post()
	_, _ = io.Copy(io.Discard, second.Body)
	_ = second.Body.Close()
	if second.StatusCode != http.StatusNotFound {
		t.Fatalf("second status=%d", second.StatusCode)
	}
}

func TestWebRTCDialRequiresVerifiedHTTPS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tests := []struct {
		name   string
		config DialConfig
		want   error
	}{
		{name: "HTTP", config: DialConfig{SignalingURL: "http://example.test/offer"}, want: ErrTLSRequired},
		{
			name: "skip verification",
			config: DialConfig{
				SignalingURL: "https://example.test/offer",
				TLSConfig:    &tls.Config{InsecureSkipVerify: true}, //nolint:gosec -- verifies rejection
			},
			want: ErrTLSVerificationRequired,
		},
		{
			name: "TLS 1.2", config: DialConfig{
				SignalingURL: "https://example.test/offer", TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
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
}

func createGatheredOffer(t *testing.T) (pion.SessionDescription, *pion.PeerConnection) {
	t.Helper()
	peer, err := pion.NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatalf("create peer: %v", err)
	}
	if _, err := peer.CreateDataChannel("updates", nil); err != nil {
		_ = peer.Close()
		t.Fatalf("create data channel: %v", err)
	}
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		_ = peer.Close()
		t.Fatalf("create offer: %v", err)
	}
	gathered := pion.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(offer); err != nil {
		_ = peer.Close()
		t.Fatalf("set local offer: %v", err)
	}
	select {
	case <-gathered:
	case <-time.After(5 * time.Second):
		_ = peer.Close()
		t.Fatal("offer gathering timed out")
	}
	return *peer.LocalDescription(), peer
}

func secureSignalingHTTPClient(config *tls.Config) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, TLSClientConfig: config.Clone(),
		DisableCompression: true, ForceAttemptHTTP2: false,
	}}
}
