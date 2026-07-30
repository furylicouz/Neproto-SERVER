package protocol

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestContinuityFrameRoundTrip(t *testing.T) {
	constellationID := continuityTestID(1)
	flowID := continuityTestID(17)
	frames := []ContinuityFrame{
		{Type: ContinuityConstellationCreate, MessageID: 1, ConstellationID: constellationID},
		{Type: ContinuityLeaseIssue, MessageID: 2, ConstellationID: constellationID, Token: bytes.Repeat([]byte{1}, MinContinuityTokenSize)},
		{Type: ContinuityLeaseAttach, MessageID: 3, ConstellationID: constellationID, Token: bytes.Repeat([]byte{2}, 64)},
		{Type: ContinuityLeaseAccept, MessageID: 4, ConstellationID: constellationID, Token: bytes.Repeat([]byte{3}, MaxContinuityTokenSize)},
		{Type: ContinuityFlowResume, MessageID: 5, ConstellationID: constellationID, FlowID: flowID, SendOffset: 4096, ReceiveOffset: 2048},
		{Type: ContinuityFlowAck, MessageID: 6, ConstellationID: constellationID, FlowID: flowID, SendOffset: 8192, ReceiveOffset: 4096},
		{Type: ContinuityFlowAbort, MessageID: 7, ConstellationID: constellationID, FlowID: flowID},
	}

	for _, frame := range frames {
		frame := frame
		t.Run(frame.Type.String(), func(t *testing.T) {
			raw, err := frame.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			parsed, err := ParseContinuityFrame(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !reflect.DeepEqual(parsed, frame) {
				t.Fatalf("round trip mismatch: got=%+v want=%+v", parsed, frame)
			}
		})
	}
}

func TestContinuityFrameRejectsInvalidFields(t *testing.T) {
	constellationID := continuityTestID(1)
	flowID := continuityTestID(17)
	validLease := ContinuityFrame{
		Type: ContinuityLeaseIssue, MessageID: 1, ConstellationID: constellationID,
		Token: bytes.Repeat([]byte{1}, MinContinuityTokenSize),
	}
	validFlow := ContinuityFrame{
		Type: ContinuityFlowResume, MessageID: 1, ConstellationID: constellationID,
		FlowID: flowID, SendOffset: 1, ReceiveOffset: 1,
	}

	tests := map[string]ContinuityFrame{
		"unknown type":          withContinuityType(validLease, 255),
		"zero message id":       withContinuityMessageID(validLease, 0),
		"large message id":      withContinuityMessageID(validLease, MaxSequence+1),
		"zero constellation id": withContinuityConstellationID(validLease, ContinuityID{}),
		"lease flow id":         withContinuityFlowID(validLease, flowID),
		"lease send offset":     withContinuitySendOffset(validLease, 1),
		"lease short token":     withContinuityToken(validLease, bytes.Repeat([]byte{1}, MinContinuityTokenSize-1)),
		"lease oversized token": withContinuityToken(validLease, bytes.Repeat([]byte{1}, MaxContinuityTokenSize+1)),
		"flow zero id":          withContinuityFlowID(validFlow, ContinuityID{}),
		"flow token":            withContinuityToken(validFlow, bytes.Repeat([]byte{1}, MinContinuityTokenSize)),
		"flow send offset":      withContinuitySendOffset(validFlow, MaxSequence+1),
		"flow receive offset":   withContinuityReceiveOffset(validFlow, MaxSequence+1),
		"abort non-zero offset": withContinuitySendOffset(ContinuityFrame{Type: ContinuityFlowAbort, MessageID: 1, ConstellationID: constellationID, FlowID: flowID}, 1),
		"create flow id":        withContinuityFlowID(ContinuityFrame{Type: ContinuityConstellationCreate, MessageID: 1, ConstellationID: constellationID}, flowID),
		"create token":          withContinuityToken(ContinuityFrame{Type: ContinuityConstellationCreate, MessageID: 1, ConstellationID: constellationID}, bytes.Repeat([]byte{1}, MinContinuityTokenSize)),
	}

	for name, frame := range tests {
		frame := frame
		t.Run(name, func(t *testing.T) {
			if _, err := frame.MarshalBinary(); !errors.Is(err, ErrInvalidContinuity) {
				t.Fatalf("marshal error=%v", err)
			}
		})
	}
}

func TestParseContinuityFrameRejectsNonCanonicalAndTrailingData(t *testing.T) {
	frame := ContinuityFrame{
		Type: ContinuityFlowAck, MessageID: 1,
		ConstellationID: continuityTestID(1), FlowID: continuityTestID(17),
		SendOffset: 5, ReceiveOffset: 3,
	}
	raw, err := frame.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	nonCanonical := append([]byte(nil), raw...)
	nonCanonical = append(nonCanonical[:6], append([]byte{0x81, 0x00}, nonCanonical[7:]...)...)
	if _, err := ParseContinuityFrame(nonCanonical); !errors.Is(err, ErrInvalidContinuity) {
		t.Fatalf("non-canonical message id error=%v", err)
	}

	trailing := append(append([]byte(nil), raw...), 0)
	if _, err := ParseContinuityFrame(trailing); !errors.Is(err, ErrInvalidContinuity) {
		t.Fatalf("trailing data error=%v", err)
	}
}

func TestContinuityTLVRoundTrip(t *testing.T) {
	frame := ContinuityFrame{
		Type: ContinuityFlowResume, MessageID: 9,
		ConstellationID: continuityTestID(1), FlowID: continuityTestID(17),
		SendOffset: 1234, ReceiveOffset: 567,
	}
	tlv, err := NewContinuityTLV(frame)
	if err != nil {
		t.Fatalf("new TLV: %v", err)
	}
	if tlv.Type != ExtensionTLVContinuity || tlv.Mandatory() {
		t.Fatalf("unexpected TLV type=%d mandatory=%v", tlv.Type, tlv.Mandatory())
	}
	parsed, err := ParseContinuityTLV(tlv)
	if err != nil {
		t.Fatalf("parse TLV: %v", err)
	}
	if !reflect.DeepEqual(parsed, frame) {
		t.Fatalf("TLV round trip mismatch: got=%+v want=%+v", parsed, frame)
	}
}

func TestLegacyExtensionParametersIgnoreOptionalContinuityTLV(t *testing.T) {
	parameters := ExtensionParameters{
		Capabilities:           CapabilityAdaptiveWindow,
		MaxSessionReceiveBytes: 8 * 1024 * 1024,
		MaxStreamWindowBytes:   1024 * 1024,
	}
	envelope, err := parameters.Envelope(ExtensionOffer, 1)
	if err != nil {
		t.Fatal(err)
	}
	tlv, err := NewContinuityTLV(ContinuityFrame{
		Type: ContinuityLeaseIssue, MessageID: 2,
		ConstellationID: continuityTestID(1),
		Token:           bytes.Repeat([]byte{7}, MinContinuityTokenSize),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope.TLVs = append(envelope.TLVs, tlv)

	parsed, err := ParseExtensionParameters(envelope)
	if err != nil {
		t.Fatalf("legacy parameters rejected optional continuity TLV: %v", err)
	}
	if parsed != parameters {
		t.Fatalf("legacy parameters changed: got=%+v want=%+v", parsed, parameters)
	}
}

func TestConstellationCapabilityDoesNotOverlapExistingCapabilities(t *testing.T) {
	existing := CapabilityReliableUDP | CapabilityUnreliableDatagrams |
		CapabilityAdaptiveWindow | CapabilityCarrierMigration | CapabilityMosaicCover
	if CapabilityConstellationContinuity == 0 || CapabilityConstellationContinuity&existing != 0 {
		t.Fatalf("constellation capability overlaps existing mask: %x", CapabilityConstellationContinuity)
	}
	if CapabilityForwardSecrecy == 0 || CapabilityForwardSecrecy&(existing|CapabilityConstellationContinuity) != 0 {
		t.Fatalf("forward secrecy capability overlaps existing mask: %x", CapabilityForwardSecrecy)
	}
}

func continuityTestID(seed byte) ContinuityID {
	var id ContinuityID
	for index := range id {
		id[index] = seed + byte(index)
	}
	return id
}

func withContinuityType(frame ContinuityFrame, messageType ContinuityMessageType) ContinuityFrame {
	frame.Type = messageType
	return frame
}

func withContinuityMessageID(frame ContinuityFrame, messageID uint64) ContinuityFrame {
	frame.MessageID = messageID
	return frame
}

func withContinuityConstellationID(frame ContinuityFrame, id ContinuityID) ContinuityFrame {
	frame.ConstellationID = id
	return frame
}

func withContinuityFlowID(frame ContinuityFrame, id ContinuityID) ContinuityFrame {
	frame.FlowID = id
	return frame
}

func withContinuitySendOffset(frame ContinuityFrame, offset uint64) ContinuityFrame {
	frame.SendOffset = offset
	return frame
}

func withContinuityReceiveOffset(frame ContinuityFrame, offset uint64) ContinuityFrame {
	frame.ReceiveOffset = offset
	return frame
}

func withContinuityToken(frame ContinuityFrame, token []byte) ContinuityFrame {
	frame.Token = token
	return frame
}
