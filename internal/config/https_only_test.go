package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func TestParseMobileClientAcceptsStrictHTTPSOnlyPolicy(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	raw := []byte(`{
  "server_identity":"vpn.example.test","secret_file":"keychain",
  "server_addresses":["8.8.8.8"],
  "https_url":"wss://vpn.example.test/private/https/session",
  "profile":"web","carrier_policy":"https-only",
  "max_cover_overhead_percent":30,"initial_window_bytes":2097152,
  "max_streams":128,"max_parallel_carriers":1,
  "https_timeout":"10s","carrier_cache_ttl":"10m"
}`)

	client, err := ParseMobileClientBytes(raw, secret)
	if err != nil {
		t.Fatal(err)
	}
	if client.CarrierPolicy != CarrierPolicyHTTPSOnly || client.HTTPSURL == "" ||
		client.HTTP3URL != "" || client.WebRTCSignalingURL != "" ||
		client.MaxParallelCarriers != 1 || client.RequireDatagrams {
		t.Fatalf("unexpected HTTPS-only profile: %+v", client)
	}
}

func TestParseMobileClientRejectsAlternateCarrierInHTTPSOnlyPolicy(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	raw := []byte(`{
  "server_identity":"vpn.example.test","secret_file":"keychain",
  "server_addresses":["8.8.8.8"],
  "https_url":"wss://vpn.example.test/private/https/session",
  "http3_url":"https://vpn.example.test/private/http3/session",
  "profile":"web","carrier_policy":"https-only",
  "max_cover_overhead_percent":30,"initial_window_bytes":2097152,
  "max_streams":128,"max_parallel_carriers":1,
  "https_timeout":"10s","http3_timeout":"5s","carrier_cache_ttl":"10m"
}`)

	if _, err := ParseMobileClientBytes(raw, secret); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("alternate carrier error = %v", err)
	}
}
