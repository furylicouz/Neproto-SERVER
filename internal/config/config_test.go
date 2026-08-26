package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cover"
	"neproto.local/chameleon/internal/protocol"
)

func TestLoadStrictClientAndServerConfigs(t *testing.T) {
	directory := t.TempDir()
	secretPath := writeTestSecret(t, directory, bytes.Repeat([]byte{0x42}, 32))
	clientPath := filepath.Join(directory, "client.json")
	clientJSON := `{
  "server_identity":"vpn.example.com",
  "secret_file":"` + filepath.ToSlash(secretPath) + `",
  "socks_listen":"127.0.0.1:1080",
  "https_url":"wss://vpn.example.com/private/https/session",
  "webrtc_signaling_url":"https://vpn.example.com/private/webrtc/offer",
  "http3_url":"https://vpn.example.com/private/http3/session",
  "profile":"interactive",
  "max_cover_overhead_percent":30,
  "initial_window_bytes":262144,
  "max_streams":128,
  "max_socks_connections":128,
  "webrtc_timeout":"5s",
  "https_timeout":"10s",
  "http3_timeout":"5s",
  "carrier_cache_ttl":"10m",
  "require_datagrams":true,
  "enable_constellation":true,
  "enable_forward_secrecy":true
}`
	writeConfig(t, clientPath, clientJSON)
	client, err := LoadClient(clientPath)
	if err != nil {
		t.Fatalf("load client: %v", err)
	}
	if client.ProfileID() != cover.ProfileInteractive || client.WebRTCTimeout.Duration != 5*time.Second ||
		client.HTTP3Timeout.Duration != 5*time.Second || client.HTTP3URL == "" ||
		client.CarrierPolicy != CarrierPolicyPerformance || !client.RequireDatagrams ||
		!client.EnableConstellation || !client.EnableForwardSecrecy {
		t.Fatalf("client normalization mismatch: %#v", client)
	}
	udpFirstPath := filepath.Join(directory, "client-udp-first.json")
	udpFirstJSON := strings.Replace(clientJSON, `"profile":"interactive",`,
		`"profile":"interactive","carrier_policy":"udp-first",`, 1)
	writeConfig(t, udpFirstPath, udpFirstJSON)
	udpFirst, err := LoadClient(udpFirstPath)
	if err != nil || udpFirst.CarrierPolicy != CarrierPolicyUDPFirst {
		t.Fatalf("load UDP-first policy: policy=%q error=%v", udpFirst.CarrierPolicy, err)
	}
	invalidPolicyPath := filepath.Join(directory, "client-invalid-policy.json")
	invalidPolicyJSON := strings.Replace(clientJSON, `"profile":"interactive",`,
		`"profile":"interactive","carrier_policy":"fastest",`, 1)
	writeConfig(t, invalidPolicyPath, invalidPolicyJSON)
	if _, err := LoadClient(invalidPolicyPath); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid carrier policy error=%v", err)
	}
	if client.Secret.Bytes() != [32]byte{0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42} {
		t.Fatal("client secret mismatch")
	}

	serverPath := filepath.Join(directory, "server.json")
	serverJSON := `{
  "server_identity":"vpn.example.com",
  "secret_file":"` + filepath.ToSlash(secretPath) + `",
  "listen":"127.0.0.1:9080",
  "metrics_listen":"127.0.0.1:9464",
  "cluster_directory":"` + filepath.ToSlash(filepath.Join(directory, "cluster")) + `",
  "cluster_catalog_ttl":"1h",
  "https_path":"/private/https/session",
  "webrtc_path":"/private/webrtc/offer",
  "enable_http3":true,
	"enable_webrtc_datagrams":true,
	"enable_http3_datagrams":true,
  "http3_listen":":443",
  "http3_path":"/private/http3/session",
  "http3_cert_file":"` + filepath.ToSlash(filepath.Join(directory, "fullchain.pem")) + `",
  "http3_key_file":"` + filepath.ToSlash(filepath.Join(directory, "privkey.pem")) + `",
  "udp_port_min":40000,
  "udp_port_max":40100,
  "max_cover_overhead_percent":30,
  "initial_window_bytes":262144,
  "max_streams":128,
  "max_sessions":32,
  "max_webrtc_peers":32,
  "max_http3_sessions":32,
  "max_target_connections":128,
  "dial_timeout":"10s",
  "gather_timeout":"8s",
  "connect_timeout":"12s",
  "http3_handshake_timeout":"5s",
  "http3_idle_timeout":"30s",
  "shutdown_timeout":"10s"
  ,"enable_constellation":true,"enable_forward_secrecy":true
}`
	writeConfig(t, serverPath, serverJSON)
	server, err := LoadServer(serverPath)
	if err != nil {
		t.Fatalf("load server: %v", err)
	}
	if server.UDPPortMin != 40000 || server.DialTimeout.Duration != 10*time.Second ||
		server.MetricsListen != "127.0.0.1:9464" ||
		server.ClusterDirectory != filepath.Join(directory, "cluster") || server.ClusterCatalogTTL.Duration != time.Hour ||
		!server.EnableHTTP3 || !server.EnableWebRTCDatagrams || !server.EnableHTTP3Datagrams ||
		server.HTTP3Listen != ":443" || server.MaxHTTP3Sessions != 32 ||
		server.Secret.Bytes() != client.Secret.Bytes() || !server.EnableConstellation ||
		!server.EnableForwardSecrecy {
		t.Fatalf("server normalization mismatch: %#v", server)
	}
	if server.ResourceLimits.MaxSessionsPerUser == 0 ||
		server.ResourceLimits.MaxTCPConnectionsGlobal < server.MaxTargetConnections ||
		server.ResourceLimits.MaxUDPAssociationsPerUser == 0 ||
		server.ResourceLimits.UDPBytesPerSecondGlobal == 0 {
		t.Fatalf("production resource defaults were not applied: %#v", server.ResourceLimits)
	}
}

