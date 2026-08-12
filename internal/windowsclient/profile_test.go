package windowsclient

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"testing"

	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/onboarding"
)

func TestImportURIProducesDirectNP2Configuration(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x42))
	uri, err := onboarding.EncodeURI(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x24)),
		Name: "Amsterdam", ServerIdentity: "vpn.example.com", ServerAddresses: []string{"1.1.1.1"},
		HTTPSPath: "/private/https/session", WebRTCPath: "/private/webrtc/offer",
		HTTP3Path: "/private/http3/session", MaxParallelCarriers: 3,
		EnableConstellation: true, EnableForwardSecrecy: true,
		ClusterID: "primary", CatalogPublicKey: base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x33)),
		Profile: "interactive", Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}

	profile, rawConfig, importedSecret, err := ImportURI(uri, "00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	if importedSecret != secret || profile.Secret != "" {
		t.Fatal("secret leaked into stored public profile")
	}
	if profile.ID == "" || profile.Name != "Amsterdam" || profile.ClusterID != "primary" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if profile.Profile != "web" {
		t.Fatalf("legacy manual profile was not normalized to automatic mode: %q", profile.Profile)
	}

	client, err := config.ParseMobileClientBytes(rawConfig, importedSecret)
	if err != nil {
		t.Fatalf("generated config is invalid: %v\n%s", err, rawConfig)
	}
	if client.ServerIdentity != "vpn.example.com" || len(client.ServerAddresses) != 1 || client.ServerAddresses[0] != netip.MustParseAddr("1.1.1.1") {
		t.Fatalf("unexpected client: %+v", client)
	}
	if client.SOCKSListen != "127.0.0.1:0" || client.MaxParallelCarriers != 1 || !client.EnableConstellation {
		t.Fatalf("not a direct constellation profile: %+v", client)
	}
	if client.CarrierPolicy != config.CarrierPolicyHTTP3Only {
		t.Fatalf("Windows diagnostic carrier policy=%q, want http3-only", client.CarrierPolicy)
	}
	if client.MaxParallelCarriers != 1 {
		t.Fatalf("Windows diagnostic carrier pool=%d, want one HTTP/3 session", client.MaxParallelCarriers)
	}
	if client.Profile != "web" {
		t.Fatalf("generated client configuration is not automatic: %q", client.Profile)
	}
	var raw map[string]any
	if err := json.Unmarshal(rawConfig, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["secret"]; ok {
		t.Fatal("secret serialized into client configuration")
	}
}

func TestImportURIRejectsMalformedDeviceID(t *testing.T) {
	_, _, _, err := ImportURI("np2://import/v2/not-base64", "hardware-id")
	if err == nil {
		t.Fatal("malformed import accepted")
	}
}

func bytesOf(size int, value byte) []byte {
	result := make([]byte, size)
	for i := range result {
		result[i] = value
	}
	return result
}
