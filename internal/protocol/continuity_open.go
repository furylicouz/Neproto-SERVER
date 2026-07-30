package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
)

const (
	ContinuityOpenVersion     = 1
	MaxContinuityOpenMetadata = 1024
)

var (
	ErrInvalidContinuityOpen = errors.New("invalid continuity OPEN metadata")
	continuityOpenMagic      = [4]byte{'N', 'P', 'C', 'O'}
)

type ContinuityOpenMode uint8

const (
	ContinuityOpenNew ContinuityOpenMode = iota + 1
	ContinuityOpenResume
)

type ContinuityOpenMetadata struct {
	Mode            ContinuityOpenMode
	ConstellationID ContinuityID
	FlowID          ContinuityID
	LeaseKey        ContinuityID
	Epoch           uint64
	SendOffset      uint64
	ReceiveOffset   uint64
	Inner           []byte
}

func (m ContinuityOpenMetadata) MarshalBinary() ([]byte, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	raw := make([]byte, 0, 64+len(m.Inner))
	raw = append(raw, continuityOpenMagic[:]...)
	raw = append(raw, ContinuityOpenVersion, byte(m.Mode))
	raw = append(raw, m.ConstellationID[:]...)
	raw = append(raw, m.FlowID[:]...)
	raw = append(raw, m.LeaseKey[:]...)
	raw = binary.AppendUvarint(raw, m.Epoch)
	raw = binary.AppendUvarint(raw, m.SendOffset)
	raw = binary.AppendUvarint(raw, m.ReceiveOffset)
	raw = binary.AppendUvarint(raw, uint64(len(m.Inner)))
	raw = append(raw, m.Inner...)
	if len(raw) > MaxContinuityOpenMetadata {
		return nil, ErrInvalidContinuityOpen
	}
	return raw, nil
}

func ParseContinuityOpenMetadata(raw []byte) (ContinuityOpenMetadata, error) {
	const fixedLength = 4 + 1 + 1 + 16 + 16 + 16
	if len(raw) < fixedLength+4 || len(raw) > MaxContinuityOpenMetadata ||
		!bytes.Equal(raw[:4], continuityOpenMagic[:]) || raw[4] != ContinuityOpenVersion {
		return ContinuityOpenMetadata{}, ErrInvalidContinuityOpen
	}
	metadata := ContinuityOpenMetadata{Mode: ContinuityOpenMode(raw[5])}
	cursor := 6
	copy(metadata.ConstellationID[:], raw[cursor:cursor+16])
	cursor += 16
	copy(metadata.FlowID[:], raw[cursor:cursor+16])
	cursor += 16
	copy(metadata.LeaseKey[:], raw[cursor:cursor+16])
	cursor += 16

	values := []*uint64{&metadata.Epoch, &metadata.SendOffset, &metadata.ReceiveOffset}
	for _, destination := range values {
		value, consumed, err := readCanonicalUvarint(raw[cursor:])
		if err != nil {
			return ContinuityOpenMetadata{}, ErrInvalidContinuityOpen
		}
		*destination = value
		cursor += consumed
	}
	innerLength, consumed, err := readCanonicalUvarint(raw[cursor:])
	if err != nil {
		return ContinuityOpenMetadata{}, ErrInvalidContinuityOpen
	}
	cursor += consumed
	if innerLength != uint64(len(raw)-cursor) {
		return ContinuityOpenMetadata{}, ErrInvalidContinuityOpen
	}
	metadata.Inner = append([]byte(nil), raw[cursor:]...)
	if err := metadata.validate(); err != nil {
		return ContinuityOpenMetadata{}, err
	}
	return metadata, nil
}

func IsContinuityOpenMetadata(raw []byte) bool {
	return len(raw) >= len(continuityOpenMagic) && bytes.Equal(raw[:4], continuityOpenMagic[:])
}

func (m ContinuityOpenMetadata) validate() error {
	if m.ConstellationID == (ContinuityID{}) || m.FlowID == (ContinuityID{}) ||
		m.LeaseKey == (ContinuityID{}) || m.Epoch == 0 || m.Epoch > MaxSequence ||
		m.SendOffset > MaxSequence || m.ReceiveOffset > MaxSequence {
		return ErrInvalidContinuityOpen
	}
	switch m.Mode {
	case ContinuityOpenNew:
		if m.Epoch != 1 || m.SendOffset != 0 || m.ReceiveOffset != 0 || len(m.Inner) == 0 {
			return ErrInvalidContinuityOpen
		}
	case ContinuityOpenResume:
		if m.Epoch < 2 || len(m.Inner) != 0 {
			return ErrInvalidContinuityOpen
		}
	default:
		return ErrInvalidContinuityOpen
	}
	return nil
}
