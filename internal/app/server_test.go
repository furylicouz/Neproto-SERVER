package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier/http3wt"
	"neproto.local/chameleon/internal/carrier/httpsws"
	rtc "neproto.local/chameleon/internal/carrier/webrtc"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/protocol"
	proxycore "neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
)

func TestRunServerBehindLoopbackTLSProxyAuthenticatesAndBlocksPrivateTarget(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "root.secret")
	secretRaw := bytes.Repeat([]byte{0x73}, protocol.RootSecretSize)
	if err := os.WriteFile(secretPath, []byte(base64.RawURLEncoding.EncodeToString(secretRaw)+"\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve backend address: %v", err)
	}
	backendAddress := probe.Addr().String()
	_ = probe.Close()
	metricsProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve metrics address: %v", err)
	}
	metricsAddress := metricsProbe.Addr().String()
	_ = metricsProbe.Close()
	configPath := filepath.Join(directory, "server.json")
	rawConfig := fmt.Sprintf(`{
  "server_identity":"edge.example.test","secret_file":%q,"listen":%q,"metrics_listen":%q,
  "https_path":"/private/https/session","webrtc_path":"/private/webrtc/offer",
  "udp_port_min":41000,"udp_port_max":41010,"max_cover_overhead_percent":30,
  "initial_window_bytes":65536,"max_streams":8,"max_sessions":4,"max_webrtc_peers":4,
  "max_target_connections":8,"dial_timeout":"2s","gather_timeout":"3s",
  "connect_timeout":"5s","shutdown_timeout":"3s","enable_constellation":true,
  "enable_forward_secrecy":true
}`, filepath.ToSlash(secretPath), backendAddress, metricsAddress)
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	serverConfig, err := config.LoadServer(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- RunServer(ctx, serverConfig) }()
	waitHTTPReady(t, ctx, "http://"+backendAddress+"/health-does-not-exist")

	backendURL, _ := url.Parse("http://" + backendAddress)
	reverse := httputil.NewSingleHostReverseProxy(backendURL)
	tlsProxy := httptest.NewTLSServer(reverse)
	defer tlsProxy.Close()
	roots := x509.NewCertPool()
	roots.AddCert(tlsProxy.Certificate())
	carrier, err := httpsws.Dial(ctx, httpsws.DialConfig{
		URL: "wss" + tlsProxy.URL[len("https"):] + serverConfig.HTTPSPath,
		TLSConfig: &tls.Config{
			RootCAs: roots, MinVersion: tls.VersionTLS13,
		},
	})
	if err != nil {
		t.Fatalf("dial proxied carrier: %v", err)
	}
	extensionRequest := productionExtensionParameters(8)
	authenticated, err := session.ConnectClient(ctx, carrier, session.AuthenticatedConfig{
		RootSecret: serverConfig.Secret.Bytes(), ServerIdentity: serverConfig.ServerIdentity,
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 64 * 1024, MaxStreams: 8, MaxCoverOverheadPercent: 30,
		ExtensionRequest: &extensionRequest, RequiredExtensions: protocol.CapabilityReliableUDP,
		ExtensionTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("authenticate mandatory v2.2 runtime session: %v", err)
	}
	negotiated, ok := authenticated.Extensions()
	if !ok || negotiated.Capabilities&protocol.CapabilityReliableUDP == 0 ||
		negotiated.MaxUDPAssociations != 8 || negotiated.MaxUDPPayload != 65507 {
		t.Fatalf("mandatory v2.2 negotiation=%+v ok=%v", negotiated, ok)
	}
	metadata, err := proxycore.EncodeTarget(proxycore.Target{Host: "127.0.0.1", Port: 9})
	if err != nil {
		t.Fatalf("encode blocked target: %v", err)
	}
	_, err = authenticated.Mux.Open(ctx, metadata)
	var rejection *session.RejectError
	if !errors.As(err, &rejection) || rejection.Code != 2 {
		t.Fatalf("private target was not policy-rejected: %v", err)
	}
	_ = authenticated.Mux.Close()

	constellationCarrier, err := httpsws.Dial(ctx, httpsws.DialConfig{
		URL:       "wss" + tlsProxy.URL[len("https"):] + serverConfig.HTTPSPath,
		TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13},
	})
	if err != nil {
		t.Fatalf("dial constellation carrier: %v", err)
	}
	constellationRequest := productionExtensionParameters(8)
	constellationRequest.Capabilities |= protocol.CapabilityConstellationContinuity
	constellationSession, err := session.ConnectClient(ctx, constellationCarrier, session.AuthenticatedConfig{
		RootSecret: serverConfig.Secret.Bytes(), ServerIdentity: serverConfig.ServerIdentity,
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 64 * 1024, MaxStreams: 8, MaxCoverOverheadPercent: 30,
		ExtensionRequest: &constellationRequest, ExtensionTimeout: time.Second,
		EnableForwardSecrecy: true,
	})
	if err != nil {
		t.Fatalf("authenticate constellation session: %v", err)
	}
	clientControl, err := constellation.NewClientControl(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientControl.Create(ctx, constellationSession); err != nil {
		t.Fatalf("create production constellation: %v", err)
	}
	constellationState := clientControl.State()
	controlChannel, err := constellation.NewControlChannel(ctx, constellation.ControlChannelConfig{
		Mux: constellationSession.Mux, ConstellationID: constellationState.ConstellationID,
		FirstMessageID: constellationState.NextMessageID, MaxFlows: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	blockedInner, err := proxycore.EncodeOpenRequest(proxycore.OpenRequest{
		Command: proxycore.CommandTCPConnect,
		Target:  proxycore.Target{Host: "127.0.0.1", Port: 9},
	})
	if err != nil {
		t.Fatal(err)
	}
	blockedOpen, err := (protocol.ContinuityOpenMetadata{
		Mode: protocol.ContinuityOpenNew, ConstellationID: constellationState.ConstellationID,
		FlowID: protocol.ContinuityID{1, 2, 3}, LeaseKey: constellationState.LeaseKey,
		Epoch: 1, Inner: blockedInner,
	}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	_, err = constellationSession.Mux.Open(ctx, blockedOpen)
	if !errors.As(err, &rejection) || rejection.Code != 2 {
		t.Fatalf("constellation private target was not policy-rejected: %v", err)
	}
	_ = controlChannel.Close()
	_ = constellationSession.Mux.Close()

	webRTCCarrier, err := rtc.Dial(ctx, rtc.DialConfig{
		SignalingURL: "https" + tlsProxy.URL[len("https"):] + serverConfig.WebRTCPath,
		TLSConfig: &tls.Config{
			RootCAs: roots, MinVersion: tls.VersionTLS13,
		},
		GatherTimeout:  5 * time.Second,
		ConnectTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial proxied WebRTC carrier: %v", err)
	}
	webRTCAuthenticated, err := session.ConnectClient(ctx, webRTCCarrier, session.AuthenticatedConfig{
		RootSecret: serverConfig.Secret.Bytes(), ServerIdentity: serverConfig.ServerIdentity,
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 64 * 1024, MaxStreams: 8, MaxCoverOverheadPercent: 30,
	})
	if err != nil {
		t.Fatalf("authenticate proxied WebRTC session: %v", err)
	}
	expectedMetrics := []string{
		`np2_server_selected_carrier_total{carrier="https"} 2`,
		`np2_server_selected_carrier_total{carrier="webrtc"} 1`,
		`np2_server_auth_failures_total{carrier="https"} 0`,
	}
	metricsBody := waitForServerMetrics(t, "http://"+metricsAddress+"/metrics", expectedMetrics)
	_ = webRTCAuthenticated.Mux.Close()
	for _, expected := range expectedMetrics {
		if !strings.Contains(metricsBody, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, metricsBody)
		}
	}
	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func waitForServerMetrics(t *testing.T, address string, expected []string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastBody string
	for time.Now().Before(deadline) {
		response, err := http.Get(address)
		if err == nil {
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				lastBody = string(raw)
				complete := true
				for _, value := range expected {
					if !strings.Contains(lastBody, value) {
						complete = false
						break
					}
				}
				if complete {
					return lastBody
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("metrics did not converge before deadline:\n%s", lastBody)
	return ""
}

func TestRunServerHTTP3AuthenticatesOverWebTransport(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "root.secret")
	secretRaw := bytes.Repeat([]byte{0x38}, protocol.RootSecretSize)
	if err := os.WriteFile(secretPath, []byte(base64.RawURLEncoding.EncodeToString(secretRaw)+"\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	certPath, keyPath, roots := writeHTTP3Certificate(t, directory, "edge.example.test")
	tcpProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve backend address: %v", err)
	}
	backendAddress := tcpProbe.Addr().String()
	_ = tcpProbe.Close()
	udpProbe, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve HTTP/3 address: %v", err)
	}
	http3Address := udpProbe.LocalAddr().String()
	_ = udpProbe.Close()
	_, http3Port, _ := net.SplitHostPort(http3Address)
	configPath := filepath.Join(directory, "server.json")
	rawConfig := fmt.Sprintf(`{
  "server_identity":"edge.example.test","secret_file":%q,"listen":%q,
  "https_path":"/private/https/session","webrtc_path":"/private/webrtc/offer",
	"enable_http3":true,"enable_http3_datagrams":true,
	"http3_listen":%q,"http3_path":"/private/http3/session",
  "http3_cert_file":%q,"http3_key_file":%q,"max_http3_sessions":4,
  "http3_handshake_timeout":"3s","http3_idle_timeout":"30s",
  "udp_port_min":41020,"udp_port_max":41030,"max_cover_overhead_percent":30,
  "initial_window_bytes":65536,"max_streams":8,"max_sessions":4,"max_webrtc_peers":4,
  "max_target_connections":8,"dial_timeout":"2s","gather_timeout":"3s",
  "connect_timeout":"5s","shutdown_timeout":"3s"
}`, filepath.ToSlash(secretPath), backendAddress, http3Address,
		filepath.ToSlash(certPath), filepath.ToSlash(keyPath))
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	serverConfig, err := config.LoadServer(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- RunServer(ctx, serverConfig) }()
	waitHTTPReady(t, ctx, "http://"+backendAddress+"/health-does-not-exist")

	var h3Carrier *http3wt.Conn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h3Carrier, err = http3wt.Dial(ctx, http3wt.DialConfig{
			URL: "https://edge.example.test:" + http3Port + serverConfig.HTTP3Path,
			TLSConfig: &tls.Config{
				RootCAs: roots, MinVersion: tls.VersionTLS13, ServerName: "edge.example.test",
			},
			ServerAddresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		})
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial HTTP/3 carrier: %v", err)
	}
	extensionRequest := productionExtensionParametersForCarrier(8, h3Carrier)
	authenticated, err := session.ConnectClient(ctx, h3Carrier, session.AuthenticatedConfig{
		RootSecret: serverConfig.Secret.Bytes(), ServerIdentity: serverConfig.ServerIdentity,
		Features:      protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileWeb,
		InitialWindow: 64 * 1024, MaxStreams: 8, MaxCoverOverheadPercent: 30,
		ExtensionRequest: &extensionRequest, RequiredExtensions: protocol.CapabilityUnreliableDatagrams,
		ExtensionTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("authenticate HTTP/3 session: %v", err)
	}
	negotiated, ok := authenticated.Extensions()
	if !ok || negotiated.Capabilities&protocol.CapabilityUnreliableDatagrams == 0 ||
		authenticated.Datagrams == nil || !authenticated.Datagrams.Enabled() ||
		authenticated.Datagrams.MaxPayload() != int(negotiated.UnreliableDatagramSize) {
		t.Fatalf("HTTP/3 datagram negotiation=%+v ok=%v mux=%v", negotiated, ok, authenticated.Datagrams)
	}
	_ = authenticated.Mux.Close()
	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func waitHTTPReady(t *testing.T, ctx context.Context, endpoint string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("backend did not become ready: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func writeHTTP3Certificate(t *testing.T, directory, identity string) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate HTTP/3 key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: identity},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{identity},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create HTTP/3 certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal HTTP/3 key: %v", err)
	}
	certPath := filepath.Join(directory, "fullchain.pem")
	keyPath := filepath.Join(directory, "privkey.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write HTTP/3 certificate: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write HTTP/3 key: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse HTTP/3 certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return certPath, keyPath, roots
}
