package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

func TestMuxCarriesMultipleConcurrentStreams(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 64)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const streamCount = 32
	serverErr := make(chan error, 1)
	go func() {
		var wait sync.WaitGroup
		for index := 0; index < streamCount; index++ {
			incoming, err := server.Accept(ctx)
			if err != nil {
				serverErr <- err
				return
			}
			wait.Add(1)
			go func(incoming *Incoming) {
				defer wait.Done()
				stream, err := incoming.Accept()
				if err != nil {
					serverErr <- err
					return
				}
				if _, err := io.Copy(stream, stream); err != nil {
					serverErr <- err
					return
				}
				if err := stream.CloseWrite(); err != nil {
					serverErr <- err
				}
			}(incoming)
		}
		wait.Wait()
		close(serverErr)
	}()

	clientErr := make(chan error, streamCount)
	var wait sync.WaitGroup
	for index := 0; index < streamCount; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			metadata := []byte{byte(index), 0xa5}
			stream, err := client.Open(ctx, metadata)
			if err != nil {
				clientErr <- err
				return
			}
			payload := bytes.Repeat([]byte{byte(index + 1)}, 16*1024+index)
			if _, err := stream.Write(payload); err != nil {
				clientErr <- err
				return
			}
			if err := stream.CloseWrite(); err != nil {
				clientErr <- err
				return
			}
			response, err := io.ReadAll(stream)
			if err != nil {
				clientErr <- err
				return
			}
			if !bytes.Equal(response, payload) {
				clientErr <- errors.New("echo payload mismatch")
			}
		}(index)
	}
	wait.Wait()
	close(clientErr)
	for err := range clientErr {
		t.Errorf("client stream: %v", err)
	}
	for err := range serverErr {
		t.Errorf("server stream: %v", err)
	}
}

func TestMuxPingProvesBidirectionalCarrierLiveness(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("client ping: %v", err)
	}
	if err := server.Ping(ctx); err != nil {
		t.Fatalf("server ping: %v", err)
	}
}

func TestMuxPingHonorsCancellationAndBoundsWaiters(t *testing.T) {
	client, server, wire := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	wire.blockReceive.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := client.Ping(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ping cancellation error=%v", err)
	}
	client.pingMu.Lock()
	pending := len(client.pendingPings)
	client.pingMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending pings leaked=%d", pending)
	}
}

func TestMuxPreservesOpenMetadata(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 4096, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wantMetadata := []byte("tcp|example.net|443")
	accepted := make(chan error, 1)
	go func() {
		incoming, err := server.Accept(ctx)
		if err != nil {
			accepted <- err
			return
		}
		if !bytes.Equal(incoming.Metadata(), wantMetadata) {
			accepted <- errors.New("open metadata mismatch")
			return
		}
		stream, err := incoming.Accept()
		if err == nil {
			err = stream.CloseWrite()
		}
		accepted <- err
	}()

	stream, err := client.Open(ctx, wantMetadata)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if _, err := io.ReadAll(stream); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := <-accepted; err != nil {
		t.Fatalf("accept stream: %v", err)
	}
}

