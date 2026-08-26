package windowsclient

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"neproto.local/chameleon/internal/config"
)

type legacyFixtureProtector struct {
	unprotectCalls int
	protectCalls   int
}

func (p *legacyFixtureProtector) Protect([]byte) ([]byte, error) {
	p.protectCalls++
	return nil, errors.New("legacy fixture must not be rewritten")
}

func (p *legacyFixtureProtector) Unprotect(ciphertext []byte) ([]byte, error) {
	p.unprotectCalls++
	if !bytes.Equal(ciphertext, []byte{1, 2, 3, 4}) {
		return nil, errors.New("unexpected legacy ciphertext")
	}
	return []byte(base64.RawURLEncoding.EncodeToString(bytesOf(32, 0x44))), nil
}

func TestLegacyStoreIsReadThroughIdempotentAndHostRedacted(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "legacy-client-state-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	statePath := filepath.Join(directory, storeFileName)
	if err := os.WriteFile(statePath, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	protector := &legacyFixtureProtector{}

	store, err := OpenStore(directory, protector)
	if err != nil {
		t.Fatal(err)
	}
	profiles := store.Profiles()
	if len(profiles) != 1 || profiles[0].ID != "legacy-primary" ||
		store.SelectedProfileID() != "legacy-primary" {
		t.Fatalf("profiles=%+v selected=%q", profiles, store.SelectedProfileID())
	}
	_, clientConfig, secret, err := store.Selected()
	if err != nil {
		t.Fatal(err)
	}
	if protector.unprotectCalls != 1 || protector.protectCalls != 0 || secret == "" {
		t.Fatalf("unprotect=%d protect=%d secret=%t", protector.unprotectCalls, protector.protectCalls, secret != "")
	}
	if !bytes.Contains(clientConfig, []byte(`"carrier_policy":"http3-only"`)) ||
		bytes.Contains(clientConfig, []byte(secret)) {
		t.Fatalf("unsafe migrated config: %s", clientConfig)
	}
	parsed, err := config.ParseMobileClientBytes(clientConfig, secret)
	if err != nil || parsed.ServerIdentity != "legacy.example.com" ||
		parsed.CarrierPolicy != config.CarrierPolicyHTTP3Only {
		t.Fatalf("legacy profile did not adapt to strict HTTP/3: %+v err=%v", parsed, err)
	}

	api := NewAPI(NewController(store, &fakeBackend{}), store)
	response := api.Handle(Request{ID: "legacy-list", Method: MethodHostV1ProfilesList, Params: json.RawMessage(`{}`)})
	if !response.OK {
		t.Fatalf("host list failed: %#v", response)
	}
	hostJSON, _ := json.Marshal(response.Result)
	for _, forbidden := range [][]byte{
		[]byte("legacy-credential-fixture"), []byte("protected_secret"), []byte("AQIDBA"), []byte("np2://"),
	} {
		if bytes.Contains(hostJSON, forbidden) {
			t.Fatalf("Host API exposed %q: %s", forbidden, hostJSON)
		}
	}

	afterFirstOpen, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterFirstOpen, fixture) {
		t.Fatal("legacy state was rewritten during read-through migration")
	}
	if _, err := OpenStore(directory, protector); err != nil {
		t.Fatal(err)
	}
	afterSecondOpen, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(afterSecondOpen, fixture) {
		t.Fatalf("second open was not idempotent: %v", err)
	}
}

func TestHostProfileCredentialFlagDoesNotAssumeCiphertextExists(t *testing.T) {
	store, err := OpenStore(t.TempDir(), xorProtector(0x29))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.Import(testOnboardingURI(t, "Primary", "vpn.example.com", "1.1.1.1", 0x28))
	if err != nil {
		t.Fatal(err)
	}
	if !store.HasCredential(profile.ID) {
		t.Fatal("valid protected credential was not detected")
	}
	store.mu.Lock()
	store.state.Profiles[0].ProtectedSecret = ""
	store.mu.Unlock()
	if store.HasCredential(profile.ID) {
		t.Fatal("missing protected credential was reported as available")
	}
	api := NewAPI(NewController(store, &fakeBackend{}), store)
	summary := api.hostProfile(profile)
	if summary.HasCredential {
		t.Fatal("Host API assumed credential availability")
	}
}
