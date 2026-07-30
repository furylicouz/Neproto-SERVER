package session

import (
	"context"
	"errors"

	"neproto.local/chameleon/internal/protocol"
)

var ErrContinuityControl = errors.New("invalid continuity control message")

// SendContinuity carries one canonical NP/2 continuity message inside the
// existing authenticated extension control channel. Callers must negotiate
// CapabilityConstellationContinuity before using it.
func (m *Mux) SendContinuity(ctx context.Context, frame protocol.ContinuityFrame) error {
	if m == nil || ctx == nil {
		return ErrInvalidConfig
	}
	tlv, err := protocol.NewContinuityTLV(frame)
	if err != nil {
		return errors.Join(ErrContinuityControl, err)
	}
	return m.SendExtension(ctx, protocol.ExtensionEnvelope{
		Type: protocol.ExtensionUpdate, MessageID: frame.MessageID,
		TLVs: []protocol.ExtensionTLV{tlv},
	})
}

// ReceiveContinuity accepts only a single continuity TLV in an extension
// update. Matching inner and outer IDs bind replay handling to the existing
// extension message-ID rules.
func (m *Mux) ReceiveContinuity(ctx context.Context) (protocol.ContinuityFrame, error) {
	if m == nil || ctx == nil {
		return protocol.ContinuityFrame{}, ErrInvalidConfig
	}
	envelope, err := m.ReceiveExtension(ctx)
	if err != nil {
		return protocol.ContinuityFrame{}, err
	}
	if envelope.Type != protocol.ExtensionUpdate || len(envelope.TLVs) != 1 ||
		envelope.TLVs[0].Type != protocol.ExtensionTLVContinuity {
		return protocol.ContinuityFrame{}, ErrContinuityControl
	}
	frame, err := protocol.ParseContinuityTLV(envelope.TLVs[0])
	if err != nil || frame.MessageID != envelope.MessageID {
		return protocol.ContinuityFrame{}, errors.Join(ErrContinuityControl, err)
	}
	return frame, nil
}
