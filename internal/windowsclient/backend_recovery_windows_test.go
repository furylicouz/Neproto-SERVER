//go:build windows

package windowsclient

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/onboarding"
)

type recoveryRouteManager struct {
	err   error
	calls int
}

func (m *recoveryRouteManager) Recover(context.Context) error {
	m.calls++
	return m.err
}
func (*recoveryRouteManager) PrepareEndpoints(context.Context, []string) error  { return nil }
func (*recoveryRouteManager) ActivateTunnel(context.Context, string, int) error { return nil }
func (*recoveryRouteManager) Cleanup(context.Context) error                     { return nil }

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

func (*blockingCleanupRouteManager) Recover(context.Context) error                     { return nil }
func (*blockingCleanupRouteManager) PrepareEndpoints(context.Context, []string) error  { return nil }
func (*blockingCleanupRouteManager) ActivateTunnel(context.Context, string, int) error { return nil }
func (m *blockingCleanupRouteManager) Cleanup(ctx context.Context) error {
	close(m.started)
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type orderedRouteManager struct{ events *[]string }

func (*orderedRouteManager) Recover(context.Context) error { return nil }
func (m *orderedRouteManager) PrepareEndpoints(context.Context, []string) error {
	*m.events = append(*m.events, "prepare")
	return nil
}
func (m *orderedRouteManager) ActivateTunnel(context.Context, string, int) error {
	*m.events = append(*m.events, "activate")
	return nil
}
func (m *orderedRouteManager) Cleanup(context.Context) error {
	*m.events = append(*m.events, "cleanup")
	return nil
}

type prepareFailureRouteManager struct {
	events     *[]string
	cleanupErr error
}

func (*prepareFailureRouteManager) Recover(context.Context) error { return nil }
func (m *prepareFailureRouteManager) PrepareEndpoints(context.Context, []string) error {
	*m.events = append(*m.events, "prepare")
	return errors.New("prepare failed")
}
func (m *prepareFailureRouteManager) ActivateTunnel(context.Context, string, int) error {
	*m.events = append(*m.events, "activate")
	return nil
}
func (m *prepareFailureRouteManager) Cleanup(context.Context) error {
	*m.events = append(*m.events, "cleanup")
	return m.cleanupErr
}

func TestWindowsBackendPreparesEndpointBeforeDialAndRollsBackDialFailure(t *testing.T) {
	events := []string{}
	backend := newWindowsBackend(&orderedRouteManager{events: &events}, nil)
	backend.startNP2 = func(string, string) error {
		events = append(events, "dial")
		return errors.New("dial failed")
	}
	secret := base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x42))
	_, profile, secret, err := profileFromOnboarding(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x24)),
		Name: "Primary", ServerIdentity: "nepopus.lyntragram.ru", ServerAddresses: []string{"37.252.23.223"},
		HTTPSPath: "/" + strings.Repeat("1", 48), WebRTCPath: "/" + strings.Repeat("2", 48), HTTP3Path: "/" + strings.Repeat("3", 48),
		Secret: secret,
	}, "00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := backend.Connect(context.Background(), profile, secret); err == nil {
		t.Fatal("expected dial failure")
	} else if err.Error() != "dial failed" {
		t.Fatalf("unexpected pre-dial failure: %v", err)
	}
	want := []string{"prepare", "dial", "cleanup"}
	if len(events) != len(want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events=%v want=%v", events, want)
		}
	}
}

func TestWindowsBackendCleansUpFailedEndpointPreparation(t *testing.T) {
	events := []string{}
	cleanupErr := errors.New("cleanup failed")
	backend := newWindowsBackend(&prepareFailureRouteManager{events: &events, cleanupErr: cleanupErr}, nil)
	secret := base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x42))
	_, profile, secret, err := profileFromOnboarding(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x24)),
		Name: "Primary", ServerIdentity: "nepopus.lyntragram.ru", ServerAddresses: []string{"37.252.23.223"},
		HTTPSPath: "/" + strings.Repeat("1", 48), WebRTCPath: "/" + strings.Repeat("2", 48), HTTP3Path: "/" + strings.Repeat("3", 48),
		Secret: secret,
	}, "00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := backend.Connect(context.Background(), profile, secret); err == nil {
		t.Fatal("expected endpoint preparation failure")
	}
	if want := []string{"prepare", "cleanup"}; len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("events=%v want=%v", events, want)
	}
	backend.mu.Lock()
	recoveryErr := backend.recoveryErr
	backend.mu.Unlock()
	if !errors.Is(recoveryErr, cleanupErr) {
		t.Fatalf("recovery error=%v want %v", recoveryErr, cleanupErr)
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
