package protocol

import "testing"

func FuzzDecodeCell(f *testing.F) {
	typeMap, err := NewTypeMap([32]byte{0xf1})
	if err != nil {
		f.Fatalf("create type map: %v", err)
	}
	seed, err := EncodeCell(typeMap, Cell{
		Kind:     CellData,
		StreamID: 1,
		Sequence: 2,
		Payload:  []byte("seed"),
		Padding:  []byte{1, 2, 3},
	})
	if err != nil {
		f.Fatalf("encode seed: %v", err)
	}
	f.Add(seed)
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = DecodeCell(typeMap, raw)
	})
}
