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

func TestStrictHTTP3ConnectorConstructsOnlyHTTP3(t *testing.T) {
	var http3Calls atomic.Int64
	var httpsCalls atomic.Int64
	var webRTCCalls atomic.Int64
	runtime := newFakeRuntime()
	connection := &fakeCarrier{}

	connector, err := NewStrictHTTP3Connector(StrictHTTP3Dependencies{
		DialHTTP3: func(context.Context, config.Client) (carrier.Carrier, error) {
			http3Calls.Add(1)
			return connection, nil
		},
		Authenticate: func(context.Context, config.Client, carrier.Carrier) (Runtime, error) {
			return runtime, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := connector(context.Background(), strictHTTP3Config())
	if err != nil {
		t.Fatal(err)
	}
	if got != runtime || http3Calls.Load() != 1 {
		t.Fatalf("runtime=%v HTTP/3 calls=%d", got, http3Calls.Load())
	}
	if httpsCalls.Load() != 0 || webRTCCalls.Load() != 0 {
		t.Fatalf("alternate calls: HTTPS=%d WebRTC=%d", httpsCalls.Load(), webRTCCalls.Load())
	}
}

func TestStrictHTTP3ConnectorOmitsUnimplementedConstellationCapability(t *testing.T) {
	connection := &fakeCarrier{}
	runtime := newFakeRuntime()
	var dialConfig config.Client
	var authenticationConfig config.Client
	connector, err := NewStrictHTTP3Connector(StrictHTTP3Dependencies{
		DialHTTP3: func(_ context.Context, candidate config.Client) (carrier.Carrier, error) {
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

	candidate := strictHTTP3Config()
	candidate.EnableConstellation = true
	candidate.EnableForwardSecrecy = true
	got, err := connector(context.Background(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if got != runtime {
		t.Fatalf("runtime = %v", got)
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

func TestNewProductionStrictHTTP3CoreConstructsWithoutDialing(t *testing.T) {
	core, err := NewProductionStrictHTTP3Core()
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

func TestStrictHTTP3FailureNeverInvokesAlternateDialers(t *testing.T) {
	var http3Calls atomic.Int64
	var authenticateCalls atomic.Int64
	var httpsCalls atomic.Int64
	var webRTCCalls atomic.Int64
	want := errors.New("UDP path unavailable")
	connector, err := NewStrictHTTP3Connector(StrictHTTP3Dependencies{
		DialHTTP3: func(context.Context, config.Client) (carrier.Carrier, error) {
			http3Calls.Add(1)
			return nil, want
		},
		Authenticate: func(context.Context, config.Client, carrier.Carrier) (Runtime, error) {
			authenticateCalls.Add(1)
			return nil, errors.New("must not authenticate")
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, connectErr := connector(context.Background(), strictHTTP3Config())
	if !errors.Is(connectErr, want) {
		t.Fatalf("connect error = %v", connectErr)
	}
	if http3Calls.Load() != 1 || authenticateCalls.Load() != 0 ||
		httpsCalls.Load() != 0 || webRTCCalls.Load() != 0 {
		t.Fatalf("calls HTTP/3=%d auth=%d HTTPS=%d WebRTC=%d",
			http3Calls.Load(), authenticateCalls.Load(), httpsCalls.Load(), webRTCCalls.Load())
	}
}

func TestStrictHTTP3ConnectorRejectsNonStrictConfigurationBeforeDial(t *testing.T) {
	var calls atomic.Int64
	connector, err := NewStrictHTTP3Connector(StrictHTTP3Dependencies{
		DialHTTP3: func(context.Context, config.Client) (carrier.Carrier, error) {
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

	for _, candidate := range []config.Client{
		{},
		{CarrierPolicy: config.CarrierPolicyPerformance, HTTP3URL: "https://vpn.example.test/http3", MaxParallelCarriers: 1},
		{CarrierPolicy: config.CarrierPolicyHTTP3Only, HTTP3URL: "https://vpn.example.test/http3", MaxParallelCarriers: 2},
		{CarrierPolicy: config.CarrierPolicyHTTP3Only, HTTP3URL: "https://vpn.example.test/http3", MaxParallelCarriers: 1,
			HTTPSURL: "wss://vpn.example.test/private/https/session"},
	} {
		if _, connectErr := connector(context.Background(), candidate); !errors.Is(connectErr, ErrStrictHTTP3Required) {
			t.Fatalf("config %+v error = %v", candidate, connectErr)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("dial calls = %d", calls.Load())
	}
}

func TestClassifyHTTP3DialErrorReportsStableStages(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  clienthost.Code
		wantStage clienthost.Stage
	}{
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "vpn.example.test"},
			wantCode: clienthost.CodeDNSFailed, wantStage: clienthost.StageDNSResolution},
		{name: "quic_udp", err: &net.OpError{Op: "read", Net: "udp", Err: errors.New("blocked")},
			wantCode: clienthost.CodeUDPUnreachable, wantStage: clienthost.StageQUICHandshake},
		{name: "tls", err: x509.UnknownAuthorityError{Cert: &x509.Certificate{}},
			wantCode: clienthost.CodeTLSFailed, wantStage: clienthost.StageTLSHandshake},
		{name: "deadline", err: context.DeadlineExceeded,
			wantCode: clienthost.CodeHTTP3Timeout, wantStage: clienthost.StageWebTransportConnect},
		{name: "webtransport", err: errors.New("server rejected WebTransport session"),
			wantCode: clienthost.CodeInternal, wantStage: clienthost.StageWebTransportConnect},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapped := classifyHTTP3DialError(test.err)
			got := clienthost.MapError("connect-01", clienthost.StageUnknown, wrapped)
			if got.Code != test.wantCode || got.Stage != test.wantStage {
				t.Fatalf("mapped error = %+v", got)
			}
			if got.Message == "" || got.Message == test.err.Error() {
				t.Fatalf("unsafe or empty message = %q", got.Message)
			}
		})
	}
}

func TestStrictHTTP3AuthenticationFailureClosesCarrierAndReportsAuthStage(t *testing.T) {
	connection := &fakeCarrier{}
	want := errors.New("private authentication detail")
	connector, err := NewStrictHTTP3Connector(StrictHTTP3Dependencies{
		DialHTTP3: func(context.Context, config.Client) (carrier.Carrier, error) {
			return connection, nil
		},
		Authenticate: func(context.Context, config.Client, carrier.Carrier) (Runtime, error) {
			return nil, want
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, connectErr := connector(context.Background(), strictHTTP3Config())
	if !errors.Is(connectErr, want) {
		t.Fatalf("connect error = %v", connectErr)
	}
	if connection.closeCalls.Load() != 1 {
		t.Fatalf("carrier close calls = %d", connection.closeCalls.Load())
	}
	mapped := clienthost.MapError("connect-02", clienthost.StageUnknown, connectErr)
	if mapped.Code != clienthost.CodeNP2AuthFailed || mapped.Stage != clienthost.StageNP2Authentication {
		t.Fatalf("mapped error = %+v", mapped)
	}
}

func strictHTTP3Config() config.Client {
	return config.Client{
		CarrierPolicy:       config.CarrierPolicyHTTP3Only,
		HTTP3URL:            "https://vpn.example.test/private-http3",
		HTTP3Timeout:        config.Duration{Duration: time.Second},
		MaxParallelCarriers: 1,
	}
}

type fakeCarrier struct {
	closeCalls atomic.Int64
}

func (*fakeCarrier) Send(context.Context, []byte) error { return nil }

func (*fakeCarrier) Receive(context.Context) ([]byte, error) { return nil, errors.New("unused") }

func (c *fakeCarrier) Close() error {
	c.closeCalls.Add(1)
	return nil
}

func (*fakeCarrier) Kind() protocol.CarrierKind { return protocol.CarrierHTTP3 }
