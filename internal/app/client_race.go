package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/carrier/hybrid"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

var errNoAuthenticatedCarrier = errors.New("no authenticated NP/2 carrier available")

const (
	fastCarrierStagger = 150 * time.Millisecond
	httpsFallbackDelay = 600 * time.Millisecond
)

type authenticatedAttempt struct {
	kind  protocol.CarrierKind
	delay time.Duration
	run   func(context.Context) (*session.Authenticated, hybrid.Result, error)
}

type authenticatedOutcome struct {
	index         int
	authenticated *session.Authenticated
	selected      hybrid.Result
	err           error
}

func raceAuthenticatedCandidates(
	ctx context.Context, attempts []authenticatedAttempt,
) (*session.Authenticated, hybrid.Result, error) {
	if ctx == nil || len(attempts) == 0 || len(attempts) > 3 {
		return nil, hybrid.Result{}, errNoAuthenticatedCarrier
	}
	racingContext, cancelRace := context.WithCancel(ctx)
	defer cancelRace()
	outcomes := make(chan authenticatedOutcome, len(attempts))
	for index, attempt := range attempts {
		index, attempt := index, attempt
		go func() {
			if attempt.run == nil || attempt.delay < 0 {
				outcomes <- authenticatedOutcome{index: index, err: errNoAuthenticatedCarrier}
				return
			}
			if attempt.delay > 0 {
				timer := time.NewTimer(attempt.delay)
				select {
				case <-timer.C:
				case <-racingContext.Done():
					if !timer.Stop() {
						<-timer.C
					}
					outcomes <- authenticatedOutcome{index: index, err: racingContext.Err()}
					return
				}
			}
			authenticated, selected, err := attempt.run(racingContext)
			if err == nil && (authenticated == nil || nilCarrierValue(selected.Carrier) ||
				selected.Kind != attempt.kind || selected.Carrier.Kind() != attempt.kind ||
				authenticated.Carrier != attempt.kind) {
				err = fmt.Errorf("%w: invalid authenticated carrier %d", errNoAuthenticatedCarrier, attempt.kind)
			}
			outcomes <- authenticatedOutcome{
				index: index, authenticated: authenticated, selected: selected, err: err,
			}
		}()
	}

	var winner *authenticatedOutcome
	failures := make([]error, 0, len(attempts))
	for range attempts {
		outcome := <-outcomes
		if outcome.err == nil && winner == nil {
			winner = &outcome
			cancelRace()
			continue
		}
		closeAuthenticatedOutcome(outcome)
		if outcome.err != nil && !errors.Is(outcome.err, context.Canceled) {
			failures = append(failures, fmt.Errorf("carrier %d: %w", attempts[outcome.index].kind, outcome.err))
		}
	}
	if winner != nil {
		winner.selected.UsedFallback = winner.index != 0
		return winner.authenticated, winner.selected, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, hybrid.Result{}, err
	}
	return nil, hybrid.Result{}, errors.Join(append([]error{errNoAuthenticatedCarrier}, failures...)...)
}

func closeAuthenticatedOutcome(outcome authenticatedOutcome) {
	if outcome.authenticated != nil && outcome.authenticated.Mux != nil {
		_ = outcome.authenticated.Mux.Close()
		return
	}
	if !nilCarrierValue(outcome.selected.Carrier) {
		_ = outcome.selected.Carrier.Close()
	}
}

func nilCarrierValue(connection carrier.Carrier) bool {
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

type carrierPreference struct {
	kind     protocol.CarrierKind
	recorded time.Time
}

type carrierPreferenceCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]carrierPreference
}

func newCarrierPreferenceCache(now func() time.Time) *carrierPreferenceCache {
	if now == nil {
		now = time.Now
	}
	return &carrierPreferenceCache{now: now, entries: make(map[string]carrierPreference)}
}

