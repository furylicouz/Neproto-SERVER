package continuity

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestResumableStreamReplaysOnlyUnacknowledgedWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := newScriptedPhysical(nil, 3)
	unavailable := make(chan error, 1)
	stream, err := NewResumableStream(ResumableStreamConfig{
		Context: ctx, Initial: first, JournalBytes: 64,
		OnUnavailable: func(err error) { unavailable <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	writeResult := make(chan struct {
		n   int
		err error
	}, 1)
	go func() {
		n, writeErr := stream.Write([]byte("abcdef"))
		writeResult <- struct {
			n   int
			err error
		}{n: n, err: writeErr}
	}()
	select {
	case <-first.failed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-unavailable:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("unavailable reason=%v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	second := newScriptedPhysical(nil, -1)
	if err := stream.Replace(second, ResumeState{PeerReceiveOffset: 2, ReceiveOffset: 0}); err != nil {
		t.Fatal(err)
	}
	result := <-writeResult
	if result.n != 6 || result.err != nil {
		t.Fatalf("write result n=%d err=%v", result.n, result.err)
	}
	if got := second.written(); got != "cdef" {
		t.Fatalf("replayed=%q", got)
	}
	if offsets := stream.Offsets(); offsets.SendBase != 2 || offsets.SendEnd != 6 || offsets.Receive != 0 {
		t.Fatalf("offsets=%+v", offsets)
	}
}

func TestResumableStreamReadWaitsForReplacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := newScriptedPhysical([]byte("abc"), -1)
	stream, err := NewResumableStream(ResumableStreamConfig{
		Context: ctx, Initial: first, JournalBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	buffer := make([]byte, 3)
	if n, err := io.ReadFull(stream, buffer); err != nil || n != 3 || string(buffer) != "abc" {
		t.Fatalf("first read n=%d payload=%q err=%v", n, buffer, err)
	}
	readResult := make(chan struct {
		payload string
		err     error
	}, 1)
	go func() {
		n, readErr := stream.Read(buffer)
		readResult <- struct {
			payload string
			err     error
		}{payload: string(buffer[:n]), err: readErr}
	}()
	select {
	case <-first.failed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second := newScriptedPhysical([]byte("def"), -1)
	if err := stream.Replace(second, ResumeState{PeerReceiveOffset: 0, ReceiveOffset: 3}); err != nil {
		t.Fatal(err)
	}
	result := <-readResult
	if result.err != nil || result.payload != "def" {
		t.Fatalf("second read payload=%q err=%v", result.payload, result.err)
	}
	if offsets := stream.Offsets(); offsets.Receive != 6 {
		t.Fatalf("offsets=%+v", offsets)
	}
}

func TestResumableStreamDiscardsReplayAlreadyDeliveredBeforeFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := newScriptedPhysical([]byte("abcdef"), -1)
	stream, err := NewResumableStream(ResumableStreamConfig{
		Context: ctx, Initial: first, JournalBytes: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	buffer := make([]byte, 6)
	if _, err := io.ReadFull(stream, buffer); err != nil {
		t.Fatal(err)
	}
	failedRead := make(chan error, 1)
	go func() {
		_, readErr := stream.Read(buffer)
		failedRead <- readErr
	}()
	<-first.failed
	second := newScriptedPhysical([]byte("cdefgh"), -1)
	if err := stream.Replace(second, ResumeState{PeerReceiveOffset: 0, ReceiveOffset: 2}); err != nil {
		t.Fatal(err)
	}
	select {
	case readErr := <-failedRead:
		if readErr != nil || string(buffer[:2]) != "gh" {
			t.Fatalf("deduplicated payload=%q err=%v", buffer[:2], readErr)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if offsets := stream.Offsets(); offsets.Receive != 8 {
		t.Fatalf("offsets=%+v", offsets)
	}
}

func TestResumableStreamRejectsInvalidResumeWithoutMutation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := newScriptedPhysical(nil, 0)
	stream, err := NewResumableStream(ResumableStreamConfig{Context: ctx, Initial: first, JournalBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := stream.Write([]byte("abcd"))
		writeDone <- writeErr
	}()
	<-first.failed

	invalid := newScriptedPhysical(nil, -1)
	if err := stream.Replace(invalid, ResumeState{PeerReceiveOffset: 5, ReceiveOffset: 0}); !errors.Is(err, ErrResumableState) {
		t.Fatalf("invalid resume error=%v", err)
	}
	if invalid.closes() != 1 || stream.Offsets().SendBase != 0 {
		t.Fatalf("candidate closes=%d offsets=%+v", invalid.closes(), stream.Offsets())
	}
	valid := newScriptedPhysical(nil, -1)
	if err := stream.Replace(valid, ResumeState{PeerReceiveOffset: 0, ReceiveOffset: 0}); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
}

func TestResumableStreamBudgetFailureDoesNotWrite(t *testing.T) {
	physical := newScriptedPhysical(nil, -1)
	stream, err := NewResumableStream(ResumableStreamConfig{
		Context: context.Background(), Initial: physical, JournalBytes: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if n, err := stream.Write([]byte("abcde")); n != 0 || !errors.Is(err, ErrReplayBudget) {
		t.Fatalf("budget write n=%d err=%v", n, err)
	}
	if physical.written() != "" {
		t.Fatalf("physical write=%q", physical.written())
	}
}

func TestResumableStreamDefersAckThatRacesPhysicalWriteReturn(t *testing.T) {
	physical := newBlockingWritePhysical()
	stream, err := NewResumableStream(ResumableStreamConfig{
		Context: context.Background(), Initial: physical, JournalBytes: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	written := make(chan error, 1)
	go func() {
		_, writeErr := stream.Write([]byte("abc"))
		written <- writeErr
	}()
	select {
	case <-physical.started:
	case <-time.After(time.Second):
		t.Fatal("physical write did not start")
	}
	if err := stream.Ack(3); err != nil {
		t.Fatalf("in-flight acknowledgement: %v", err)
	}
	if offsets := stream.Offsets(); offsets.SendBase != 0 || offsets.SendEnd != 3 {
		t.Fatalf("ack retired bytes before write return: %+v", offsets)
	}
	close(physical.release)
	if err := <-written; err != nil {
		t.Fatalf("write result: %v", err)
	}
	if offsets := stream.Offsets(); offsets.SendBase != 3 || offsets.SendEnd != 3 {
		t.Fatalf("deferred acknowledgement not applied: %+v", offsets)
	}
}

func TestResumableStreamDetachesSupersededPhysicalBeforeReplacement(t *testing.T) {
	first := newScriptedPhysical(nil, -1)
	stream, err := NewResumableStream(ResumableStreamConfig{
		Context: context.Background(), Initial: first, JournalBytes: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.DetachPhysical(); err != nil {
		t.Fatalf("detach physical: %v", err)
	}
	if first.closes() != 1 {
		t.Fatalf("superseded physical closes=%d", first.closes())
	}
	second := newScriptedPhysical(nil, -1)
	if err := stream.Replace(second, ResumeState{}); err != nil {
		t.Fatalf("replace after detach: %v", err)
	}
	if _, err := stream.Write([]byte("ok")); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if got := second.written(); got != "ok" {
		t.Fatalf("replacement payload=%q", got)
	}
}

func TestResumableStreamCoalescesCumulativeReceiveNotifications(t *testing.T) {
	physical := newScriptedPhysical([]byte("abcdef"), -1)
	notifications := make(chan uint64, 2)
	stream, err := NewResumableStream(ResumableStreamConfig{
		Context: context.Background(), Initial: physical, JournalBytes: 8,
		AckEveryBytes: 4, OnReceiveOffset: func(offset uint64) error {
			notifications <- offset
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	buffer := make([]byte, 3)
	if _, err := io.ReadFull(stream, buffer); err != nil {
		t.Fatal(err)
	}
	select {
	case offset := <-notifications:
		t.Fatalf("premature notification=%d", offset)
	default:
	}
	if _, err := io.ReadFull(stream, buffer); err != nil {
		t.Fatal(err)
	}
	select {
	case offset := <-notifications:
		if offset != 6 {
			t.Fatalf("notification=%d", offset)
		}
	default:
		t.Fatal("missing coalesced notification")
	}
}

func TestResumableStreamPreservesCloseWrite(t *testing.T) {
	physical := newScriptedPhysical(nil, -1)
	stream, err := NewResumableStream(ResumableStreamConfig{
		Context: context.Background(), Initial: physical, JournalBytes: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if physical.closeWritesCount() != 1 {
		t.Fatalf("close writes=%d", physical.closeWritesCount())
	}
	if _, err := stream.Write([]byte("d")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after FIN error=%v", err)
	}
}

func TestResumableStreamCloseWakesReadAndCannotResurrect(t *testing.T) {
	left, right := net.Pipe()
	stream, err := NewResumableStream(ResumableStreamConfig{
		Context: context.Background(), Initial: left, JournalBytes: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, readErr := stream.Read(make([]byte, 1))
		readDone <- readErr
	}()
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if !errors.Is(err, ErrResumableClosed) {
			t.Fatalf("blocked read error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked read did not wake")
	}
	candidate, peer := net.Pipe()
	if err := stream.Replace(candidate, ResumeState{}); !errors.Is(err, ErrResumableClosed) {
		t.Fatalf("replace after close error=%v", err)
	}
	if _, err := peer.Write([]byte{1}); err == nil {
		t.Fatal("rejected replacement remained open")
	}
	_ = peer.Close()
	_ = right.Close()
}

func TestNewResumableStreamValidatesConfiguration(t *testing.T) {
	physical := newScriptedPhysical(nil, -1)
	for _, config := range []ResumableStreamConfig{
		{},
		{Context: context.Background(), Initial: physical},
		{Context: context.Background(), Initial: physical, JournalBytes: MaxJournalCapacity + 1},
		{Context: context.Background(), Initial: physical, JournalBytes: 8, AckEveryBytes: 9, OnReceiveOffset: func(uint64) error { return nil }},
		{Context: context.Background(), Initial: physical, JournalBytes: 8, AckEveryBytes: 1},
	} {
		if _, err := NewResumableStream(config); !errors.Is(err, ErrResumableConfig) {
			t.Fatalf("config=%+v error=%v", config, err)
		}
	}
}

type scriptedPhysical struct {
	mu sync.Mutex

	readBuffer  bytes.Buffer
	writeLimit  int
	writeTotal  int
	writes      bytes.Buffer
	closed      int
	closeWrites int
	failed      chan struct{}
	failOnce    sync.Once
}

type blockingWritePhysical struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingWritePhysical() *blockingWritePhysical {
	return &blockingWritePhysical{started: make(chan struct{}), release: make(chan struct{})}
}

func (p *blockingWritePhysical) Read([]byte) (int, error) { return 0, io.EOF }

func (p *blockingWritePhysical) Write(payload []byte) (int, error) {
	p.once.Do(func() { close(p.started) })
	<-p.release
	return len(payload), nil
}

func (*blockingWritePhysical) Close() error { return nil }

func newScriptedPhysical(readData []byte, writeLimit int) *scriptedPhysical {
	physical := &scriptedPhysical{writeLimit: writeLimit, failed: make(chan struct{})}
	_, _ = physical.readBuffer.Write(readData)
	return physical
}

func (p *scriptedPhysical) Read(destination []byte) (int, error) {
	p.mu.Lock()
	if p.readBuffer.Len() != 0 {
		n, _ := p.readBuffer.Read(destination)
		p.mu.Unlock()
		return n, nil
	}
	p.mu.Unlock()
	p.signalFailure()
	return 0, io.ErrUnexpectedEOF
}

func (p *scriptedPhysical) Write(payload []byte) (int, error) {
	p.mu.Lock()
	if p.writeLimit >= 0 {
		remaining := p.writeLimit - p.writeTotal
		if remaining <= 0 {
			p.mu.Unlock()
			p.signalFailure()
			return 0, io.ErrUnexpectedEOF
		}
		length := min(len(payload), remaining)
		_, _ = p.writes.Write(payload[:length])
		p.writeTotal += length
		failed := length < len(payload) || p.writeTotal >= p.writeLimit
		p.mu.Unlock()
		if failed {
			p.signalFailure()
			return length, io.ErrUnexpectedEOF
		}
		return length, nil
	}
	n, _ := p.writes.Write(payload)
	p.writeTotal += n
	p.mu.Unlock()
	return n, nil
}

func (p *scriptedPhysical) Close() error {
	p.mu.Lock()
	p.closed++
	p.mu.Unlock()
	return nil
}

func (p *scriptedPhysical) CloseWrite() error {
	p.mu.Lock()
	p.closeWrites++
	p.mu.Unlock()
	return nil
}

func (p *scriptedPhysical) written() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writes.String()
}

func (p *scriptedPhysical) closes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *scriptedPhysical) closeWritesCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeWrites
}

func (p *scriptedPhysical) signalFailure() {
	p.failOnce.Do(func() { close(p.failed) })
}