func TestValidateServerRequiresBoundedCatalogTTLWithClusterDirectory(t *testing.T) {
	server := validServerForLimitsTest()
	server.ClusterDirectory = filepath.Join(t.TempDir(), "cluster")
	server.ClusterCatalogTTL.Duration = 25 * time.Hour
	if err := validateServer(server); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("oversized cluster catalog TTL error = %v", err)
	}
	server.ClusterCatalogTTL.Duration = time.Hour
	if err := validateServer(server); err != nil {
		t.Fatalf("valid cluster catalog settings error = %v", err)
	}
	server.ClusterDirectory = ""
	if err := validateServer(server); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("TTL without cluster directory error = %v", err)
	}
}

func TestValidateServerRequiresCompleteClusterRelayIdentity(t *testing.T) {
	server := validServerForLimitsTest()
	server.ClusterNodeID = "master"
	if err := validateServer(server); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("partial relay config error=%v", err)
	}
	directory := t.TempDir()
	server.ClusterMasterNodeID = "master"
	server.ClusterPeerDirectory = filepath.Join(directory, "peers")
	server.ClusterPeerMapFile = filepath.Join(directory, "accepted-peers.json")
	server.ClusterDirectory = filepath.Join(directory, "cluster")
	server.ClusterCatalogTTL.Duration = time.Hour
	if err := validateServer(server); err != nil {
		t.Fatalf("complete relay config error=%v", err)
	}
}

func validServerForLimitsTest() Server {
	secret, err := ParseSecret("WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo")
	if err != nil {
		panic(err)
	}
	server := Server{
		ServerIdentity: "vpn.example.com", Secret: secret, Listen: "127.0.0.1:9080",
		HTTPSPath: "/private/https/session", WebRTCPath: "/private/webrtc/offer",
		UDPPortMin: 40000, UDPPortMax: 40100, MaxCoverOverheadPercent: 30,
		InitialWindowBytes: 262144, MaxStreams: 128, MaxSessions: 32,
		MaxWebRTCPeers: 32, MaxTargetConnections: 128,
		DialTimeout: Duration{10 * time.Second}, GatherTimeout: Duration{8 * time.Second},
		ConnectTimeout: Duration{12 * time.Second}, ShutdownTimeout: Duration{10 * time.Second},
	}
	applyServerResourceDefaults(&server)
	return server
}

