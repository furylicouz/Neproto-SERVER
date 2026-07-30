package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"neproto.local/chameleon/internal/protocol"
)

const (
	maxQueuedExtensionMessages = 16
	maxRememberedExtensionIDs  = 64
)

func (m *Mux) SendExtension(ctx context.Context, envelope protocol.ExtensionEnvelope) error {
	if m == nil || ctx == nil {
		return ErrInvalidConfig
	}
	payload, err := envelope.MarshalBinary()
	if err != nil {
		return err
	}
	return m.send(ctx, protocol.Cell{
		Kind: protocol.CellProfile, Sequence: envelope.MessageID, Payload: payload,
	})
}

func (m *Mux) ReceiveExtension(ctx context.Context) (protocol.ExtensionEnvelope, error) {
	if m == nil || ctx == nil {
		return protocol.ExtensionEnvelope{}, ErrInvalidConfig
	}
	select {
	case envelope := <-m.extensions:
		return envelope, nil
	case <-ctx.Done():
		return protocol.ExtensionEnvelope{}, ctx.Err()
	case <-m.done:
		return protocol.ExtensionEnvelope{}, m.sessionError()
	}
}

func (m *Mux) handleExtensionPayload(payload []byte) error {
	if !protocol.IsExtensionEnvelope(payload) {
		return nil
	}
	envelope, err := protocol.ParseExtensionEnvelope(payload)
	if err != nil {
		return fmt.Errorf("%w: extension envelope: %v", ErrProtocol, err)
	}
	digest := sha256.Sum256(payload)

	m.extensionMu.Lock()
	if previous, exists := m.extensionSeen[envelope.MessageID]; exists {
		m.extensionMu.Unlock()
		if bytes.Equal(previous[:], digest[:]) {
			return nil
		}
		return fmt.Errorf("%w: conflicting extension message id", ErrProtocol)
	}
	if envelope.MessageID <= m.highestExtensionID {
		m.extensionMu.Unlock()
		return nil
	}
	if len(m.extensionOrder) == maxRememberedExtensionIDs {
		oldest := m.extensionOrder[0]
		m.extensionOrder[0] = 0
		m.extensionOrder = m.extensionOrder[1:]
		delete(m.extensionSeen, oldest)
	}
	m.extensionSeen[envelope.MessageID] = digest
	m.extensionOrder = append(m.extensionOrder, envelope.MessageID)
	m.highestExtensionID = envelope.MessageID
	m.extensionMu.Unlock()

	select {
	case m.extensions <- envelope:
		return nil
	case <-m.done:
		return m.sessionError()
	default:
		return fmt.Errorf("%w: extension receive queue full", ErrProtocol)
	}
}
