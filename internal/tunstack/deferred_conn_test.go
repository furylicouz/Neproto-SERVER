package tunstack

import (
	"context"
	"net"
	"testing"
	"time"

	np2proxy "neproto.local/chameleon/internal/proxy"
)

func TestDeferredStreamCloseCancelsBlockedOpen(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	connection := newDeferredStreamConn(
		func(ctx context.Context, _ []byte) (streamConnection, error) {
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return nil, context.DeadlineExceeded
			}
		},
		np2proxy.Target{Host: "198.51.100.9", Port: 443},
		&net.TCPAddr{},
		&net.TCPAddr{},
		nil,
	)
	writeDone := make(chan error, 1)
	go func() {
		_, err := connection.Write([]byte{1})
		writeDone <- err
	}()
	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- connection.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close error=%v", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(release)
		<-closeDone
		t.Fatal("Close did not cancel a blocked deferred OPEN")
	}
	select {
	case <-writeDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("blocked Write did not return after Close")
	}
}