func TestLoadServerAcceptsAndValidatesResourceLimits(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.ToSlash(writeTestSecret(t, directory, bytes.Repeat([]byte{0x43}, 32)))
	base := `{
  "server_identity":"vpn.example.com","secret_file":"` + secretPath + `","listen":"127.0.0.1:9080",
  "https_path":"/private/https/session","webrtc_path":"/private/webrtc/offer",
  "udp_port_min":40000,"udp_port_max":40100,"max_cover_overhead_percent":30,
  "initial_window_bytes":262144,"max_streams":128,"max_sessions":32,"max_webrtc_peers":32,
  "max_target_connections":128,"dial_timeout":"10s","gather_timeout":"8s",
  "connect_timeout":"12s","shutdown_timeout":"10s",
  "resource_limits":%s
}`
	validLimits := `{
    "max_sessions_per_user":4,
    "max_tcp_connections_global":6000,"max_tcp_connections_per_user":512,
    "max_udp_associations_global":10000,"max_udp_associations_per_user":1024,
    "udp_packets_per_second_global":100000,"udp_packets_per_second_per_user":20000,
    "udp_bytes_per_second_global":268435456,"udp_bytes_per_second_per_user":67108864,
    "dns_queries_per_second_global":5000,"dns_queries_per_second_per_user":500,
    "target_creates_per_second_global":20000,"target_creates_per_second_per_user":2000
  }`
	path := filepath.Join(directory, "server-limits.json")
	writeConfig(t, path, fmt.Sprintf(base, validLimits))
	loaded, err := LoadServer(path)
	if err != nil {
		t.Fatalf("load resource limits: %v", err)
	}
	if loaded.ResourceLimits.MaxSessionsPerUser != 4 ||
		loaded.ResourceLimits.MaxUDPAssociationsGlobal != 10000 {
		t.Fatalf("resource limits mismatch: %#v", loaded.ResourceLimits)
	}

	invalidLimits := strings.Replace(validLimits,
		`"max_tcp_connections_per_user":512`, `"max_tcp_connections_per_user":6001`, 1)
	writeConfig(t, path, fmt.Sprintf(base, invalidLimits))
	if _, err := LoadServer(path); err == nil {
		t.Fatal("per-user TCP limit above global limit was accepted")
	}

	duplicateLimits := strings.Replace(validLimits,
		`"max_sessions_per_user":4`, `"max_sessions_per_user":4,"max_sessions_per_user":5`, 1)
	writeConfig(t, path, fmt.Sprintf(base, duplicateLimits))
	if _, err := LoadServer(path); err == nil {
		t.Fatal("duplicate nested resource limit was accepted")
	}
}

func TestLoadServerAcceptsIndependentCredentialDirectory(t *testing.T) {
	directory := t.TempDir()
	active := filepath.Join(directory, "users", "active")
	if err := os.MkdirAll(active, 0o700); err != nil {
		t.Fatal(err)
	}
	firstSecret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x31}, 32)) + "\n"
	secondSecret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x32}, 32)) + "\n"
	if err := os.WriteFile(filepath.Join(active, "ABEiM0RVZneImaq7zN3u_w.secret"), []byte(firstSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "_-7dzLuqmYh3ZlVEMyIRAA.secret"), []byte(secondSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "server.json")
	raw := `{
  "server_identity":"vpn.example.com",
  "credential_directory":"users/active",
  "listen":"127.0.0.1:9080",
  "https_path":"/private/https/session",
  "webrtc_path":"/private/webrtc/offer",
  "udp_port_min":40000,"udp_port_max":40100,
  "max_cover_overhead_percent":30,"initial_window_bytes":262144,
  "max_streams":128,"max_sessions":32,"max_webrtc_peers":32,
  "max_target_connections":128,"dial_timeout":"10s",
  "gather_timeout":"8s","connect_timeout":"12s","shutdown_timeout":"10s"
}`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadServer(configPath)
	if err != nil {
		t.Fatalf("load credential directory server: %v", err)
	}
	if loaded.CredentialDirectory != active || len(loaded.Credentials) != 2 || loaded.Secret != (RootSecret{}) {
		t.Fatalf("unexpected credential server: %#v", loaded)
	}
}

