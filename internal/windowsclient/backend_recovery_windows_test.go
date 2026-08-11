//go:build windows

package windowsclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recoveryRouteManager struct {
	err   error
	calls int
}

func (m *recoveryRouteManager) Recover(context.Context) error {
	m.calls++
	return m.err
}
func (*recoveryRouteManager) Apply(context.Context, string, int, []string) error { return nil }
func (*recoveryRouteManager) Cleanup(context.Context) error                      { return nil }

func TestWindowsBackendRetriesDeferredStartupRecovery(t *testing.T) {
	routes := &recoveryRouteManager{}
	backend := newWindowsBackend(routes, errors.New("startup recovery timed out"))

	if err := backend.ensureRoutesRecovered(context.Background()); err != nil {
		t.Fatal(err)
	}
	if routes.calls != 1 || backend.recoveryErr != nil {
		t.Fatalf("calls=%d recoveryErr=%v", routes.calls, backend.recoveryErr)
	}
}

func TestWindowsBackendBlocksConnectWhileDeferredRecoveryStillFails(t *testing.T) {
	routes := &recoveryRouteManager{err: errors.New("rollback failed")}
	backend := newWindowsBackend(routes, errors.New("startup recovery timed out"))

	if err := backend.ensureRoutesRecovered(context.Background()); err == nil {
		t.Fatal("expected route recovery failure")
	}
	if routes.calls != 1 || backend.recoveryErr == nil {
		t.Fatalf("calls=%d recoveryErr=%v", routes.calls, backend.recoveryErr)
	}
}

type blockingCleanupRouteManager struct {
	started chan struct{}
	release chan struct{}
}

func (*blockingCleanupRouteManager) Recover(context.Context) error { return nil }
func (*blockingCleanupRouteManager) Apply(context.Context, string, int, []string) error {
	return nil
}
func (m *blockingCleanupRouteManager) Cleanup(ctx context.Context) error {
	close(m.started)
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWindowsBackendDisconnectReturnsWhileRouteCleanupContinues(t *testing.T) {
	routes := &blockingCleanupRouteManager{started: make(chan struct{}), release: make(chan struct{})}
	backend := newWindowsBackend(routes, nil)

	started := time.Now()
	if err := backend.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("disconnect waited for route cleanup: %v", elapsed)
	}
	select {
	case <-routes.started:
	case <-time.After(time.Second):
		t.Fatal("route cleanup was not started")
	}

	waited := make(chan error, 1)
	go func() { waited <- backend.WaitForCleanup(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("cleanup wait returned early: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(routes.release)
	if err := <-waited; err != nil {
		t.Fatal(err)
	}
}