func (c *carrierPreferenceCache) load(key string, ttl time.Duration) (protocol.CarrierKind, bool) {
	if key == "" || ttl <= 0 {
		return 0, false
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	preference, ok := c.entries[key]
	if !ok || now.Before(preference.recorded) || now.Sub(preference.recorded) >= ttl {
		delete(c.entries, key)
		return 0, false
	}
	return preference.kind, true
}

func (c *carrierPreferenceCache) record(key string, kind protocol.CarrierKind) {
	if key == "" || (kind != protocol.CarrierHTTP3 && kind != protocol.CarrierWebRTC &&
		kind != protocol.CarrierHTTPS) {
		return
	}
	c.mu.Lock()
	c.entries[key] = carrierPreference{kind: kind, recorded: c.now()}
	c.mu.Unlock()
}

func (c *carrierPreferenceCache) invalidate(key string, kind protocol.CarrierKind) {
	c.mu.Lock()
	if preference, ok := c.entries[key]; ok && preference.kind == kind {
		delete(c.entries, key)
	}
	c.mu.Unlock()
}

func (c *carrierPreferenceCache) reset() {
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

var defaultCarrierPreferences = newCarrierPreferenceCache(time.Now)

// ResetClientCarrierPreferences invalidates network-scoped transport history.
// Mobile clients call it whenever the OS reports a Wi-Fi/cellular path change.
func ResetClientCarrierPreferences() {
	defaultCarrierPreferences.reset()
}

type authenticatedModeDial func(
	context.Context, config.Client, ProbeMode,
) (*session.Authenticated, hybrid.Result, error)

func raceClientCarriers(
	ctx context.Context,
	clientConfig config.Client,
	dial authenticatedModeDial,
	preferences *carrierPreferenceCache,
) (*session.Authenticated, hybrid.Result, error) {
	if ctx == nil || dial == nil || preferences == nil {
		return nil, hybrid.Result{}, errNoAuthenticatedCarrier
	}
	preferenceKey := clientConfig.ServerIdentity
	cachedKind, cached := preferences.load(preferenceKey, clientConfig.CarrierCacheTTL.Duration)
	modes := clientCarrierOrder(clientConfig.HTTP3Configured(), cachedKind, cached)
	attempts := make([]authenticatedAttempt, 0, len(modes))
	for index, mode := range modes {
		mode := mode
		kind := probeCarrierKind(mode)
		delay := time.Duration(0)
		if index > 0 {
			if mode == ProbeHTTPS {
				delay = httpsFallbackDelay
			} else {
				delay = time.Duration(index) * fastCarrierStagger
			}
		}
		attempts = append(attempts, authenticatedAttempt{
			kind: kind, delay: delay,
			run: func(attemptContext context.Context) (*session.Authenticated, hybrid.Result, error) {
				timeout := clientCarrierTimeout(clientConfig, mode)
				boundedContext, cancel := context.WithTimeout(attemptContext, timeout)
				defer cancel()
				authenticated, selected, err := dial(boundedContext, clientConfig, mode)
				if err != nil {
					preferences.invalidate(preferenceKey, kind)
				}
				return authenticated, selected, err
			},
		})
	}
	authenticated, selected, err := raceAuthenticatedCandidates(ctx, attempts)
	if err != nil {
		return nil, hybrid.Result{}, err
	}
	preferences.record(preferenceKey, selected.Kind)
	return authenticated, selected, nil
}

func clientCarrierOrder(http3Configured bool, cachedKind protocol.CarrierKind, cached bool) []ProbeMode {
	order := make([]ProbeMode, 0, 3)
	appendUnique := func(mode ProbeMode) {
		for _, existing := range order {
			if existing == mode {
				return
			}
		}
		order = append(order, mode)
	}
	if cached {
		appendUnique(carrierProbeMode(cachedKind))
	}
	if http3Configured {
		appendUnique(ProbeHTTP3)
	}
	appendUnique(ProbeWebRTC)
	appendUnique(ProbeHTTPS)
	return order
}

func clientCarrierTimeout(clientConfig config.Client, mode ProbeMode) time.Duration {
	switch mode {
	case ProbeHTTP3:
		return clientConfig.HTTP3Timeout.Duration
	case ProbeWebRTC:
		return clientConfig.WebRTCTimeout.Duration
	case ProbeHTTPS:
		return clientConfig.HTTPSTimeout.Duration
	default:
		return time.Millisecond
	}
}

func probeCarrierKind(mode ProbeMode) protocol.CarrierKind {
	switch mode {
	case ProbeHTTP3:
		return protocol.CarrierHTTP3
	case ProbeWebRTC:
		return protocol.CarrierWebRTC
	case ProbeHTTPS:
		return protocol.CarrierHTTPS
	default:
		return 0
	}
}

func carrierProbeMode(kind protocol.CarrierKind) ProbeMode {
	switch kind {
	case protocol.CarrierHTTP3:
		return ProbeHTTP3
	case protocol.CarrierWebRTC:
		return ProbeWebRTC
	case protocol.CarrierHTTPS:
		return ProbeHTTPS
	default:
		return ProbeAuto
	}
}
