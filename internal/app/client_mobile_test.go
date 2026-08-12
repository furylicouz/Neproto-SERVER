package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"neproto.local/chameleon/internal/carrier/hybrid"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/session"
)

func TestConnectClientHTTPSFirstPrefersHTTPS(t *testing.T) {
	want := &session.Authenticated{}
	var modes []ProbeMode
	got, err := connectClientHTTPSFirst(context.Background(), config.Client{}, func(
		_ context.Context, _ config.Client, mode ProbeMode,
	) (*session.Authenticated, hybrid.Result, error) {
		modes = append(modes, mode)
		return want, hybrid.Result{}, nil
	})
	if err != nil || got != want {
		t.Fatalf("connect result=%p error=%v", got, err)
	}
	if !reflect.DeepEqual(modes, []ProbeMode{ProbeHTTPS}) {
		t.Fatalf("carrier order=%v", modes)
	}
}

func TestConnectClientHTTPSFirstFallsBackToWebRTC(t *testing.T) {
	httpsErr := errors.New("HTTPS blocked")
	want := &session.Authenticated{}
	var modes []ProbeMode
	got, err := connectClientHTTPSFirst(context.Background(), config.Client{}, func(
		_ context.Context, _ config.Client, mode ProbeMode,
	) (*session.Authenticated, hybrid.Result, error) {
		modes = append(modes, mode)
		if mode == ProbeHTTPS {
			return nil, hybrid.Result{}, httpsErr
		}
		return want, hybrid.Result{}, nil
	})
	if err != nil || got != want {
		t.Fatalf("fallback result=%p error=%v", got, err)
	}
	if !reflect.DeepEqual(modes, []ProbeMode{ProbeHTTPS, ProbeWebRTC}) {
		t.Fatalf("carrier order=%v", modes)
	}
}

func TestConnectClientHTTPSFirstFallsBackToHTTP3BeforeWebRTC(t *testing.T) {
	httpsErr := errors.New("HTTPS blocked")
	want := &session.Authenticated{}
	var modes []ProbeMode
	got, err := connectClientHTTPSFirst(context.Background(), config.Client{
		HTTP3URL: "https://vpn.example.test/http3",
	}, func(
		_ context.Context, _ config.Client, mode ProbeMode,
	) (*session.Authenticated, hybrid.Result, error) {
		modes = append(modes, mode)
		switch mode {
		case ProbeHTTPS:
			return nil, hybrid.Result{}, httpsErr
		case ProbeHTTP3:
			return want, hybrid.Result{}, nil
		default:
			t.Fatalf("unexpected carrier mode %v", mode)
			return nil, hybrid.Result{}, errors.New("unexpected carrier")
		}
	})
	if err != nil || got != want {
		t.Fatalf("fallback result=%p error=%v", got, err)
	}
	if !reflect.DeepEqual(modes, []ProbeMode{ProbeHTTPS, ProbeHTTP3}) {
		t.Fatalf("carrier order=%v", modes)
	}
}

func TestConnectClientHTTPSFirstFallsBackFromHTTP3ToWebRTC(t *testing.T) {
	want := &session.Authenticated{}
	var modes []ProbeMode
	got, err := connectClientHTTPSFirst(context.Background(), config.Client{
		HTTP3URL: "https://vpn.example.test/http3",
	}, func(
		_ context.Context, _ config.Client, mode ProbeMode,
	) (*session.Authenticated, hybrid.Result, error) {
		modes = append(modes, mode)
		if mode == ProbeWebRTC {
			return want, hybrid.Result{}, nil
		}
		return nil, hybrid.Result{}, errors.New("carrier unavailable")
	})
	if err != nil || got != want {
		t.Fatalf("fallback result=%p error=%v", got, err)
	}
	if !reflect.DeepEqual(modes, []ProbeMode{ProbeHTTPS, ProbeHTTP3, ProbeWebRTC}) {
		t.Fatalf("carrier order=%v", modes)
	}
}

func TestConnectClientDatagramPreferredGivesHTTP3ItsFullAttemptBeforeWebRTC(t *testing.T) {
	want := &session.Authenticated{}
	var modes []ProbeMode
	got, err := connectClientDatagramPreferred(context.Background(), config.Client{
		HTTP3URL: "https://vpn.example.test/http3",
	}, func(
		_ context.Context, _ config.Client, mode ProbeMode,
	) (*session.Authenticated, hybrid.Result, error) {
		modes = append(modes, mode)
		if mode != ProbeHTTP3 {
			t.Fatalf("unexpected carrier mode %v", mode)
		}
		return want, hybrid.Result{}, nil
	})
	if err != nil || got != want {
		t.Fatalf("datagram-preferred result=%p error=%v", got, err)
	}
	if !reflect.DeepEqual(modes, []ProbeMode{ProbeHTTP3}) {
		t.Fatalf("carrier order=%v", modes)
	}
}

