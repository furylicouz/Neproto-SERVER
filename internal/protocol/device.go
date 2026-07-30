package protocol

import (
	"encoding/hex"
	"fmt"
)

const DeviceIDSize = 16

// DeviceID is a random per-installation identifier. It is intentionally not a
// hardware or network identifier and is authenticated as part of the NP/2
// response transcript when FeatureDeviceIdentity is negotiated.
type DeviceID [DeviceIDSize]byte

func (d DeviceID) IsZero() bool {
	return d == DeviceID{}
}

func (d DeviceID) MarshalText() ([]byte, error) {
	if d.IsZero() {
		return nil, fmt.Errorf("%w: zero device identity", ErrInvalidConfig)
	}
	raw := make([]byte, 36)
	hex.Encode(raw[0:8], d[0:4])
	raw[8] = '-'
	hex.Encode(raw[9:13], d[4:6])
	raw[13] = '-'
	hex.Encode(raw[14:18], d[6:8])
	raw[18] = '-'
	hex.Encode(raw[19:23], d[8:10])
	raw[23] = '-'
	hex.Encode(raw[24:36], d[10:16])
	return raw, nil
}

func (d *DeviceID) UnmarshalText(text []byte) error {
	if d == nil {
		return fmt.Errorf("%w: nil device identity", ErrInvalidConfig)
	}
	if len(text) != 36 || text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' {
		return fmt.Errorf("%w: device identity format", ErrInvalidConfig)
	}
	compact := make([]byte, 32)
	copy(compact[0:8], text[0:8])
	copy(compact[8:12], text[9:13])
	copy(compact[12:16], text[14:18])
	copy(compact[16:20], text[19:23])
	copy(compact[20:32], text[24:36])
	var parsed DeviceID
	if _, err := hex.Decode(parsed[:], compact); err != nil {
		return fmt.Errorf("%w: device identity format", ErrInvalidConfig)
	}
	if parsed.IsZero() {
		return fmt.Errorf("%w: zero device identity", ErrInvalidConfig)
	}
	*d = parsed
	return nil
}
