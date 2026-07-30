package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

// NewUDPTerminalStream creates one fixed-target UDP relay represented by the
// same bounded record stream used across an inter-node NP/2 hop.
func NewUDPTerminalStream(ctx context.Context, target Target, maximumPayload uint64, idleTimeout time.Duration) (DuplexStream, error) {
	return newUDPTerminalStream(
		ctx, target, maximumPayload, idleTimeout,
		DestinationPolicy{}, nil, nil, nil,
	)
}

func newUDPTerminalStream(
	ctx context.Context,
	target Target,
	maximumPayload uint64,
	idleTimeout time.Duration,
	policy DestinationPolicy,
	resolver Resolver,
	listener PacketListener,
	stats *UDPStatistics,
) (DuplexStream, error) {
	if ctx == nil || maximumPayload < 1200 || maximumPayload > MaxUDPDatagramPayload {
		return nil, ErrInvalidConfig
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := policy.Resolve(ctx, target, resolver)
	if err != nil || len(addresses) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, ErrResolution
	}
	address := netip.AddrPortFrom(addresses[0], target.Port)
	if listener == nil {
		listener = &net.ListenConfig{}
	}
	packetConnection, err := listener.ListenPacket(ctx, "udp", ":0")
	if err != nil {
		return nil, err
	}
	external, internal := newMemoryDuplexPair()
	association, err := NewUDPAssociation(internal, CommandUDPFixed, target, maximumPayload)
	if err != nil {
		_ = packetConnection.Close()
		_ = external.Close()
		_ = internal.Close()
		return nil, err
	}
	if idleTimeout <= 0 {
		idleTimeout = defaultUDPIdleTimeout
	}
	go func() {
		_ = (Server{UDPStats: stats}).relayUDPAssociation(
			ctx, association, packetConnection, CommandUDPFixed, address,
			maximumPayload, idleTimeout,
		)
		_ = internal.Close()
	}()
	return external, nil
}

type memoryDuplex struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	once   sync.Once
}

func newMemoryDuplexPair() (*memoryDuplex, *memoryDuplex) {
	leftReader, rightWriter := io.Pipe()
	rightReader, leftWriter := io.Pipe()
	return &memoryDuplex{reader: leftReader, writer: leftWriter}, &memoryDuplex{reader: rightReader, writer: rightWriter}
}

func (stream *memoryDuplex) Read(payload []byte) (int, error) {
	if stream == nil || stream.reader == nil {
		return 0, net.ErrClosed
	}
	return stream.reader.Read(payload)
}

func (stream *memoryDuplex) Write(payload []byte) (int, error) {
	if stream == nil || stream.writer == nil {
		return 0, net.ErrClosed
	}
	return stream.writer.Write(payload)
}

func (stream *memoryDuplex) CloseWrite() error {
	if stream == nil || stream.writer == nil {
		return net.ErrClosed
	}
	return stream.writer.Close()
}

func (stream *memoryDuplex) Close() error {
	if stream == nil {
		return nil
	}
	var result error
	stream.once.Do(func() {
		result = errors.Join(stream.writer.Close(), stream.reader.Close())
	})
	return result
}
