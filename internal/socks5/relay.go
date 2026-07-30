package socks5

import (
	"context"
	"errors"
	"io"
	"net"
)

type closeWriter interface {
	CloseWrite() error
}

func relay(ctx context.Context, local net.Conn, upstream io.ReadWriteCloser) error {
	errorsChannel := make(chan error, 2)
	go copyHalf(errorsChannel, upstream, local)
	go copyHalf(errorsChannel, local, upstream)

	var first error
	select {
	case first = <-errorsChannel:
		if first != nil {
			_ = local.Close()
			_ = upstream.Close()
		}
	case <-ctx.Done():
		_ = local.Close()
		_ = upstream.Close()
		first = ctx.Err()
	}
	second := <-errorsChannel
	if significantRelayError(first) {
		return first
	}
	if significantRelayError(second) {
		return second
	}
	return nil
}

func copyHalf(result chan<- error, destination io.Writer, source io.Reader) {
	_, err := io.Copy(destination, source)
	if closeable, ok := destination.(closeWriter); ok {
		_ = closeable.CloseWrite()
	}
	result <- err
}

func significantRelayError(err error) bool {
	return err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled)
}
