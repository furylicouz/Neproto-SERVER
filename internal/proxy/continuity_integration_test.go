package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func TestContinuityRuntimeMigratesTCPFlowWithoutSecondTargetDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := NewContinuityRuntime(ContinuityRuntimeConfig{
		Context: ctx, MaxFlows: 8, MaxFlowsPerPrincipal: 4,
		JournalBytes: MinContinuityJournalBytes, AckEveryBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	principal := continuity.PrincipalID{1, 2, 3}
	constellationID := protocol.ContinuityID{17, 18, 19}
	flowID := protocol.ContinuityID{33, 34, 35}
	firstLeaseKey := protocol.ContinuityID{49, 50, 51}
	targetServer, targetPeer := net.Pipe()
	t.Cleanup(func() { _ = targetPeer.Close() })
	var routeCalls atomic.Int64
	routeTCP := func(_ context.Context, userID string, target Target) (DuplexStream, bool, error) {
		if target.Host != "127.0.0.1" || target.Port != 443 {
			t.Fatalf("routed target=%+v", target)
		}
		routeCalls.Add(1)
		return &testNetDuplex{Conn: targetServer}, true, nil
	}

	firstClientMux, firstServerMux := newProxyMuxPair(t)
	firstClientControl := newProxyControlChannel(t, ctx, firstClientMux, constellationID, 3)
	firstServerControl := newProxyControlChannel(t, ctx, firstServerMux, constellationID, 3)
	firstLease := ContinuityLease{
		Principal: principal, ConstellationID: constellationID, LeaseKey: firstLeaseKey,
		Control: firstServerControl, Mux: firstServerMux,
	}
	firstServe := make(chan error, 1)
	go func() {
		firstServe <- (Server{
			Mux: firstServerMux, Policy: DestinationPolicy{AllowPrivate: true}, RouteTCP: routeTCP,
			Continuity: runtime, ContinuityLease: firstLease,
		}).Serve(ctx)
	}()
	targetMetadata, err := EncodeOpenRequest(OpenRequest{
		Command: CommandTCPConnect, Target: Target{Host: "127.0.0.1", Port: 443},
	})
	if err != nil {
		t.Fatal(err)
	}
	newMetadata := protocol.ContinuityOpenMetadata{
		Mode: protocol.ContinuityOpenNew, ConstellationID: constellationID,
		FlowID: flowID, LeaseKey: firstLeaseKey, Epoch: 1, Inner: targetMetadata,
	}
	firstPhysical := openContinuityTestStream(t, ctx, firstClientMux, newMetadata)
	firstTransport, err := session.NewTransportStream(firstClientMux, firstPhysical)
	if err != nil {
		t.Fatal(err)
	}
	controlReference := &testControlReference{control: firstClientControl}
	clientStream, err := continuity.NewResumableStream(continuity.ResumableStreamConfig{
		Context: ctx, Initial: firstTransport, JournalBytes: MinContinuityJournalBytes,
		AckEveryBytes: 1,
		OnReceiveOffset: func(offset uint64) error {
			return controlReference.sendAck(ctx, flowID, offset)
		},
		RecoverableReadError:  recoverableTransportError,
		RecoverableWriteError: recoverableTransportError,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientStream.Close() })
	clientHandler := &testClientFlowHandler{stream: clientStream}
	if err := firstClientControl.Register(flowID, clientHandler); err != nil {
		t.Fatal(err)
	}

	if _, err := clientStream.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	assertReadExact(t, ctx, targetPeer, "one")
	waitForContinuityCondition(t, ctx, func() bool {
		return clientStream.Offsets().SendBase == 3
	})
	if _, err := targetPeer.Write([]byte("reply")); err != nil {
		t.Fatal(err)
	}
	assertReadExact(t, ctx, clientStream, "reply")

	_ = firstClientMux.Close()
	_ = firstServerMux.Close()
	waitForContinuityCondition(t, ctx, func() bool { return runtime.Count() == 1 })
	pendingWrite := make(chan error, 1)
	go func() {
		_, writeErr := clientStream.Write([]byte("two"))
		pendingWrite <- writeErr
	}()

	secondClientMux, secondServerMux := newProxyMuxPair(t)
	secondClientControl := newProxyControlChannel(t, ctx, secondClientMux, constellationID, 3)
	secondServerControl := newProxyControlChannel(t, ctx, secondServerMux, constellationID, 3)
	secondLeaseKey := protocol.ContinuityID{65, 66, 67}
	secondLease := ContinuityLease{
		Principal: principal, ConstellationID: constellationID, LeaseKey: secondLeaseKey,
		Control: secondServerControl, Mux: secondServerMux,
	}
	secondServe := make(chan error, 1)
	go func() {
		secondServe <- (Server{
			Mux: secondServerMux, Policy: DestinationPolicy{AllowPrivate: true}, RouteTCP: routeTCP,
			Continuity: runtime, ContinuityLease: secondLease,
		}).Serve(ctx)
	}()
	offsets := clientStream.Offsets()
	resumeMetadata := protocol.ContinuityOpenMetadata{
		Mode: protocol.ContinuityOpenResume, ConstellationID: constellationID,
		FlowID: flowID, LeaseKey: secondLeaseKey, Epoch: 2,
		SendOffset: offsets.SendBase, ReceiveOffset: offsets.Receive,
	}
	secondPhysical := openContinuityTestStream(t, ctx, secondClientMux, resumeMetadata)
	if err := secondClientControl.Register(flowID, clientHandler); err != nil {
		t.Fatal(err)
	}
	controlReference.set(secondClientControl)
	secondTransport, err := session.NewTransportStream(secondClientMux, secondPhysical)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientStream.Replace(secondTransport, continuity.ResumeState{
		PeerReceiveOffset: offsets.SendBase, ReceiveOffset: offsets.Receive,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-pendingWrite:
		if err != nil {
			t.Fatalf("pending write: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertReadExact(t, ctx, targetPeer, "two")
	if routeCalls.Load() != 1 {
		t.Fatalf("cluster route selected %d times", routeCalls.Load())
	}
	if _, err := targetPeer.Write([]byte("again")); err != nil {
		t.Fatal(err)
	}
	assertReadExact(t, ctx, clientStream, "again")
	if runtime.Count() != 1 {
		t.Fatalf("active continuity flows=%d", runtime.Count())
	}

	_ = clientStream.Close()
	_ = secondClientMux.Close()
	_ = secondServerMux.Close()
	_ = firstClientControl.Close()
	_ = firstServerControl.Close()
	_ = secondClientControl.Close()
	_ = secondServerControl.Close()
	select {
	case <-firstServe:
	case <-time.After(time.Second):
	}
	select {
	case <-secondServe:
	case <-time.After(time.Second):
	}
}

type testNetDuplex struct{ net.Conn }

func (*testNetDuplex) CloseWrite() error { return nil }

func TestContinuityRuntimeExpiresFlowWhenReplacementNeverArrives(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runtime, err := NewContinuityRuntime(ContinuityRuntimeConfig{
		Context: ctx, MaxFlows: 2, MaxFlowsPerPrincipal: 2,
		JournalBytes: MinContinuityJournalBytes, AckEveryBytes: 1,
		MigrationTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	clientMux, serverMux := newProxyMuxPair(t)
	constellationID := protocol.ContinuityID{1, 2, 3}
	leaseKey := protocol.ContinuityID{4, 5, 6}
	clientControl := newProxyControlChannel(t, ctx, clientMux, constellationID, 3)
	serverControl := newProxyControlChannel(t, ctx, serverMux, constellationID, 3)
	targetServer, targetPeer := net.Pipe()
	defer targetPeer.Close()
	dialer := &singleConnectionDialer{connection: targetServer}
	go func() {
		_ = (Server{
			Mux: serverMux, Policy: DestinationPolicy{AllowPrivate: true}, Dialer: dialer,
			Continuity: runtime,
			ContinuityLease: ContinuityLease{
				Principal: continuity.PrincipalID{1}, ConstellationID: constellationID,
				LeaseKey: leaseKey, Control: serverControl, Mux: serverMux,
			},
		}).Serve(ctx)
	}()
	inner, err := EncodeOpenRequest(OpenRequest{
		Command: CommandTCPConnect, Target: Target{Host: "127.0.0.1", Port: 443},
	})
	if err != nil {
		t.Fatal(err)
	}
	physical := openContinuityTestStream(t, ctx, clientMux, protocol.ContinuityOpenMetadata{
		Mode: protocol.ContinuityOpenNew, ConstellationID: constellationID,
		FlowID: protocol.ContinuityID{7, 8, 9}, LeaseKey: leaseKey, Epoch: 1, Inner: inner,
	})
	defer physical.Close()
	waitForContinuityCondition(t, ctx, func() bool { return runtime.Count() == 1 })
	_ = clientMux.Close()
	_ = serverMux.Close()
	waitForContinuityCondition(t, ctx, func() bool { return runtime.Count() == 0 })
	if dialer.Calls() != 1 {
		t.Fatalf("target dial calls=%d", dialer.Calls())
	}
	_ = targetPeer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := targetPeer.Read(make([]byte, 1)); err == nil {
		t.Fatal("expired flow left target open")
	}
	_ = clientControl.Close()
	_ = serverControl.Close()
}

func newProxyControlChannel(
	t *testing.T,
	ctx context.Context,
	mux *session.Mux,
	constellationID protocol.ContinuityID,
	firstMessageID uint64,
) *constellation.ControlChannel {
	t.Helper()
	channel, err := constellation.NewControlChannel(ctx, constellation.ControlChannelConfig{
		Mux: mux, ConstellationID: constellationID,
		FirstMessageID: firstMessageID, MaxFlows: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

func openContinuityTestStream(
	t *testing.T,
	ctx context.Context,
	mux *session.Mux,
	metadata protocol.ContinuityOpenMetadata,
) *session.Stream {
	t.Helper()
	raw, err := metadata.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	stream, err := mux.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func assertReadExact(t *testing.T, ctx context.Context, reader io.Reader, expected string) {
	t.Helper()
	result := make(chan struct {
		payload string
		err     error
	}, 1)
	go func() {
		buffer := make([]byte, len(expected))
		_, err := io.ReadFull(reader, buffer)
		result <- struct {
			payload string
			err     error
		}{payload: string(buffer), err: err}
	}()
	select {
	case actual := <-result:
		if actual.err != nil || actual.payload != expected {
			t.Fatalf("read payload=%q expected=%q err=%v", actual.payload, expected, actual.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func waitForContinuityCondition(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

type singleConnectionDialer struct {
	mu         sync.Mutex
	connection net.Conn
	calls      int
}

func (d *singleConnectionDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.connection == nil {
		return nil, errors.New("target connection already consumed")
	}
	connection := d.connection
	d.connection = nil
	return connection, nil
}

func (d *singleConnectionDialer) Calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

type testControlReference struct {
	mu      sync.Mutex
	control *constellation.ControlChannel
}

func (r *testControlReference) set(control *constellation.ControlChannel) {
	r.mu.Lock()
	r.control = control
	r.mu.Unlock()
}

func (r *testControlReference) sendAck(
	ctx context.Context,
	flowID protocol.ContinuityID,
	offset uint64,
) error {
	r.mu.Lock()
	control := r.control
	r.mu.Unlock()
	if control == nil {
		return constellation.ErrControlChannelClosed
	}
	return control.SendAck(ctx, flowID, offset)
}

type testClientFlowHandler struct {
	stream *continuity.ResumableStream
}

func (h *testClientFlowHandler) Acknowledge(offset uint64) error {
	return h.stream.Ack(offset)
}

func (h *testClientFlowHandler) Abort(err error) {
	if errors.Is(err, constellation.ErrControlChannelClosed) {
		return
	}
	_ = h.stream.Close()
}
