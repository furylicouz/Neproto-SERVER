package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http/httptest"
	"testing"
	"time"

	rtc "neproto.local/chameleon/internal/carrier/webrtc"
)

func TestSOCKSConnectOverAuthenticatedWebRTCCarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	server, err := rtc.NewServer(rtc.ServerConfig{
		Path: "/media/session", MaxPeers: 4,
		GatherTimeout: 5 * time.Second, ConnectTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatalf("create WebRTC server: %v", err)
	}
	defer server.Close()
	signaling := httptest.NewUnstartedServer(server.Handler())
	signaling.Config.ErrorLog = log.New(io.Discard, "", 0)
	signaling.StartTLS()
	defer signaling.Close()
	roots := x509.NewCertPool()
	roots.AddCert(signaling.Certificate())
	clientCarrier, err := rtc.Dial(ctx, rtc.DialConfig{
		SignalingURL: signaling.URL + "/media/session",
		TLSConfig: &tls.Config{
			RootCAs: roots, MinVersion: tls.VersionTLS13,
		},
		GatherTimeout: 5 * time.Second, ConnectTimeout: 8 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial WebRTC carrier: %v", err)
	}
	defer clientCarrier.Close()
	serverCarrier, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("accept WebRTC carrier: %v", err)
	}
	defer serverCarrier.Close()
	runSOCKSE2E(t, ctx, clientCarrier, serverCarrier)
}
