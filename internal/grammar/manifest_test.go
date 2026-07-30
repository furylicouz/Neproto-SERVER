package grammar

import (
	"bytes"
	"errors"
	"testing"
)

func TestDefaultManifestCanonicalRoundTrip(t *testing.T) {
	want := DefaultManifest()
	raw, err := want.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip=%+v want=%+v", got, want)
	}
}

func TestManifestRejectsUnknownUnboundedAndCarrierInvalidData(t *testing.T) {
	valid, err := DefaultManifest().MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	tests := [][]byte{
		{},
		append(valid, []byte(` {}`)...),
		bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":2`), 1),
		bytes.Replace(valid, []byte(`"https":{`), []byte(`"unknown":1,"https":{`), 1),
		bytes.Replace(valid, []byte(`"max_concurrent":3`), []byte(`"max_concurrent":9`), 1),
		bytes.Replace(valid, []byte(`"max_datagram_bytes":0`), []byte(`"max_datagram_bytes":1200`), 1),
	}
	for _, raw := range tests {
		if _, err := Parse(raw); !errors.Is(err, ErrManifestInvalid) {
			t.Fatalf("raw=%q err=%v", raw, err)
		}
	}
	if _, err := Parse(bytes.Repeat([]byte{'x'}, MaxManifestSize+1)); !errors.Is(err, ErrManifestSize) {
		t.Fatalf("oversize err=%v", err)
	}
}

func FuzzManifestParse(f *testing.F) {
	raw, _ := DefaultManifest().MarshalBinary()
	f.Add(raw)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, candidate []byte) {
		manifest, err := Parse(candidate)
		if err != nil {
			return
		}
		if err := manifest.Validate(); err != nil {
			t.Fatalf("parser accepted invalid manifest: %v", err)
		}
	})
}