func TestConfigRejectsUnknownFieldsAndUnsafeNetworkValues(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.ToSlash(writeTestSecret(t, directory, bytes.Repeat([]byte{1}, 32)))
	validClient := `{
  "server_identity":"vpn.example.com","secret_file":"` + secretPath + `",
  "socks_listen":"127.0.0.1:1080","https_url":"wss://vpn.example.com/private/https/session",
  "webrtc_signaling_url":"https://vpn.example.com/private/webrtc/offer","profile":"web",
  "max_cover_overhead_percent":30,"initial_window_bytes":262144,"max_streams":128,
  "max_socks_connections":128,"webrtc_timeout":"5s","https_timeout":"10s","carrier_cache_ttl":"10m"
}`
	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown field", raw: strings.TrimSuffix(validClient, "}") + `,"password":"secret"}`},
		{name: "public SOCKS bind", raw: strings.Replace(validClient, "127.0.0.1:1080", "0.0.0.0:1080", 1)},
		{name: "plain WebSocket", raw: strings.Replace(validClient, "wss://", "ws://", 1)},
		{name: "plain signaling", raw: strings.Replace(validClient, "https://", "http://", 1)},
		{name: "identity mismatch", raw: strings.Replace(validClient, "wss://vpn.example.com", "wss://other.example.com", 1)},
		{name: "short route", raw: strings.Replace(validClient, "/private/https/session", "/short", 1)},
		{name: "numeric duration", raw: strings.Replace(validClient, `"5s"`, `5000`, 1)},
		{name: "invalid profile", raw: strings.Replace(validClient, `"web"`, `"game-name"`, 1)},
		{name: "duplicate field", raw: strings.Replace(validClient, `"server_identity":"vpn.example.com"`, `"server_identity":"vpn.example.com","server_identity":"vpn.example.com"`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(directory, strings.ReplaceAll(test.name, " ", "_")+".json")
			writeConfig(t, path, test.raw)
			if _, err := LoadClient(path); err == nil {
				t.Fatal("unsafe config was accepted")
			}
		})
	}
}

func TestServerRejectsPublicBindAndUnsafeUDPRange(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.ToSlash(writeTestSecret(t, directory, bytes.Repeat([]byte{2}, 32)))
	valid := `{
  "server_identity":"vpn.example.com","secret_file":"` + secretPath + `","listen":"127.0.0.1:9080",
  "https_path":"/private/https/session","webrtc_path":"/private/webrtc/offer",
  "udp_port_min":40000,"udp_port_max":40100,"max_cover_overhead_percent":30,
  "initial_window_bytes":262144,"max_streams":128,"max_sessions":32,"max_webrtc_peers":32,
  "max_target_connections":128,"dial_timeout":"10s","gather_timeout":"8s",
  "connect_timeout":"12s","shutdown_timeout":"10s"
}`
	tests := []string{
		strings.Replace(valid, `"listen":"127.0.0.1:9080"`, `"listen":"127.0.0.1:9080","metrics_listen":"0.0.0.0:9464"`, 1),
		strings.Replace(valid, `"listen":"127.0.0.1:9080"`, `"listen":"127.0.0.1:9080","metrics_listen":"127.0.0.1:9080"`, 1),
		strings.Replace(valid, "127.0.0.1:9080", "0.0.0.0:9080", 1),
		strings.Replace(valid, "127.0.0.1:9080", "127.0.0.1:0", 1),
		strings.Replace(valid, `"udp_port_min":40000`, `"udp_port_min":0`, 1),
		strings.Replace(valid, `"udp_port_max":40100`, `"udp_port_max":39999`, 1),
		strings.Replace(valid, `"udp_port_max":40100`, `"udp_port_max":42000`, 1),
		strings.Replace(valid, "/private/webrtc/offer", "/private/https/session", 1),
	}
	for index, raw := range tests {
		path := filepath.Join(directory, "server-invalid-"+string(rune('a'+index))+".json")
		writeConfig(t, path, raw)
		if _, err := LoadServer(path); err == nil {
			t.Fatalf("unsafe server config %d was accepted", index)
		}
	}
}

