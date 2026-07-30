package hybrid

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

func TestSelectorPrefersHealthyWebRTCWithoutDialingHTTPS(t *testing.T) {
	rtc := &stubCarrier{kind: protocol.CarrierWebRTC}
	httpsCalls := 0
	selector, err := New(Config{
		WebRTC: func(context.Context) (carrier.Carrier, error) { return rtc, nil },
		HTTPS: func(context.Context) (carrier.Carrier, error) {
			httpsCalls++
			return &stubCarrier{kind: protocol.CarrierHTTPS}, nil
		},
		WebRTCTimeout: time.Second, HTTPSTimeout: time.Second, CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("create selector: %v", err)
	}
	result, err := selector.Dial(context.Background())
	if err != nil {
		t.Fatalf("dial hybrid: %v", err)
	}
	if result.Carrier != rtc || result.Kind != protocol.CarrierWebRTC || result.UsedFallback {
		t.Fatalf("unexpected selection: %#v", result)
	}
	if httpsCalls != 0 {
		t.Fatalf("HTTPS dialed %d times", httpsCalls)
	}
}

func TestSelectorPrefersConfiguredHTTP3(t *testing.T) {
	h3 := &stubCarrier{kind: protocol.CarrierHTTP3}
	webRTCCalls := 0
	selector, err := New(Config{
		HTTP3: func(context.Context) (carrier.Carrier, error) { return h3, nil },
		WebRTC: func(context.Context) (carrier.Carrier, error) {
			webRTCCalls++
			return &stubCarrier{kind: protocol.CarrierWebRTC}, nil
		},
		HTTPS:        func(context.Context) (carrier.Carrier, error) { return &stubCarrier{kind: protocol.CarrierHTTPS}, nil },
		HTTP3Timeout: time.Second, WebRTCTimeout: time.Second, HTTPSTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("create selector: %v", err)
	}
	result, err := selector.Dial(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if result.Carrier != h3 || result.Kind != protocol.CarrierHTTP3 || result.UsedFallback || webRTCCalls != 0 {
		t.Fatalf("result=%+v webRTC calls=%d", result, webRTCCalls)
	}
}

func TestSelectorFallsBackAfterBoundedWebRTCFailure(t *testing.T) {
	https := &stubCarrier{kind: protocol.CarrierHTTPS}
	selector, err := New(Config{
		WebRTC: func(ctx context.Context) (carrier.Carrier, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		HTTPS:         func(context.Context) (carrier.Carrier, error) { return https, nil },
		WebRTCTimeout: 50 * time.Millisecond,
		HTTPSTimeout:  time.Second,
		CacheTTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("create selector: %v", err)
	}
	started := time.Now()
	result, err := selector.Dial(context.Background())
	if err != nil {
		t.Fatalf("dial hybrid: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("fallback took %v", elapsed)
	}
	if result.Carrier != https || result.Kind != protocol.CarrierHTTPS || !result.UsedFallback {
		t.Fatalf("unexpected fallback: %#v", result)
	}
}

func TestSelectorFallsBackWhenDialerReturnsTypedNilWithError(t *testing.T) {
	var typedNil *stubCarrier
	https := &stubCarrier{kind: protocol.CarrierHTTPS}
	selector, err := New(Config{
		WebRTC: func(context.Context) (carrier.Carrier, error) {
			return typedNil, errors.New("UDP unavailable")
		},
		HTTPS:         func(context.Context) (carrier.Carrier, error) { return https, nil },
		WebRTCTimeout: time.Second,
		HTTPSTimeout:  time.Second,
		CacheTTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("create selector: %v", err)
	}
	result, err := selector.Dial(context.Background())
	if err != nil {
		t.Fatalf("typed-nil dial must fall back without panic: %v", err)
	}
	if result.Carrier != https || result.Kind != protocol.CarrierHTTPS || !result.UsedFallback {
		t.Fatalf("unexpected fallback: %#v", result)
	}
}

func TestSelectorUsesFreshCacheAndInvalidatesFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var mu sync.Mutex
	rtcCalls := 0
	httpsCalls := 0
	rtcHealthy := false
	selector, err := New(Config{
		WebRTC: func(context.Context) (carrier.Carrier, error) {
			mu.Lock()
			defer mu.Unlock()
			rtcCalls++
			if !rtcHealthy {
				return nil, errors.New("UDP blocked")
			}
			return &stubCarrier{kind: protocol.CarrierWebRTC}, nil
		},
		HTTPS: func(context.Context) (carrier.Carrier, error) {
			mu.Lock()
			defer mu.Unlock()
			httpsCalls++
			return &stubCarrier{kind: protocol.CarrierHTTPS}, nil
		},
		WebRTCTimeout: time.Second, HTTPSTimeout: time.Second, CacheTTL: time.Minute,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("create selector: %v", err)
	}
	first, err := selector.Dial(context.Background())
	if err != nil || first.Kind != protocol.CarrierHTTPS {
		t.Fatalf("first selection=%#v err=%v", first, err)
	}
	second, err := selector.Dial(context.Background())
	if err != nil || second.Kind != protocol.CarrierHTTPS || second.UsedFallback {
		t.Fatalf("cached selection=%#v err=%v", second, err)
	}
	mu.Lock()
	if rtcCalls != 1 || httpsCalls != 2 {
		t.Fatalf("calls rtc=%d https=%d", rtcCalls, httpsCalls)
	}
	rtcHealthy = true
	mu.Unlock()
	selector.RecordFailure(protocol.CarrierHTTPS)
	third, err := selector.Dial(context.Background())
	if err != nil || third.Kind != protocol.CarrierWebRTC {
		t.Fatalf("selection after invalidation=%#v err=%v", third, err)
	}
}

func TestSelectorRejectsWrongCarrierKindAndClosesIt(t *testing.T) {
	wrong := &stubCarrier{kind: protocol.CarrierHTTPS}
	selector, err := New(Config{
		WebRTC:        func(context.Context) (carrier.Carrier, error) { return wrong, nil },
		HTTPS:         func(context.Context) (carrier.Carrier, error) { return nil, errors.New("HTTPS unavailable") },
		WebRTCTimeout: time.Second, HTTPSTimeout: time.Second, CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("create selector: %v", err)
	}
	if _, err := selector.Dial(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
	if !wrong.Closed() {
		t.Fatal("wrong-kind carrier was not closed")
	}
}

func TestSelectorDoesNotFallbackAfterParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	httpsCalls := 0
	selector, err := New(Config{
		WebRTC: func(context.Context) (carrier.Carrier, error) {
			cancel()
			return nil, context.Canceled
		},
		HTTPS: func(context.Context) (carrier.Carrier, error) {
			httpsCalls++
			return &stubCarrier{kind: protocol.CarrierHTTPS}, nil
		},
		WebRTCTimeout: time.Second, HTTPSTimeout: time.Second, CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("create selector: %v", err)
	}
	if _, err := selector.Dial(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if httpsCalls != 0 {
		t.Fatal("HTTPS was dialed after parent cancellation")
	}
}

type stubCarrier struct {
	kind   protocol.CarrierKind
	mu     sync.Mutex
	closed bool
}

func (c *stubCarrier) Send(context.Context, []byte) error      { return nil }
func (c *stubCarrier) Receive(context.Context) ([]byte, error) { return nil, io.EOF }
func (c *stubCarrier) Kind() protocol.CarrierKind              { return c.kind }
func (c *stubCarrier) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}
func (c *stubCarrier) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
