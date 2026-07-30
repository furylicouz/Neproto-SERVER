package session

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

func TestMuxCarriesContinuityControlInsideProtectedExtension(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	frame := protocol.ContinuityFrame{
		Type: protocol.ContinuityLeaseIssue, MessageID: 2,
		ConstellationID: sessionContinuityID(1),
		Token:           bytes.Repeat([]byte{7}, protocol.MinContinuityTokenSize),
	}
	if err := server.SendContinuity(ctx, frame); err != nil {
		t.Fatalf("send continuity: %v", err)
	}
	received, err := client.ReceiveContinuity(ctx)
	if err != nil {
		t.Fatalf("receive continuity: %v", err)
	}
	if received.Type != frame.Type || received.MessageID != frame.MessageID ||
		received.ConstellationID != frame.ConstellationID || !bytes.Equal(received.Token, frame.Token) {
		t.Fatalf("received=%+v want=%+v", received, frame)
	}
}

func TestReceiveContinuityRejectsNonUpdateEnvelope(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	frame := protocol.ContinuityFrame{
		Type: protocol.ContinuityLeaseIssue, MessageID: 2,
		ConstellationID: sessionContinuityID(1),
		Token:           bytes.Repeat([]byte{7}, protocol.MinContinuityTokenSize),
	}
	tlv, err := protocol.NewContinuityTLV(frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SendExtension(context.Background(), protocol.ExtensionEnvelope{
		Type: protocol.ExtensionOffer, MessageID: frame.MessageID, TLVs: []protocol.ExtensionTLV{tlv},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReceiveContinuity(context.Background()); !errors.Is(err, ErrContinuityControl) {
		t.Fatalf("non-update error=%v", err)
	}
}

func TestReceiveContinuityRejectsMismatchedMessageIDAndExtraTLV(t *testing.T) {
	tests := []struct {
		name     string
		envelope protocol.ExtensionEnvelope
	}{
		{
			name: "message ID mismatch",
			envelope: continuityTestEnvelope(t, 3, protocol.ContinuityFrame{
				Type: protocol.ContinuityFlowAck, MessageID: 2,
				ConstellationID: sessionContinuityID(1), FlowID: sessionContinuityID(17),
			}),
		},
		{
			name: "extra TLV",
			envelope: func() protocol.ExtensionEnvelope {
				envelope := continuityTestEnvelope(t, 2, protocol.ContinuityFrame{
					Type: protocol.ContinuityFlowAck, MessageID: 2,
					ConstellationID: sessionContinuityID(1), FlowID: sessionContinuityID(17),
				})
				envelope.TLVs = append(envelope.TLVs, protocol.ExtensionTLV{Type: 9, Value: []byte{1}})
				return envelope
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, server, _ := newTestMuxPair(t, 64*1024, 8)
			t.Cleanup(func() {
				_ = client.Close()
				_ = server.Close()
			})
			if err := server.SendExtension(context.Background(), test.envelope); err != nil {
				t.Fatal(err)
			}
			if _, err := client.ReceiveContinuity(context.Background()); !errors.Is(err, ErrContinuityControl) {
				t.Fatalf("control error=%v", err)
			}
		})
	}
}

func continuityTestEnvelope(
	t *testing.T,
	messageID uint64,
	frame protocol.ContinuityFrame,
) protocol.ExtensionEnvelope {
	t.Helper()
	tlv, err := protocol.NewContinuityTLV(frame)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.ExtensionEnvelope{
		Type: protocol.ExtensionUpdate, MessageID: messageID,
		TLVs: []protocol.ExtensionTLV{tlv},
	}
}

func sessionContinuityID(seed byte) protocol.ContinuityID {
	var id protocol.ContinuityID
	for index := range id {
		id[index] = seed + byte(index)
	}
	return id
}
