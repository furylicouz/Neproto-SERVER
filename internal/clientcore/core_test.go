package clientcore

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
)

func TestCoreInstancesOwnIndependentCancellationAndState(t *testing.T) {
	firstRuntime := newFakeRuntime()
	secondRuntime := newFakeRuntime()
	first, err := New(Options{Connect: connectorReturning(firstRuntime)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Options{Connect: connectorReturning(secondRuntime)})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := first.Connect(context.Background(), validRequest("first")); err != nil {
		t.Fatalf("connect first: %v", err)
	}
	if _, err := second.Connect(context.Background(), validRequest("second")); err != nil {
		t.Fatalf("connect second: %v", err)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("close first: %v", err)
	}

	if got := first.Snapshot().State; got != clienthost.StateDisconnected {
		t.Fatalf("first state = %q", got)
	}
	if got := second.Snapshot().State; got != clienthost.StateConnected {
		t.Fatalf("second state = %q", got)
	}
	select {
	case <-secondRuntime.closed:
		t.Fatal("closing first core closed second runtime")
	default:
	}
	if err := second.Close(context.Background()); err != nil {
		t.Fatalf("close second: %v", err)
	}
}

func TestCoreCloseIsIdempotentAndClosesRuntimeOnce(t *testing.T) {
	runtime := newFakeRuntime()
	core, err := New(Options{Connect: connectorReturning(runtime)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Connect(context.Background(), validRequest("connect")); err != nil {
		t.Fatal(err)
	}
	if err := core.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := core.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := runtime.closeCalls.Load(); got != 1 {
		t.Fatalf("runtime close calls = %d", got)
	}
	if _, err := core.Connect(context.Background(), validRequest("after-close")); !errors.Is(err, ErrClosed) {
		t.Fatalf("connect after close error = %v", err)
	}
}

func TestCoreCloseCancelsConnectorAndWaitsForOwnedWork(t *testing.T) {
	connectorStarted := make(chan struct{})
	connectorExited := make(chan struct{})
	core, err := New(Options{Connect: func(ctx context.Context, _ config.Client) (Runtime, error) {
		close(connectorStarted)
		<-ctx.Done()
		close(connectorExited)
		return nil, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	connectResult := make(chan error, 1)
	go func() {
		_, connectErr := core.Connect(context.Background(), validRequest("blocking"))
		connectResult <- connectErr
	}()
	select {
	case <-connectorStarted:
	case <-time.After(time.Second):
		t.Fatal("connector did not start")
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := core.Close(closeCtx); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-connectorExited:
	default:
		t.Fatal("close returned before connector exited")
	}
	if err := <-connectResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("connect error = %v", err)
	}
}

func TestCoreRejectsNonHTTP3Runtime(t *testing.T) {
	runtime := newFakeRuntime()
	runtime.carrier = clienthost.CarrierNone
	core, err := New(Options{Connect: connectorReturning(runtime)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = core.Close(context.Background()) }()

	if _, err := core.Connect(context.Background(), validRequest("wrong-carrier")); !errors.Is(err, ErrUnexpectedCarrier) {
		t.Fatalf("connect error = %v", err)
	}
	if got := core.Snapshot().State; got != clienthost.StateFailed {
		t.Fatalf("state = %q", got)
	}
	select {
	case <-runtime.closed:
	case <-time.After(time.Second):
		t.Fatal("rejected runtime was not closed")
	}
}

func connectorReturning(runtime Runtime) Connector {
	return func(context.Context, config.Client) (Runtime, error) { return runtime, nil }
}

func validRequest(operationID string) ConnectRequest {
	return ConnectRequest{
		OperationID: operationID,
		ProfileID:   "profile-1",
		Profile: config.Client{
			ServerIdentity: "vpn.example.test",
		},
	}
}

type fakeRuntime struct {
	carrier    clienthost.Carrier
	closed     chan struct{}
	closeOnce  sync.Once
	closeCalls atomic.Int64
	probeCalls atomic.Int64
	probeErr   error
	probeStart chan struct{}
	probeGate  chan struct{}
	waitDone   chan struct{}
	waitOnce   sync.Once

	handoverCalls        atomic.Int64
	handoverTarget       Runtime
	closeCallsAtHandover int64
	handoverErr          error
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		carrier: clienthost.CarrierHTTP3WebTransport,
		closed:  make(chan struct{}),
	}
}

func (r *fakeRuntime) Carrier() clienthost.Carrier { return r.carrier }

func (r *fakeRuntime) Probe(context.Context) error {
	r.probeCalls.Add(1)
	if r.probeStart != nil {
		close(r.probeStart)
	}
	if r.probeGate != nil {
		<-r.probeGate
	}
	return r.probeErr
}

func (r *fakeRuntime) HandoverPacketPathTo(replacement Runtime) error {
	r.handoverCalls.Add(1)
	r.handoverTarget = replacement
	r.closeCallsAtHandover = r.closeCalls.Load()
	return r.handoverErr
}

func (r *fakeRuntime) Wait(ctx context.Context) error {
	defer func() {
		if r.waitDone != nil {
			r.waitOnce.Do(func() { close(r.waitDone) })
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.closed:
		return nil
	}
}

func (r *fakeRuntime) Close() error {
	r.closeCalls.Add(1)
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}
