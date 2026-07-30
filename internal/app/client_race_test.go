package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/carrier/hybrid"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func TestRaceAuthenticatedCandidatesSelectsFirstAuthenticatedAndClosesLoser(t *testing.T) {
	winnerCarrier := &raceStubCarrier{kind: protocol.CarrierWebRTC}
	loserCarrier := &raceStubCarrier{kind: protocol.CarrierHTTP3}
	winnerSession := &session.Authenticated{Carrier: protocol.CarrierWebRTC}

	result, selected, err := raceAuthenticatedCandidates(context.Background(), []authenticatedAttempt{
		{
			kind: protocol.CarrierHTTP3,
			run: func(context.Context) (*session.Authenticated, hybrid.Result, error) {
				time.Sleep(25 * time.Millisecond)
				return &session.Authenticated{Carrier: protocol.CarrierHTTP3}, hybrid.Result{
					Carrier: loserCarrier, Kind: protocol.CarrierHTTP3,
				}, nil
			},
		},
		{
			kind: protocol.CarrierWebRTC,
			run: func(context.Context) (*session.Authenticated, hybrid.Result, error) {
				time.Sleep(5 * time.Millisecond)
				return winnerSession, hybrid.Result{
					Carrier: winnerCarrier, Kind: protocol.CarrierWebRTC,
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("race authenticated candidates: %v", err)
	}
	if result != winnerSession || selected.Kind != protocol.CarrierWebRTC || !selected.UsedFallback {
		t.Fatalf("result=%p selected=%+v", result, selected)
	}
	if !loserCarrier.Closed() {
		t.Fatal("authenticated losing candidate was not closed")
	}
	if winnerCarrier.Closed() {
		t.Fatal("winning candidate was closed")
	}
}

func TestRaceAuthenticatedCandidatesStartsFallbackWithoutWaitingForUDPTimeouts(t *testing.T) {
	httpsCarrier := &raceStubCarrier{kind: protocol.CarrierHTTPS}
	blocked := func(ctx context.Context) (*session.Authenticated, hybrid.Result, error) {
		<-ctx.Done()
		return nil, hybrid.Result{}, ctx.Err()
	}
	started := time.Now()
	_, selected, err := raceAuthenticatedCandidates(context.Background(), []authenticatedAttempt{
		{kind: protocol.CarrierHTTP3, run: blocked},
		{kind: protocol.CarrierWebRTC, run: blocked},
		{
			kind:  protocol.CarrierHTTPS,
			delay: 20 * time.Millisecond,
			run: func(context.Context) (*session.Authenticated, hybrid.Result, error) {
				return &session.Authenticated{Carrier: protocol.CarrierHTTPS}, hybrid.Result{
					Carrier: httpsCarrier, Kind: protocol.CarrierHTTPS,
				}, nil
			},
		},
	})
	if err != nil || selected.Kind != protocol.CarrierHTTPS {
		t.Fatalf("fallback selected=%+v error=%v", selected, err)
	}
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("HTTPS fallback waited for UDP timeouts: %v", elapsed)
	}
}

func TestRaceAuthenticatedCandidatesJoinsFailures(t *testing.T) {
	h3Err := errors.New("HTTP/3 blocked")
	rtcErr := errors.New("WebRTC blocked")
	_, _, err := raceAuthenticatedCandidates(context.Background(), []authenticatedAttempt{
		{kind: protocol.CarrierHTTP3, run: func(context.Context) (*session.Authenticated, hybrid.Result, error) {
			return nil, hybrid.Result{}, h3Err
		}},
		{kind: protocol.CarrierWebRTC, run: func(context.Context) (*session.Authenticated, hybrid.Result, error) {
			return nil, hybrid.Result{}, rtcErr
		}},
	})
	if !errors.Is(err, h3Err) || !errors.Is(err, rtcErr) {
		t.Fatalf("joined error=%v", err)
	}
}

func TestCarrierPreferenceCacheExpiresAndResets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cache := newCarrierPreferenceCache(func() time.Time { return now })
	cache.record("edge.example", protocol.CarrierHTTP3)
	if kind, ok := cache.load("edge.example", time.Minute); !ok || kind != protocol.CarrierHTTP3 {
		t.Fatalf("fresh preference=(%v,%v)", kind, ok)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := cache.load("edge.example", time.Minute); ok {
		t.Fatal("expired preference remained cached")
	}
	cache.record("edge.example", protocol.CarrierWebRTC)
	cache.reset()
	if _, ok := cache.load("edge.example", time.Minute); ok {
		t.Fatal("network reset retained carrier preference")
	}
}

func TestClientCarrierOrderUsesFreshPreferenceWithoutDroppingFallbacks(t *testing.T) {
	got := clientCarrierOrder(true, protocol.CarrierWebRTC, true)
	want := []ProbeMode{ProbeWebRTC, ProbeHTTP3, ProbeHTTPS}
	if len(got) != len(want) {
		t.Fatalf("order=%v want=%v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("order=%v want=%v", got, want)
		}
	}
}

func TestRaceClientCarriersCachesOnlyAuthenticatedWinner(t *testing.T) {
	cache := newCarrierPreferenceCache(time.Now)
	clientConfig := config.Client{
		ServerIdentity: "edge.example", HTTP3URL: "https://edge.example/private-h3",
		HTTP3Timeout:    config.Duration{Duration: time.Second},
		WebRTCTimeout:   config.Duration{Duration: time.Second},
		HTTPSTimeout:    config.Duration{Duration: time.Second},
		CarrierCacheTTL: config.Duration{Duration: time.Minute},
	}
	var mu sync.Mutex
	var modes []ProbeMode
	dial := func(
		_ context.Context, _ config.Client, mode ProbeMode,
	) (*session.Authenticated, hybrid.Result, error) {
		mu.Lock()
		modes = append(modes, mode)
		mu.Unlock()
		if mode == ProbeHTTP3 {
			return nil, hybrid.Result{}, errors.New("HTTP/3 path unavailable")
		}
		connection := &raceStubCarrier{kind: probeCarrierKind(mode)}
		return &session.Authenticated{Carrier: probeCarrierKind(mode)}, hybrid.Result{
			Carrier: connection, Kind: probeCarrierKind(mode),
		}, nil
	}
	authenticated, selected, err := raceClientCarriers(
		context.Background(), clientConfig, dial, cache,
	)
	if err != nil || authenticated == nil || selected.Kind != protocol.CarrierWebRTC {
		t.Fatalf("selected=%+v authenticated=%p error=%v", selected, authenticated, err)
	}
	if kind, ok := cache.load(clientConfig.ServerIdentity, time.Minute); !ok || kind != protocol.CarrierWebRTC {
		t.Fatalf("cached preference=(%v,%v)", kind, ok)
	}

	mu.Lock()
	modes = nil
	mu.Unlock()
	_, selected, err = raceClientCarriers(context.Background(), clientConfig, dial, cache)
	if err != nil || selected.Kind != protocol.CarrierWebRTC || selected.UsedFallback {
		t.Fatalf("cached selection=%+v error=%v", selected, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(modes) == 0 || modes[0] != ProbeWebRTC {
		t.Fatalf("cached attempt order=%v", modes)
	}
}

func TestRequiredClientExtensionsRequireFastDatagrams(t *testing.T) {
	if got := requiredClientExtensions(config.Client{RequireDatagrams: true}); got != protocol.CapabilityReliableUDP|protocol.CapabilityUnreliableDatagrams {
		t.Fatalf("required capabilities=%08b", got)
	}
	if got := requiredClientExtensions(config.Client{}); got != 0 {
		t.Fatalf("optional capabilities=%08b", got)
	}
	if got := requiredClientExtensions(config.Client{EnableForwardSecrecy: true}); got != protocol.CapabilityForwardSecrecy {
		t.Fatalf("forward secrecy capabilities=%08b", got)
	}
}

type raceStubCarrier struct {
	kind   protocol.CarrierKind
	mu     sync.Mutex
	closed bool
}

var _ carrier.Carrier = (*raceStubCarrier)(nil)

func (*raceStubCarrier) Send(context.Context, []byte) error      { return nil }
func (*raceStubCarrier) Receive(context.Context) ([]byte, error) { return nil, io.EOF }
func (c *raceStubCarrier) Kind() protocol.CarrierKind            { return c.kind }
func (c *raceStubCarrier) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}
func (c *raceStubCarrier) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
