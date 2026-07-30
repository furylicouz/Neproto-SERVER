package protocol

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func TestExtensionEnvelopeExactRoundTrip(t *testing.T) {
	associations, err := NewExtensionUvarintTLV(ExtensionTLVMaxUDPAssociations, 128)
	if err != nil {
		t.Fatal(err)
	}
	maximumPayload, err := NewExtensionUvarintTLV(ExtensionTLVMaxUDPPayload, 65507)
	if err != nil {
		t.Fatal(err)
	}
	original := ExtensionEnvelope{
		Type: ExtensionOffer, MessageID: 7,
		TLVs: []ExtensionTLV{
			NewExtensionCapabilitiesTLV(CapabilityReliableUDP | CapabilityUnreliableDatagrams),
			associations,
			maximumPayload,
		},
	}
	raw, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal extension: %v", err)
	}
	const expectedHex = "4e5045580101070301080000000000000003020280010303e3ff03"
	if actual := hex.EncodeToString(raw); actual != expectedHex {
		t.Fatalf("wire=%s, want %s", actual, expectedHex)
	}
	parsed, err := ParseExtensionEnvelope(raw)
	if err != nil {
		t.Fatalf("parse extension: %v", err)
	}
	if parsed.Type != original.Type || parsed.MessageID != original.MessageID ||
		len(parsed.TLVs) != len(original.TLVs) {
		t.Fatalf("parsed envelope mismatch: %+v", parsed)
	}
	for index := range original.TLVs {
		if parsed.TLVs[index].Type != original.TLVs[index].Type ||
			!bytes.Equal(parsed.TLVs[index].Value, original.TLVs[index].Value) {
			t.Fatalf("parsed TLV %d mismatch: %+v", index, parsed.TLVs[index])
		}
	}
	capabilities, err := parsed.TLVs[0].Capabilities()
	if err != nil || capabilities != CapabilityReliableUDP|CapabilityUnreliableDatagrams {
		t.Fatalf("capabilities=%d err=%v", capabilities, err)
	}
	associationsValue, err := parsed.TLVs[1].Uvarint()
	if err != nil || associationsValue != 128 {
		t.Fatalf("associations=%d err=%v", associationsValue, err)
	}
}

func TestExtensionEnvelopeRejectsNonCanonicalInputs(t *testing.T) {
	valid := ExtensionEnvelope{
		Type: ExtensionOffer, MessageID: 1,
		TLVs: []ExtensionTLV{NewExtensionCapabilitiesTLV(CapabilityReliableUDP)},
	}
	validRaw, err := valid.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "empty", raw: nil},
		{name: "wrong magic", raw: append([]byte("FAIL"), validRaw[4:]...)},
		{name: "wrong version", raw: append(append([]byte(nil), validRaw[:4]...), append([]byte{2}, validRaw[5:]...)...)},
		{name: "wrong message type", raw: append(append([]byte(nil), validRaw[:5]...), append([]byte{0xff}, validRaw[6:]...)...)},
		{name: "truncated", raw: validRaw[:len(validRaw)-1]},
		{name: "trailing", raw: append(append([]byte(nil), validRaw...), 0)},
		{name: "noncanonical message id", raw: append(append([]byte(nil), validRaw[:6]...), append([]byte{0x81, 0x00}, validRaw[7:]...)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseExtensionEnvelope(test.raw); !errors.Is(err, ErrInvalidExtension) {
				t.Fatalf("expected invalid extension, got %v", err)
			}
		})
	}
}

func TestExtensionEnvelopeRequiresSortedUniqueBoundedTLVs(t *testing.T) {
	tests := []struct {
		name string
		tlvs []ExtensionTLV
	}{
		{
			name: "duplicate",
			tlvs: []ExtensionTLV{{Type: 1, Value: []byte{1}}, {Type: 1, Value: []byte{2}}},
		},
		{
			name: "unsorted",
			tlvs: []ExtensionTLV{{Type: 2, Value: []byte{1}}, {Type: 1, Value: []byte{2}}},
		},
		{
			name: "zero type",
			tlvs: []ExtensionTLV{{Type: 0, Value: []byte{1}}},
		},
		{
			name: "oversized value",
			tlvs: []ExtensionTLV{{Type: 1, Value: make([]byte, MaxExtensionTLVValueSize+1)}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := ExtensionEnvelope{Type: ExtensionOffer, MessageID: 1, TLVs: test.tlvs}
			if _, err := envelope.MarshalBinary(); !errors.Is(err, ErrInvalidExtension) {
				t.Fatalf("expected invalid extension, got %v", err)
			}
		})
	}

	tooMany := make([]ExtensionTLV, MaxExtensionTLVs+1)
	for index := range tooMany {
		tooMany[index] = ExtensionTLV{Type: uint64(index + 1)}
	}
	envelope := ExtensionEnvelope{Type: ExtensionOffer, MessageID: 1, TLVs: tooMany}
	if _, err := envelope.MarshalBinary(); !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("expected TLV count rejection, got %v", err)
	}
}

func TestExtensionTLVHelpersRejectWrongRepresentations(t *testing.T) {
	mandatory := ExtensionTLV{Type: ExtensionMandatoryFlag | 99, Value: []byte{1}}
	if !mandatory.Mandatory() || mandatory.BaseType() != 99 {
		t.Fatalf("mandatory TLV metadata mismatch: %+v", mandatory)
	}
	if _, err := (ExtensionTLV{Type: ExtensionTLVCapabilities, Value: []byte{1}}).Capabilities(); !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("expected malformed capabilities rejection, got %v", err)
	}
	if _, err := (ExtensionTLV{Type: ExtensionTLVMaxUDPPayload, Value: []byte{0x81, 0x00}}).Uvarint(); !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("expected noncanonical uvarint rejection, got %v", err)
	}
	if _, err := NewExtensionUvarintTLV(0, 1); !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("expected zero type rejection, got %v", err)
	}
}