func TestSecretFileMustContainCanonicalStrongValue(t *testing.T) {
	directory := t.TempDir()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "short", raw: base64.RawURLEncoding.EncodeToString([]byte("short"))},
		{name: "padded", raw: base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))},
		{name: "spaces", raw: " " + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))},
		{name: "zero", raw: base64.RawURLEncoding.EncodeToString(make([]byte, 32))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(directory, test.name+".secret")
			if err := os.WriteFile(path, []byte(test.raw+"\n"), 0o600); err != nil {
				t.Fatalf("write secret: %v", err)
			}
			if _, err := LoadSecret(path); !errors.Is(err, ErrInvalidSecret) {
				t.Fatalf("expected invalid secret, got %v", err)
			}
		})
	}
}

func TestGenerateSecretUsesCanonicalBase64URL(t *testing.T) {
	encoded, err := GenerateSecret(bytes.NewReader(bytes.Repeat([]byte{0xab}, 32)))
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}
	if strings.Contains(encoded, "=") {
		t.Fatal("generated secret is padded")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || !bytes.Equal(decoded, bytes.Repeat([]byte{0xab}, 32)) {
		t.Fatalf("generated secret=%q decoded=%x err=%v", encoded, decoded, err)
	}
}

func TestParseClientBytesLoadsKeychainStyleSecretWithoutFileIO(t *testing.T) {
	raw := []byte(`{
  "server_identity":"vpn.example.com","secret_file":"keychain",
  "socks_listen":"127.0.0.1:1080","https_url":"wss://vpn.example.com/private/https/session",
  "webrtc_signaling_url":"https://vpn.example.com/private/webrtc/offer","profile":"interactive",
  "max_cover_overhead_percent":30,"initial_window_bytes":262144,"max_streams":128,
  "max_socks_connections":128,"webrtc_timeout":"5s","https_timeout":"10s","carrier_cache_ttl":"10m"
}`)
	encoded := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))

	client, err := ParseClientBytes(raw, encoded)
	if err != nil {
		t.Fatalf("parse client bytes: %v", err)
	}
	if client.Secret.Bytes() != [32]byte{0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a} {
		t.Fatal("parsed client secret mismatch")
	}
}

func TestParseClientBytesAllowsEphemeralLoopbackSOCKSPort(t *testing.T) {
	raw := []byte(`{
  "server_identity":"vpn.example.com","secret_file":"keychain",
  "socks_listen":"127.0.0.1:0","https_url":"wss://vpn.example.com/private/https/session",
  "webrtc_signaling_url":"https://vpn.example.com/private/webrtc/offer","profile":"interactive",
  "max_cover_overhead_percent":30,"initial_window_bytes":262144,"max_streams":128,
  "max_socks_connections":128,"webrtc_timeout":"5s","https_timeout":"10s","carrier_cache_ttl":"10m"
}`)

	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))
	if _, err := ParseClientBytes(raw, secret); err != nil {
		t.Fatalf("ephemeral loopback SOCKS port rejected: %v", err)
	}
}

