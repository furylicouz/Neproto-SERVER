package session

import (
	"errors"
	"io"
)

var ErrCarrierLost = errors.New("authenticated carrier lost")

// TransportStream annotates stream errors with ErrCarrierLost only when the
// parent Mux is terminal. A plain io.EOF while the Mux is alive remains a real
// per-flow FIN and must not trigger continuity migration.
type TransportStream struct {
	stream *Stream
	mux    *Mux
}

func NewTransportStream(mux *Mux, stream *Stream) (*TransportStream, error) {
	if mux == nil || stream == nil || stream.mux != mux {
		return nil, ErrInvalidConfig
	}
	return &TransportStream{stream: stream, mux: mux}, nil
}

func (s *TransportStream) Read(destination []byte) (int, error) {
	if s == nil || s.stream == nil || s.mux == nil {
		return 0, ErrInvalidConfig
	}
	n, err := s.stream.Read(destination)
	return n, s.classify(err)
}

func (s *TransportStream) Write(payload []byte) (int, error) {
	if s == nil || s.stream == nil || s.mux == nil {
		return 0, ErrInvalidConfig
	}
	n, err := s.stream.Write(payload)
	return n, s.classify(err)
}

func (s *TransportStream) CloseWrite() error {
	if s == nil || s.stream == nil || s.mux == nil {
		return ErrInvalidConfig
	}
	return s.classify(s.stream.CloseWrite())
}

func (s *TransportStream) Close() error {
	if s == nil || s.stream == nil {
		return nil
	}
	return s.stream.Close()
}

func (s *TransportStream) ID() uint64 {
	if s == nil || s.stream == nil {
		return 0
	}
	return s.stream.ID()
}

func (s *TransportStream) classify(err error) error {
	if err == nil || s.mux.Err() == nil || errors.Is(err, ErrCarrierLost) {
		return err
	}
	return errors.Join(ErrCarrierLost, err)
}

var _ io.ReadWriteCloser = (*TransportStream)(nil)