func TestExtensionParametersRoundTripAndSubsetValidation(t *testing.T) {
	offer := ExtensionParameters{
		Capabilities: ExtensionCapability(
			CapabilityReliableUDP | CapabilityUnreliableDatagrams |
				CapabilityAdaptiveWindow | CapabilityCarrierMigration,
		),
		MaxUDPAssociations:     256,
		MaxUDPPayload:          65507,
		UDPIdleTimeoutMS:       60_000,
		MaxSessionReceiveBytes: 64 * 1024 * 1024,
		MaxStreamWindowBytes:   8 * 1024 * 1024,
		UnreliableDatagramSize: 1200,
	}
	envelope, err := offer.Envelope(ExtensionOffer, 41)
	if err != nil {
		t.Fatalf("build offer: %v", err)
	}
	parsed, err := ParseExtensionParameters(envelope)
	if err != nil {
		t.Fatalf("parse offer parameters: %v", err)
	}
	if parsed != offer {
		t.Fatalf("parameters=%+v, want %+v", parsed, offer)
	}
	accept := offer
	accept.Capabilities &^= CapabilityUnreliableDatagrams | CapabilityCarrierMigration
	accept.MaxUDPAssociations = 128
	accept.MaxUDPPayload = 4096
	accept.UnreliableDatagramSize = 0
	if err := ValidateExtensionAccept(offer, accept); err != nil {
		t.Fatalf("valid subset rejected: %v", err)
	}
	accept.MaxUDPAssociations = offer.MaxUDPAssociations + 1
	if err := ValidateExtensionAccept(offer, accept); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("oversized accept error=%v", err)
	}
}

func TestMosaicCapabilityIsAdditiveAndOptional(t *testing.T) {
	offer := ExtensionParameters{
		Capabilities:           CapabilityReliableUDP | CapabilityMosaicCover,
		MaxUDPAssociations:     16,
		MaxUDPPayload:          65507,
		UDPIdleTimeoutMS:       60_000,
		MaxSessionReceiveBytes: 16 * 1024 * 1024,
		MaxStreamWindowBytes:   2 * 1024 * 1024,
	}
	envelope, err := offer.Envelope(ExtensionOffer, 51)
	if err != nil {
		t.Fatalf("encode Mosaic offer: %v", err)
	}
	parsed, err := ParseExtensionParameters(envelope)
	if err != nil || parsed.Capabilities&CapabilityMosaicCover == 0 {
		t.Fatalf("Mosaic capability did not round trip: %+v err=%v", parsed, err)
	}

	legacyAccept := offer
	legacyAccept.Capabilities &^= CapabilityMosaicCover
	if err := ValidateExtensionAccept(offer, legacyAccept); err != nil {
		t.Fatalf("legacy peer subset rejected: %v", err)
	}
}

func TestForwardSecretKeyShareIsCapabilityBound(t *testing.T) {
	parameters := ExtensionParameters{
		Capabilities:           CapabilityForwardSecrecy,
		MaxSessionReceiveBytes: 1024 * 1024,
		MaxStreamWindowBytes:   64 * 1024,
		ForwardSecretKeyShare:  [32]byte{1, 2, 3},
	}
	envelope, err := parameters.Envelope(ExtensionOffer, 1)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseExtensionParameters(envelope)
	if err != nil || parsed != parameters {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
	missing := parameters
	missing.ForwardSecretKeyShare = [32]byte{}
	if _, err := missing.Envelope(ExtensionOffer, 1); !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("missing key share error=%v", err)
	}
	unbound := parameters
	unbound.Capabilities &^= CapabilityForwardSecrecy
	if _, err := unbound.Envelope(ExtensionOffer, 1); !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("unbound key share error=%v", err)
	}
}

func TestExtensionParametersRejectUnknownMandatoryAndInvalidBounds(t *testing.T) {
	parameters := ExtensionParameters{
		Capabilities:           CapabilityReliableUDP,
		MaxUDPAssociations:     1,
		MaxUDPPayload:          1200,
		UDPIdleTimeoutMS:       5000,
		MaxSessionReceiveBytes: 1024 * 1024,
		MaxStreamWindowBytes:   64 * 1024,
	}
	envelope, err := parameters.Envelope(ExtensionOffer, 1)
	if err != nil {
		t.Fatal(err)
	}
	envelope.TLVs = append(envelope.TLVs, ExtensionTLV{
		Type: ExtensionMandatoryFlag | 99, Value: []byte{1},
	})
	if _, err := ParseExtensionParameters(envelope); !errors.Is(err, ErrUnsupportedExtension) {
		t.Fatalf("unknown mandatory TLV error=%v", err)
	}

	invalid := parameters
	invalid.MaxUDPPayload = 1199
	if _, err := invalid.Envelope(ExtensionOffer, 1); !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("invalid UDP payload error=%v", err)
	}
	invalid = parameters
	invalid.Capabilities |= CapabilityUnreliableDatagrams
	invalid.UnreliableDatagramSize = 0
	if _, err := invalid.Envelope(ExtensionOffer, 1); !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("missing unreliable limit error=%v", err)
	}
}
