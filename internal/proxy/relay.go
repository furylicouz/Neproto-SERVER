package proxy

import (
	"context"
	"errors"
	"io"
	"net"

	"neproto.local/chameleon/internal/session"
)

type relayStream interface {
	io.ReadWriteCloser
	CloseWrite() error
}

func relayTarget(ctx context.Context, stream relayStream, target net.Conn) error {
	defer target.Close()
	defer stream.Close()
	return relayOpenTarget(ctx, stream, target)
}

func relayDuplex(ctx context.Context, left, right DuplexStream) error {
	defer left.Close()
	defer right.Close()
	stop := context.AfterFunc(ctx, func() {
		_ = left.Close()
		_ = right.Close()
	})
	defer stop()
	results := make(chan error, 2)
	go func() {
		_, err := io.Copy(right, left)
		_ = right.CloseWrite()
		results <- err
	}()
	go func() {
		_, err := io.Copy(left, right)
		_ = left.CloseWrite()
		results <- err
	}()
	first := <-results
	if relayFailed(first) {
		_ = left.Close()
		_ = right.Close()
	}
	second := <-results
	if relayFailed(first) {
		return first
	}
	if relayFailed(second) {
		return second
	}
	return nil
}

func relayOpenTarget(ctx context.Context, stream relayStream, target io.ReadWriteCloser) error {
	stop := context.AfterFunc(ctx, func() {
		_ = target.Close()
		_ = stream.Close()
	})
	defer stop()

	results := make(chan error, 2)
	go func() {
		_, err := io.Copy(target, stream)
		if closeWriter, ok := target.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
		results <- err
	}()
	go func() {
		_, err := io.Copy(stream, target)
		_ = stream.CloseWrite()
		results <- err
	}()

	var first error
	select {
	case first = <-results:
		if relayFailed(first) {
			_ = target.Close()
			_ = stream.Close()
		}
	case <-ctx.Done():
		_ = target.Close()
		_ = stream.Close()
		first = ctx.Err()
	}
	second := <-results
	if relayFailed(first) {
		return first
	}
	if relayFailed(second) {
		return second
	}
	return nil
}

func relayFailed(err error) bool {
	return err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, session.ErrClosed) && !errors.Is(err, session.ErrReset)
}
