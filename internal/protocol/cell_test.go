package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

func TestTypeMapIsDeterministicAndBijective(t *testing.T) {
	seed := [32]byte{0x42, 0x18, 0x99}
	first, err := NewTypeMap(seed)
	if err != nil {
		t.Fatalf("create first map: %v", err)
	}
	second, err := NewTypeMap(seed)
	if err != nil {
		t.Fatalf("create second map: %v", err)
	}
	if first != second {
		t.Fatal("same seed produced different type maps")
	}

	seen := make(map[byte]CellKind, CellKindCount)
	for kind := CellOpen; kind < CellKindCount; kind++ {
		wire, err := first.EncodeKind(kind)
		if err != nil {
			t.Fatalf("encode kind %d: %v", kind, err)
		}
		if wire == 0 {
			t.Fatalf("kind %d mapped to reserved zero code", kind)
		}
		if previous, exists := seen[wire]; exists {
			t.Fatalf("wire code %d maps both %d and %d", wire, previous, kind)
		}
		seen[wire] = kind
		decoded, err := first.DecodeKind(wire)
		if err != nil {
			t.Fatalf("decode wire code %d: %v", wire, err)
		}
		if decoded != kind {
			t.Fatalf("wire code %d decoded as %d, want %d", wire, decoded, kind)
		}
	}
}

func TestTypeMapChangesWithSessionSeed(t *testing.T) {
	first, err := NewTypeMap([32]byte{1})
	if err != nil {
		t.Fatalf("create first map: %v", err)
	}
	second, err := NewTypeMap([32]byte{2})
	if err != nil {
		t.Fatalf("create second map: %v", err)
	}
	if first == second {
		t.Fatal("different session seeds produced identical type maps")
	}
}

