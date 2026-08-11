package windowsclient

import (
	"encoding/json"
	"testing"
)

func TestAPIImportsListsAndSelectsProfiles(t *testing.T) {
	store, err := OpenStore(t.TempDir(), xorProtector(0x62))
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{started: make(chan struct{}), release: make(chan struct{})}
	api := NewAPI(NewController(store, backend), store)
	uri := testOnboardingURI(t, "Primary", "vpn.example.com", "1.1.1.1", 0x66)
	params, _ := json.Marshal(map[string]string{"uri": uri})
	response := api.Handle(Request{ID: "1", Method: MethodProfilesImport, Params: params})
	if !response.OK {
		t.Fatalf("import failed: %s", response.Error)
	}
	response = api.Handle(Request{ID: "2", Method: MethodProfilesList, Params: json.RawMessage(`{}`)})
	if !response.OK {
		t.Fatalf("list failed: %s", response.Error)
	}
	raw, _ := json.Marshal(response.Result)
	var payload struct {
		Profiles []Profile `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Profiles) != 1 || payload.Profiles[0].Name != "Primary" {
		t.Fatalf("profiles=%s err=%v", raw, err)
	}
}

func TestAPIRejectsUnknownParameters(t *testing.T) {
	store, err := OpenStore(t.TempDir(), xorProtector(0x12))
	if err != nil {
		t.Fatal(err)
	}
	api := NewAPI(NewController(store, &fakeBackend{}), store)
	response := api.Handle(Request{ID: "1", Method: MethodProfilesRemove, Params: json.RawMessage(`{"id":"x","force":true}`)})
	if response.OK {
		t.Fatal("unknown parameter accepted")
	}
}
