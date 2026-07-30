package protocol

import (
	"bytes"
	"testing"
)

func FuzzParseExtensionEnvelope(f *testing.F) {
	seed, err := (ExtensionEnvelope{
		Type: ExtensionOffer, MessageID: 1,
		TLVs: []ExtensionTLV{NewExtensionCapabilitiesTLV(CapabilityReliableUDP)},
	}).MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("NPEX"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, raw []byte) {
		envelope, parseErr := ParseExtensionEnvelope(raw)
		if parseErr != nil {
			return
		}
		canonical, marshalErr := envelope.MarshalBinary()
		if marshalErr != nil {
			t.Fatalf("parsed envelope cannot marshal: %v", marshalErr)
		}
		if !bytes.Equal(canonical, raw) {
			t.Fatalf("accepted noncanonical envelope: %x != %x", raw, canonical)
		}
	})
}
