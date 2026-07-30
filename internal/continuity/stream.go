package continuity

import (
	"context"
	"errors"
	"io"
	"sync"

	"neproto.local/chameleon/internal/protocol"
)

const replayCopyBufferSize = 32 * 1024

type ResumableStreamConfig struct {
	Context               context.Context
	Initial               io.ReadWriteCloser
	JournalBytes          int
	AckEveryBytes         uint64
	OnReceiveOffset       func(uint64) error
	OnUnavailable         func(error)
	RecoverableReadError  func(error) bool
	RecoverableWriteError func(error) bool
}

type ResumeState struct {
	// PeerReceiveOffset cumulatively acknowledges this stream's writes.
	PeerReceiveOffset uint64
	// ReceiveOffset is the exact offset where the replacement peer will start.
	ReceiveOffset uint64
}

type ResumableOffsets struct {
	SendBase uint64
	SendEnd  uint64
	Receive  uint64
}

// ResumableStream presents one stable byte stream over a sequence of physical
// streams. It owns every accepted physical stream and a bounded replay journal.
// A replacement is admitted only after the current physical stream has failed.
type ResumableStream struct {
	ctx              context.Context
	journal          *Journal
	recoverableRead  func(error) bool
	recoverableWrite func(error) bool
	ackEvery         uint64
	onReceive        func(uint64) error
	onUnavailable    func(error)

	writeMu   sync.Mutex
	readMu    sync.Mutex
	replaceMu sync.Mutex
	ackMu     sync.Mutex
	mu        sync.Mutex

	physical         io.ReadWriteCloser
	transmitted      uint64
	receiveOffset    uint64
	lastNotified     uint64
	localFIN         bool
	finTransmitted   bool
	discard          uint64
	writeAttemptEnd  uint64
	writeAttemptDone chan struct{}
	pendingAck       uint64
	notify           chan struct{}
	closed           bool
	closeOnce        sync.Once
	closeErr         error
}

func NewResumableStream(config ResumableStreamConfig) (*ResumableStream, error) {
	if config.Context == nil || nilReadWriteCloser(config.Initial) ||
		config.JournalBytes <= 0 || config.JournalBytes > MaxJournalCapacity ||
		(config.AckEveryBytes != 0 && (config.OnReceiveOffset == nil ||
			config.AckEveryBytes > uint64(config.JournalBytes))) {
		return nil, ErrResumableConfig
	}
	journal, err := NewJournal(config.JournalBytes)
	if err != nil {
		return nil, errors.Join(ErrResumableConfig, err)
	}
	recoverableRead := config.RecoverableReadError
	if recoverableRead == nil {
		recoverableRead = func(err error) bool {
			return err != nil && !errors.Is(err, io.EOF)
		}
	}
	recoverableWrite := config.RecoverableWriteError
	if recoverableWrite == nil {
		recoverableWrite = func(err error) bool { return err != nil }
	}
	return &ResumableStream{
		ctx: config.Context, journal: journal, recoverableRead: recoverableRead,
		recoverableWrite: recoverableWrite,
		ackEvery:         config.AckEveryBytes, onReceive: config.OnReceiveOffset,
		onUnavailable: config.OnUnavailable,
		physical:      config.Initial, notify: make(chan struct{}),
	}, nil
}