func TestMuxFlowControlBlocksUntilPeerReads(t *testing.T) {
	const window = 64
	client, server, _ := newTestMuxPair(t, window, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	incomingCh := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		incomingCh <- incoming
	}()
	clientStreamCh := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("flow"))
		clientStreamCh <- stream
	}()
	incoming := <-incomingCh
	if incoming == nil {
		t.Fatal("server did not receive incoming stream")
	}
	serverStream, err := incoming.Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	clientStream := <-clientStreamCh
	if clientStream == nil {
		t.Fatal("client stream did not open")
	}

	payload := bytes.Repeat([]byte{0x5a}, window*8)
	writeDone := make(chan error, 1)
	go func() {
		_, err := clientStream.Write(payload)
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		t.Fatalf("write completed before peer read: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	readDone := make(chan error, 1)
	go func() {
		readPayload := make([]byte, len(payload))
		_, err := io.ReadFull(serverStream, readPayload)
		if err == nil && !bytes.Equal(readPayload, payload) {
			err = errors.New("flow-controlled payload mismatch")
		}
		readDone <- err
	}()
	if err := <-writeDone; err != nil {
		t.Fatalf("write after peer read: %v", err)
	}
	if err := <-readDone; err != nil {
		t.Fatalf("read flow-controlled payload: %v", err)
	}
}

func TestStreamCoalescesConsumedCreditIntoFewerWindowUpdates(t *testing.T) {
	const window = 256 * 1024
	left, right := newMemoryCarrierPair()
	t.Cleanup(func() { _ = left.Close() })
	typeMap := testTypeMap(t)
	mux := &Mux{
		carrier: left, typeMap: typeMap, initialWindow: window,
		ctx: context.Background(), done: make(chan struct{}),
	}
	stream := newStream(mux, 1, 0, window, false)
	stream.recvSequence = 1
	chunk := bytes.Repeat([]byte{0x6a}, 20*1024)

	if err := stream.handleCell(protocol.Cell{
		Kind: protocol.CellData, StreamID: 1, Sequence: 1, Payload: chunk,
	}); err != nil {
		t.Fatalf("handle first DATA: %v", err)
	}
	first := make([]byte, len(chunk))
	if _, err := io.ReadFull(stream, first); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	noUpdate, cancelNoUpdate := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelNoUpdate()
	if raw, err := right.Receive(noUpdate); err == nil {
		cell, decodeErr := protocol.DecodeCell(typeMap, raw)
		t.Fatalf("first small read sent an uncoalesced cell kind=%v decode=%v", cell.Kind, decodeErr)
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait for absent WINDOW_UPDATE: %v", err)
	}

	if err := stream.handleCell(protocol.Cell{
		Kind: protocol.CellData, StreamID: 1, Sequence: 2, Payload: chunk,
	}); err != nil {
		t.Fatalf("handle second DATA: %v", err)
	}
	second := make([]byte, len(chunk))
	if _, err := io.ReadFull(stream, second); err != nil {
		t.Fatalf("read second chunk: %v", err)
	}
	updateContext, cancelUpdate := context.WithTimeout(context.Background(), time.Second)
	defer cancelUpdate()
	raw, err := right.Receive(updateContext)
	if err != nil {
		t.Fatalf("receive coalesced WINDOW_UPDATE: %v", err)
	}
	cell, err := protocol.DecodeCell(typeMap, raw)
	if err != nil {
		t.Fatalf("decode coalesced WINDOW_UPDATE: %v", err)
	}
	credit, err := parseCredit(cell.Payload)
	if err != nil {
		t.Fatalf("parse coalesced credit: %v", err)
	}
	if cell.Kind != protocol.CellWindowUpdate || credit != 2*uint64(len(chunk)) {
		t.Fatalf("coalesced update kind=%v credit=%d", cell.Kind, credit)
	}
}

func TestStreamFlushesPendingCreditAfterFinalBufferedRead(t *testing.T) {
	const window = 2 * 1024 * 1024
	left, right := newMemoryCarrierPair()
	t.Cleanup(func() { _ = left.Close() })
	typeMap := testTypeMap(t)
	mux := &Mux{
		carrier: left, typeMap: typeMap, initialWindow: window,
		ctx: context.Background(), done: make(chan struct{}),
	}
	stream := newStream(mux, 1, 0, window, false)
	stream.recvSequence = 1
	chunk := bytes.Repeat([]byte{0x7b}, 20*1024)
	if err := stream.handleCell(protocol.Cell{
		Kind: protocol.CellData, StreamID: 1, Sequence: 1, Payload: chunk,
	}); err != nil {
		t.Fatalf("handle final DATA: %v", err)
	}
	if err := stream.handleCell(protocol.Cell{
		Kind: protocol.CellFin, StreamID: 1, Sequence: 2,
	}); err != nil {
		t.Fatalf("handle FIN: %v", err)
	}
	read := make([]byte, len(chunk))
	if _, err := io.ReadFull(stream, read); err != nil {
		t.Fatalf("read final chunk: %v", err)
	}
	updateContext, cancelUpdate := context.WithTimeout(context.Background(), time.Second)
	defer cancelUpdate()
	raw, err := right.Receive(updateContext)
	if err != nil {
		t.Fatalf("receive final WINDOW_UPDATE: %v", err)
	}
	cell, err := protocol.DecodeCell(typeMap, raw)
	if err != nil {
		t.Fatalf("decode final WINDOW_UPDATE: %v", err)
	}
	credit, err := parseCredit(cell.Payload)
	if err != nil {
		t.Fatalf("parse final credit: %v", err)
	}
	if cell.Kind != protocol.CellWindowUpdate || credit != uint64(len(chunk)) {
		t.Fatalf("final update kind=%v credit=%d", cell.Kind, credit)
	}
}

func TestMuxRejectsWindowCreditAboveNegotiatedLimit(t *testing.T) {
	client, server, clientCarrier := newTestMuxPair(t, 64, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	incomingCh := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		incomingCh <- incoming
	}()
	clientStreamCh := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("credit-limit"))
		clientStreamCh <- stream
	}()
	incoming := <-incomingCh
	serverStream, err := incoming.Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	if stream := <-clientStreamCh; stream == nil {
		t.Fatal("client stream did not open")
	}

	payload := binary.AppendUvarint(nil, 65)
	raw, err := protocol.EncodeCell(testTypeMap(t), protocol.Cell{
		Kind: protocol.CellWindowUpdate, StreamID: 1, Sequence: 1, Payload: payload,
	})
	if err != nil {
		t.Fatalf("encode invalid credit cell: %v", err)
	}
	if err := clientCarrier.Send(ctx, raw); err != nil {
		t.Fatalf("inject invalid credit cell: %v", err)
	}

	buffer := make([]byte, 1)
	_, err = serverStream.Read(buffer)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("expected protocol error, got %v", err)
	}
}

