package onboarding

import (
	"reflect"
	"strings"
	"testing"
)

func TestProfileURIExactRoundTrip(t *testing.T) {
	profile := Profile{
		Version:         1,
		CredentialID:    "ABEiM0RVZneImaq7zN3u_w",
		Name:            "Alice iPhone",
		ServerIdentity:  "vpn.example.com",
		ServerAddresses: []string{"8.8.8.8", "2606:4700:4700::1111"},
		HTTPSPath:       "/private_https_route_0123456789",
		WebRTCPath:      "/private_webrtc_route_0123456789",
		Profile:         "web",
		Secret:          "WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo",
	}
	uri, err := EncodeURI(profile)
	if err != nil {
		t.Fatalf("encode URI: %v", err)
	}
	if !strings.HasPrefix(uri, LegacyPrefix) || strings.Contains(uri, "=") || strings.Contains(uri, profile.Secret) {
		t.Fatalf("unexpected URI encoding: %q", uri)
	}
	decoded, err := DecodeURI(uri)
	if err != nil {
		t.Fatalf("decode URI: %v", err)
	}
	if decoded.Version != profile.Version || decoded.CredentialID != profile.CredentialID ||
		decoded.Name != profile.Name || decoded.ServerIdentity != profile.ServerIdentity ||
		decoded.HTTPSPath != profile.HTTPSPath || decoded.WebRTCPath != profile.WebRTCPath ||
		decoded.Profile != profile.Profile || decoded.Secret != profile.Secret ||
		len(decoded.ServerAddresses) != 2 || decoded.ServerAddresses[1] != profile.ServerAddresses[1] {
		t.Fatalf("round trip mismatch: %#v", decoded)
	}
}

func TestProductionProfileV2RoundTripIncludesHTTP3Policy(t *testing.T) {
	profile := Profile{
		Version:              2,
		CredentialID:         "ABEiM0RVZneImaq7zN3u_w",
		Name:                 "Alice production iPhone",
		ServerIdentity:       "vpn.example.com",
		ServerAddresses:      []string{"8.8.8.8"},
		HTTPSPath:            "/private_https_route_0123456789",
		WebRTCPath:           "/private_webrtc_route_0123456789",
		HTTP3Path:            "/private_http3_route_01234567890",
		RequireDatagrams:     true,
		MaxParallelCarriers:  3,
		EnableConstellation:  true,
		EnableForwardSecrecy: true,
		ClusterID:            "cluster-01",
		CatalogPublicKey:     "REREREREREREREREREREREREREREREREREREREREREQ",
		Profile:              "interactive",
		Secret:               "WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo",
	}
	uri, err := EncodeURI(profile)
	if err != nil {
		t.Fatalf("encode v2 URI: %v", err)
	}
	if !strings.HasPrefix(uri, Prefix) {
		t.Fatalf("production URI prefix=%q", uri)
	}
	decoded, err := DecodeURI(uri)
	if err != nil {
		t.Fatalf("decode v2 URI: %v", err)
	}
	if !reflect.DeepEqual(decoded, profile) {
		t.Fatalf("round trip mismatch: %#v", decoded)
	}
}

func TestProfileRejectsIncompleteOrLegacyClusterPin(t *testing.T) {
	profile := Profile{
		Version: 2, CredentialID: "ABEiM0RVZneImaq7zN3u_w", Name: "Phone",
		ServerIdentity: "vpn.example.com", ServerAddresses: []string{"8.8.8.8"},
		HTTPSPath: "/private_https_route_0123456789", WebRTCPath: "/private_webrtc_route_0123456789",
		HTTP3Path: "/private_http3_route_01234567890", Profile: "web",
		Secret:    "WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo",
		ClusterID: "cluster-01",
	}
	if _, err := EncodeURI(profile); err == nil {
		t.Fatal("accepted cluster ID without catalog public key")
	}
	profile.CatalogPublicKey = "REREREREREREREREREREREREREREREREREREREREREQ"
	profile.Version = 1
	profile.HTTP3Path = ""
	if _, err := EncodeURI(profile); err == nil {
		t.Fatal("legacy profile accepted cluster pin")
	}
}

func TestProductionProfileV2RejectsInvalidCarrierPool(t *testing.T) {
	profile := Profile{
		Version: 2, CredentialID: "ABEiM0RVZneImaq7zN3u_w", Name: "Phone",
		ServerIdentity: "vpn.example.com", ServerAddresses: []string{"8.8.8.8"},
		HTTPSPath:  "/private_https_route_0123456789",
		WebRTCPath: "/private_webrtc_route_0123456789",
		HTTP3Path:  "/private_http3_route_01234567890", Profile: "web",
		Secret: "WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo",
	}
	profile.MaxParallelCarriers = 4
	if _, err := EncodeURI(profile); err == nil {
		t.Fatal("accepted oversized carrier pool")
	}
	profile.Version = 1
	profile.HTTP3Path = ""
	profile.MaxParallelCarriers = 3
	profile.EnableConstellation = false
	profile.EnableForwardSecrecy = false
	if _, err := EncodeURI(profile); err == nil {
		t.Fatal("legacy profile accepted carrier pool extension")
	}
	profile.MaxParallelCarriers = 0
	profile.EnableConstellation = true
	if _, err := EncodeURI(profile); err == nil {
		t.Fatal("legacy profile accepted constellation extension")
	}
}

func TestDecodeURIRejectsUntrustedPayloads(t *testing.T) {
	valid := `{"version":1,"credential_id":"ABEiM0RVZneImaq7zN3u_w","name":"Phone","server_identity":"vpn.example.com","server_addresses":["8.8.8.8"],"https_path":"/private_https_route_0123456789","webrtc_path":"/private_webrtc_route_0123456789","profile":"web","secret":"WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo"}`
	tests := map[string]string{
		"wrong scheme":        "https://import/v1/" + encodeRaw(valid),
		"unknown field":       strings.Replace(valid, `"version":1`, `"version":1,"admin":true`, 1),
		"duplicate field":     strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		"private address":     strings.Replace(valid, `"8.8.8.8"`, `"10.0.0.1"`, 1),
		"benchmark address":   strings.Replace(valid, `"8.8.8.8"`, `"198.18.1.1"`, 1),
		"same paths":          strings.Replace(valid, `/private_webrtc_route_0123456789`, `/private_https_route_0123456789`, 1),
		"bad secret":          strings.Replace(valid, `WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo`, `short`, 1),
		"unsupported version": strings.Replace(valid, `"version":1`, `"version":2`, 1),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			uri := raw
			if !strings.Contains(raw, "://") {
				uri = LegacyPrefix + encodeRaw(raw)
			}
			if _, err := DecodeURI(uri); err == nil {
				t.Fatalf("accepted untrusted payload: %s", raw)
			}
		})
	}
}

func TestDecodeURIRejectsOversizedInputBeforeDecoding(t *testing.T) {
	if _, err := DecodeURI(Prefix + strings.Repeat("A", MaxURIBytes)); err == nil {
		t.Fatal("accepted oversized onboarding URI")
	}
}
