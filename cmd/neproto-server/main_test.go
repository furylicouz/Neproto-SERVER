package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerVersionGenerateSecretAndUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := execute([]string{"version"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "neproto-server") {
		t.Fatalf("version code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"generate-secret"}, &stdout, &stderr); code != 0 {
		t.Fatalf("generate code=%d stderr=%q", code, stderr.String())
	}
	encoded := strings.TrimSpace(stdout.String())
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || strings.Contains(encoded, "=") {
		t.Fatalf("generated secret is not canonical: %q err=%v", encoded, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("usage code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestClusterAttestationRequiresStrictInstalledNodeState(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "node.json")
	valid := `{"version":1,"cluster_id":"cluster-01","node_id":"edge-01","name":"Helsinki Edge","region":"Finland","roles":["relay","egress"],"master_node_id":"master","installed_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"cluster-attest", "--state", path, "--format", "token"}, &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) != "NP2_CLUSTER_NODE_READY" {
		t.Fatalf("attest code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if err := os.WriteFile(path, []byte(strings.TrimSuffix(valid, "}")+`,"peer_secret":"leak"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"cluster-attest", "--state", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("unsafe attestation state accepted: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
