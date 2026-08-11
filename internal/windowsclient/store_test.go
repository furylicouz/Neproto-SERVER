package windowsclient

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/onboarding"
)

type xorProtector byte

func (p xorProtector) Protect(value []byte) ([]byte, error)   { return xorBytes(value, byte(p)), nil }
func (p xorProtector) Unprotect(value []byte) ([]byte, error) { return xorBytes(value, byte(p)), nil }

func xorBytes(value []byte, key byte) []byte {
	result := append([]byte(nil), value...)
	for index := range result {
		result[index] ^= key
	}
	return result
}

func TestStoreAppliesClusterCatalogAndPersistsEffectiveRoutes(t *testing.T) {
	store, err := OpenStore(t.TempDir(), xorProtector(0x27))
	if err != nil {
		t.Fatal(err)
	}
	uri, err := onboarding.EncodeURI(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, 0x71)),
		Name: "Primary", ServerIdentity: "vpn.example.com", ServerAddresses: []string{"1.1.1.1"},
		HTTPSPath: "/private/https/session", WebRTCPath: "/private/webrtc/offer", HTTP3Path: "/private/http3/session",
		MaxParallelCarriers: 3, EnableConstellation: true, ClusterID: "cluster-one",
		CatalogPublicKey: base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x73)), Profile: "web",
		Secret: base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x72)),
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := store.Import(uri)
	if err != nil {
		t.Fatal(err)
	}
	route := cluster.Route{ID: "admin-openai", Name: "OpenAI", Priority: 10, Enabled: true,
		Source: cluster.RouteSourceAdmin, Match: cluster.RouteMatch{DomainSuffixes: []string{"openai.com"}},
		Action: cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{"edge"}}}
	catalog := cluster.Catalog{Version: 1, ClusterID: "cluster-one", Revision: 2,
		IssuedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour), UserID: "user-one",
		Servers: []cluster.CatalogServer{
			{NodeID: "master", Name: "Moscow", Region: "Russia", ServerIdentity: "vpn.example.com", ServerAddresses: []string{"1.1.1.1"}, Enabled: true},
			{NodeID: "edge", Name: "Amsterdam", Region: "Netherlands", ServerIdentity: "nl.example.com", ServerAddresses: []string{"8.8.8.8"}, HTTPSPath: "/edge/https/session", WebRTCPath: "/edge/webrtc/offer", HTTP3Path: "/edge/http3/session", Enabled: true},
		}, AdminRoutes: []cluster.Route{route}, Permissions: cluster.CatalogPermissions{AllowClientRoutes: true}}
	if err := store.ApplyCatalog(bootstrap.ID, catalog); err != nil {
		t.Fatal(err)
	}
	if len(store.Profiles()) != 2 {
		t.Fatalf("profiles=%+v", store.Profiles())
	}
	if state, ok := store.Catalog(bootstrap.ID); !ok || state.Revision != 2 {
		t.Fatalf("catalog=%+v ok=%v", state, ok)
	}
	if routes := store.EffectiveRoutes(bootstrap.ID); len(routes) != 1 || routes[0].ID != route.ID {
		t.Fatalf("routes=%+v", routes)
	}
	var edgeID string
	for _, profile := range store.Profiles() {
		if profile.ClusterNodeID == "edge" {
			edgeID = profile.ID
		}
	}
	if edgeID == "" {
		t.Fatal("edge profile missing")
	}
	if err := store.Remove(edgeID); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyCatalog(bootstrap.ID, catalog); err != nil {
		t.Fatal(err)
	}
	if len(store.Profiles()) != 1 {
		t.Fatalf("suppressed edge was restored: %+v", store.Profiles())
	}
}

func TestStoreImportsPersistsSelectsAndRemovesProfiles(t *testing.T) {
	directory := t.TempDir()
	store, err := OpenStore(directory, xorProtector(0x5a))
	if err != nil {
		t.Fatal(err)
	}
	uri := testOnboardingURI(t, "Primary", "vpn.example.com", "1.1.1.1", 0x31)
	profile, err := store.Import(uri)
	if err != nil {
		t.Fatal(err)
	}
	if store.SelectedProfileID() != profile.ID {
		t.Fatal("first profile not selected")
	}
	loaded, rawConfig, secret, err := store.Selected()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != profile.ID || len(rawConfig) == 0 || secret == "" {
		t.Fatalf("bad selected profile: %+v", loaded)
	}

	rawState, err := os.ReadFile(filepath.Join(directory, storeFileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rawState, []byte(secret)) {
		t.Fatal("plaintext secret persisted")
	}

	reopened, err := OpenStore(directory, xorProtector(0x5a))
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Profiles()) != 1 || reopened.DeviceID() != store.DeviceID() {
		t.Fatal("state did not survive restart")
	}
	if err := reopened.Remove(profile.ID); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Profiles()) != 0 || reopened.SelectedProfileID() != "" {
		t.Fatal("profile was not removed")
	}
}

func TestStoreRejectsDuplicateWithoutOverwriting(t *testing.T) {
	store, err := OpenStore(t.TempDir(), xorProtector(0x33))
	if err != nil {
		t.Fatal(err)
	}
	uri := testOnboardingURI(t, "Primary", "vpn.example.com", "1.1.1.1", 0x41)
	if _, err := store.Import(uri); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Import(uri); err != ErrProfileExists {
		t.Fatalf("error=%v want %v", err, ErrProfileExists)
	}
}

func testOnboardingURI(t *testing.T, name, identity, address string, marker byte) string {
	t.Helper()
	uri, err := onboarding.EncodeURI(onboarding.Profile{
		Version: 2, CredentialID: base64.RawURLEncoding.EncodeToString(bytesOf(16, marker)),
		Name: name, ServerIdentity: identity, ServerAddresses: []string{address},
		HTTPSPath: "/private/https/session", WebRTCPath: "/private/webrtc/offer",
		HTTP3Path: "/private/http3/session", MaxParallelCarriers: 3,
		Profile: "web", Secret: base64.RawURLEncoding.EncodeToString(bytesOf(32, marker)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return uri
}