func TestConnectClientDatagramPreferredFallsBackToWebRTCWithoutHTTPS(t *testing.T) {
	want := &session.Authenticated{}
	var modes []ProbeMode
	got, err := connectClientDatagramPreferred(context.Background(), config.Client{
		HTTP3URL: "https://vpn.example.test/http3",
	}, func(
		_ context.Context, _ config.Client, mode ProbeMode,
	) (*session.Authenticated, hybrid.Result, error) {
		modes = append(modes, mode)
		if mode == ProbeWebRTC {
			return want, hybrid.Result{}, nil
		}
		return nil, hybrid.Result{}, errors.New("carrier unavailable")
	})
	if err != nil || got != want {
		t.Fatalf("datagram fallback result=%p error=%v", got, err)
	}
	if !reflect.DeepEqual(modes, []ProbeMode{ProbeHTTP3, ProbeWebRTC}) {
		t.Fatalf("carrier order=%v", modes)
	}
}

func TestConnectClientDatagramPreferredDoesNotFallbackFromHTTP3Only(t *testing.T) {
	var modes []ProbeMode
	_, err := connectClientDatagramPreferred(context.Background(), config.Client{
		CarrierPolicy: config.CarrierPolicy("http3-only"),
		HTTP3URL:      "https://vpn.example.test/http3",
	}, func(
		_ context.Context, _ config.Client, mode ProbeMode,
	) (*session.Authenticated, hybrid.Result, error) {
		modes = append(modes, mode)
		return nil, hybrid.Result{}, errors.New("HTTP/3 unavailable")
	})
	if err == nil {
		t.Fatal("HTTP/3-only connector unexpectedly succeeded")
	}
	if !reflect.DeepEqual(modes, []ProbeMode{ProbeHTTP3}) {
		t.Fatalf("HTTP/3-only carrier attempts=%v", modes)
	}
}

func TestConnectClientHTTPSFirstUsesOnlyHTTP3ForHTTP3OnlyPolicy(t *testing.T) {
	want := &session.Authenticated{}
	var modes []ProbeMode
	got, err := connectClientHTTPSFirst(context.Background(), config.Client{
		CarrierPolicy: config.CarrierPolicyHTTP3Only,
		HTTP3URL:      "https://vpn.example.test/http3",
	}, func(
		_ context.Context, _ config.Client, mode ProbeMode,
	) (*session.Authenticated, hybrid.Result, error) {
		modes = append(modes, mode)
		return want, hybrid.Result{}, nil
	})
	if err != nil || got != want {
		t.Fatalf("HTTP/3-only compatibility connector result=%p error=%v", got, err)
	}
	if !reflect.DeepEqual(modes, []ProbeMode{ProbeHTTP3}) {
		t.Fatalf("HTTP/3-only compatibility attempts=%v", modes)
	}
}

func TestClientConnectorForRunAcceptsHTTP3Only(t *testing.T) {
	want := &session.Authenticated{}
	calls := 0
	selected, err := clientConnectorForRun(config.Client{CarrierPolicy: config.CarrierPolicyHTTP3Only},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			return nil, errors.New("unexpected compatibility connector")
		},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			calls++
			return want, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := selected(context.Background(), config.Client{})
	if err != nil || got != want || calls != 1 {
		t.Fatalf("HTTP/3-only run connector result=%p error=%v calls=%d", got, err, calls)
	}
}

func TestPrimaryClientProbeModeUsesOnlyHTTP3ForHTTP3OnlyPolicy(t *testing.T) {
	if got := primaryClientProbeMode(config.Client{CarrierPolicy: config.CarrierPolicyHTTP3Only}); got != ProbeHTTP3 {
		t.Fatalf("HTTP/3-only primary mode=%v", got)
	}
	if got := primaryClientProbeMode(config.Client{CarrierPolicy: config.CarrierPolicyPerformance}); got != ProbeAuto {
		t.Fatalf("adaptive primary mode=%v", got)
	}
}

func TestConnectClientForRunUsesAdaptiveDatagramPolicyByDefault(t *testing.T) {
	want := &session.Authenticated{}
	performanceCalls := 0
	udpFirstCalls := 0
	got, err := connectClientForRun(context.Background(), config.Client{},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			performanceCalls++
			return nil, errors.New("unexpected compatibility connector")
		},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			udpFirstCalls++
			return want, nil
		},
	)
	if err != nil || got != want || performanceCalls != 0 || udpFirstCalls != 1 {
		t.Fatalf("result=%p error=%v performance=%d udp-first=%d",
			got, err, performanceCalls, udpFirstCalls)
	}
}

func TestConnectClientForRunHonorsUDPFirstPolicy(t *testing.T) {
	want := &session.Authenticated{}
	performanceCalls := 0
	udpFirstCalls := 0
	got, err := connectClientForRun(context.Background(), config.Client{
		CarrierPolicy: config.CarrierPolicyUDPFirst,
	},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			performanceCalls++
			return nil, errors.New("unexpected performance connector")
		},
		func(context.Context, config.Client) (*session.Authenticated, error) {
			udpFirstCalls++
			return want, nil
		},
	)
	if err != nil || got != want || performanceCalls != 0 || udpFirstCalls != 1 {
		t.Fatalf("result=%p error=%v performance=%d udp-first=%d",
			got, err, performanceCalls, udpFirstCalls)
	}
}
