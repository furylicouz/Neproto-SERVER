package session

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestMuxStatsAccountForLifecycleAndCells(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 64*1024, 8)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverDone := make(chan error, 1)
	go func() {
		incoming, err := server.Accept(ctx)
		if err != nil {
			serverDone <- err
			return
		}
		stream, err := incoming.Accept()
		if err == nil {
			_, err = io.Copy(io.Discard, stream)
		}
		if err == nil {
			err = stream.CloseWrite()
		}
		serverDone <- err
	}()

	stream, err := client.Open(ctx, []byte("metrics-target"))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	payload := []byte("useful application bytes")
	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}
	if _, err := io.ReadAll(stream); err != nil {
		t.Fatalf("drain response: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server relay: %v", err)
	}

	clientStats := client.Stats()
	if clientStats.LocallyOpenedStreams != 1 || clientStats.ActiveStreams != 0 ||
		clientStats.RetiredStreams != 1 {
		t.Fatalf("unexpected client lifecycle stats: %+v", clientStats)
	}
	if clientStats.SentCells < 3 || clientStats.ReceivedCells < 2 ||
		clientStats.SentCellPayloadBytes < uint64(len(payload)) {
		t.Fatalf("unexpected client cell stats: %+v", clientStats)
	}
	serverStats := server.Stats()
	if serverStats.RemotelyOpenedStreams != 1 || serverStats.ActiveStreams != 0 ||
		serverStats.RetiredStreams != 1 {
		t.Fatalf("unexpected server lifecycle stats: %+v", serverStats)
	}
}

func TestMuxStatsRecordFlowControlStalls(t *testing.T) {
	client, server, _ := newTestMuxPair(t, 4, 8)
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
	streamCh := make(chan *Stream, 1)
	go func() {
		stream, _ := client.Open(ctx, []byte("flow-stats"))
		streamCh <- stream
	}()
	incoming := <-incomingCh
	peer, err := incoming.Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	stream := <-streamCh
	if stream == nil {
		t.Fatal("client stream did not open")
	}

	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := stream.Write([]byte("eightbyt"))
		writeDone <- writeErr
	}()

	deadline := time.Now().Add(time.Second)
	for client.Stats().FlowControlStalls == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stats := client.Stats(); stats.FlowControlStalls == 0 {
		t.Fatalf("flow-control stall was not counted: %+v", stats)
	}
	buffer := make([]byte, 8)
	if _, err := io.ReadFull(peer, buffer); err != nil {
		t.Fatalf("read peer data: %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("complete stalled write: %v", err)
	}
}
