package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestContinuityOpenMetadataRoundTripsNewAndResume(t *testing.T) {
	constellationID := continuityTestID(1)
	flowID := continuityTestID(17)
	leaseKey := continuityTestID(33)
	tests := []ContinuityOpenMetadata{
		{
			Mode: ContinuityOpenNew, ConstellationID: constellationID,
			FlowID: flowID, LeaseKey: leaseKey, Epoch: 1,
			Inner: []byte{1, 2, 3, 4},
		},
		{
			Mode: ContinuityOpenResume, ConstellationID: constellationID,
			FlowID: flowID, LeaseKey: leaseKey, Epoch: 2,
			SendOffset: 1234, ReceiveOffset: 5678,
		},
	}
	for _, expected := range tests {
		raw, err := expected.MarshalBinary()
		if err != nil {
			t.Fatalf("mode=%v marshal: %v", expected.Mode, err)
		}
		if len(raw) > MaxContinuityOpenMetadata {
			t.Fatalf("encoded length=%d", len(raw))
		}
		actual, err := ParseContinuityOpenMetadata(raw)
		if err != nil {
			t.Fatalf("mode=%v parse: %v", expected.Mode, err)
		}
		if actual.Mode != expected.Mode || actual.ConstellationID != expected.ConstellationID ||
			actual.FlowID != expected.FlowID || actual.LeaseKey != expected.LeaseKey ||
			actual.Epoch != expected.Epoch || actual.SendOffset != expected.SendOffset ||
			actual.ReceiveOffset != expected.ReceiveOffset || !bytes.Equal(actual.Inner, expected.Inner) {
			t.Fatalf("actual=%+v expected=%+v", actual, expected)
		}
	}
}

func TestContinuityOpenMetadataStrictValidation(t *testing.T) {
	valid := ContinuityOpenMetadata{
		Mode: ContinuityOpenNew, ConstellationID: continuityTestID(1),
		FlowID: continuityTestID(17), LeaseKey: continuityTestID(33),
		Epoch: 1, Inner: []byte{1},
	}
	invalid := []ContinuityOpenMetadata{
		{},
		{Mode: ContinuityOpenNew, ConstellationID: valid.ConstellationID, FlowID: valid.FlowID, LeaseKey: valid.LeaseKey, Epoch: 2, Inner: []byte{1}},
		{Mode: ContinuityOpenNew, ConstellationID: valid.ConstellationID, FlowID: valid.FlowID, LeaseKey: valid.LeaseKey, Epoch: 1},
		{Mode: ContinuityOpenResume, ConstellationID: valid.ConstellationID, FlowID: valid.FlowID, LeaseKey: valid.LeaseKey, Epoch: 1},
		{Mode: ContinuityOpenResume, ConstellationID: valid.ConstellationID, FlowID: valid.FlowID, LeaseKey: valid.LeaseKey, Epoch: 2, Inner: []byte{1}},
	}
	for _, metadata := range invalid {
		if _, err := metadata.MarshalBinary(); !errors.Is(err, ErrInvalidContinuityOpen) {
			t.Fatalf("metadata=%+v error=%v", metadata, err)
		}
	}
	raw, err := valid.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	mutations := [][]byte{
		append(append([]byte(nil), raw...), 0),
		append([]byte("BAD!"), raw[4:]...),
		append([]byte(nil), raw[:len(raw)-1]...),
	}
	for _, mutation := range mutations {
		if _, err := ParseContinuityOpenMetadata(mutation); !errors.Is(err, ErrInvalidContinuityOpen) {
			t.Fatalf("mutation=%x error=%v", mutation, err)
		}
	}
}

func TestContinuityOpenMetadataRejectsNonCanonicalVarints(t *testing.T) {
	metadata := ContinuityOpenMetadata{
		Mode: ContinuityOpenResume, ConstellationID: continuityTestID(1),
		FlowID: continuityTestID(17), LeaseKey: continuityTestID(33), Epoch: 2,
	}
	raw, err := metadata.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	const epochOffset = 4 + 1 + 1 + 16 + 16 + 16
	nonCanonical := append([]byte(nil), raw[:epochOffset]...)
	nonCanonical = append(nonCanonical, 0x82, 0x00)
	nonCanonical = append(nonCanonical, raw[epochOffset+1:]...)
	if _, err := ParseContinuityOpenMetadata(nonCanonical); !errors.Is(err, ErrInvalidContinuityOpen) {
		t.Fatalf("non-canonical error=%v raw=%x", err, nonCanonical)
	}

	oversized := metadata
	oversized.Mode = ContinuityOpenNew
	oversized.Epoch = 1
	oversized.Inner = bytes.Repeat([]byte{1}, MaxContinuityOpenMetadata)
	if _, err := oversized.MarshalBinary(); !errors.Is(err, ErrInvalidContinuityOpen) {
		t.Fatalf("oversized error=%v", err)
	}
}

func TestContinuityOpenDetectionRequiresExactMagic(t *testing.T) {
	metadata := ContinuityOpenMetadata{
		Mode: ContinuityOpenNew, ConstellationID: continuityTestID(1),
		FlowID: continuityTestID(17), LeaseKey: continuityTestID(33), Epoch: 1,
		Inner: binary.AppendUvarint(nil, 1),
	}
	raw, err := metadata.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !IsContinuityOpenMetadata(raw) || IsContinuityOpenMetadata([]byte("NPC")) ||
		IsContinuityOpenMetadata([]byte("NPCXlegacy")) {
		t.Fatal("continuity OPEN magic detection mismatch")
	}
}