func TestMuxRejectsDuplicateOrRegressedSequence(t *testing.T) {
	client, server, clientCarrier := newTestMuxPair(t, 4096, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	incomingCh := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		incomingCh <- incoming
	}()
	clientStreamCh := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("sequence"))
		clientStreamCh <- stream
	}()
	incoming := <-incomingCh
	serverStream, err := incoming.Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	if stream := <-clientStreamCh; stream == nil {
		t.Fatal("client stream did not open")
	}

	typeMap := testTypeMap(t)
	raw, err := protocol.EncodeCell(typeMap, protocol.Cell{
		Kind:     protocol.CellData,
		StreamID: 1,
		Sequence: 99,
		Payload:  []byte("out-of-order"),
	})
	if err != nil {
		t.Fatalf("encode invalid sequence cell: %v", err)
	}
	if err := clientCarrier.Send(ctx, raw); err != nil {
		t.Fatalf("inject invalid sequence cell: %v", err)
	}

	buffer := make([]byte, 1)
	_, err = serverStream.Read(buffer)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("expected protocol error, got %v", err)
	}
}

func TestMuxEnforcesStreamLimit(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 4096, 1)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		incoming, _ := server.Accept(ctx)
		if incoming != nil {
			_, _ = incoming.Accept()
		}
	}()
	if _, err := client.Open(ctx, []byte("first")); err != nil {
		t.Fatalf("open first stream: %v", err)
	}
	if _, err := client.Open(ctx, []byte("second")); !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("expected stream limit error, got %v", err)
	}
}

func TestMuxPreservesRejectionCode(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 4096, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		incoming, _ := server.Accept(ctx)
		if incoming != nil {
			_ = incoming.Reject(5)
		}
	}()
	_, err := client.Open(ctx, []byte("rejected"))
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("expected rejection, got %v", err)
	}
	var rejection *RejectError
	if !errors.As(err, &rejection) || rejection.Code != 5 {
		t.Fatalf("expected rejection code 5, got %#v", rejection)
	}
}

func TestStreamCloseResetsOnlyThatStream(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 4096, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstIncoming := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		firstIncoming <- incoming
	}()
	firstClientCh := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("first"))
		firstClientCh <- stream
	}()
	firstServer, err := (<-firstIncoming).Accept()
	if err != nil {
		t.Fatalf("accept first stream: %v", err)
	}
	firstClient := <-firstClientCh
	if err := firstClient.Close(); err != nil {
		t.Fatalf("close first stream: %v", err)
	}
	if _, err := firstServer.Read(make([]byte, 1)); !errors.Is(err, ErrReset) {
		t.Fatalf("peer did not receive reset: %v", err)
	}

	secondIncoming := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		secondIncoming <- incoming
	}()
	secondClientCh := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("second"))
		secondClientCh <- stream
	}()
	secondServer, err := (<-secondIncoming).Accept()
	if err != nil {
		t.Fatalf("accept second stream: %v", err)
	}
	secondClient := <-secondClientCh
	if _, err := secondClient.Write([]byte("ok")); err != nil {
		t.Fatalf("second stream write: %v", err)
	}
	buffer := make([]byte, 2)
	if _, err := io.ReadFull(secondServer, buffer); err != nil || string(buffer) != "ok" {
		t.Fatalf("second stream read=%q err=%v", buffer, err)
	}
}

