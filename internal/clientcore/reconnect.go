package clientcore

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"neproto.local/chameleon/internal/clienthost"
)

var (
	ErrReconnectExhausted = errors.New("HTTP/3 reconnect attempts exhausted")
	ErrProbeUnavailable   = errors.New("authenticated session probe is unavailable")
)

const (
	reconnectAttempts       = 6
	reconnectTotalTimeout   = 30 * time.Second
	reconnectProbeTimeout   = 1500 * time.Millisecond
	reconnectInitialBackoff = 250 * time.Millisecond
	reconnectBackoffCap     = 8 * time.Second
)

type ReconnectPolicy struct {
	MaxAttempts    int
	TotalTimeout   time.Duration
	ProbeTimeout   time.Duration
	InitialBackoff time.Duration
	BackoffCap     time.Duration
	Jitter         func(time.Duration) time.Duration
	Sleep          func(context.Context, time.Duration) error
}

func defaultReconnectPolicy() ReconnectPolicy {
	return ReconnectPolicy{
		MaxAttempts:    reconnectAttempts,
		TotalTimeout:   reconnectTotalTimeout,
		ProbeTimeout:   reconnectProbeTimeout,
		InitialBackoff: reconnectInitialBackoff,
		BackoffCap:     reconnectBackoffCap,
		Jitter:         fullJitter,
		Sleep:          sleepContext,
	}
}

func normalizeReconnectPolicy(policy ReconnectPolicy) (ReconnectPolicy, error) {
	defaults := defaultReconnectPolicy()
	if policy.MaxAttempts == 0 {
		policy.MaxAttempts = defaults.MaxAttempts
	}
	if policy.TotalTimeout == 0 {
		policy.TotalTimeout = defaults.TotalTimeout
	}
	if policy.ProbeTimeout == 0 {
		policy.ProbeTimeout = defaults.ProbeTimeout
	}
	if policy.InitialBackoff == 0 {
		policy.InitialBackoff = defaults.InitialBackoff
	}
	if policy.BackoffCap == 0 {
		policy.BackoffCap = defaults.BackoffCap
	}
	if policy.Jitter == nil {
		policy.Jitter = defaults.Jitter
	}
	if policy.Sleep == nil {
		policy.Sleep = defaults.Sleep
	}
	if policy.MaxAttempts != reconnectAttempts || policy.TotalTimeout != reconnectTotalTimeout ||
		policy.ProbeTimeout <= 0 || policy.ProbeTimeout > 5*time.Second ||
		policy.InitialBackoff != reconnectInitialBackoff || policy.BackoffCap != reconnectBackoffCap {
		return ReconnectPolicy{}, ErrInvalidOptions
	}
	return policy, nil
}

