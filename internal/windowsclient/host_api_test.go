package windowsclient

import (
	"bytes"
	"encoding/json"
	"testing"

	"neproto.local/chameleon/internal/clienthost"
)

func TestHostV1NegotiatesVersionAndFailsClosed(t *testing.T) {
	api := newHostAPITestAPI(t)

	response := api.Handle(Request{
		ID: "cap-1", Method: MethodHostV1Capabilities,
		Params: json.RawMessage(`{"api_major":1,"api_minor":0}`),
	})
	if !response.OK {
		t.Fatalf("capabilities failed: %#v", response)
	}
	raw, _ := json.Marshal(response.Result)
	if !bytes.Contains(raw, []byte(`"supports_http3_web_transport":true`)) ||
		!bytes.Contains(raw, []byte(`"platform":"windows"`)) {
		t.Fatalf("capabilities=%s", raw)
	}

	response = api.Handle(Request{
		ID: "cap-2", Method: MethodHostV1Capabilities,
		Params: json.RawMessage(`{"api_major":2,"api_minor":0}`),
	})
	if response.OK || response.HostError == nil ||
		response.HostError.Code != clienthost.CodeUnsupportedAPIVersion ||
		response.HostError.Stage != clienthost.StageHostNegotiation {
		t.Fatalf("mismatch response=%#v", response)
	}
}

func TestHostV1ProfilesAreRedactedAndOperationsAreValidated(t *testing.T) {
	api := newHostAPITestAPI(t)
	uri := testOnboardingURI(t, "Primary", "vpn.example.com", "1.1.1.1", 0x71)
	params, _ := json.Marshal(map[string]string{
		"onboarding_value": uri,
		"operation_id":     "import-1",
	})
	response := api.Handle(Request{ID: "profile-1", Method: MethodHostV1ProfilesImport, Params: params})
	if !response.OK {
		t.Fatalf("import failed: %#v", response)
	}
	raw, _ := json.Marshal(response.Result)
	for _, forbidden := range [][]byte{[]byte("credential_id"), []byte("protected_secret"), []byte("np2://")} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("host result exposed %q: %s", forbidden, raw)
		}
	}
	if !bytes.Contains(raw, []byte(`"has_credential":true`)) ||
		!bytes.Contains(raw, []byte(`"selected":true`)) {
		t.Fatalf("summary=%s", raw)
	}

	invalid, _ := json.Marshal(map[string]string{
		"onboarding_value": uri,
		"operation_id":     "contains space",
	})
	response = api.Handle(Request{ID: "profile-2", Method: MethodHostV1ProfilesImport, Params: invalid})
	if response.OK || response.HostError == nil || response.HostError.Code != clienthost.CodeInvalidProfile {
		t.Fatalf("invalid operation accepted: %#v", response)
	}
}

func TestHostV1StatusAndDiagnosticsUseBoundedStableModels(t *testing.T) {
	api := newHostAPITestAPI(t)
	api.controller.mu.Lock()
	api.controller.appendLogLocked("error", "np2://import/v2/secret")
	api.controller.appendLogLocked("info", "HTTP/3 probe started")
	api.controller.mu.Unlock()

	status := api.Handle(Request{ID: "status-1", Method: MethodHostV1Status, Params: json.RawMessage(`{}`)})
	if !status.OK {
		t.Fatalf("status failed: %#v", status)
	}
	statusRaw, _ := json.Marshal(status.Result)
	if !bytes.Contains(statusRaw, []byte(`"state":"disconnected"`)) ||
		!bytes.Contains(statusRaw, []byte(`"carrier":"none"`)) ||
		!bytes.Contains(statusRaw, []byte(`"sequence":`)) {
		t.Fatalf("status=%s", statusRaw)
	}

	diagnostics := api.Handle(Request{
		ID: "diagnostics-1", Method: MethodHostV1Diagnostics,
		Params: json.RawMessage(`{"limit":1}`),
	})
	if !diagnostics.OK {
		t.Fatalf("diagnostics failed: %#v", diagnostics)
	}
	raw, _ := json.Marshal(diagnostics.Result)
	if bytes.Contains(raw, []byte("np2://")) || !bytes.Contains(raw, []byte(`"carrier_policy":"http3-only"`)) {
		t.Fatalf("diagnostics=%s", raw)
	}
	var payload struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Events) != 1 {
		t.Fatalf("events=%d err=%v raw=%s", len(payload.Events), err, raw)
	}
}

func newHostAPITestAPI(t *testing.T) *API {
	t.Helper()
	store, err := OpenStore(t.TempDir(), xorProtector(0x52))
	if err != nil {
		t.Fatal(err)
	}
	return NewAPI(NewController(store, &fakeBackend{}), store)
}
