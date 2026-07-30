package session

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"

	"neproto.local/chameleon/internal/protocol"
)

const (
	windowUpdateDivisor      = 8
	maxWindowUpdateThreshold = 256 * 1024
)

type Stream struct {
	mux *Mux
	id  uint64

	// writeMu preserves byte ordering between concurrent public Write and
	// CloseWrite calls. sendMu independently serializes DATA and control cells
	// so WINDOW_UPDATE can progress while a large Write waits for peer credit.
	writeMu sync.Mutex
	sendMu  sync.Mutex
	mu      sync.Mutex

	sendSequence  uint64
	recvSequence  uint64
	sendWindow    uint64
	sendCredit    uint64
	recvCredit    uint64
	pendingCredit uint64

	chunks     [][]byte
	chunkStart int
	peerFIN    bool
	localFIN   bool
	readErr    error
	writeErr   error
	resetSent  bool

	readNotify   chan struct{}
	creditNotify chan struct{}
	openResult   chan error
	openOnce     sync.Once
}

func newStream(mux *Mux, id, sendCredit, recvCredit uint64, opening bool) *Stream {
	stream := &Stream{
		mux:          mux,
		id:           id,
		sendSequence: 1,
		recvSequence: 0,
		sendWindow:   sendCredit,
		sendCredit:   sendCredit,
		recvCredit:   recvCredit,
		readNotify:   make(chan struct{}),
		creditNotify: make(chan struct{}),
		openResult:   make(chan error, 1),
	}
	if !opening {
		stream.openOnce.Do(func() { stream.openResult <- nil })
	}
	return stream
}

// ID is the authenticated session-local stream identifier. The UDP fast path
// binds its datagram endpoint to this ID after OPEN/OPEN_OK succeeds.
func (s *Stream) ID() uint64 {
	if s == nil {
		return 0
	}
	return s.id
}

func (s *Stream) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	for {
		s.mu.Lock()
		if len(s.chunks) != 0 {
			chunk := s.chunks[0]
			n := copy(destination, chunk[s.chunkStart:])
			s.chunkStart += n
			if s.chunkStart == len(chunk) {
				s.chunks[0] = nil
				s.chunks = s.chunks[1:]
				s.chunkStart = 0
			}
			s.pendingCredit += uint64(n)
			credit := s.takePendingCreditLocked(
				s.recvCredit == 0 ||
					(s.peerFIN && len(s.chunks) == 0),
			)
			s.mu.Unlock()
			if err := s.sendWindowUpdate(credit); err != nil {
				return n, err
			}
			s.retireIfComplete()
			return n, nil
		}
		if s.readErr != nil {
			err := s.readErr
			s.mu.Unlock()
			return 0, err
		}
		if s.peerFIN {
			credit := s.takePendingCreditLocked(true)
			s.mu.Unlock()
			if err := s.sendWindowUpdate(credit); err != nil {
				return 0, err
			}
			s.retireIfComplete()
			return 0, io.EOF
		}
		notify := s.readNotify
		s.mu.Unlock()
		select {
		case <-notify:
		case <-s.mux.done:
			return 0, s.mux.sessionError()
		}
	}
}

func (s *Stream) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	written := 0
	for written < len(payload) {
		s.mu.Lock()
		if s.sendCredit == 0 && s.writeErr == nil && !s.localFIN {
			s.mux.stats.flowControlStalls.Add(1)
		}
		for s.sendCredit == 0 && s.writeErr == nil && !s.localFIN {
			notify := s.creditNotify
			s.mu.Unlock()
			select {
			case <-notify:
			case <-s.mux.done:
				return written, s.mux.sessionError()
			}
			s.mu.Lock()
		}
		if s.writeErr != nil {
			err := s.writeErr
			s.mu.Unlock()
			return written, err
		}
		if s.localFIN {
			s.mu.Unlock()
			return written, io.ErrClosedPipe
		}
		s.mu.Unlock()

		s.sendMu.Lock()
		s.mu.Lock()
		if s.writeErr != nil {
			err := s.writeErr
			s.mu.Unlock()
			s.sendMu.Unlock()
			return written, err
		}
		if s.localFIN {
			s.mu.Unlock()
			s.sendMu.Unlock()
			return written, io.ErrClosedPipe
		}
		length := min(uint64(len(payload)-written), s.sendCredit, uint64(protocol.MaxCellPayloadSize))
		s.sendCredit -= length
		sequence, err := s.nextSendSequenceLocked()
		s.mu.Unlock()
		if err != nil {
			s.sendMu.Unlock()
			s.fail(err)
			return written, err
		}
		err = s.mux.send(s.mux.ctx, protocol.Cell{
			Kind: protocol.CellData, StreamID: s.id, Sequence: sequence,
			Payload: payload[written : written+int(length)],
		})
		s.sendMu.Unlock()
		if err != nil {
			s.fail(err)
			return written, err
		}
		written += int(length)
	}
	return written, nil
}