func TestStreamCloseUnblocksFlowControlledWrite(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	incomingCh := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		incomingCh <- incoming
	}()
	clientStreamCh := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("cancel-write"))
		clientStreamCh <- stream
	}()
	serverStream, err := (<-incomingCh).Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	clientStream := <-clientStreamCh
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := clientStream.Write(make([]byte, 4096))
		writeDone <- writeErr
	}()
	buffer := make([]byte, 64)
	if _, err := io.ReadFull(serverStream, buffer); err != nil {
		t.Fatalf("read first flow-control window: %v", err)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- clientStream.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close blocked writer: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("close did not unblock flow-controlled write")
	}
	if err := <-writeDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("write returned %v", err)
	}
	for {
		if _, err := serverStream.Read(buffer); err != nil {
			if !errors.Is(err, ErrReset) {
				t.Fatalf("peer did not receive reset after blocked write: %v", err)
			}
			break
		}
	}
}

func TestStreamResetReleasesPeerStreamLimit(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 4096, 1)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstIncoming := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		firstIncoming <- incoming
	}()
	firstClientCh := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("first"))
		firstClientCh <- stream
	}()
	firstServer, err := (<-firstIncoming).Accept()
	if err != nil {
		t.Fatalf("accept first stream: %v", err)
	}
	if err := (<-firstClientCh).Close(); err != nil {
		t.Fatalf("close first stream: %v", err)
	}
	if _, err := firstServer.Read(make([]byte, 1)); !errors.Is(err, ErrReset) {
		t.Fatalf("read first reset: %v", err)
	}

	secondIncoming := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		secondIncoming <- incoming
	}()
	secondClientCh := make(chan struct {
		stream *Stream
		err    error
	}, 1)
	go func() {
		stream, openErr := client.Open(ctx, []byte("second"))
		secondClientCh <- struct {
			stream *Stream
			err    error
		}{stream, openErr}
	}()
	secondServer := <-secondIncoming
	if secondServer == nil {
		t.Fatal("second stream was not admitted after reset")
	}
	if _, err := secondServer.Accept(); err != nil {
		t.Fatalf("accept second stream: %v", err)
	}
	if result := <-secondClientCh; result.err != nil || result.stream == nil {
		t.Fatalf("open second stream: %v", result.err)
	}
}

func TestGracefulStreamWaitsForFinalFlowControlAcknowledgement(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 4096, 4)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	incomingResult := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		incomingResult <- incoming
	}()
	clientResult := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("graceful-response"))
		clientResult <- stream
	}()
	serverStream, err := (<-incomingResult).Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	clientStream := <-clientResult

	request := []byte("request")
	if _, err := clientStream.Write(request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := clientStream.CloseWrite(); err != nil {
		t.Fatalf("close request: %v", err)
	}
	if got, err := io.ReadAll(serverStream); err != nil || !bytes.Equal(got, request) {
		t.Fatalf("read request: got=%q err=%v", got, err)
	}

	response := bytes.Repeat([]byte("response"), 64)
	if _, err := serverStream.Write(response); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if err := serverStream.CloseWrite(); err != nil {
		t.Fatalf("close response: %v", err)
	}
	// The target relay returns here and closes its Stream before the client
	// has consumed the response. Its final WINDOW_UPDATE is still valid.
	if err := serverStream.Close(); err != nil {
		t.Fatalf("close server stream: %v", err)
	}
	if got, err := io.ReadAll(clientStream); err != nil || !bytes.Equal(got, response) {
		t.Fatalf("read response: got=%d bytes err=%v", len(got), err)
	}

	select {
	case <-server.done:
		t.Fatalf("late final WINDOW_UPDATE terminated session: %v", server.sessionError())
	case <-time.After(100 * time.Millisecond):
	}
	deadline := time.Now().Add(time.Second)
	for {
		client.mu.Lock()
		clientStreams := len(client.streams)
		client.mu.Unlock()
		server.mu.Lock()
		serverStreams := len(server.streams)
		server.mu.Unlock()
		if clientStreams == 0 && serverStreams == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("graceful stream leaked: client=%d server=%d", clientStreams, serverStreams)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLateFrameForLocallyResetStreamDoesNotTerminateSession(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 4096, 4)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	incomingResult := make(chan *Incoming, 1)
	go func() {
		incoming, _ := server.Accept(ctx)
		incomingResult <- incoming
	}()
	clientResult := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("cancel-race"))
		clientResult <- stream
	}()
	serverStream, err := (<-incomingResult).Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	clientStream := <-clientResult
	streamID := clientStream.id

	if err := clientStream.Close(); err != nil {
		t.Fatalf("reset client stream: %v", err)
	}
	if _, err := serverStream.Read(make([]byte, 1)); !errors.Is(err, ErrReset) {
		t.Fatalf("server did not observe reset: %v", err)
	}
	// DATA was already in flight when the peer processed RESET. It belongs to
	// a known retired stream and must not take down unrelated multiplexed work.
	if err := server.send(ctx, protocol.Cell{
		Kind: protocol.CellData, StreamID: streamID, Sequence: 2, Payload: []byte("late"),
	}); err != nil {
		t.Fatalf("send late data: %v", err)
	}
	select {
	case <-client.done:
		t.Fatalf("late frame terminated session: %v", client.sessionError())
	case <-time.After(100 * time.Millisecond):
	}
	if err := client.handleCell(protocol.Cell{
		Kind: protocol.CellData, StreamID: 999, Sequence: 1, Payload: []byte("unknown"),
	}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("never-opened stream was not rejected: %v", err)
	}
	openPayload := binary.AppendUvarint(nil, 4096)
	if err := server.handleCell(protocol.Cell{
		Kind: protocol.CellOpen, StreamID: streamID, Sequence: 0, Payload: openPayload,
	}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("retired stream ID reuse was not rejected: %v", err)
	}
}

