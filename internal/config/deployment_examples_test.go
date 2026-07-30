package config

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploymentExamplesStayValidAndConsistent(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.ToSlash(writeTestSecret(t, directory, bytes.Repeat([]byte{0x5a}, 32)))

	serverPath := materializeExample(t, directory, "server.json.example", "server.json", secretPath)
	clientPath := materializeExample(t, directory, "client.json.example", "client.json", secretPath)

	server, err := LoadServer(serverPath)
	if err != nil {
		t.Fatalf("load server deployment example: %v", err)
	}
	client, err := LoadClient(clientPath)
	if err != nil {
		t.Fatalf("load client deployment example: %v", err)
	}
	if client.ServerIdentity != server.ServerIdentity {
		t.Fatalf("identity mismatch: client=%q server=%q", client.ServerIdentity, server.ServerIdentity)
	}

	httpsURL, err := url.Parse(client.HTTPSURL)
	if err != nil {
		t.Fatalf("parse HTTPS carrier URL: %v", err)
	}
	webrtcURL, err := url.Parse(client.WebRTCSignalingURL)
	if err != nil {
		t.Fatalf("parse WebRTC signaling URL: %v", err)
	}
	if httpsURL.Path != server.HTTPSPath || webrtcURL.Path != server.WebRTCPath {
		t.Fatalf("route mismatch: client=(%q, %q) server=(%q, %q)", httpsURL.Path, webrtcURL.Path, server.HTTPSPath, server.WebRTCPath)
	}

	caddyfile, err := os.ReadFile(filepath.Join("..", "..", "deploy", "caddy", "Caddyfile.example"))
	if err != nil {
		t.Fatalf("read Caddy example: %v", err)
	}
	for _, route := range []string{server.HTTPSPath, server.WebRTCPath} {
		if !strings.Contains(string(caddyfile), "handle "+route+" {") {
			t.Fatalf("Caddy example does not proxy exact route %q", route)
		}
	}
}

func materializeExample(t *testing.T, directory, sourceName, targetName, secretPath string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "examples", sourceName))
	if err != nil {
		t.Fatalf("read deployment example %q: %v", sourceName, err)
	}
	materialized := strings.Replace(string(raw), "/etc/neproto/client.secret", secretPath, 1)
	materialized = strings.Replace(materialized, "/etc/neproto/server.secret", secretPath, 1)
	path := filepath.Join(directory, targetName)
	writeConfig(t, path, materialized)
	return path
}
