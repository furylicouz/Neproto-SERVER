package tunstack

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/fdbased"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
)

const (
	darwinUTUNHeaderSize = 4
	darwinAFInet         = 2
	darwinAFInet6        = 30
)

// ErrInvalidUTUNPacket identifies a malformed or unsupported Darwin utun
// record without exposing packet contents.
var ErrInvalidUTUNPacket = errors.New("invalid Darwin utun packet")

// darwinUTUNFramer translates between Darwin's utun record format and raw IP
// packets. Darwin prefixes every record with a big-endian, 32-bit address
// family. The generic offset support in tun2socks treats those bytes as zero
// padding and counts them against the MTU, which corrupts outbound records and
// drops full-MTU inbound packets.
type darwinUTUNFramer struct {
	rw io.ReadWriteCloser

	readMu         sync.Mutex
	readBuffer     []byte
	pendingReadErr error

	writeMu     sync.Mutex
	writeBuffer []byte

	closeOnce sync.Once
	closeErr  error
}

func newDarwinUTUNFramer(rw io.ReadWriteCloser, mtu uint32) (*darwinUTUNFramer, error) {
	if rw == nil || mtu < minimumMTU || mtu > maximumMTU {
		return nil, ErrInvalidStackConfig
	}
	frameSize := darwinUTUNHeaderSize + int(mtu)
	return &darwinUTUNFramer{
		rw:          rw,
		readBuffer:  make([]byte, frameSize),
		writeBuffer: make([]byte, frameSize),
	}, nil
}

func (f *darwinUTUNFramer) Read(packet []byte) (int, error) {
	f.readMu.Lock()
	defer f.readMu.Unlock()

	if f.pendingReadErr != nil {
		err := f.pendingReadErr
		f.pendingReadErr = nil
		return 0, err
	}

	n, readErr := f.rw.Read(f.readBuffer)
	if n == 0 {
		return 0, readErr
	}
	if n < darwinUTUNHeaderSize+1 || n > len(f.readBuffer) {
		return 0, ErrInvalidUTUNPacket
	}

	payload := f.readBuffer[darwinUTUNHeaderSize:n]
	wantFamily, err := darwinFamilyForPacket(payload)
	if err != nil || binary.BigEndian.Uint32(f.readBuffer[:darwinUTUNHeaderSize]) != wantFamily {
		return 0, ErrInvalidUTUNPacket
	}
	if len(packet) < len(payload) {
		return 0, io.ErrShortBuffer
	}

	copy(packet, payload)
	if readErr != nil {
		// Preserve a valid final packet. The endpoint receives the terminal error
		// on its next read and can then stop its dispatch loop cleanly.
		f.pendingReadErr = readErr
	}
	return len(payload), nil
}

func (f *darwinUTUNFramer) Write(packet []byte) (int, error) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()

	if len(packet) == 0 || len(packet) > len(f.writeBuffer)-darwinUTUNHeaderSize {
		return 0, ErrInvalidUTUNPacket
	}
	family, err := darwinFamilyForPacket(packet)
	if err != nil {
		return 0, err
	}

	binary.BigEndian.PutUint32(f.writeBuffer[:darwinUTUNHeaderSize], family)
	copy(f.writeBuffer[darwinUTUNHeaderSize:], packet)
	frame := f.writeBuffer[:darwinUTUNHeaderSize+len(packet)]
	n, writeErr := f.rw.Write(frame)
	if n < 0 || n > len(frame) {
		return 0, io.ErrShortWrite
	}
	payloadWritten := n - darwinUTUNHeaderSize
	if payloadWritten < 0 {
		payloadWritten = 0
	}
	if payloadWritten > len(packet) {
		payloadWritten = len(packet)
	}
	if writeErr == nil && n != len(frame) {
		writeErr = io.ErrShortWrite
	}
	return payloadWritten, writeErr
}

func (f *darwinUTUNFramer) Close() error {
	f.closeOnce.Do(func() {
		f.closeErr = f.rw.Close()
	})
	return f.closeErr
}

func darwinFamilyForPacket(packet []byte) (uint32, error) {
	if len(packet) == 0 {
		return 0, ErrInvalidUTUNPacket
	}
	switch packet[0] >> 4 {
	case 4:
		return darwinAFInet, nil
	case 6:
		return darwinAFInet6, nil
	default:
		return 0, ErrInvalidUTUNPacket
	}
}

type darwinUTUNDevice struct {
	*iobased.Endpoint
	framer *darwinUTUNFramer
	fd     int
	once   sync.Once
}

func newDarwinUTUNDevice(fd int, mtu uint32) (device.Device, error) {
	if fd < 0 {
		return nil, ErrInvalidStackConfig
	}
	file := os.NewFile(uintptr(fd), strconv.Itoa(fd))
	if file == nil {
		return nil, ErrInvalidStackConfig
	}
	framer, err := newDarwinUTUNFramer(file, mtu)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	endpoint, err := iobased.New(framer, mtu, 0)
	if err != nil {
		_ = framer.Close()
		return nil, err
	}
	return &darwinUTUNDevice{Endpoint: endpoint, framer: framer, fd: fd}, nil
}

func (d *darwinUTUNDevice) Name() string {
	return strconv.Itoa(d.fd)
}

func (d *darwinUTUNDevice) Type() string {
	return fdbased.Driver
}

func (d *darwinUTUNDevice) Close() {
	d.once.Do(func() {
		// Closing the file first unblocks the endpoint's inbound read loop.
		_ = d.framer.Close()
		d.Endpoint.Close()
	})
}

var _ device.Device = (*darwinUTUNDevice)(nil)
