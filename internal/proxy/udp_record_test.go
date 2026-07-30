package proxy

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func TestUDPRecordExactRoundTrip(t *testing.T) {
	original := UDPRecord{
		Type: UDPRecordDatagram, PacketID: 7, HasTarget: true,
		Target: Target{Host: "1.1.1.1", Port: 53}, Payload: []byte("dns"),
	}
	raw, err := EncodeUDPRecord(original)
	if err != nil {
		t.Fatalf("encode record: %v", err)
	}
	const expectedHex = "01070108012401010101003503646e73"
	if actual := hex.EncodeToString(raw); actual != expectedHex {
		t.Fatalf("wire=%s, want %s", actual, expectedHex)
	}
	decoded, err := DecodeUDPRecord(raw)
	if err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if decoded.Type != original.Type || decoded.PacketID != original.PacketID ||
		decoded.HasTarget != original.HasTarget || decoded.Target != original.Target ||
		!bytes.Equal(decoded.Payload, original.Payload) {
		t.Fatalf("record mismatch: %+v", decoded)
	}
}

func TestUDPRecordSupportsFixedDatagramErrorAndClose(t *testing.T) {
	records := []UDPRecord{
		{Type: UDPRecordDatagram, PacketID: 1, Payload: []byte{}},
		{Type: UDPRecordError, PacketID: 2, ErrorCode: UDPErrorPolicyDenied, ErrorMessage: "target denied"},
		{Type: UDPRecordClose, PacketID: 3},
	}
	for _, original := range records {
		raw, err := EncodeUDPRecord(original)
		if err != nil {
			t.Fatalf("encode %+v: %v", original, err)
		}
		decoded, err := DecodeUDPRecord(raw)
		if err != nil {
			t.Fatalf("decode %+v: %v", original, err)
		}
		if decoded.Type != original.Type || decoded.PacketID != original.PacketID ||
			decoded.ErrorCode != original.ErrorCode || decoded.ErrorMessage != original.ErrorMessage ||
			!bytes.Equal(decoded.Payload, original.Payload) {
			t.Fatalf("round trip mismatch: got %+v want %+v", decoded, original)
		}
	}
}

func TestUDPRecordRejectsMalformedAndOversizedInput(t *testing.T) {
	invalidRecords := []UDPRecord{
		{Type: UDPRecordDatagram, PacketID: 0},
		{Type: UDPRecordDatagram, PacketID: 1, Payload: make([]byte, MaxUDPDatagramPayload+1)},
		{Type: UDPRecordDatagram, PacketID: 1, HasTarget: true},
		{Type: UDPRecordError, PacketID: 1, ErrorCode: 0},
		{Type: UDPRecordError, PacketID: 1, ErrorCode: UDPErrorPolicyDenied, ErrorMessage: string(bytes.Repeat([]byte{'x'}, MaxUDPErrorMessageBytes+1))},
		{Type: UDPRecordClose, PacketID: 1, Payload: []byte{1}},
		{Type: UDPRecordType(0xff), PacketID: 1},
	}
	for _, record := range invalidRecords {
		if _, err := EncodeUDPRecord(record); !errors.Is(err, ErrInvalidUDPRecord) {
			t.Fatalf("invalid record accepted: %+v err=%v", record, err)
		}
	}

	valid, err := EncodeUDPRecord(UDPRecord{Type: UDPRecordDatagram, PacketID: 1, Payload: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	candidates := [][]byte{
		nil,
		valid[:len(valid)-1],
		append(append([]byte(nil), valid...), 0),
		{byte(UDPRecordDatagram), 0x81, 0x00, 0, 0},
		{byte(UDPRecordDatagram), 1, 0x80, 0},
		{byte(UDPRecordClose), '0', 0, 2, '0', '0'},
	}
	for _, raw := range candidates {
		if _, err := DecodeUDPRecord(raw); !errors.Is(err, ErrInvalidUDPRecord) {
			t.Fatalf("malformed record accepted: %x err=%v", raw, err)
		}
	}
}