func (s *ResumableStream) Write(payload []byte) (int, error) {
	if s == nil {
		return 0, ErrResumableConfig
	}
	if len(payload) == 0 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrResumableClosed
	}
	if s.localFIN {
		s.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	s.mu.Unlock()
	start, end, err := s.journal.Append(payload)
	if err != nil {
		return 0, err
	}
	for {
		physical, transmitted, err := s.waitPhysical()
		if err != nil {
			return writtenForRange(start, end, transmitted), err
		}
		if transmitted >= end {
			return len(payload), nil
		}
		if transmitted < start {
			return writtenForRange(start, end, transmitted), ErrResumableState
		}
		position := int(transmitted - start)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return writtenForRange(start, end, transmitted), ErrResumableClosed
		}
		if s.physical != physical || s.transmitted != transmitted {
			s.mu.Unlock()
			continue
		}
		s.writeAttemptEnd = end
		s.writeAttemptDone = make(chan struct{})
		s.mu.Unlock()
		n, writeErr := physical.Write(payload[position:])
		if n < 0 || n > len(payload)-position {
			n = 0
			writeErr = ErrResumableState
		}
		var deferredAck uint64
		var attemptDone chan struct{}
		invalidDeferredAck := false
		s.mu.Lock()
		if !s.closed && (s.physical == physical || s.physical == nil) && s.transmitted == transmitted {
			if uint64(n) > protocol.MaxSequence-s.transmitted {
				writeErr = ErrResumableState
				n = 0
			} else {
				s.transmitted += uint64(n)
			}
		}
		if s.writeAttemptEnd != 0 {
			s.writeAttemptEnd = 0
			attemptDone = s.writeAttemptDone
			s.writeAttemptDone = nil
			if s.pendingAck != 0 {
				if s.pendingAck <= s.transmitted {
					deferredAck = s.pendingAck
				} else if writeErr != nil || n != len(payload)-position {
					invalidDeferredAck = true
				}
				s.pendingAck = 0
			}
		}
		currentTransmitted := s.transmitted
		closed := s.closed
		s.mu.Unlock()
		if deferredAck != 0 {
			if err := s.ackJournal(deferredAck, true); err != nil {
				if attemptDone != nil {
					close(attemptDone)
				}
				return writtenForRange(start, end, currentTransmitted), err
			}
		}
		if attemptDone != nil {
			close(attemptDone)
		}
		if invalidDeferredAck {
			return writtenForRange(start, end, currentTransmitted), ErrResumableState
		}
		if closed {
			return writtenForRange(start, end, currentTransmitted), ErrResumableClosed
		}
		if currentTransmitted >= end {
			return len(payload), nil
		}
		if writeErr != nil || n == 0 {
			if writeErr == nil {
				writeErr = io.ErrNoProgress
			}
			if !s.recoverableWrite(writeErr) {
				return writtenForRange(start, end, currentTransmitted), writeErr
			}
			s.markUnavailable(physical, writeErr)
			continue
		}
	}
}

