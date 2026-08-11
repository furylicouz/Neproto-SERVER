package windowsclient

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestDecodeRequestIsStrictAndBounded(t *testing.T) {
	request, err := DecodeRequest(strings.NewReader(`{"id":"1","method":"status","params":{}}`))
	if err != nil || request.Method != MethodStatus {
		t.Fatalf("decode: %#v %v", request, err)
	}
	for _, raw := range []string{
		`{"id":"1","method":"status","params":{},"extra":true}`,
		`{"id":"1","id":"2","method":"status","params":{}}`,
		`{"id":"1","method":"status","params":{}} {}`,
	} {
		if _, err := DecodeRequest(strings.NewReader(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, err := DecodeRequest(bytes.NewReader(make([]byte, MaxIPCMessageBytes+1))); err == nil {
		t.Fatal("accepted oversized request")
	}
}

func TestIPCFrameRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	payload := []byte(`{"id":"1","method":"status","params":{}}`)
	if err := WriteFrame(&buffer, payload); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(buffer.Bytes()[:4]) != uint32(len(payload)) {
		t.Fatal("invalid frame prefix")
	}
	raw, err := ReadFrame(&buffer)
	if err != nil || !bytes.Equal(raw, payload) {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
}

func TestResponseRedactionDoesNotExposeSecret(t *testing.T) {
	response := Response{ID: "1", OK: false, Error: "credential rejected"}
	raw, err := EncodeResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("root_secret")) {
		t.Fatal("secret field exposed")
	}
}
