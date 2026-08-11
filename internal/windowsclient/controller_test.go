package windowsclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/onboarding"
)

type fakeBackend struct {
	mu          sync.Mutex
	started     chan struct{}
	release     chan struct{}
	connectErr  error
	disconnects int
	catalog     []byte
}

func (*fakeBackend) SetRoutes([]byte) error { return nil }

func (b *fakeBackend) Connect(ctx context.Context, profile []byte, secret string) (BackendStatus, error) {
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
		return BackendStatus{}, ctx.Err()
	}
	if b.connectErr != nil {
		return BackendStatus{}, b.connectErr
	}
	return BackendStatus{
		Carrier: "https", ServerAddresses: []string{"1.1.1.1"},
		NP2ConnectMilliseconds: 840, WindowsSetupMilliseconds: 160,
	}, nil
}
func (b *fakeBackend) Disconnect(context.Context) error {
	b.mu.Lock()
	b.disconnects++
	b.mu.Unlock()
	return nil
}
func (*fakeBackend) Snapshot() BackendStatus {
	return BackendStatus{Carrier: "https", DownloadBytesPerSecond: 42}
}
func (b *fakeBackend) FetchCatalog(context.Context) ([]byte, error) {
	if len(b.catalog) == 0 {
		return nil, errors.New("unavailable")
	}
	return append([]byte(nil), b.catalog...), nil
}

func TestControllerVerifiesAndAppliesClusterCatalog(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.TempDir(), xorProtector(0x45))
	if err != nil {
		t.Fatal(err)
	}
	uri, err := onboarding.EncodeURI(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x55)), Name: "Primary",
		ServerIdentity: "vpn.example.com", ServerAddresses: []string{"1.1.1.1"}, HTTPSPath: "/private/https/session",
		WebRTCPath: "/private/webrtc/offer", HTTP3Path: "/private/http3/session", MaxParallelCarriers: 3,
		EnableConstellation: true, ClusterID: "cluster-one", CatalogPublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
		Profile: "web", Secret: base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x56)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Import(uri); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	catalog := cluster.Catalog{Version: cluster.CatalogVersion, ClusterID: "cluster-one", Revision: 1,
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), UserID: "user-one",
		Servers:     []cluster.CatalogServer{{NodeID: "master", Name: "Primary", Region: "Russia", ServerIdentity: "vpn.example.com", ServerAddresses: []string{"1.1.1.1"}, Enabled: true}, {NodeID: "edge", Name: "Edge", Region: "Netherlands", ServerIdentity: "nl.example.com", ServerAddresses: []string{"8.8.8.8"}, Enabled: true}},
		Permissions: cluster.CatalogPermissions{AllowClientRoutes: true}}
	catalog, err = cluster.SignCatalog(catalog, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cluster.EncodeCatalogEnvelope(catalog)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{started: make(chan struct{}), release: make(chan struct{}), catalog: raw}
	controller := NewController(store, backend)
	if err := controller.Connect(); err != nil {
		t.Fatal(err)
	}
	close(backend.release)
	waitForState(t, controller, StateConnected)
	state, err := controller.SyncCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 1 || len(store.Profiles()) != 2 {
		t.Fatalf("state=%+v profiles=%+v", state, store.Profiles())
	}
}

func TestControllerCatalogFailureIsLoggedWithoutFailingTunnel(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.TempDir(), xorProtector(0x46))
	if err != nil {
		t.Fatal(err)
	}
	uri, err := onboarding.EncodeURI(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x65)), Name: "Primary",
		ServerIdentity: "vpn.example.com", ServerAddresses: []string{"1.1.1.1"}, HTTPSPath: "/private/https/session",
		WebRTCPath: "/private/webrtc/offer", HTTP3Path: "/private/http3/session", MaxParallelCarriers: 3,
		EnableConstellation: true, ClusterID: "cluster-one", CatalogPublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
		Profile: "web", Secret: base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x66)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Import(uri); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{started: make(chan struct{}), release: make(chan struct{})}
	controller := NewController(store, backend)
	if err := controller.Connect(); err != nil {
		t.Fatal(err)
	}
	close(backend.release)
	waitForState(t, controller, StateConnected)

	if _, err := controller.SyncCatalog(); err == nil {
		t.Fatal("catalog synchronization unexpectedly succeeded")
	}
	if status := controller.Status(); status.State != StateConnected || status.LastError != "" {
		t.Fatalf("catalog failure changed tunnel status: %+v", status)
	}
	logs := controller.Logs(10)
	if len(logs) == 0 || logs[len(logs)-1].Level != "warning" ||
		!strings.Contains(logs[len(logs)-1].Message, "Cluster catalogue synchronization failed") {
		t.Fatalf("catalog failure was not logged as a warning: %+v", logs)
	}
}

func TestControllerConnectsAsynchronouslyAndDisconnects(t *testing.T) {
	store, err := OpenStore(t.TempDir(), xorProtector(0x11))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Import(testOnboardingURI(t, "Primary", "vpn.example.com", "1.1.1.1", 0x42)); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{started: make(chan struct{}), release: make(chan struct{})}
	controller := NewController(store, backend)
	if err := controller.Connect(); err != nil {
		t.Fatal(err)
	}
	<-backend.started
	if got := controller.Status().State; got != StateConnecting {
		t.Fatalf("state=%q", got)
	}
	close(backend.release)
	waitForState(t, controller, StateConnected)
	if controller.Status().DownloadBytesPerSecond != 42 {
		t.Fatal("backend metrics not published")
	}
	logs := controller.Logs(10)
	if len(logs) == 0 || !strings.Contains(logs[len(logs)-1].Message, "NP/2 840 ms") ||
		!strings.Contains(logs[len(logs)-1].Message, "Windows 160 ms") {
		t.Fatalf("connection timing missing from logs: %+v", logs)
	}
	if err := controller.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if got := controller.Status().State; got != StateStopped {
		t.Fatalf("state=%q", got)
	}
}

func TestControllerReportsConnectFailureWithoutSecret(t *testing.T) {
	store, err := OpenStore(t.TempDir(), xorProtector(0x11))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Import(testOnboardingURI(t, "Primary", "vpn.example.com", "1.1.1.1", 0x51)); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{started: make(chan struct{}), release: make(chan struct{}), connectErr: errors.New("carrier failed")}
	controller := NewController(store, backend)
	if err := controller.Connect(); err != nil {
		t.Fatal(err)
	}
	close(backend.release)
	waitForState(t, controller, StateFailed)
	if controller.Status().LastError != "carrier failed" {
		t.Fatalf("error=%q", controller.Status().LastError)
	}
}

func waitForState(t *testing.T, controller *Controller, state State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if controller.Status().State == state {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state=%q want %q", controller.Status().State, state)
}