func TestParseMobileClientBytesRequiresNoSOCKSAdapterFields(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))
	raw := []byte(`{
  "server_identity":"vpn.example.com","secret_file":"keychain",
	"device_id":"10223344-5566-7788-99aa-bbccddeef001",
	"server_addresses":["104.171.136.10"],
  "https_url":"wss://vpn.example.com/private/https/session",
  "webrtc_signaling_url":"https://vpn.example.com/private/webrtc/offer","profile":"interactive",
  "max_cover_overhead_percent":30,"initial_window_bytes":262144,"max_streams":128,
  "webrtc_timeout":"5s","https_timeout":"10s","carrier_cache_ttl":"10m"
}`)
	client, err := ParseMobileClientBytes(raw, secret)
	if err != nil {
		t.Fatalf("parse direct mobile profile: %v", err)
	}
	if client.SOCKSListen != "127.0.0.1:0" || client.MaxSOCKSConnections != client.MaxStreams {
		t.Fatalf("mobile-only internal adapter defaults were not applied: %#v", client)
	}
	if client.MaxParallelCarriers != 3 {
		t.Fatalf("mobile performance carrier pool=%d, want 3", client.MaxParallelCarriers)
	}
	if client.DeviceID != (protocol.DeviceID{0x10, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xf0, 0x01}) {
		t.Fatalf("mobile device id=%x", client.DeviceID)
	}

	explicitPool := bytes.Replace(raw, []byte(`"max_streams":128,`),
		[]byte(`"max_streams":128,"max_parallel_carriers":2,`), 1)
	explicitClient, err := ParseMobileClientBytes(explicitPool, secret)
	if err != nil || explicitClient.MaxParallelCarriers != 2 {
		t.Fatalf("explicit mobile carrier pool=%d error=%v", explicitClient.MaxParallelCarriers, err)
	}
	oversizedPool := bytes.Replace(raw, []byte(`"max_streams":128,`),
		[]byte(`"max_streams":128,"max_parallel_carriers":4,`), 1)
	if _, err := ParseMobileClientBytes(oversizedPool, secret); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("oversized mobile carrier pool error=%v", err)
	}
	quietProfile := bytes.Replace(raw, []byte(`"profile":"interactive"`), []byte(`"profile":"quiet"`), 1)
	quietClient, err := ParseMobileClientBytes(quietProfile, secret)
	if err != nil || quietClient.MaxParallelCarriers != 1 {
		t.Fatalf("quiet mobile carrier pool=%d error=%v", quietClient.MaxParallelCarriers, err)
	}

	withSOCKS := bytes.Replace(raw, []byte(`"secret_file":"keychain",`),
		[]byte(`"secret_file":"keychain","socks_listen":"127.0.0.1:1080",`), 1)
	if _, err := ParseMobileClientBytes(withSOCKS, secret); err == nil {
		t.Fatal("mobile profile accepted a SOCKS adapter field")
	}

	withoutPinnedAddress := bytes.Replace(raw, []byte(`"server_addresses":["104.171.136.10"],`), nil, 1)
	if _, err := ParseMobileClientBytes(withoutPinnedAddress, secret); err == nil {
		t.Fatal("mobile profile accepted no pinned server address")
	}
	fakeAddress := bytes.Replace(raw, []byte("104.171.136.10"), []byte("198.18.1.233"), 1)
	if _, err := ParseMobileClientBytes(fakeAddress, secret); err == nil {
		t.Fatal("mobile profile accepted a benchmark-range Fake-IP")
	}
	malformedDevice := bytes.Replace(raw, []byte("10223344-5566-7788-99aa-bbccddeef001"), []byte("not-a-device-id"), 1)
	if _, err := ParseMobileClientBytes(malformedDevice, secret); err == nil {
		t.Fatal("mobile profile accepted a malformed device identity")
	}
	zeroDevice := bytes.Replace(raw, []byte("10223344-5566-7788-99aa-bbccddeef001"), []byte("00000000-0000-0000-0000-000000000000"), 1)
	if _, err := ParseMobileClientBytes(zeroDevice, secret); err == nil {
		t.Fatal("mobile profile accepted a zero device identity")
	}
}

func TestDesktopClientDefaultsToSingleCarrier(t *testing.T) {
	raw := []byte(`{
  "server_identity":"vpn.example.com","secret_file":"keychain",
  "socks_listen":"127.0.0.1:1080","https_url":"wss://vpn.example.com/private/https/session",
  "webrtc_signaling_url":"https://vpn.example.com/private/webrtc/offer","profile":"interactive",
  "max_cover_overhead_percent":30,"initial_window_bytes":262144,"max_streams":128,
  "max_socks_connections":128,"webrtc_timeout":"5s","https_timeout":"10s","carrier_cache_ttl":"10m"
}`)
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))
	client, err := ParseClientBytes(raw, secret)
	if err != nil {
		t.Fatal(err)
	}
	if client.MaxParallelCarriers != 1 {
		t.Fatalf("desktop carrier pool=%d, want 1", client.MaxParallelCarriers)
	}
}

