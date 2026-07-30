package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

func TestInstalledClusterPeerRequiresAuthenticatedNP2Session(t *testing.T) {
	root := t.TempDir()
	writeTestInstallation(t, root)
	manager, err := admin.Open(root, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddUser("Bootstrap", "web"); err != nil {
		t.Fatal(err)
	}
	writeAttestationServerConfig(t, root)
	state, err := manager.EnsureLocalCluster()
	if err != nil {
		t.Fatal(err)
	}
	material, err := manager.NewClusterPeerMaterial()
	if err != nil {
		t.Fatal(err)
	}
	endpoint := admin.ClusterPeerEndpoint{
		NodeID: "edge-01", ServerIdentity: "edge.example.com", ServerAddresses: []string{"1.1.1.1"},
		HTTPSPath: "/" + strings.Repeat("1", 48), WebRTCPath: "/" + strings.Repeat("2", 48),
		HTTP3Path: "/" + strings.Repeat("3", 48),
	}
	if err := manager.InstallClusterPeer(state.Nodes[0].ID, endpoint, material); err != nil {
		t.Fatal(err)
	}

	if err := attestInstalledClusterPeer(manager, endpoint.NodeID, func(context.Context, config.Client) (*session.Authenticated, error) {
		return nil, nil
	}); err == nil {
		t.Fatal("nil authenticated session was accepted")
	}
	called := false
	if err := attestInstalledClusterPeer(manager, endpoint.NodeID, func(_ context.Context, peer config.Client) (*session.Authenticated, error) {
		called = true
		if peer.ServerIdentity != endpoint.ServerIdentity {
			t.Fatalf("peer identity=%q", peer.ServerIdentity)
		}
		if peer.EnableConstellation {
			t.Fatal("peer attestation used client constellation admission")
		}
		return &session.Authenticated{Mux: newAttestationMux(t)}, nil
	}); err != nil || !called {
		t.Fatalf("authenticated attestation called=%v err=%v", called, err)
	}
}

func writeAttestationServerConfig(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, "etc", "neproto")
	document := map[string]any{
		"server_identity": "vpn.example.com", "credential_directory": filepath.Join(directory, "users", "active"),
		"listen": "127.0.0.1:9080", "metrics_listen": "127.0.0.1:9464",
		"https_path": "/" + strings.Repeat("1", 48), "webrtc_path": "/" + strings.Repeat("2", 48),
		"enable_http3": true, "enable_webrtc_datagrams": true, "enable_http3_datagrams": true,
		"http3_listen": ":443", "http3_path": "/" + strings.Repeat("3", 48),
		"http3_cert_file": filepath.Join(directory, "fullchain.pem"), "http3_key_file": filepath.Join(directory, "privkey.pem"),
		"udp_port_min": 40000, "udp_port_max": 40100,
		"max_cover_overhead_percent": 20, "initial_window_bytes": 1048576,
		"max_streams": 256, "max_sessions": 64, "max_webrtc_peers": 64, "max_http3_sessions": 64,
		"max_target_connections": 256, "dial_timeout": "10s", "gather_timeout": "8s", "connect_timeout": "12s",
		"http3_handshake_timeout": "5s", "http3_idle_timeout": "45s", "shutdown_timeout": "10s",
		"enable_constellation": true, "enable_forward_secrecy": true,
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "server.json"), raw, 0o640); err != nil {
		t.Fatal(err)
	}
}

func newAttestationMux(t *testing.T) *session.Mux {
	t.Helper()
	transport := newAttestationCarrier()
	typeMap, err := protocol.NewTypeMap([32]byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	mux, err := session.New(session.Config{
		Role: session.RoleClient, Carrier: transport, TypeMap: typeMap,
		InitialWindow: 64 * 1024, MaxStreams: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mux
}

type attestationCarrier struct {
	done chan struct{}
	once sync.Once
}

var _ carrier.Carrier = (*attestationCarrier)(nil)

func newAttestationCarrier() *attestationCarrier {
	return &attestationCarrier{done: make(chan struct{})}
}
func (*attestationCarrier) Send(context.Context, []byte) error { return nil }
func (value *attestationCarrier) Receive(ctx context.Context) ([]byte, error) {
	select {
	case <-value.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (value *attestationCarrier) Close() error {
	value.once.Do(func() { close(value.done) })
	return nil
}
func (*attestationCarrier) Kind() protocol.CarrierKind { return protocol.CarrierHTTPS }