func (s *Stream) CloseWrite() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.mu.Lock()
	if s.writeErr != nil {
		err := s.writeErr
		s.mu.Unlock()
		return err
	}
	if s.localFIN {
		s.mu.Unlock()
		return nil
	}
	s.localFIN = true
	s.signalCreditLocked()
	sequence, err := s.nextSendSequenceLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	err = s.mux.send(s.mux.ctx, protocol.Cell{Kind: protocol.CellFin, StreamID: s.id, Sequence: sequence})
	if err == nil {
		s.retireIfComplete()
	}
	return err
}

func (s *Stream) Close() error {
	s.mu.Lock()
	gracefullyClosed := s.localFIN && s.peerFIN && len(s.chunks) == 0
	if gracefullyClosed {
		retire := s.canRetireLocked()
		s.mu.Unlock()
		if retire {
			s.mux.removeStream(s.id, s)
		}
		return nil
	}
	if s.readErr == nil {
		s.readErr = ErrClosed
	}
	if s.writeErr == nil {
		s.writeErr = ErrClosed
	}
	s.signalReadLocked()
	s.signalCreditLocked()
	s.mu.Unlock()
	err := s.sendReset(0)
	s.mux.removeStream(s.id, s)
	return err
}

func (s *Stream) handleCell(cell protocol.Cell) error {
	s.mu.Lock()
	defer func() {
		retire := s.canRetireLocked()
		s.mu.Unlock()
		if retire {
			s.mux.removeStream(s.id, s)
		}
	}()

	if cell.Kind == protocol.CellOpenOK || cell.Kind == protocol.CellOpenFail {
		if s.recvSequence != 0 || cell.Sequence != 0 {
			return fmtProtocol("unexpected open response")
		}
		s.recvSequence = 1
		if cell.Kind == protocol.CellOpenFail {
			if len(cell.Payload) != 1 {
				return fmtProtocol("invalid OPEN_FAIL")
			}
			s.completeOpen(&RejectError{Code: cell.Payload[0]})
			return nil
		}
		credit, err := parseCredit(cell.Payload)
		if err != nil || credit > MaxInitialWindow {
			return fmtProtocol("invalid OPEN_OK")
		}
		s.sendWindow = credit
		s.sendCredit = credit
		s.signalCreditLocked()
		s.completeOpen(nil)
		return nil
	}

	if cell.Sequence != s.recvSequence {
		return fmtProtocol("unexpected stream sequence")
	}
	if s.recvSequence == protocol.MaxSequence {
		return fmtProtocol("stream sequence exhausted")
	}
	s.recvSequence++

	switch cell.Kind {
	case protocol.CellData:
		if s.peerFIN || uint64(len(cell.Payload)) > s.recvCredit {
			return fmtProtocol("invalid DATA")
		}
		s.recvCredit -= uint64(len(cell.Payload))
		if len(cell.Payload) != 0 {
			s.chunks = append(s.chunks, cell.Payload)
			s.signalReadLocked()
		}
	case protocol.CellFin:
		if len(cell.Payload) != 0 || s.peerFIN {
			return fmtProtocol("invalid FIN")
		}
		s.peerFIN = true
		s.signalReadLocked()
	case protocol.CellReset:
		if len(cell.Payload) > 1 {
			return fmtProtocol("invalid RESET")
		}
		s.readErr = ErrReset
		s.writeErr = ErrReset
		s.signalReadLocked()
		s.signalCreditLocked()
	case protocol.CellWindowUpdate:
		credit, err := parseCredit(cell.Payload)
		if err != nil || credit > s.sendWindow ||
			s.sendCredit > math.MaxUint64-credit || s.sendCredit+credit > s.sendWindow {
			return fmtProtocol("invalid WINDOW_UPDATE")
		}
		s.sendCredit += credit
		s.signalCreditLocked()
	default:
		return fmtProtocol("invalid stream cell")
	}
	return nil
}