func TestParseMobileClientBytesAcceptsHTTP3OnlyWithConfiguredHTTP3(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x7a}, 32))
	raw := []byte(`{
  "server_identity":"vpn.example.com","secret_file":"keychain",
  "device_id":"10223344-5566-7788-99aa-bbccddeef001",
  "server_addresses":["104.171.136.10"],
  "http3_url":"https://vpn.example.com/private/http3/session",
  "profile":"web","carrier_policy":"http3-only",
  "max_cover_overhead_percent":30,"initial_window_bytes":262144,"max_streams":128,
  "max_parallel_carriers":1,
  "http3_timeout":"5s","carrier_cache_ttl":"10m"
}`)
	client, err := ParseMobileClientBytes(raw, secret)
	if err != nil {
		t.Fatalf("parse HTTP/3-only Windows profile: %v", err)
	}
	if client.CarrierPolicy != CarrierPolicyHTTP3Only || !client.HTTP3Configured() ||
		client.MaxParallelCarriers != 1 || client.HTTPSURL != "" || client.WebRTCSignalingURL != "" {
		t.Fatalf("unexpected HTTP/3-only profile: %+v", client)
	}

	withoutHTTP3 := bytes.Replace(raw,
		[]byte(`  "http3_url":"https://vpn.example.com/private/http3/session",`+"\n"), nil, 1)
	withoutHTTP3 = bytes.Replace(withoutHTTP3, []byte(`  "http3_timeout":"5s",`), nil, 1)
	if _, err := ParseMobileClientBytes(withoutHTTP3, secret); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("HTTP/3-only profile without HTTP/3 error=%v", err)
	}

	withCompatibilityURL := bytes.Replace(raw, []byte(`  "http3_url"`),
		[]byte(`  "https_url":"wss://vpn.example.com/private/https/session",`+"\n"+`  "http3_url"`), 1)
	if _, err := ParseMobileClientBytes(withCompatibilityURL, secret); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("HTTP/3-only profile with compatibility URL error=%v", err)
	}
}

func TestParseClientBytesRejectsUntrustedProfileAndSecretInputs(t *testing.T) {
	valid := `{
  "server_identity":"vpn.example.com","secret_file":"keychain",
  "socks_listen":"127.0.0.1:1080","https_url":"wss://vpn.example.com/private/https/session",
  "webrtc_signaling_url":"https://vpn.example.com/private/webrtc/offer","profile":"interactive",
  "max_cover_overhead_percent":30,"initial_window_bytes":262144,"max_streams":128,
  "max_socks_connections":128,"webrtc_timeout":"5s","https_timeout":"10s","carrier_cache_ttl":"10m"
}`
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))
	tests := []struct {
		name   string
		raw    string
		secret string
	}{
		{name: "unknown profile field", raw: strings.TrimSuffix(valid, "}") + `,"password":"leak"}`, secret: secret},
		{name: "duplicate identity", raw: strings.Replace(valid, `"server_identity":"vpn.example.com"`, `"server_identity":"vpn.example.com","server_identity":"vpn.example.com"`, 1), secret: secret},
		{name: "padded secret", raw: valid, secret: base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))},
		{name: "zero secret", raw: valid, secret: base64.RawURLEncoding.EncodeToString(make([]byte, 32))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseClientBytes([]byte(test.raw), test.secret); err == nil {
				t.Fatal("unsafe mobile input was accepted")
			}
		})
	}
}

func writeTestSecret(t *testing.T, directory string, raw []byte) string {
	t.Helper()
	path := filepath.Join(directory, "root.secret")
	encoded := base64.RawURLEncoding.EncodeToString(raw) + "\n"
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return path
}

func writeConfig(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