// NetworkChanged probes the current authenticated session, then performs at
// most six replacements through the same Core connector. A production Core's
// connector contains only HTTP/3 WebTransport dependencies.
func (c *Core) NetworkChanged(
	ctx context.Context,
	operationID string,
) (clienthost.Snapshot, error) {
	if ctx == nil || clienthost.ValidateOperationID(operationID) != nil {
		return c.Snapshot(), clienthost.ErrInvalidInput
	}

	c.mu.Lock()
	if c.closed {
		snapshot := c.snapshot
		c.mu.Unlock()
		return snapshot, ErrClosed
	}
	if c.snapshot.State == clienthost.StateReconnecting {
		snapshot := c.snapshot
		c.mu.Unlock()
		return snapshot, ErrReconnectActive
	}
	if c.snapshot.State != clienthost.StateConnected || c.runtime == nil {
		snapshot := c.snapshot
		c.mu.Unlock()
		return snapshot, ErrNotConnected
	}
	generation := c.generation
	current := c.runtime
	currentCancel := c.activeCancel
	request := c.request
	policy := c.reconnect
	c.snapshot.State = clienthost.StateReconnecting
	c.snapshot.LastError = nil
	c.snapshot.Sequence++
	c.workers.Add(1)
	c.mu.Unlock()
	defer c.workers.Done()

	reconnectContext, reconnectCancel := context.WithTimeout(c.rootContext, policy.TotalTimeout)
	stopCallerCancellation := context.AfterFunc(ctx, reconnectCancel)
	defer func() {
		stopCallerCancellation()
		reconnectCancel()
	}()

	probeContext, probeCancel := context.WithTimeout(reconnectContext, policy.ProbeTimeout)
	probeErr := current.Probe(probeContext)
	probeCancel()
	if probeErr == nil {
		c.mu.Lock()
		if !c.closed && c.generation == generation && c.runtime == current &&
			c.snapshot.State == clienthost.StateReconnecting {
			c.snapshot.State = clienthost.StateConnected
			c.snapshot.Sequence++
			result := c.snapshot
			c.mu.Unlock()
			return result, nil
		}
		c.mu.Unlock()
		return c.Snapshot(), context.Canceled
	}

	if currentCancel != nil {
		currentCancel()
	}
	_ = current.Close()

	lastErr := probeErr
	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		if attempt > 0 {
			delay := policy.Jitter(reconnectBackoff(attempt, policy))
			if delay < 0 || delay > policy.BackoffCap {
				delay = policy.BackoffCap
			}
			if err := policy.Sleep(reconnectContext, delay); err != nil {
				lastErr = err
				break
			}
		}
		if err := reconnectContext.Err(); err != nil {
			lastErr = err
			break
		}

		replacement, err := c.connect(reconnectContext, request.Profile)
		if err == nil && replacement == nil {
			err = ErrNoRuntime
		}
		if err != nil {
			lastErr = err
			continue
		}
		owned := &ownedRuntime{runtime: replacement}
		if replacement.Carrier() != clienthost.CarrierHTTP3WebTransport {
			_ = owned.Close()
			lastErr = ErrUnexpectedCarrier
			continue
		}

		c.mu.Lock()
		if c.closed || c.generation != generation || c.runtime != current ||
			c.snapshot.State != clienthost.StateReconnecting {
			c.mu.Unlock()
			_ = owned.Close()
			return c.Snapshot(), context.Canceled
		}
		sessionContext, sessionCancel := context.WithCancel(c.rootContext)
		sessionDone := make(chan struct{})
		c.runtime = owned
		c.activeCancel = sessionCancel
		c.activeDone = sessionDone
		c.snapshot.State = clienthost.StateConnected
		c.snapshot.Carrier = clienthost.CarrierHTTP3WebTransport
		c.snapshot.ConnectedAtUnixMS = c.now().UnixMilli()
		c.snapshot.LastError = nil
		c.snapshot.Sequence++
		result := c.snapshot
		c.workers.Add(1)
		c.mu.Unlock()

		go func() {
			defer c.workers.Done()
			c.monitor(generation, sessionContext, sessionDone, owned, operationID)
		}()
		return result, nil
	}

	if lastErr == nil {
		lastErr = clienthost.WrapError(
			clienthost.CodeHTTP3Timeout,
			clienthost.StageWebTransportConnect,
			"HTTP/3 reconnect attempts exhausted.",
			true,
			ErrReconnectExhausted,
		)
	}
	c.mu.Lock()
	if !c.closed && c.generation == generation && c.runtime == current &&
		c.snapshot.State == clienthost.StateReconnecting {
		mapped := clienthost.MapError(operationID, clienthost.StageWebTransportConnect, lastErr)
		c.runtime = nil
		c.activeCancel = nil
		c.activeDone = nil
		c.request = ConnectRequest{}
		c.snapshot.State = clienthost.StateFailed
		c.snapshot.Carrier = clienthost.CarrierNone
		c.snapshot.ConnectedAtUnixMS = 0
		c.snapshot.LastError = &mapped
		c.snapshot.Sequence++
		result := c.snapshot
		c.mu.Unlock()
		return result, errors.Join(ErrReconnectExhausted, lastErr)
	}
	c.mu.Unlock()
	return c.Snapshot(), context.Canceled
}

func reconnectBackoff(attempt int, policy ReconnectPolicy) time.Duration {
	ceiling := policy.InitialBackoff
	for index := 1; index < attempt && ceiling < policy.BackoffCap; index++ {
		if ceiling > policy.BackoffCap/2 {
			return policy.BackoffCap
		}
		ceiling *= 2
	}
	if ceiling > policy.BackoffCap {
		return policy.BackoffCap
	}
	return ceiling
}

func fullJitter(maximum time.Duration) time.Duration {
	if maximum <= 0 {
		return 0
	}
	sample, err := rand.Int(rand.Reader, big.NewInt(int64(maximum)+1))
	if err != nil {
		return maximum / 2
	}
	return time.Duration(sample.Int64())
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *ownedRuntime) Probe(ctx context.Context) error {
	probe, ok := r.runtime.(interface{ Probe(context.Context) error })
	if !ok {
		return ErrProbeUnavailable
	}
	return probe.Probe(ctx)
}
