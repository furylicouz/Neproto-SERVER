package httpsws

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/coder/websocket"
	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

var (
	ErrUnexpectedMessage = errors.New("https carrier requires binary websocket messages")
	ErrMessageTooLarge   = errors.New("https carrier message too large")
)

type Conn struct {
	websocket *websocket.Conn
	readMu    sync.Mutex
	writeMu   sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

var _ carrier.Carrier = (*Conn)(nil)

func newConn(connection *websocket.Conn) *Conn {
	connection.SetReadLimit(protocol.MaxCellSize)
	return &Conn{websocket: connection}
}

func (c *Conn) Send(ctx context.Context, raw []byte) error {
	if len(raw) > protocol.MaxCellSize {
		return ErrMessageTooLarge
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.websocket.Write(ctx, websocket.MessageBinary, raw)
}

func (c *Conn) Receive(ctx context.Context) ([]byte, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	messageType, raw, err := c.websocket.Read(ctx)
	if err != nil {
		if errors.Is(err, websocket.ErrMessageTooBig) {
			return nil, ErrMessageTooLarge
		}
		switch websocket.CloseStatus(err) {
		case websocket.StatusNormalClosure, websocket.StatusGoingAway:
			return nil, io.EOF
		default:
			return nil, err
		}
	}
	if messageType != websocket.MessageBinary {
		_ = c.websocket.CloseNow()
		return nil, ErrUnexpectedMessage
	}
	if len(raw) > protocol.MaxCellSize {
		return nil, ErrMessageTooLarge
	}
	return raw, nil
}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.websocket.CloseNow()
	})
	return c.closeErr
}

func (c *Conn) Kind() protocol.CarrierKind {
	return protocol.CarrierHTTPS
}