func TestRetiredStreamJournalIsBounded(t *testing.T) {
	mux := &Mux{
		retired: make(map[uint64]struct{}), retiredLimit: 2,
	}
	mux.mu.Lock()
	mux.retireStreamLocked(1)
	mux.retireStreamLocked(3)
	mux.retireStreamLocked(5)
	mux.mu.Unlock()
	if len(mux.retired) != 2 || len(mux.retiredOrder) != 2 {
		t.Fatalf("retired journal grew past limit: map=%d order=%d", len(mux.retired), len(mux.retiredOrder))
	}
	if _, exists := mux.retired[1]; exists {
		t.Fatal("oldest retired stream was not evicted")
	}
}

func newTestMuxPair(t *testing.T, initialWindow uint64, maxStreams int) (*Mux, *Mux, *memoryCarrier) {
	t.Helper()
	left, right := newMemoryCarrierPair()
	typeMap := testTypeMap(t)
	client, err := New(Config{
		Role:          RoleClient,
		Carrier:       left,
		TypeMap:       typeMap,
		InitialWindow: initialWindow,
		MaxStreams:    maxStreams,
	})
	if err != nil {
		t.Fatalf("create client mux: %v", err)
	}
	server, err := New(Config{
		Role:          RoleServer,
		Carrier:       right,
		TypeMap:       typeMap,
		InitialWindow: initialWindow,
		MaxStreams:    maxStreams,
	})
	if err != nil {
		_ = client.Close()
		t.Fatalf("create server mux: %v", err)
	}
	return client, server, left
}

func testTypeMap(t *testing.T) protocol.TypeMap {
	t.Helper()
	typeMap, err := protocol.NewTypeMap([32]byte{0xd1, 0x27})
	if err != nil {
		t.Fatalf("create type map: %v", err)
	}
	return typeMap
}

type memoryCarrier struct {
	in           <-chan []byte
	out          chan<- []byte
	done         <-chan struct{}
	close        func()
	closeMux     sync.Once
	blockReceive atomic.Bool
}

var _ carrier.Carrier = (*memoryCarrier)(nil)

func newMemoryCarrierPair() (*memoryCarrier, *memoryCarrier) {
	leftToRight := make(chan []byte, 1024)
	rightToLeft := make(chan []byte, 1024)
	done := make(chan struct{})
	var closeOnce sync.Once
	closePair := func() { closeOnce.Do(func() { close(done) }) }
	return &memoryCarrier{in: rightToLeft, out: leftToRight, done: done, close: closePair},
		&memoryCarrier{in: leftToRight, out: rightToLeft, done: done, close: closePair}
}

func (m *memoryCarrier) Send(ctx context.Context, raw []byte) error {
	copyOfRaw := append([]byte(nil), raw...)
	select {
	case m.out <- copyOfRaw:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.done:
		return io.EOF
	}
}

func (m *memoryCarrier) Receive(ctx context.Context) ([]byte, error) {
	if m.blockReceive.Load() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.done:
			return nil, io.EOF
		}
	}
	select {
	case raw := <-m.in:
		if m.blockReceive.Load() {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-m.done:
				return nil, io.EOF
			}
		}
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.done:
		return nil, io.EOF
	}
}

func (m *memoryCarrier) Close() error {
	m.closeMux.Do(m.close)
	return nil
}

func (m *memoryCarrier) Kind() protocol.CarrierKind {
	return protocol.CarrierHTTPS
}