// CloseWrite preserves TCP half-close semantics across a replacement. A FIN
// is sent only after every journaled byte has reached that physical stream.
func (s *ResumableStream) CloseWrite() error {
	if s == nil {
		return ErrResumableConfig
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrResumableClosed
	}
	if s.localFIN && s.finTransmitted {
		s.mu.Unlock()
		return nil
	}
	if !s.localFIN {
		s.localFIN = true
	}
	s.mu.Unlock()
	for {
		physical, transmitted, err := s.waitPhysical()
		if err != nil {
			return err
		}
		if transmitted < s.journal.State().EndOffset {
			return ErrResumableState
		}
		s.mu.Lock()
		if s.finTransmitted {
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		closer, ok := physical.(interface{ CloseWrite() error })
		if !ok {
			return nil
		}
		if err := closer.CloseWrite(); err != nil {
			s.markUnavailable(physical, err)
			continue
		}
		s.mu.Lock()
		if s.physical == physical {
			s.finTransmitted = true
		}
		s.mu.Unlock()
		return nil
	}
}

func (s *ResumableStream) Read(destination []byte) (int, error) {
	if s == nil {
		return 0, ErrResumableConfig
	}
	if len(destination) == 0 {
		return 0, nil
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	for {
		physical, _, err := s.waitPhysical()
		if err != nil {
			return 0, err
		}
		n, readErr := physical.Read(destination)
		if n < 0 || n > len(destination) {
			n = 0
			readErr = ErrResumableState
		}
		if n != 0 {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return 0, ErrResumableClosed
			}
			if s.physical != physical {
				s.mu.Unlock()
				return 0, ErrResumableState
			}
			if s.discard != 0 {
				discarded := min(uint64(n), s.discard)
				s.discard -= discarded
				remaining := n - int(discarded)
				if remaining != 0 {
					copy(destination[:remaining], destination[int(discarded):n])
				}
				n = remaining
			}
			if uint64(n) > protocol.MaxSequence-s.receiveOffset {
				s.mu.Unlock()
				return 0, ErrResumableState
			}
			s.receiveOffset += uint64(n)
			receiveOffset := s.receiveOffset
			notify := s.ackEvery != 0 && receiveOffset-s.lastNotified >= s.ackEvery
			if notify {
				s.lastNotified = receiveOffset
			}
			s.mu.Unlock()
			if notify {
				if notifyErr := s.onReceive(receiveOffset); notifyErr != nil {
					s.markUnavailable(physical, notifyErr)
				}
			}
			if readErr != nil && s.recoverableRead(readErr) {
				s.markUnavailable(physical, readErr)
			}
			if n != 0 {
				return n, nil
			}
			if readErr != nil && !s.recoverableRead(readErr) {
				return 0, ErrResumableState
			}
			continue
		}
		if readErr == nil {
			readErr = io.ErrNoProgress
		}
		if !s.recoverableRead(readErr) {
			return 0, readErr
		}
		s.markUnavailable(physical, readErr)
	}
}

// Replace replays acknowledged-safe bytes before publishing the replacement
// to blocked readers/writers. Invalid or failed candidates are always closed.
func (s *ResumableStream) Replace(next io.ReadWriteCloser, state ResumeState) error {
	if s == nil {
		if !nilReadWriteCloser(next) {
			_ = next.Close()
		}
		return ErrResumableConfig
	}
	if nilReadWriteCloser(next) {
		return ErrResumableState
	}
	s.replaceMu.Lock()
	defer s.replaceMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = next.Close()
		return ErrResumableClosed
	}
	if s.physical != nil || state.ReceiveOffset > s.receiveOffset ||
		state.PeerReceiveOffset > s.transmitted {
		s.mu.Unlock()
		_ = next.Close()
		return ErrResumableState
	}
	s.mu.Unlock()

	if err := s.ackJournal(state.PeerReceiveOffset, false); err != nil {
		_ = next.Close()
		return errors.Join(ErrResumableState, err)
	}
	journalState := s.journal.State()
	buffer := make([]byte, min(replayCopyBufferSize, max(1, journalState.BufferedBytes)))
	offset := state.PeerReceiveOffset
	for offset < journalState.EndOffset {
		n, following, err := s.journal.Replay(offset, buffer)
		if err != nil || n == 0 {
			_ = next.Close()
			return errors.Join(ErrResumableState, err)
		}
		if err := writeAll(next, buffer[:n]); err != nil {
			_ = next.Close()
			return errors.Join(ErrResumableState, err)
		}
		offset = following
	}
	s.mu.Lock()
	localFIN := s.localFIN
	s.mu.Unlock()
	if localFIN {
		if closer, ok := next.(interface{ CloseWrite() error }); ok {
			if err := closer.CloseWrite(); err != nil {
				_ = next.Close()
				return errors.Join(ErrResumableState, err)
			}
		}
	}

	s.mu.Lock()
	if s.closed || s.physical != nil || state.ReceiveOffset > s.receiveOffset {
		s.mu.Unlock()
		_ = next.Close()
		return ErrResumableClosed
	}
	s.physical = next
	s.transmitted = journalState.EndOffset
	s.finTransmitted = localFIN
	s.discard = s.receiveOffset - state.ReceiveOffset
	s.signalLocked()
	s.mu.Unlock()
	return nil
}

