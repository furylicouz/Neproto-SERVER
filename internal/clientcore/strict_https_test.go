package clientcore

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
)

func TestStrictHTTPSConnectorConstructsOnlyHTTPS(t *testing.T) {
	var calls atomic.Int64
	runtime := newFakeRuntime()
	connection := &strictHTTPSCarrier{}
	connector, err := NewStrictHTTPSConnector(StrictHTTPSDependencies{
		DialHTTPS: func(context.Context, config.Client) (carrier.Carrier, error) {
			calls.Add(1)
			return connection, nil
		},
		Authenticate: func(context.Context, config.Client, carrier.Carrier) (Runtime, error) {
			return runtime, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := connector(context.Background(), strictHTTPSConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got != runtime || calls.Load() != 1 {
		t.Fatalf("runtime=%v HTTPS calls=%d", got, calls.Load())
	}
}

func TestStrictHTTPSConnectorRejectsAlternateCarrierBeforeDial(t *testing.T) {
	var calls atomic.Int64
	connector, err := NewStrictHTTPSConnector(StrictHTTPSDependencies{
		DialHTTPS: func(context.Context, config.Client) (carrier.Carrier, error) {
			calls.Add(1)
			return nil, errors.New("must not dial")
		},
		Authenticate: func(context.Context, config.Client, carrier.Carrier) (Runtime, error) {
			return nil, errors.New("must not authenticate")
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	candidate := strictHTTPSConfig()
	candidate.HTTP3URL = "https://vpn.example.test/private/http3/session"
	candidate.HTTP3Timeout = config.Duration{Duration: time.Second}
	if _, connectErr := connector(context.Background(), candidate); !errors.Is(connectErr, ErrStrictHTTPSRequired) {
		t.Fatalf("connect error = %v", connectErr)
	}
	if calls.Load() != 0 {
		t.Fatalf("HTTPS dial calls = %d", calls.Load())
	}
}

func TestStrictHTTPSConnectorOmitsUnimplementedConstellationCapability(t *testing.T) {
	connection := &strictHTTPSCarrier{}
	runtime := newFakeRuntime()
	var dialConfig config.Client
	var authenticationConfig config.Client
	connector, err := NewStrictHTTPSConnector(StrictHTTPSDependencies{
		DialHTTPS: func(_ context.Context, candidate config.Client) (carrier.Carrier, error) {
			dialConfig = candidate
			return connection, nil
		},
		Authenticate: func(_ context.Context, candidate config.Client, _ carrier.Carrier) (Runtime, error) {
			authenticationConfig = candidate
			return runtime, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	candidate := strictHTTPSConfig()
	candidate.EnableConstellation = true
	candidate.EnableForwardSecrecy = true
	if _, err := connector(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if dialConfig.EnableConstellation || authenticationConfig.EnableConstellation {
		t.Fatal("strict single-session core advertised unimplemented Constellation continuity")
	}
	if !dialConfig.EnableForwardSecrecy || !authenticationConfig.EnableForwardSecrecy {
		t.Fatal("strict single-session core disabled independent Forward Secrecy")
	}
	if !candidate.EnableConstellation {
		t.Fatal("connector mutated the imported profile configuration")
	}
}

func TestClassifyHTTPSDialErrorReportsStableStages(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  clienthost.Code
		wantStage clienthost.Stage
	}{
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "vpn.example.test"},
			wantCode: clienthost.CodeDNSFailed, wantStage: clienthost.StageDNSResolution},
		{name: "tls", err: x509.UnknownAuthorityError{Cert: &x509.Certificate{}},
			wantCode: clienthost.CodeTLSFailed, wantStage: clienthost.StageTLSHandshake},
		{name: "deadline", err: context.DeadlineExceeded,
			wantCode: clienthost.CodeHostUnavailable, wantStage: clienthost.StageTLSHandshake},
		{name: "websocket", err: errors.New("private websocket failure"),
			wantCode: clienthost.CodeHostUnavailable, wantStage: clienthost.StageTLSHandshake},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapped := classifyHTTPSDialError(test.err)
			got := clienthost.MapError("connect-https", clienthost.StageUnknown, wrapped)
			if got.Code != test.wantCode || got.Stage != test.wantStage {
				t.Fatalf("mapped error = %+v", got)
			}
			if got.Message == "" || got.Message == test.err.Error() {
				t.Fatalf("unsafe or empty message = %q", got.Message)
			}
		})
	}
}

func TestStrictHTTPSAuthenticationFailureClosesCarrierAndReportsAuthStage(t *testing.T) {
	connection := &strictHTTPSCarrier{}
	want := errors.New("private authentication detail")
	connector, err := NewStrictHTTPSConnector(StrictHTTPSDependencies{
		DialHTTPS: func(context.Context, config.Client) (carrier.Carrier, error) {
			return connection, nil
		},
		Authenticate: func(context.Context, config.Client, carrier.Carrier) (Runtime, error) {
			return nil, want
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, connectErr := connector(context.Background(), strictHTTPSConfig())
	if !errors.Is(connectErr, want) {
		t.Fatalf("connect error = %v", connectErr)
	}
	if !connection.closed.Load() {
		t.Fatal("carrier was not closed")
	}
	mapped := clienthost.MapError("connect-https-auth", clienthost.StageUnknown, connectErr)
	if mapped.Code != clienthost.CodeNP2AuthFailed || mapped.Stage != clienthost.StageNP2Authentication {
		t.Fatalf("mapped error = %+v", mapped)
	}
}

func TestNewProductionStrictHTTPSCoreConstructsWithoutDialing(t *testing.T) {
	core, err := NewProductionStrictHTTPSCore()
	if err != nil {
		t.Fatal(err)
	}
	if got := core.Snapshot(); got.State != clienthost.StateDisconnected || got.Carrier != clienthost.CarrierNone {
		t.Fatalf("initial snapshot = %+v", got)
	}
	if err := core.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func strictHTTPSConfig() config.Client {
	return config.Client{
		CarrierPolicy:       config.CarrierPolicyHTTPSOnly,
		HTTPSURL:            "wss://vpn.example.test/private/https/session",
		HTTPSTimeout:        config.Duration{Duration: time.Second},
		MaxParallelCarriers: 1,
	}
}

type strictHTTPSCarrier struct {
	closed atomic.Bool
}

func (*strictHTTPSCarrier) Send(context.Context, []byte) error { return nil }
func (*strictHTTPSCarrier) Receive(context.Context) ([]byte, error) {
	return nil, context.Canceled
}
func (c *strictHTTPSCarrier) Close() error {
	c.closed.Store(true)
	return nil
}
func (*strictHTTPSCarrier) Kind() protocol.CarrierKind { return protocol.CarrierHTTPS }
