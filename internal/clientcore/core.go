// Package clientcore owns one bounded NP/2 client lifecycle per Core instance.
// It contains no UI or platform tunnel integration and accepts only an
// HTTP/3 WebTransport runtime.
package clientcore

import (
	"context"
	"errors"
	"sync"
	"time"

	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
)

var (
	ErrInvalidOptions    = errors.New("invalid client core options")
	ErrClosed            = errors.New("client core is closed")
	ErrAlreadyActive     = errors.New("client core is already active")
	ErrNotConnected      = errors.New("client core is not connected")
	ErrReconnectActive   = errors.New("client core reconnect is already active")
	ErrNoRuntime         = errors.New("client connector returned no runtime")
	ErrUnexpectedCarrier = errors.New("client connector returned a non-HTTP/3 runtime")
)

// Runtime is an authenticated NP/2 session whose carrier has already been
// established. Platform hosts attach their packet tunnel through a richer
// adapter in a later layer; the lifecycle core owns Wait and Close.
type Runtime interface {
	Carrier() clienthost.Carrier
	Wait(context.Context) error
	Close() error
}

// Connector establishes one authenticated runtime from an already validated
// client configuration. The strict production constructor is added separately
// so lifecycle tests can inject deterministic runtimes.
type Connector func(context.Context, config.Client) (Runtime, error)

type Options struct {
	Connect   Connector
	Now       func() time.Time
	Reconnect ReconnectPolicy
}

type ConnectRequest struct {
	OperationID string
	ProfileID   string
	Profile     config.Client
}

// Core is single-use by design: one Core owns one native tunnel lifetime.
// Closing it is terminal and idempotent. Reconnection within that lifetime is
// same-carrier work and does not construct a different transport.
type Core struct {
	mu        sync.Mutex
	connect   Connector
	now       func() time.Time
	reconnect ReconnectPolicy

	rootContext context.Context
	rootCancel  context.CancelFunc
	closed      bool
	generation  uint64
	snapshot    clienthost.Snapshot

	activeCancel context.CancelFunc
	activeDone   chan struct{}
	runtime      *ownedRuntime

	closeDone chan struct{}
	closeErr  error
	workers   sync.WaitGroup
	request   ConnectRequest
}

func New(options Options) (*Core, error) {
	if options.Connect == nil {
		return nil, ErrInvalidOptions
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	reconnect, err := normalizeReconnectPolicy(options.Reconnect)
	if err != nil {
		return nil, err
	}
	rootContext, rootCancel := context.WithCancel(context.Background())
	return &Core{
		connect:     options.Connect,
		now:         options.Now,
		reconnect:   reconnect,
		rootContext: rootContext,
		rootCancel:  rootCancel,
		snapshot: clienthost.Snapshot{
			State:   clienthost.StateDisconnected,
			Carrier: clienthost.CarrierNone,
		},
	}, nil
}

func (c *Core) Connect(ctx context.Context, request ConnectRequest) (clienthost.Snapshot, error) {
	if ctx == nil || clienthost.ValidateOperationID(request.OperationID) != nil ||
		clienthost.ValidateProfileID(request.ProfileID) != nil {
		return c.Snapshot(), clienthost.ErrInvalidInput
	}

	c.mu.Lock()
	if c.closed {
		snapshot := c.snapshot
		c.mu.Unlock()
		return snapshot, ErrClosed
	}
	if c.activeDone != nil {
		snapshot := c.snapshot
		c.mu.Unlock()
		return snapshot, ErrAlreadyActive
	}
	c.generation++
	generation := c.generation
	connectDone := make(chan struct{})
	connectContext, connectCancel := context.WithCancel(c.rootContext)
	stopCallerCancellation := context.AfterFunc(ctx, connectCancel)
	c.activeCancel = connectCancel
	c.activeDone = connectDone
	c.snapshot.State = clienthost.StateConnecting
	c.snapshot.ProfileID = request.ProfileID
	c.snapshot.Carrier = clienthost.CarrierNone
	c.snapshot.ConnectedAtUnixMS = 0
	c.snapshot.LastError = nil
	c.snapshot.Sequence++
	c.workers.Add(1)
	c.mu.Unlock()
	defer c.workers.Done()

	runtime, connectErr := c.connect(connectContext, request.Profile)
	stopCallerCancellation()
	connectCancel()
	if connectErr == nil && runtime == nil {
		connectErr = ErrNoRuntime
	}
	if connectErr != nil {
		c.finishConnectFailure(generation, connectDone, request.OperationID, connectErr)
		return c.Snapshot(), connectErr
	}

	owned := &ownedRuntime{runtime: runtime}
	if runtime.Carrier() != clienthost.CarrierHTTP3WebTransport {
		_ = owned.Close()
		c.finishConnectFailure(generation, connectDone, request.OperationID, ErrUnexpectedCarrier)
		return c.Snapshot(), ErrUnexpectedCarrier
	}

	c.mu.Lock()
	if c.closed || c.generation != generation || c.activeDone != connectDone {
		c.mu.Unlock()
		_ = owned.Close()
		close(connectDone)
		return c.Snapshot(), context.Canceled
	}
	sessionContext, sessionCancel := context.WithCancel(c.rootContext)
	sessionDone := make(chan struct{})
	c.activeCancel = sessionCancel
	c.activeDone = sessionDone
	c.runtime = owned
	c.request = request
	c.snapshot.State = clienthost.StateConnected
	c.snapshot.Carrier = clienthost.CarrierHTTP3WebTransport
	c.snapshot.ConnectedAtUnixMS = c.now().UnixMilli()
	c.snapshot.LastError = nil
	c.snapshot.Sequence++
	result := c.snapshot
	close(connectDone)
	c.workers.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.workers.Done()
		c.monitor(generation, sessionContext, sessionDone, owned, request.OperationID)
	}()
	return result, nil
}

