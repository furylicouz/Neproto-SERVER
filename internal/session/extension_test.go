package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

func TestMuxCarriesProtectedExtensionEnvelope(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	offer := protocol.ExtensionEnvelope{
		Type: protocol.ExtensionOffer, MessageID: 1,
		TLVs: []protocol.ExtensionTLV{
			protocol.NewExtensionCapabilitiesTLV(
				protocol.CapabilityReliableUDP | protocol.CapabilityAdaptiveWindow,
			),
		},
	}
	if err := server.SendExtension(ctx, offer); err != nil {
		t.Fatalf("send offer: %v", err)
	}
	received, err := client.ReceiveExtension(ctx)
	if err != nil {
		t.Fatalf("receive offer: %v", err)
	}
	if received.Type != offer.Type || received.MessageID != offer.MessageID || len(received.TLVs) != 1 {
		t.Fatalf("received offer mismatch: %+v", received)
	}
}

func TestMuxIgnoresLegacyProfilePayload(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	if err := server.send(context.Background(), protocol.Cell{
		Kind: protocol.CellProfile, Payload: []byte("legacy-profile-payload"),
	}); err != nil {
		t.Fatalf("send legacy profile: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := client.ReceiveExtension(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("legacy payload reached extension consumer: %v", err)
	}
	if stats := client.Stats(); stats.ProtocolErrors != 0 {
		t.Fatalf("legacy profile caused protocol error: %+v", stats)
	}
}

func TestMuxIgnoresIdenticalExtensionReplay(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	envelope := protocol.ExtensionEnvelope{
		Type: protocol.ExtensionOffer, MessageID: 5,
		TLVs: []protocol.ExtensionTLV{
			protocol.NewExtensionCapabilitiesTLV(protocol.CapabilityReliableUDP),
		},
	}
	if err := server.SendExtension(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	if err := server.SendExtension(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.ReceiveExtension(ctx); err != nil {
		t.Fatalf("receive first offer: %v", err)
	}
	short, cancelShort := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelShort()
	if _, err := client.ReceiveExtension(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("replayed offer was delivered: %v", err)
	}
}

func TestMuxRejectsConflictingExtensionMessageIDReuse(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	first := protocol.ExtensionEnvelope{
		Type: protocol.ExtensionOffer, MessageID: 9,
		TLVs: []protocol.ExtensionTLV{
			protocol.NewExtensionCapabilitiesTLV(protocol.CapabilityReliableUDP),
		},
	}
	conflict := protocol.ExtensionEnvelope{
		Type: protocol.ExtensionOffer, MessageID: 9,
		TLVs: []protocol.ExtensionTLV{
			protocol.NewExtensionCapabilitiesTLV(protocol.CapabilityAdaptiveWindow),
		},
	}
	if err := server.SendExtension(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReceiveExtension(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := server.SendExtension(context.Background(), conflict); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Wait(ctx); !errors.Is(err, ErrProtocol) {
		t.Fatalf("expected protocol error, got %v", err)
	}
}
