package constellation

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

const MaxControlFlows = 65_536

var (
	ErrControlChannelConfig = errors.New("invalid constellation control channel configuration")
	ErrControlChannelClosed = errors.New("constellation control channel closed")
	ErrControlFlowCapacity  = errors.New("constellation control flow capacity exceeded")
	ErrControlFlowDuplicate = errors.New("constellation control flow already registered")
	ErrControlFlowAborted   = errors.New("constellation control flow aborted")
	ErrControlProtocol      = errors.New("constellation control protocol violation")
)

// FlowControlHandler methods must be bounded and non-blocking. A handler is
// invoked only for its own flow; acknowledgement failures abort that flow but
// leave the authenticated carrier and unrelated flows alive.
type FlowControlHandler interface {
	Acknowledge(offset uint64) error
	Abort(error)
}

type ControlChannelConfig struct {
	Mux             *session.Mux
	ConstellationID protocol.ContinuityID
	FirstMessageID  uint64
	MaxFlows        int
}

type ControlChannel struct {
	mux             *session.Mux
	constellationID protocol.ContinuityID
	maxFlows        int
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}

	mu       sync.Mutex
	handlers map[protocol.ContinuityID]FlowControlHandler
	closed   bool
	err      error

	sendMu        sync.Mutex
	nextMessageID uint64
	closeOnce     sync.Once
}

func NewControlChannel(ctx context.Context, config ControlChannelConfig) (*ControlChannel, error) {
	if ctx == nil || config.Mux == nil || config.ConstellationID == (protocol.ContinuityID{}) ||
		config.FirstMessageID == 0 || config.FirstMessageID > protocol.MaxSequence ||
		config.MaxFlows <= 0 || config.MaxFlows > MaxControlFlows {
		return nil, ErrControlChannelConfig
	}
	channelContext, cancel := context.WithCancel(ctx)
	channel := &ControlChannel{
		mux: config.Mux, constellationID: config.ConstellationID, maxFlows: config.MaxFlows,
		ctx: channelContext, cancel: cancel, done: make(chan struct{}),
		handlers:      make(map[protocol.ContinuityID]FlowControlHandler, config.MaxFlows),
		nextMessageID: config.FirstMessageID,
	}
	go channel.receiveLoop()
	return channel, nil
}

func (c *ControlChannel) Register(id protocol.ContinuityID, handler FlowControlHandler) error {
	if c == nil {
		return ErrControlChannelConfig
	}
	if id == (protocol.ContinuityID{}) || nilFlowControlHandler(handler) {
		return ErrControlChannelConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrControlChannelClosed
	}
	if _, exists := c.handlers[id]; exists {
		return ErrControlFlowDuplicate
	}
	if len(c.handlers) >= c.maxFlows {
		return ErrControlFlowCapacity
	}
	c.handlers[id] = handler
	return nil
}

func (c *ControlChannel) Unregister(id protocol.ContinuityID) bool {
	if c == nil || id == (protocol.ContinuityID{}) {
		return false
	}
	c.mu.Lock()
	_, exists := c.handlers[id]
	delete(c.handlers, id)
	c.mu.Unlock()
	return exists
}

func (c *ControlChannel) SendAck(ctx context.Context, flowID protocol.ContinuityID, offset uint64) error {
	return c.send(ctx, protocol.ContinuityFrame{
		Type: protocol.ContinuityFlowAck, ConstellationID: c.constellationID,
		FlowID: flowID, ReceiveOffset: offset,
	})
}

func (c *ControlChannel) SendAbort(ctx context.Context, flowID protocol.ContinuityID) error {
	return c.send(ctx, protocol.ContinuityFrame{
		Type: protocol.ContinuityFlowAbort, ConstellationID: c.constellationID,
		FlowID: flowID,
	})
}

func (c *ControlChannel) Wait(ctx context.Context) error {
	if c == nil || ctx == nil {
		return ErrControlChannelConfig
	}
	select {
	case <-c.done:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err != nil {
			return err
		}
		return ErrControlChannelClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *ControlChannel) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() { c.cancel() })
	<-c.done
	return nil
}

func (c *ControlChannel) send(ctx context.Context, frame protocol.ContinuityFrame) error {
	if c == nil || ctx == nil || frame.FlowID == (protocol.ContinuityID{}) {
		return ErrControlChannelConfig
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrControlChannelClosed
	}
	c.mu.Unlock()
	if c.nextMessageID == 0 || c.nextMessageID > protocol.MaxSequence {
		return ErrControlChannelClosed
	}
	frame.MessageID = c.nextMessageID
	c.nextMessageID++
	if err := c.mux.SendContinuity(ctx, frame); err != nil {
		return errors.Join(ErrControlChannelClosed, err)
	}
	return nil
}

func (c *ControlChannel) receiveLoop() {
	var terminal error
	defer func() { c.finish(terminal) }()
	for {
		frame, err := c.mux.ReceiveContinuity(c.ctx)
		if err != nil {
			if c.ctx.Err() != nil {
				terminal = ErrControlChannelClosed
			} else {
				terminal = errors.Join(ErrControlChannelClosed, err)
			}
			return
		}
		if frame.ConstellationID != c.constellationID {
			terminal = ErrControlProtocol
			_ = c.mux.Close()
			return
		}
		switch frame.Type {
		case protocol.ContinuityFlowAck:
			c.handleAck(frame)
		case protocol.ContinuityFlowAbort:
			c.handleAbort(frame.FlowID, ErrControlFlowAborted)
		default:
			terminal = ErrControlProtocol
			_ = c.mux.Close()
			return
		}
	}
}

func (c *ControlChannel) handleAck(frame protocol.ContinuityFrame) {
	c.mu.Lock()
	handler := c.handlers[frame.FlowID]
	c.mu.Unlock()
	if handler == nil {
		_ = c.SendAbort(c.ctx, frame.FlowID)
		return
	}
	if err := handler.Acknowledge(frame.ReceiveOffset); err != nil {
		c.handleAbort(frame.FlowID, errors.Join(ErrControlFlowAborted, err))
		_ = c.SendAbort(c.ctx, frame.FlowID)
	}
}

func (c *ControlChannel) handleAbort(id protocol.ContinuityID, reason error) {
	c.mu.Lock()
	handler := c.handlers[id]
	delete(c.handlers, id)
	c.mu.Unlock()
	if handler != nil {
		handler.Abort(reason)
	}
}

func (c *ControlChannel) finish(terminal error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.err = terminal
	handlers := make([]FlowControlHandler, 0, len(c.handlers))
	for _, handler := range c.handlers {
		handlers = append(handlers, handler)
	}
	clear(c.handlers)
	c.mu.Unlock()
	for _, handler := range handlers {
		handler.Abort(ErrControlChannelClosed)
	}
	close(c.done)
}

func nilFlowControlHandler(handler FlowControlHandler) bool {
	if handler == nil {
		return true
	}
	value := reflect.ValueOf(handler)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
