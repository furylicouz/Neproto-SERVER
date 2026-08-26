package tunstack

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestDarwinUTUNReadStripsIPv4HeaderAndAcceptsFullMTU(t *testing.T) {
	payload := make([]byte, 1500)
	payload[0] = 0x45
	for i := 1; i < len(payload); i++ {
		payload[i] = byte(i)
	}
	rw := &recordingReadWriteCloser{
		reader: bytes.NewReader(append([]byte{0, 0, 0, darwinAFInet}, payload...)),
	}
	framer, err := newDarwinUTUNFramer(rw, 1500)
	if err != nil {
		t.Fatalf("new framer: %v", err)
	}

	got := make([]byte, 1500)
	n, err := framer.Read(got)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("payload length=%d, want %d", n, len(payload))
	}
	if !bytes.Equal(got[:n], payload) {
		t.Fatal("IPv4 payload changed while stripping the utun header")
	}
}

func TestDarwinUTUNReadStripsIPv6Header(t *testing.T) {
	payload := []byte{0x60, 0, 0, 0, 0, 0, 59, 64}
	rw := &recordingReadWriteCloser{
		reader: bytes.NewReader(append([]byte{0, 0, 0, darwinAFInet6}, payload...)),
	}
	framer, err := newDarwinUTUNFramer(rw, 1280)
	if err != nil {
		t.Fatalf("new framer: %v", err)
	}

	got := make([]byte, 1280)
	n, err := framer.Read(got)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got[:n], payload) {
		t.Fatalf("payload=%x, want %x", got[:n], payload)
	}
}

func TestDarwinUTUNReadRejectsFamilyVersionMismatch(t *testing.T) {
	frame := append([]byte{0, 0, 0, darwinAFInet6}, []byte{0x45, 0, 0, 20}...)
	rw := &recordingReadWriteCloser{reader: bytes.NewReader(frame)}
	framer, err := newDarwinUTUNFramer(rw, 1280)
	if err != nil {
		t.Fatalf("new framer: %v", err)
	}

	if _, err := framer.Read(make([]byte, 1280)); !errors.Is(err, ErrInvalidUTUNPacket) {
		t.Fatalf("error=%v, want %v", err, ErrInvalidUTUNPacket)
	}
}

func TestDarwinUTUNWriteAddsAddressFamilyHeader(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
		family byte
	}{
		{name: "IPv4", packet: []byte{0x45, 0, 0, 20}, family: darwinAFInet},
		{name: "IPv6", packet: []byte{0x60, 0, 0, 0}, family: darwinAFInet6},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rw := &recordingReadWriteCloser{}
			framer, err := newDarwinUTUNFramer(rw, 1280)
			if err != nil {
				t.Fatalf("new framer: %v", err)
			}

			n, err := framer.Write(test.packet)
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			if n != len(test.packet) {
				t.Fatalf("payload length=%d, want %d", n, len(test.packet))
			}
			want := append([]byte{0, 0, 0, test.family}, test.packet...)
			if !bytes.Equal(rw.writes.Bytes(), want) {
				t.Fatalf("utun frame=%x, want %x", rw.writes.Bytes(), want)
			}
		})
	}
}

func TestDarwinUTUNWriteRejectsUnknownIPVersion(t *testing.T) {
	rw := &recordingReadWriteCloser{}
	framer, err := newDarwinUTUNFramer(rw, 1280)
	if err != nil {
		t.Fatalf("new framer: %v", err)
	}

	if _, err := framer.Write([]byte{0x70, 0, 0, 0}); !errors.Is(err, ErrInvalidUTUNPacket) {
		t.Fatalf("error=%v, want %v", err, ErrInvalidUTUNPacket)
	}
	if rw.writes.Len() != 0 {
		t.Fatalf("wrote %d bytes for an invalid packet", rw.writes.Len())
	}
}

func TestDarwinUTUNWriteReportsShortPayloadWrite(t *testing.T) {
	rw := &recordingReadWriteCloser{writeLimit: 6}
	framer, err := newDarwinUTUNFramer(rw, 1280)
	if err != nil {
		t.Fatalf("new framer: %v", err)
	}

	n, err := framer.Write([]byte{0x45, 0, 0, 20})
	if n != 2 {
		t.Fatalf("payload length=%d, want 2", n)
	}
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error=%v, want %v", err, io.ErrShortWrite)
	}
}

type recordingReadWriteCloser struct {
	reader     io.Reader
	writes     bytes.Buffer
	writeLimit int
	closed     bool
}

func (rw *recordingReadWriteCloser) Read(p []byte) (int, error) {
	if rw.reader == nil {
		return 0, io.EOF
	}
	return rw.reader.Read(p)
}

func (rw *recordingReadWriteCloser) Write(p []byte) (int, error) {
	if rw.writeLimit > 0 && len(p) > rw.writeLimit {
		_, _ = rw.writes.Write(p[:rw.writeLimit])
		return rw.writeLimit, nil
	}
	return rw.writes.Write(p)
}

func (rw *recordingReadWriteCloser) Close() error {
	rw.closed = true
	return nil
}
