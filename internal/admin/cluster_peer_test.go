package admin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/clusterrelay"
	"neproto.local/chameleon/internal/config"
)

func TestManagerInstallsAuthenticatedClusterPeerRuntime(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	now := time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC)
	random := bytes.NewReader(bytes.Repeat([]byte{0x5a}, 4096))
	manager, err := Open(root, random, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddUser("Bootstrap", "web"); err != nil {
		t.Fatal(err)
	}
	writeValidClusterServerConfig(t, root)
	master := cluster.Node{
		ID: "master", Name: "Master", Region: "Moscow", Roles: []cluster.NodeRole{cluster.RoleMaster, cluster.RoleIngress},
		PublicIdentity: "vpn.example.com", PublicAddresses: []string{"8.8.8.8"}, NP2Endpoint: "vpn.example.com:443",
		HTTPSPath: hexPath('1'), WebRTCPath: hexPath('2'), HTTP3Path: hexPath('3'), Enabled: true, ClientVisible: true,
		CredentialID: "local-master", HostKeySHA256: "SHA256:local", ProvisionedAt: now, UpdatedAt: now,
	}
	if _, err := manager.InitializeCluster("cluster-01", master); err != nil {
		t.Fatal(err)
	}
	material := ClusterPeerMaterial{
		CredentialID: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x21}, 16)),
		Secret:       [32]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
	}
	endpoint := ClusterPeerEndpoint{
		NodeID: "edge-fi", ServerIdentity: "edge.example.com", ServerAddresses: []string{"1.1.1.1"},
		HTTPSPath: hexPath('4'), WebRTCPath: hexPath('5'), HTTP3Path: hexPath('6'),
	}
	if err := manager.InstallClusterPeer("master", endpoint, material); err != nil {
		t.Fatalf("InstallClusterPeer() error = %v", err)
	}

	server, err := config.LoadServer(filepath.Join(root, "etc", "neproto", "server.json"))
	if err != nil {
		t.Fatalf("cluster server config invalid: %v", err)
	}
	if server.ClusterNodeID != "master" || server.ClusterMasterNodeID != "master" {
		t.Fatalf("cluster runtime identity = %q/%q", server.ClusterNodeID, server.ClusterMasterNodeID)
	}
	peers, err := clusterrelay.LoadPeerConfigs(server.ClusterPeerDirectory)
	if err != nil || peers["edge-fi"].ServerIdentity != "edge.example.com" {
		t.Fatalf("peer configs=%v err=%v", peers, err)
	}
	if peers["edge-fi"].EnableConstellation {
		t.Fatal("cluster peer profile incorrectly enabled client constellation admission")
	}
	principals, err := clusterrelay.LoadAcceptedPeers(server.ClusterPeerMapFile)
	if err != nil || principals[material.CredentialID] != "edge-fi" {
		t.Fatalf("peer principals=%v err=%v", principals, err)
	}
	activeSecret := filepath.Join(root, "etc", "neproto", "users", "active", material.CredentialID+".secret")
	if raw, err := os.ReadFile(activeSecret); err != nil || strings.TrimSpace(string(raw)) != base64.RawURLEncoding.EncodeToString(material.Secret[:]) {
		t.Fatalf("inbound peer credential missing or wrong: %v", err)
	}
	if err := manager.InstallClusterPeer("master", endpoint, material); err == nil {
		t.Fatal("duplicate peer install was accepted")
	}
	if err := manager.RemoveClusterPeer(endpoint.NodeID, material.CredentialID); err != nil {
		t.Fatalf("RemoveClusterPeer() error = %v", err)
	}
	server, err = config.LoadServer(filepath.Join(root, "etc", "neproto", "server.json"))
	if err != nil || server.ClusterNodeID != "" || server.ClusterDirectory == "" {
		t.Fatalf("server after last peer removal=%+v err=%v", server, err)
	}
	if _, err := os.Stat(activeSecret); !os.IsNotExist(err) {
		t.Fatalf("peer credential remains after removal: %v", err)
	}
}

func writeValidClusterServerConfig(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, "etc", "neproto")
	document := map[string]any{
		"server_identity":      "vpn.example.com",
		"credential_directory": filepath.Join(directory, "users", "active"),
		"listen":               "127.0.0.1:9080", "metrics_listen": "127.0.0.1:9464",
		"https_path": hexPath('1'), "webrtc_path": hexPath('2'),
		"enable_http3": true, "enable_webrtc_datagrams": true, "enable_http3_datagrams": true,
		"http3_listen": ":443", "http3_path": hexPath('3'),
		"http3_cert_file": filepath.Join(directory, "fullchain.pem"), "http3_key_file": filepath.Join(directory, "privkey.pem"),
		"udp_port_min": 40000, "udp_port_max": 40100,
		"max_cover_overhead_percent": 20, "initial_window_bytes": 1048576,
		"max_streams": 256, "max_sessions": 64, "max_webrtc_peers": 64, "max_http3_sessions": 64,
		"max_target_connections": 256,
		"dial_timeout":           "10s", "gather_timeout": "8s", "connect_timeout": "12s",
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

func hexPath(fill byte) string { return "/" + strings.Repeat(string(fill), 48) }
