package carrier

import (
	"context"

	"neproto.local/chameleon/internal/protocol"
)

type Carrier interface {
	Send(context.Context, []byte) error
	Receive(context.Context) ([]byte, error)
	Close() error
	Kind() protocol.CarrierKind
}

// DatagramCarrier is implemented by carriers that can preserve unreliable
// message boundaries without imposing reliable-stream head-of-line blocking.
// Callers must fall back to Carrier.Send when a payload exceeds
// MaxDatagramPayload or SendDatagram reports that the current path is smaller.
type DatagramCarrier interface {
	Carrier
	SendDatagram(context.Context, []byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
	MaxDatagramPayload() int
}
