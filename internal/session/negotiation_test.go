package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

func TestExtensionNegotiationSelectsBoundedSubset(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	offer := productionExtensionParameters()
	request := offer
	request.Capabilities &^= protocol.CapabilityCarrierMigration
	request.MaxUDPAssociations = 64
	request.MaxUDPPayload = 4096
	request.MaxSessionReceiveBytes = 32 * 1024 * 1024
	request.MaxStreamWindowBytes = 4 * 1024 * 1024

	serverResult := make(chan protocol.ExtensionParameters, 1)
	serverErr := make(chan error, 1)
	go func() {
		selected, err := NegotiateServerExtensions(ctx, server, offer, 1)
		serverResult <- selected
		serverErr <- err
	}()
	selected, err := NegotiateClientExtensions(
		ctx, client, request,
		protocol.CapabilityReliableUDP|protocol.CapabilityAdaptiveWindow,
	)
	if err != nil {
		t.Fatalf("client negotiation: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server negotiation: %v", err)
	}
	serverSelected := <-serverResult
	if selected != serverSelected || selected != request {
		t.Fatalf("selected mismatch: client=%+v server=%+v want=%+v", selected, serverSelected, request)
	}
}

func TestExtensionNegotiationRejectsMissingRequiredCapability(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	offer := productionExtensionParameters()
	offer.Capabilities &^= protocol.CapabilityUnreliableDatagrams
	offer.UnreliableDatagramSize = 0
	serverDone := make(chan error, 1)
	go func() {
		_, err := NegotiateServerExtensions(ctx, server, offer, 1)
		serverDone <- err
	}()
	_, err := NegotiateClientExtensions(
		ctx, client, productionExtensionParameters(),
		protocol.CapabilityReliableUDP|protocol.CapabilityUnreliableDatagrams,
	)
	if !errors.Is(err, ErrRequiredExtension) {
		t.Fatalf("required capability error=%v", err)
	}
	cancel()
	<-serverDone
}

func productionExtensionParameters() protocol.ExtensionParameters {
	return protocol.ExtensionParameters{
		Capabilities: protocol.CapabilityReliableUDP |
			protocol.CapabilityUnreliableDatagrams |
			protocol.CapabilityAdaptiveWindow |
			protocol.CapabilityCarrierMigration,
		MaxUDPAssociations:     256,
		MaxUDPPayload:          65507,
		UDPIdleTimeoutMS:       60_000,
		MaxSessionReceiveBytes: 64 * 1024 * 1024,
		MaxStreamWindowBytes:   8 * 1024 * 1024,
		UnreliableDatagramSize: 1200,
	}
}
