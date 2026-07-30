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

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/carrier/httpsws"
	"neproto.local/chameleon/internal/carrier/hybrid"
	"neproto.local/chameleon/internal/protocol"
)

func TestForcedWebRTCFailureFallsBackToAuthenticatedHTTPSSOCKSPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	acceptor, err := httpsws.NewAcceptor(httpsws.AcceptorConfig{Path: "/fallback/session"})
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
	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}
	selector, err := hybrid.New(hybrid.Config{
		WebRTC: func(attempt context.Context) (carrier.Carrier, error) {
			<-attempt.Done()
			return nil, attempt.Err()
		},
		HTTPS: func(attempt context.Context) (carrier.Carrier, error) {
			return httpsws.Dial(attempt, httpsws.DialConfig{
				URL: "wss" + tlsServer.URL[len("https"):] + "/fallback/session", TLSConfig: tlsConfig,
			})
		},
		WebRTCTimeout: 75 * time.Millisecond,
		HTTPSTimeout:  3 * time.Second,
		CacheTTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("create hybrid selector: %v", err)
	}
	started := time.Now()
	selected, err := selector.Dial(ctx)
	if err != nil {
		t.Fatalf("select carrier: %v", err)
	}
	if selected.Kind != protocol.CarrierHTTPS || !selected.UsedFallback {
		t.Fatalf("unexpected selection: %#v", selected)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("HTTPS fallback exceeded deadline: %v", elapsed)
	}
	defer selected.Carrier.Close()
	serverCarrier, err := acceptor.Accept(ctx)
	if err != nil {
		t.Fatalf("accept fallback carrier: %v", err)
	}
	defer serverCarrier.Close()
	runSOCKSE2E(t, ctx, selected.Carrier, serverCarrier)
}
