package protocol

import (
	"bytes"
	"testing"
)

func FuzzParseContinuityFrame(f *testing.F) {
	seeds := []ContinuityFrame{
		{
			Type: ContinuityLeaseIssue, MessageID: 1,
			ConstellationID: continuityTestID(1),
			Token:           bytes.Repeat([]byte{1}, MinContinuityTokenSize),
		},
		{
			Type: ContinuityFlowResume, MessageID: 2,
			ConstellationID: continuityTestID(1), FlowID: continuityTestID(17),
			SendOffset: 8192, ReceiveOffset: 4096,
		},
	}
	for _, seed := range seeds {
		raw, err := seed.MarshalBinary()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		frame, err := ParseContinuityFrame(raw)
		if err != nil {
			return
		}
		canonical, err := frame.MarshalBinary()
		if err != nil {
			t.Fatalf("parsed frame cannot be marshaled: %v", err)
		}
		if !bytes.Equal(canonical, raw) {
			t.Fatalf("accepted non-canonical frame: got=%x want=%x", raw, canonical)
		}
	})
}
