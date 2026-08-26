package clientcore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
)

func TestNetworkChangedKeepsLiveHTTP3SessionWhenProbeSucceeds(t *testing.T) {
	runtime := newFakeRuntime()
	var connectCalls atomic.Int64
	core, err := New(Options{
		Connect: func(context.Context, config.Client) (Runtime, error) {
			connectCalls.Add(1)
			return runtime, nil
		},
		Reconnect: deterministicReconnectPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = core.Close(context.Background()) }()
	if _, err := core.Connect(context.Background(), validRequest("initial")); err != nil {
		t.Fatal(err)
	}

	got, err := core.NetworkChanged(context.Background(), "network-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != clienthost.StateConnected || got.Carrier != clienthost.CarrierHTTP3WebTransport {
		t.Fatalf("snapshot = %+v", got)
	}
	if runtime.probeCalls.Load() != 1 || connectCalls.Load() != 1 {
		t.Fatalf("probe calls=%d connect calls=%d", runtime.probeCalls.Load(), connectCalls.Load())
	}
	if runtime.closeCalls.Load() != 0 {
		t.Fatalf("live runtime close calls=%d", runtime.closeCalls.Load())
	}
}

func TestNetworkChangedReconnectsUsingSameStrictConnector(t *testing.T) {
	first := newFakeRuntime()
	first.probeErr = errors.New("old path failed")
	second := newFakeRuntime()
	var connectCalls atomic.Int64
	core, err := New(Options{
		Connect: func(context.Context, config.Client) (Runtime, error) {
			if connectCalls.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		},
		Reconnect: deterministicReconnectPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = core.Close(context.Background()) }()
	if _, err := core.Connect(context.Background(), validRequest("initial")); err != nil {
		t.Fatal(err)
	}

	got, err := core.NetworkChanged(context.Background(), "network-02")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != clienthost.StateConnected || connectCalls.Load() != 2 {
		t.Fatalf("snapshot=%+v connect calls=%d", got, connectCalls.Load())
	}
	if first.closeCalls.Load() != 1 {
		t.Fatalf("old runtime close calls=%d", first.closeCalls.Load())
	}
	if second.closeCalls.Load() != 0 {
		t.Fatalf("replacement runtime was closed")
	}
	if first.handoverCalls.Load() != 1 {
		t.Fatalf("packet path handover calls=%d", first.handoverCalls.Load())
	}
	if first.handoverTarget != second {
		t.Fatal("packet path was not handed to the authenticated replacement")
	}
	if first.closeCallsAtHandover != 0 {
		t.Fatalf("old runtime closed before packet path handover: close calls=%d", first.closeCallsAtHandover)
	}
}

func TestNetworkChangedPreservesPacketPathWhenOldWaitExitsDuringProbe(t *testing.T) {
	first := newFakeRuntime()
	first.probeErr = errors.New("old path failed")
	first.probeStart = make(chan struct{})
	first.probeGate = make(chan struct{})
	first.waitDone = make(chan struct{})
	second := newFakeRuntime()
	var connectCalls atomic.Int64
	core, err := New(Options{
		Connect: func(context.Context, config.Client) (Runtime, error) {
			if connectCalls.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		},
		Reconnect: deterministicReconnectPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = core.Close(context.Background()) }()
	if _, err := core.Connect(context.Background(), validRequest("initial")); err != nil {
		t.Fatal(err)
	}

	type result struct {
		snapshot clienthost.Snapshot
		err      error
	}
	resultReady := make(chan result, 1)
	go func() {
		snapshot, reconnectErr := core.NetworkChanged(context.Background(), "network-race")
		resultReady <- result{snapshot: snapshot, err: reconnectErr}
	}()
	<-first.probeStart
	first.closeOnce.Do(func() { close(first.closed) })
	<-first.waitDone
	close(first.probeGate)

	got := <-resultReady
	if got.err != nil || got.snapshot.State != clienthost.StateConnected {
		t.Fatalf("reconnect result=%+v error=%v", got.snapshot, got.err)
	}
	if first.closeCallsAtHandover != 0 {
		t.Fatalf("monitor closed old runtime before handover: close calls=%d", first.closeCallsAtHandover)
	}
}

func TestNetworkChangedClosesEveryRejectedHandoverAndKeepsAttemptsBounded(t *testing.T) {
	first := newFakeRuntime()
	first.probeErr = errors.New("old path failed")
	first.handoverErr = errors.New("packet path handover failed")
	replacements := make([]*fakeRuntime, reconnectAttempts)
	var connectCalls atomic.Int64
	core, err := New(Options{
		Connect: func(context.Context, config.Client) (Runtime, error) {
			call := connectCalls.Add(1)
			if call == 1 {
				return first, nil
			}
			replacement := newFakeRuntime()
			replacements[call-2] = replacement
			return replacement, nil
		},
		Reconnect: deterministicReconnectPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Connect(context.Background(), validRequest("initial")); err != nil {
		t.Fatal(err)
	}

	got, reconnectErr := core.NetworkChanged(context.Background(), "network-handover-fails")
	if !errors.Is(reconnectErr, ErrReconnectExhausted) ||
		!errors.Is(reconnectErr, first.handoverErr) {
		t.Fatalf("reconnect error=%v", reconnectErr)
	}
	if got.State != clienthost.StateFailed || first.handoverCalls.Load() != reconnectAttempts {
		t.Fatalf("snapshot=%+v handover calls=%d", got, first.handoverCalls.Load())
	}
	for index, replacement := range replacements {
		if replacement == nil || replacement.closeCalls.Load() != 1 {
			t.Fatalf("replacement %d close calls=%v", index, replacement)
		}
	}
	if first.closeCalls.Load() != 1 {
		t.Fatalf("old runtime close calls=%d", first.closeCalls.Load())
	}
	if err := core.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReconnectStopsAfterSixHTTP3AttemptsAndClosesOwnedState(t *testing.T) {
	first := newFakeRuntime()
	first.probeErr = errors.New("probe failed")
	var connectCalls atomic.Int64
	want := errors.New("HTTP/3 unavailable")
	core, err := New(Options{
		Connect: func(context.Context, config.Client) (Runtime, error) {
			if connectCalls.Add(1) == 1 {
				return first, nil
			}
			return nil, want
		},
		Reconnect: deterministicReconnectPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Connect(context.Background(), validRequest("initial")); err != nil {
		t.Fatal(err)
	}

	got, reconnectErr := core.NetworkChanged(context.Background(), "network-03")
	if !errors.Is(reconnectErr, ErrReconnectExhausted) || !errors.Is(reconnectErr, want) {
		t.Fatalf("reconnect error=%v", reconnectErr)
	}
	if connectCalls.Load() != 7 {
		t.Fatalf("total connect calls=%d, want initial + six reconnects", connectCalls.Load())
	}
	if got.State != clienthost.StateFailed || got.Carrier != clienthost.CarrierNone || got.LastError == nil {
		t.Fatalf("snapshot=%+v", got)
	}
	if first.closeCalls.Load() != 1 {
		t.Fatalf("old runtime close calls=%d", first.closeCalls.Load())
	}
	if err := core.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultReconnectPolicyHasThirtySecondOverallDeadline(t *testing.T) {
	policy := defaultReconnectPolicy()
	if policy.MaxAttempts != 6 || policy.TotalTimeout != 30*time.Second || policy.BackoffCap != 8*time.Second {
		t.Fatalf("default policy=%+v", policy)
	}

	first := newFakeRuntime()
	first.probeErr = errors.New("probe failed")
	var connectCalls atomic.Int64
	var observed time.Duration
	policy.Jitter = func(time.Duration) time.Duration { return 0 }
	core, err := New(Options{
		Connect: func(ctx context.Context, _ config.Client) (Runtime, error) {
			if connectCalls.Add(1) == 1 {
				return first, nil
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("reconnect context has no overall deadline")
			}
			observed = time.Until(deadline)
			return nil, context.DeadlineExceeded
		},
		Reconnect: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Connect(context.Background(), validRequest("initial")); err != nil {
		t.Fatal(err)
	}
	_, _ = core.NetworkChanged(context.Background(), "network-04")
	if observed < 29*time.Second || observed > 30*time.Second {
		t.Fatalf("observed overall deadline=%s", observed)
	}
	_ = core.Close(context.Background())
}

func deterministicReconnectPolicy() ReconnectPolicy {
	policy := defaultReconnectPolicy()
	policy.Jitter = func(time.Duration) time.Duration { return 0 }
	return policy
}