func TestCellRoundTrip(t *testing.T) {
	typeMap, err := NewTypeMap([32]byte{0x77})
	if err != nil {
		t.Fatalf("create type map: %v", err)
	}
	original := Cell{
		Kind:     CellData,
		StreamID: 17,
		Sequence: 9,
		Payload:  []byte("hello through chameleon"),
		Padding:  bytes.Repeat([]byte{0xa3}, 127),
	}

	raw, err := EncodeCell(typeMap, original)
	if err != nil {
		t.Fatalf("encode cell: %v", err)
	}
	decoded, err := DecodeCell(typeMap, raw)
	if err != nil {
		t.Fatalf("decode cell: %v", err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("cell mismatch\ngot:  %#v\nwant: %#v", decoded, original)
	}
}

func TestCellWireTypeChangesAcrossSessions(t *testing.T) {
	first, err := NewTypeMap([32]byte{0x11})
	if err != nil {
		t.Fatalf("create first map: %v", err)
	}
	second, err := NewTypeMap([32]byte{0x22})
	if err != nil {
		t.Fatalf("create second map: %v", err)
	}
	cell := Cell{Kind: CellOpen, StreamID: 1}

	firstRaw, err := EncodeCell(first, cell)
	if err != nil {
		t.Fatalf("encode first cell: %v", err)
	}
	secondRaw, err := EncodeCell(second, cell)
	if err != nil {
		t.Fatalf("encode second cell: %v", err)
	}
	if firstRaw[0] == secondRaw[0] {
		t.Fatalf("test seeds unexpectedly produced the same wire code %d", firstRaw[0])
	}
}

func TestDecodeCellRejectsNonCanonicalVarint(t *testing.T) {
	typeMap, err := NewTypeMap([32]byte{0x81})
	if err != nil {
		t.Fatalf("create type map: %v", err)
	}
	wireType, err := typeMap.EncodeKind(CellData)
	if err != nil {
		t.Fatalf("encode kind: %v", err)
	}
	raw := []byte{wireType, 0x81, 0x00, 0x00, 0x00, 0x00}

	_, err = DecodeCell(typeMap, raw)
	if !errors.Is(err, ErrNonCanonicalVarint) {
		t.Fatalf("expected non-canonical varint error, got %v", err)
	}
}

func TestDecodeCellRejectsUnknownType(t *testing.T) {
	typeMap, err := NewTypeMap([32]byte{0x91})
	if err != nil {
		t.Fatalf("create type map: %v", err)
	}
	known := make(map[byte]bool, CellKindCount)
	for kind := CellOpen; kind < CellKindCount; kind++ {
		wire, err := typeMap.EncodeKind(kind)
		if err != nil {
			t.Fatalf("encode kind: %v", err)
		}
		known[wire] = true
	}
	unknown := byte(1)
	for known[unknown] {
		unknown++
	}

	_, err = DecodeCell(typeMap, []byte{unknown, 0, 0, 0, 0})
	if !errors.Is(err, ErrUnknownCellType) {
		t.Fatalf("expected unknown type error, got %v", err)
	}
}

func TestCellRejectsBoundsAndInvalidStream(t *testing.T) {
	typeMap, err := NewTypeMap([32]byte{0xa1})
	if err != nil {
		t.Fatalf("create type map: %v", err)
	}
	tests := []struct {
		name string
		cell Cell
		err  error
	}{
		{
			name: "payload too large",
			cell: Cell{Kind: CellData, StreamID: 1, Payload: make([]byte, MaxCellPayloadSize+1)},
			err:  ErrCellTooLarge,
		},
		{
			name: "padding too large",
			cell: Cell{Kind: CellData, StreamID: 1, Padding: make([]byte, MaxCellPaddingSize+1)},
			err:  ErrCellTooLarge,
		},
		{
			name: "stream cell on control stream",
			cell: Cell{Kind: CellData, StreamID: 0},
			err:  ErrInvalidCell,
		},
		{
			name: "control cell on data stream",
			cell: Cell{Kind: CellPing, StreamID: 3},
			err:  ErrInvalidCell,
		},
		{
			name: "stream id above protocol range",
			cell: Cell{Kind: CellData, StreamID: MaxStreamID + 1},
			err:  ErrInvalidCell,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := EncodeCell(typeMap, tt.cell)
			if !errors.Is(err, tt.err) {
				t.Fatalf("expected %v, got %v", tt.err, err)
			}
		})
	}
}

func TestDecodeCellRejectsTruncationAndTrailingBytes(t *testing.T) {
	typeMap, err := NewTypeMap([32]byte{0xb1})
	if err != nil {
		t.Fatalf("create type map: %v", err)
	}
	raw, err := EncodeCell(typeMap, Cell{
		Kind:     CellData,
		StreamID: 1,
		Payload:  []byte("payload"),
		Padding:  []byte("pad"),
	})
	if err != nil {
		t.Fatalf("encode cell: %v", err)
	}

	for _, candidate := range [][]byte{
		raw[:len(raw)-1],
		append(append([]byte(nil), raw...), 0),
	} {
		if _, err := DecodeCell(typeMap, candidate); !errors.Is(err, ErrInvalidCell) {
			t.Fatalf("expected invalid cell error, got %v", err)
		}
	}
}

func TestDecodeCellRejectsAdvertisedOversizedPayloadBeforeAllocation(t *testing.T) {
	typeMap, err := NewTypeMap([32]byte{0xc1})
	if err != nil {
		t.Fatalf("create type map: %v", err)
	}
	wireType, err := typeMap.EncodeKind(CellData)
	if err != nil {
		t.Fatalf("encode kind: %v", err)
	}
	raw := []byte{wireType}
	raw = binary.AppendUvarint(raw, 1)
	raw = binary.AppendUvarint(raw, 0)
	raw = binary.AppendUvarint(raw, MaxCellPayloadSize+1)
	raw = binary.AppendUvarint(raw, 0)

	_, err = DecodeCell(typeMap, raw)
	if !errors.Is(err, ErrCellTooLarge) {
		t.Fatalf("expected cell too large error, got %v", err)
	}
}
