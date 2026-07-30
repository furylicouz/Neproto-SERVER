package constellation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func TestControlChannelDispatchesAcknowledgementsAndAbort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientMux, serverMux := newControlChannelMuxPair(t)
	client := newTestControlChannel(t, ctx, clientMux, 3, 4)
	server := newTestControlChannel(t, ctx, serverMux, 3, 4)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		_ = clientMux.Close()
		_ = serverMux.Close()
	})
	flowID := controllerID(17)
	serverHandler := newControlHandler()
	if err := server.Register(flowID, serverHandler); err != nil {
		t.Fatal(err)
	}
	if err := client.SendAck(ctx, flowID, 4096); err != nil {
		t.Fatal(err)
	}
	if offset := serverHandler.waitAck(t, ctx); offset != 4096 {
		t.Fatalf("ack offset=%d", offset)
	}
	if err := client.SendAbort(ctx, flowID); err != nil {
		t.Fatal(err)
	}
	if abortErr := serverHandler.waitAbort(t, ctx); !errors.Is(abortErr, ErrControlFlowAborted) {
		t.Fatalf("abort error=%v", abortErr)
	}
}

func TestControlChannelBadAckAbortsOnlyAffectedFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientMux, serverMux := newControlChannelMuxPair(t)
	client := newTestControlChannel(t, ctx, clientMux, 3, 4)
	server := newTestControlChannel(t, ctx, serverMux, 3, 4)
	defer client.Close()
	defer server.Close()
	defer clientMux.Close()
	defer serverMux.Close()

	badID := controllerID(17)
	badServer := newControlHandler()
	badServer.ackErr = errors.New("future acknowledgement")
	badClient := newControlHandler()
	goodID := controllerID(18)
	goodServer := newControlHandler()
	if err := server.Register(badID, badServer); err != nil {
		t.Fatal(err)
	}
	if err := client.Register(badID, badClient); err != nil {
		t.Fatal(err)
	}
	if err := server.Register(goodID, goodServer); err != nil {
		t.Fatal(err)
	}
	if err := client.SendAck(ctx, badID, 99); err != nil {
		t.Fatal(err)
	}
	if abortErr := badClient.waitAbort(t, ctx); !errors.Is(abortErr, ErrControlFlowAborted) {
		t.Fatalf("client abort=%v", abortErr)
	}
	if err := client.SendAck(ctx, goodID, 7); err != nil {
		t.Fatalf("channel died after one flow conflict: %v", err)
	}
	if offset := goodServer.waitAck(t, ctx); offset != 7 {
		t.Fatalf("good ack=%d", offset)
	}
}

func TestControlChannelEnforcesRegistrationBounds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	left, right := newControlChannelMuxPair(t)
	channel := newTestControlChannel(t, ctx, left, 3, 1)
	defer channel.Close()
	defer left.Close()
	defer right.Close()
	handler := newControlHandler()
	if err := channel.Register(controllerID(17), handler); err != nil {
		t.Fatal(err)
	}
	if err := channel.Register(controllerID(17), handler); !errors.Is(err, ErrControlFlowDuplicate) {
		t.Fatalf("duplicate error=%v", err)
	}
	if err := channel.Register(controllerID(18), newControlHandler()); !errors.Is(err, ErrControlFlowCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
	if !channel.Unregister(controllerID(17)) || channel.Unregister(controllerID(17)) {
		t.Fatal("unregister state mismatch")
	}
	if err := channel.Register(controllerID(18), newControlHandler()); err != nil {
		t.Fatalf("register after release: %v", err)
	}
}

func TestControlChannelCloseAbortsHandlersAndRejectsSends(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	left, right := newControlChannelMuxPair(t)
	channel := newTestControlChannel(t, ctx, left, 3, 2)
	handler := newControlHandler()
	if err := channel.Register(controllerID(17), handler); err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	if abortErr := handler.waitAbort(t, context.Background()); !errors.Is(abortErr, ErrControlChannelClosed) {
		t.Fatalf("close abort=%v", abortErr)
	}
	if err := channel.SendAck(context.Background(), controllerID(17), 1); !errors.Is(err, ErrControlChannelClosed) {
		t.Fatalf("send after close error=%v", err)
	}
	_ = left.Close()
	_ = right.Close()
}

func newTestControlChannel(
	t *testing.T,
	ctx context.Context,
	mux *session.Mux,
	firstMessageID uint64,
	maxFlows int,
) *ControlChannel {
	t.Helper()
	channel, err := NewControlChannel(ctx, ControlChannelConfig{
		Mux: mux, ConstellationID: controllerID(1),
		FirstMessageID: firstMessageID, MaxFlows: maxFlows,
	})
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

func newControlChannelMuxPair(t *testing.T) (*session.Mux, *session.Mux) {
	t.Helper()
	leftCarrier, rightCarrier := newControlMemoryCarrierPair()
	typeMap, err := protocol.NewTypeMap([32]byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	left, err := session.New(session.Config{
		Role: session.RoleClient, Carrier: leftCarrier, TypeMap: typeMap,
		InitialWindow: 64 * 1024, MaxStreams: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := session.New(session.Config{
		Role: session.RoleServer, Carrier: rightCarrier, TypeMap: typeMap,
		InitialWindow: 64 * 1024, MaxStreams: 8,
	})
	if err != nil {
		_ = left.Close()
		t.Fatal(err)
	}
	return left, right
}

type controlHandler struct {
	mu sync.Mutex

	ackErr error
	acks   chan uint64
	aborts chan error
}

func newControlHandler() *controlHandler {
	return &controlHandler{acks: make(chan uint64, 2), aborts: make(chan error, 2)}
}

func (h *controlHandler) Acknowledge(offset uint64) error {
	h.mu.Lock()
	err := h.ackErr
	h.mu.Unlock()
	if err != nil {
		return err
	}
	h.acks <- offset
	return nil
}

func (h *controlHandler) Abort(err error) {
	h.aborts <- err
}

func (h *controlHandler) waitAck(t *testing.T, ctx context.Context) uint64 {
	t.Helper()
	select {
	case offset := <-h.acks:
		return offset
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return 0
	}
}

func (h *controlHandler) waitAbort(t *testing.T, ctx context.Context) error {
	t.Helper()
	select {
	case err := <-h.aborts:
		return err
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return nil
	case <-time.After(time.Second):
		t.Fatal("abort not delivered")
		return nil
	}
}