// DetachPhysical makes the current physical stream unavailable without closing
// the logical stream. It is used only after a higher authenticated lease epoch
// supersedes the old carrier and is idempotent when the carrier already failed.
func (s *ResumableStream) DetachPhysical() error {
	if s == nil {
		return ErrResumableConfig
	}
	s.replaceMu.Lock()
	defer s.replaceMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrResumableClosed
	}
	physical := s.physical
	attemptDone := s.writeAttemptDone
	s.physical = nil
	if physical != nil {
		s.signalLocked()
	}
	s.mu.Unlock()
	if physical != nil {
		_ = physical.Close()
	}
	if attemptDone != nil {
		// Closing the superseded carrier unblocks this exact physical Write.
		// A writer merely waiting for a replacement has no attempt channel, so
		// the migration barrier cannot wait on itself.
		<-attemptDone
	}
	return nil
}

func (s *ResumableStream) Ack(offset uint64) error {
	if s == nil {
		return ErrResumableConfig
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrResumableClosed
	}
	if offset > s.transmitted {
		if s.writeAttemptEnd != 0 && offset <= s.writeAttemptEnd {
			if offset > s.pendingAck {
				s.pendingAck = offset
			}
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		return ErrResumableState
	}
	s.mu.Unlock()
	return s.ackJournal(offset, false)
}

func (s *ResumableStream) ackJournal(offset uint64, deferred bool) error {
	s.ackMu.Lock()
	defer s.ackMu.Unlock()
	if deferred && offset <= s.journal.State().BaseOffset {
		return nil
	}
	return s.journal.Ack(offset)
}

func (s *ResumableStream) Offsets() ResumableOffsets {
	if s == nil {
		return ResumableOffsets{}
	}
	journalState := s.journal.State()
	s.mu.Lock()
	receiveOffset := s.receiveOffset
	s.mu.Unlock()
	return ResumableOffsets{
		SendBase: journalState.BaseOffset, SendEnd: journalState.EndOffset,
		Receive: receiveOffset,
	}
}

func (s *ResumableStream) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		physical := s.physical
		s.physical = nil
		s.signalLocked()
		s.mu.Unlock()
		s.journal.Close()
		if physical != nil {
			s.closeErr = physical.Close()
		}
	})
	return s.closeErr
}

func (s *ResumableStream) waitPhysical() (io.ReadWriteCloser, uint64, error) {
	for {
		s.mu.Lock()
		if s.closed {
			transmitted := s.transmitted
			s.mu.Unlock()
			return nil, transmitted, ErrResumableClosed
		}
		if s.physical != nil {
			physical := s.physical
			transmitted := s.transmitted
			s.mu.Unlock()
			return physical, transmitted, nil
		}
		notify := s.notify
		transmitted := s.transmitted
		s.mu.Unlock()
		select {
		case <-notify:
		case <-s.ctx.Done():
			return nil, transmitted, s.ctx.Err()
		}
	}
}

func (s *ResumableStream) markUnavailable(failed io.ReadWriteCloser, reason error) {
	s.mu.Lock()
	if s.closed || s.physical != failed {
		s.mu.Unlock()
		return
	}
	s.physical = nil
	if s.localFIN {
		s.finTransmitted = false
	}
	s.signalLocked()
	s.mu.Unlock()
	_ = failed.Close()
	if s.onUnavailable != nil {
		s.onUnavailable(reason)
	}
}

func (s *ResumableStream) signalLocked() {
	close(s.notify)
	s.notify = make(chan struct{})
}

func writtenForRange(start, end, transmitted uint64) int {
	if transmitted <= start {
		return 0
	}
	if transmitted >= end {
		return int(end - start)
	}
	return int(transmitted - start)
}

func writeAll(destination io.Writer, payload []byte) error {
	for len(payload) != 0 {
		n, err := destination.Write(payload)
		if n < 0 || n > len(payload) {
			return ErrResumableState
		}
		payload = payload[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
