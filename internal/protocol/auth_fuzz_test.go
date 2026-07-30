package protocol

import "testing"

func FuzzParseAuthenticationMessages(f *testing.F) {
	challenge := Challenge{Version: Version, SupportedFeatures: FeatureMultiplex}
	f.Add(byte(0), challenge.MarshalBinary())
	f.Add(byte(1), (&Response{RequestedFeatures: FeatureMultiplex}).MarshalBinary())
	f.Add(byte(2), (&Confirm{}).MarshalBinary())
	f.Add(byte(3), []byte{})

	f.Fuzz(func(t *testing.T, messageType byte, raw []byte) {
		switch messageType % 3 {
		case 0:
			_, _ = ParseChallenge(raw)
		case 1:
			_, _ = ParseResponse(raw)
		case 2:
			_, _ = ParseConfirm(raw)
		}
	})
}