func (c *Core) finishConnectFailure(
	generation uint64,
	done chan struct{},
	operationID string,
	err error,
) {
	c.mu.Lock()
	if !c.closed && c.generation == generation && c.activeDone == done {
		mapped := clienthost.MapError(operationID, clienthost.StageUnknown, err)
		c.snapshot.State = clienthost.StateFailed
		c.snapshot.Carrier = clienthost.CarrierNone
		c.snapshot.ConnectedAtUnixMS = 0
		c.snapshot.LastError = &mapped
		c.snapshot.Sequence++
		c.activeCancel = nil
		c.activeDone = nil
	}
	c.mu.Unlock()
	close(done)
}

func (c *Core) monitor(
	generation uint64,
	ctx context.Context,
	done chan struct{},
	runtime *ownedRuntime,
	operationID string,
) {
	waitErr := runtime.Wait(ctx)
	closeErr := runtime.Close()

	c.mu.Lock()
	if !c.closed && c.generation == generation && c.runtime == runtime &&
		c.snapshot.State != clienthost.StateReconnecting {
		c.runtime = nil
		c.activeCancel = nil
		c.activeDone = nil
		c.snapshot.Carrier = clienthost.CarrierNone
		c.snapshot.ConnectedAtUnixMS = 0
		if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
			mapped := clienthost.MapError(operationID, clienthost.StagePacketForwarding, waitErr)
			c.snapshot.State = clienthost.StateFailed
			c.snapshot.LastError = &mapped
		} else if closeErr != nil {
			mapped := clienthost.MapError(operationID, clienthost.StagePacketForwarding, closeErr)
			c.snapshot.State = clienthost.StateFailed
			c.snapshot.LastError = &mapped
		} else {
			c.snapshot.State = clienthost.StateDisconnected
			c.snapshot.LastError = nil
		}
		c.snapshot.Sequence++
	}
	c.mu.Unlock()
	close(done)
}

func (c *Core) Snapshot() clienthost.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := c.snapshot
	if c.snapshot.LastError != nil {
		lastError := *c.snapshot.LastError
		result.LastError = &lastError
	}
	return result
}

// Close permanently closes this instance. Cleanup continues independently of
// the caller's deadline, while each caller waits only for its own context.
func (c *Core) Close(ctx context.Context) error {
	if ctx == nil {
		return clienthost.ErrInvalidInput
	}

	c.mu.Lock()
	if c.closeDone == nil {
		c.closed = true
		c.generation++
		c.closeDone = make(chan struct{})
		cancel := c.activeCancel
		done := c.activeDone
		runtime := c.runtime
		c.snapshot.State = clienthost.StateDisconnecting
		c.snapshot.Sequence++
		c.rootCancel()
		go c.shutdown(cancel, done, runtime)
	}
	closeDone := c.closeDone
	c.mu.Unlock()

	select {
	case <-closeDone:
		c.mu.Lock()
		err := c.closeErr
		c.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Core) shutdown(cancel context.CancelFunc, done chan struct{}, runtime *ownedRuntime) {
	if cancel != nil {
		cancel()
	}
	var closeErr error
	if runtime != nil {
		closeErr = runtime.Close()
	}
	if done != nil {
		<-done
	}
	c.workers.Wait()

	c.mu.Lock()
	c.runtime = nil
	c.activeCancel = nil
	c.activeDone = nil
	c.request = ConnectRequest{}
	c.snapshot.State = clienthost.StateDisconnected
	c.snapshot.Carrier = clienthost.CarrierNone
	c.snapshot.ConnectedAtUnixMS = 0
	c.snapshot.LastError = nil
	c.snapshot.Sequence++
	c.closeErr = closeErr
	close(c.closeDone)
	c.mu.Unlock()
}

type ownedRuntime struct {
	runtime Runtime
	once    sync.Once
	err     error
}

func (r *ownedRuntime) Wait(ctx context.Context) error { return r.runtime.Wait(ctx) }

func (r *ownedRuntime) Close() error {
	r.once.Do(func() { r.err = r.runtime.Close() })
	return r.err
}
