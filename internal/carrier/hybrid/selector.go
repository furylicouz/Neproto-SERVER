package hybrid

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

const (
	defaultWebRTCTimeout = 5 * time.Second
	defaultHTTP3Timeout  = 5 * time.Second
	defaultHTTPSTimeout  = 10 * time.Second
	defaultCacheTTL      = 10 * time.Minute
)

var (
	ErrInvalidConfig = errors.New("invalid hybrid selector configuration")
	ErrUnavailable   = errors.New("all carriers unavailable")
	ErrCarrierKind   = errors.New("dialer returned wrong carrier kind")
)

type DialFunc func(context.Context) (carrier.Carrier, error)

type Config struct {
	HTTP3         DialFunc
	WebRTC        DialFunc
	HTTPS         DialFunc
	HTTP3Timeout  time.Duration
	WebRTCTimeout time.Duration
	HTTPSTimeout  time.Duration
	CacheTTL      time.Duration
	Now           func() time.Time
}

type Result struct {
	Carrier      carrier.Carrier
	Kind         protocol.CarrierKind
	UsedFallback bool
}

type Selector struct {
	http3         DialFunc
	webRTC        DialFunc
	https         DialFunc
	http3Timeout  time.Duration
	webRTCTimeout time.Duration
	httpsTimeout  time.Duration
	cacheTTL      time.Duration
	now           func() time.Time

	mu        sync.Mutex
	cacheKind protocol.CarrierKind
	cacheTime time.Time
}

func New(config Config) (*Selector, error) {
	if config.WebRTC == nil || config.HTTPS == nil || config.HTTP3Timeout < 0 || config.WebRTCTimeout < 0 ||
		config.HTTPSTimeout < 0 || config.CacheTTL < 0 {
		return nil, ErrInvalidConfig
	}
	webRTCTimeout := config.WebRTCTimeout
	if webRTCTimeout == 0 {
		webRTCTimeout = defaultWebRTCTimeout
	}
	http3Timeout := config.HTTP3Timeout
	if http3Timeout == 0 {
		http3Timeout = defaultHTTP3Timeout
	}
	httpsTimeout := config.HTTPSTimeout
	if httpsTimeout == 0 {
		httpsTimeout = defaultHTTPSTimeout
	}
	cacheTTL := config.CacheTTL
	if cacheTTL == 0 {
		cacheTTL = defaultCacheTTL
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Selector{
		http3: config.HTTP3, webRTC: config.WebRTC, https: config.HTTPS,
		http3Timeout:  http3Timeout,
		webRTCTimeout: webRTCTimeout, httpsTimeout: httpsTimeout,
		cacheTTL: cacheTTL, now: now,
	}, nil
}

func (s *Selector) Dial(ctx context.Context) (Result, error) {
	if ctx == nil {
		return Result{}, ErrInvalidConfig
	}
	order := s.attemptOrder()
	failures := make([]error, 0, len(order))
	for index, kind := range order {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		dial, timeout := s.dialer(kind)
		attemptContext, cancelAttempt := context.WithTimeout(ctx, timeout)
		connection, err := dial(attemptContext)
		attemptErr := attemptContext.Err()
		cancelAttempt()
		if err == nil && attemptErr != nil {
			err = attemptErr
		}
		if nilCarrier(connection) {
			connection = nil
			if err == nil {
				err = ErrUnavailable
			}
		}
		if err == nil && connection.Kind() != kind {
			_ = connection.Close()
			err = fmt.Errorf("%w: expected %d, got %d", ErrCarrierKind, kind, connection.Kind())
			connection = nil
		}
		if err == nil {
			s.recordSuccess(kind)
			return Result{Carrier: connection, Kind: kind, UsedFallback: index != 0}, nil
		}
		if connection != nil {
			_ = connection.Close()
		}
		s.RecordFailure(kind)
		failures = append(failures, fmt.Errorf("carrier %d: %w", kind, err))
		if parentErr := ctx.Err(); parentErr != nil {
			return Result{}, parentErr
		}
	}
	return Result{}, errors.Join(append([]error{ErrUnavailable}, failures...)...)
}

func nilCarrier(connection carrier.Carrier) bool {
	if connection == nil {
		return true
	}
	value := reflect.ValueOf(connection)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (s *Selector) RecordFailure(kind protocol.CarrierKind) {
	s.mu.Lock()
	if s.cacheKind == kind {
		s.cacheKind = 0
		s.cacheTime = time.Time{}
	}
	s.mu.Unlock()
}

func (s *Selector) attemptOrder() []protocol.CarrierKind {
	now := s.now()
	s.mu.Lock()
	cached := s.cacheKind
	fresh := cached != 0 && now.Sub(s.cacheTime) >= 0 && now.Sub(s.cacheTime) < s.cacheTTL
	if !fresh {
		s.cacheKind = 0
		s.cacheTime = time.Time{}
		cached = 0
	}
	s.mu.Unlock()

	order := make([]protocol.CarrierKind, 0, 3)
	appendUnique := func(kind protocol.CarrierKind) {
		for _, existing := range order {
			if existing == kind {
				return
			}
		}
		order = append(order, kind)
	}
	if cached != 0 {
		appendUnique(cached)
	}
	if s.http3 != nil {
		appendUnique(protocol.CarrierHTTP3)
	}
	appendUnique(protocol.CarrierWebRTC)
	appendUnique(protocol.CarrierHTTPS)
	return order
}

func (s *Selector) dialer(kind protocol.CarrierKind) (DialFunc, time.Duration) {
	if kind == protocol.CarrierHTTP3 {
		return s.http3, s.http3Timeout
	}
	if kind == protocol.CarrierWebRTC {
		return s.webRTC, s.webRTCTimeout
	}
	return s.https, s.httpsTimeout
}

func (s *Selector) recordSuccess(kind protocol.CarrierKind) {
	now := s.now()
	s.mu.Lock()
	s.cacheKind = kind
	s.cacheTime = now
	s.mu.Unlock()
}
