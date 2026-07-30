package protocol

import "testing"

func FuzzParseContinuityOpenMetadata(f *testing.F) {
	seed, err := (ContinuityOpenMetadata{
		Mode: ContinuityOpenNew, ConstellationID: continuityTestID(1),
		FlowID: continuityTestID(17), LeaseKey: continuityTestID(33),
		Epoch: 1, Inner: []byte{1, 2, 3},
	}).MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("NPCO"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		metadata, err := ParseContinuityOpenMetadata(raw)
		if err != nil {
			return
		}
		encoded, err := metadata.MarshalBinary()
		if err != nil {
			t.Fatalf("accepted metadata cannot marshal: %v", err)
		}
		if string(encoded) != string(raw) {
			t.Fatalf("accepted non-canonical encoding")
		}
	})
}