func (s *Stream) retireIfComplete() {
	s.mu.Lock()
	retire := s.canRetireLocked()
	s.mu.Unlock()
	if retire {
		s.mux.removeStream(s.id, s)
	}
}

func (s *Stream) canRetireLocked() bool {
	return s.localFIN && s.peerFIN && len(s.chunks) == 0 &&
		s.sendWindow > 0 && s.sendCredit == s.sendWindow &&
		s.recvCredit == s.mux.initialWindow && s.pendingCredit == 0
}

func (s *Stream) takePendingCreditLocked(force bool) uint64 {
	if s.pendingCredit == 0 ||
		(!force && s.pendingCredit < windowUpdateThreshold(s.mux.initialWindow)) {
		return 0
	}
	credit := s.pendingCredit
	s.pendingCredit = 0
	s.recvCredit += credit
	return credit
}

func windowUpdateThreshold(window uint64) uint64 {
	if window == 0 {
		return 0
	}
	threshold := window / windowUpdateDivisor
	if threshold == 0 {
		threshold = 1
	}
	return min(threshold, uint64(maxWindowUpdateThreshold), window)
}

func (s *Stream) sendWindowUpdate(credit uint64) error {
	if credit == 0 {
		return nil
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.mu.Lock()
	if s.writeErr != nil {
		err := s.writeErr
		s.mu.Unlock()
		return err
	}
	sequence, err := s.nextSendSequenceLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	payload := binary.AppendUvarint(nil, credit)
	return s.mux.send(s.mux.ctx, protocol.Cell{Kind: protocol.CellWindowUpdate, StreamID: s.id, Sequence: sequence, Payload: payload})
}

func (s *Stream) sendReset(code byte) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.mu.Lock()
	if s.resetSent {
		s.mu.Unlock()
		return nil
	}
	if s.writeErr != nil && !errors.Is(s.writeErr, ErrProtocol) && !errors.Is(s.writeErr, ErrClosed) {
		s.mu.Unlock()
		return s.writeErr
	}
	s.resetSent = true
	sequence, err := s.nextSendSequenceLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.mux.send(s.mux.ctx, protocol.Cell{Kind: protocol.CellReset, StreamID: s.id, Sequence: sequence, Payload: []byte{code}})
}

func (s *Stream) nextSendSequenceLocked() (uint64, error) {
	if s.sendSequence > protocol.MaxSequence {
		return 0, fmtProtocol("stream sequence exhausted")
	}
	sequence := s.sendSequence
	s.sendSequence++
	return sequence, nil
}

func (s *Stream) fail(err error) {
	if err == nil {
		err = ErrClosed
	}
	s.mu.Lock()
	if s.readErr == nil {
		s.readErr = err
	}
	if s.writeErr == nil {
		s.writeErr = err
	}
	s.signalReadLocked()
	s.signalCreditLocked()
	s.completeOpen(err)
	s.mu.Unlock()
}

func (s *Stream) completeOpen(err error) {
	s.openOnce.Do(func() { s.openResult <- err })
}

func (s *Stream) signalReadLocked() {
	close(s.readNotify)
	s.readNotify = make(chan struct{})
}

func (s *Stream) signalCreditLocked() {
	close(s.creditNotify)
	s.creditNotify = make(chan struct{})
}

func fmtProtocol(detail string) error {
	return errors.Join(ErrProtocol, errors.New(detail))
}
