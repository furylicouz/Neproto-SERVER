package tunstack

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	np2proxy "neproto.local/chameleon/internal/proxy"
)

const (
	firstFlightWaitTimeout = 2 * time.Second
	tcpOpenTimeout         = 10 * time.Second
)

type deferredStreamConn struct {
	mu               sync.Mutex
	opener           streamOpenFunc
	target           np2proxy.Target
	local            net.Addr
	remote           net.Addr
	stream           *streamConn
	pending          []byte
	openErr          error
	closed           bool
	timer            *time.Timer
	ready            chan struct{}
	readyOnce        sync.Once
	observeDecision  func(bool)
	decisionOnce     sync.Once
	lifecycleContext context.Context
	lifecycleCancel  context.CancelFunc
}

var _ net.Conn = (*deferredStreamConn)(nil)

func newDeferredStreamConn(
	opener streamOpenFunc,
	target np2proxy.Target,
	local, remote net.Addr,
	observeDecision func(bool),
) *deferredStreamConn {
	lifecycleContext, lifecycleCancel := context.WithCancel(context.Background())
	connection := &deferredStreamConn{
		opener: opener, target: target, local: local, remote: remote,
		ready: make(chan struct{}), observeDecision: observeDecision,
		lifecycleContext: lifecycleContext, lifecycleCancel: lifecycleCancel,
	}
	connection.timer = time.AfterFunc(firstFlightWaitTimeout, connection.openNumericAfterTimeout)
	return connection
}

func (c *deferredStreamConn) Read(destination []byte) (int, error) {
	c.mu.Lock()
	if c.stream != nil {
		stream := c.stream
		c.mu.Unlock()
		return stream.Read(destination)
	}
	ready := c.ready
	c.mu.Unlock()
	<-ready
	c.mu.Lock()
	stream, err := c.stream, c.openErr
	if c.closed && err == nil {
		err = net.ErrClosed
	}
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if stream == nil {
		return 0, net.ErrClosed
	}
	return stream.Read(destination)
}

func (c *deferredStreamConn) Write(payload []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	if c.openErr != nil {
		err := c.openErr
		c.mu.Unlock()
		return 0, err
	}
	if c.stream != nil {
		stream := c.stream
		c.mu.Unlock()
		return stream.Write(payload)
	}
	if len(payload) > firstFlightMaximumBytes-len(c.pending) {
		stream, err := c.openLocked("")
		c.mu.Unlock()
		if err != nil {
			return 0, err
		}
		return stream.Write(payload)
	}
	c.pending = append(c.pending, payload...)
	domain, decision := inspectFirstFlight(c.pending)
	if decision == firstFlightNeedMore && len(c.pending) < firstFlightMaximumBytes {
		c.mu.Unlock()
		return len(payload), nil
	}
	if decision != firstFlightUseDomain {
		domain = ""
	}
	_, err := c.openLocked(domain)
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (c *deferredStreamConn) Close() error {
	c.lifecycleCancel()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.signalReadyLocked()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	stream := c.stream
	c.pending = nil
	c.mu.Unlock()
	if stream != nil {
		return stream.Close()
	}
	return nil
}

func (c *deferredStreamConn) CloseWrite() error {
	stream, err := c.requireStream("")
	if err != nil {
		return err
	}
	return stream.CloseWrite()
}

func (c *deferredStreamConn) CloseRead() error {
	c.mu.Lock()
	stream := c.stream
	c.mu.Unlock()
	if stream != nil {
		return stream.CloseRead()
	}
	return nil
}

func (c *deferredStreamConn) LocalAddr() net.Addr  { return c.local }
func (c *deferredStreamConn) RemoteAddr() net.Addr { return c.remote }

func (c *deferredStreamConn) SetDeadline(deadline time.Time) error {
	stream, err := c.requireStream("")
	if err != nil {
		return err
	}
	return stream.SetDeadline(deadline)
}

func (c *deferredStreamConn) SetReadDeadline(deadline time.Time) error {
	stream, err := c.requireStream("")
	if err != nil {
		return err
	}
	return stream.SetReadDeadline(deadline)
}

func (c *deferredStreamConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	stream := c.stream
	c.mu.Unlock()
	if stream != nil {
		return stream.SetWriteDeadline(deadline)
	}
	return nil
}

func (c *deferredStreamConn) requireStream(domain string) (*streamConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.openLocked(domain)
}

func (c *deferredStreamConn) openLocked(domain string) (*streamConn, error) {
	if c.closed {
		c.signalReadyLocked()
		return nil, net.ErrClosed
	}
	if c.stream != nil {
		return c.stream, nil
	}
	if c.openErr != nil {
		c.signalReadyLocked()
		return nil, c.openErr
	}
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	target := c.target
	if domain != "" {
		target.Host = domain
	}
	c.decisionOnce.Do(func() {
		if c.observeDecision != nil {
			c.observeDecision(domain != "")
		}
	})
	metadata, err := np2proxy.EncodeTarget(target)
	if err != nil {
		c.openErr = errors.Join(ErrInvalidMetadata, err)
		c.signalReadyLocked()
		return nil, c.openErr
	}
	ctx, cancel := context.WithTimeout(c.lifecycleContext, tcpOpenTimeout)
	stream, err := c.opener(ctx, metadata)
	cancel()
	if err != nil {
		c.openErr = err
		c.signalReadyLocked()
		return nil, err
	}
	connection := &streamConn{stream: stream, local: c.local, remote: c.remote}
	if len(c.pending) > 0 {
		if _, err := io.Copy(stream, bytes.NewReader(c.pending)); err != nil {
			_ = connection.Close()
			c.openErr = err
			c.pending = nil
			c.signalReadyLocked()
			return nil, err
		}
	}
	c.pending = nil
	c.stream = connection
	c.signalReadyLocked()
	return connection, nil
}

func (c *deferredStreamConn) openNumericAfterTimeout() {
	c.mu.Lock()
	_, _ = c.openLocked("")
	c.mu.Unlock()
}

func (c *deferredStreamConn) signalReadyLocked() {
	c.readyOnce.Do(func() { close(c.ready) })
}
