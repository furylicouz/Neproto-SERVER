package proxy

import (
	"encoding/binary"
	"errors"
	"unicode/utf8"

	"neproto.local/chameleon/internal/protocol"
)

const (
	MaxUDPDatagramPayload   = 65507
	MaxUDPErrorMessageBytes = 128
	maxUDPRecordTargetBytes = 512
	udpRecordFlagTarget     = 0x01
)

var ErrInvalidUDPRecord = errors.New("invalid UDP record")

type UDPRecordType byte

const (
	UDPRecordDatagram UDPRecordType = iota + 1
	UDPRecordError
	UDPRecordClose
)

type UDPErrorCode byte

const (
	UDPErrorGeneral UDPErrorCode = iota + 1
	UDPErrorPolicyDenied
	UDPErrorResolution
	UDPErrorOversized
	UDPErrorRateLimited
)

type UDPRecord struct {
	Type         UDPRecordType
	PacketID     uint64
	HasTarget    bool
	Target       Target
	Payload      []byte
	ErrorCode    UDPErrorCode
	ErrorMessage string
}

func EncodeUDPRecord(record UDPRecord) ([]byte, error) {
	if err := validateUDPRecord(record); err != nil {
		return nil, err
	}
	raw := []byte{byte(record.Type)}
	raw = binary.AppendUvarint(raw, record.PacketID)
	flags := byte(0)
	if record.HasTarget {
		flags |= udpRecordFlagTarget
	}
	raw = append(raw, flags)
	if record.HasTarget {
		target, err := EncodeTarget(record.Target)
		if err != nil || len(target) > maxUDPRecordTargetBytes {
			return nil, ErrInvalidUDPRecord
		}
		raw = binary.AppendUvarint(raw, uint64(len(target)))
		raw = append(raw, target...)
	}
	body := record.Payload
	if record.Type == UDPRecordError {
		body = make([]byte, 1, 1+len(record.ErrorMessage))
		body[0] = byte(record.ErrorCode)
		body = append(body, record.ErrorMessage...)
	}
	raw = binary.AppendUvarint(raw, uint64(len(body)))
	raw = append(raw, body...)
	return raw, nil
}

func DecodeUDPRecord(raw []byte) (UDPRecord, error) {
	if len(raw) < 4 {
		return UDPRecord{}, ErrInvalidUDPRecord
	}
	record := UDPRecord{Type: UDPRecordType(raw[0])}
	cursor := 1
	packetID, consumed, err := readCanonicalUvarint(raw[cursor:])
	if err != nil || packetID == 0 || packetID > protocol.MaxSequence {
		return UDPRecord{}, ErrInvalidUDPRecord
	}
	record.PacketID = packetID
	cursor += consumed
	if cursor >= len(raw) {
		return UDPRecord{}, ErrInvalidUDPRecord
	}
	flags := raw[cursor]
	cursor++
	if flags&^udpRecordFlagTarget != 0 {
		return UDPRecord{}, ErrInvalidUDPRecord
	}
	if flags&udpRecordFlagTarget != 0 {
		length, lengthBytes, parseErr := readCanonicalUvarint(raw[cursor:])
		if parseErr != nil || length == 0 || length > maxUDPRecordTargetBytes {
			return UDPRecord{}, ErrInvalidUDPRecord
		}
		cursor += lengthBytes
		if length > uint64(len(raw)-cursor) {
			return UDPRecord{}, ErrInvalidUDPRecord
		}
		end := cursor + int(length)
		target, parseErr := DecodeTarget(raw[cursor:end])
		if parseErr != nil {
			return UDPRecord{}, ErrInvalidUDPRecord
		}
		record.HasTarget = true
		record.Target = target
		cursor = end
	}
	length, lengthBytes, err := readCanonicalUvarint(raw[cursor:])
	if err != nil || length > MaxUDPDatagramPayload || length > uint64(len(raw)-cursor-lengthBytes) {
		return UDPRecord{}, ErrInvalidUDPRecord
	}
	cursor += lengthBytes
	if length != uint64(len(raw)-cursor) {
		return UDPRecord{}, ErrInvalidUDPRecord
	}
	body := raw[cursor:]
	switch record.Type {
	case UDPRecordDatagram:
		record.Payload = append([]byte(nil), body...)
	case UDPRecordError:
		if len(body) == 0 {
			return UDPRecord{}, ErrInvalidUDPRecord
		}
		record.ErrorCode = UDPErrorCode(body[0])
		record.ErrorMessage = string(body[1:])
	case UDPRecordClose:
		if len(body) != 0 {
			return UDPRecord{}, ErrInvalidUDPRecord
		}
	default:
		return UDPRecord{}, ErrInvalidUDPRecord
	}
	if err := validateUDPRecord(record); err != nil {
		return UDPRecord{}, err
	}
	return record, nil
}

func validateUDPRecord(record UDPRecord) error {
	if record.PacketID == 0 || record.PacketID > protocol.MaxSequence {
		return ErrInvalidUDPRecord
	}
	if record.HasTarget {
		if record.Target == (Target{}) {
			return ErrInvalidUDPRecord
		}
		if _, err := EncodeTarget(record.Target); err != nil {
			return ErrInvalidUDPRecord
		}
	} else if record.Target != (Target{}) {
		return ErrInvalidUDPRecord
	}
	switch record.Type {
	case UDPRecordDatagram:
		if len(record.Payload) > MaxUDPDatagramPayload || record.ErrorCode != 0 || record.ErrorMessage != "" {
			return ErrInvalidUDPRecord
		}
	case UDPRecordError:
		if record.HasTarget || len(record.Payload) != 0 || !record.ErrorCode.valid() ||
			!validUDPErrorMessage(record.ErrorMessage) {
			return ErrInvalidUDPRecord
		}
	case UDPRecordClose:
		if record.HasTarget || len(record.Payload) != 0 || record.ErrorCode != 0 || record.ErrorMessage != "" {
			return ErrInvalidUDPRecord
		}
	default:
		return ErrInvalidUDPRecord
	}
	return nil
}

func (c UDPErrorCode) valid() bool {
	return c >= UDPErrorGeneral && c <= UDPErrorRateLimited
}

func validUDPErrorMessage(message string) bool {
	if len(message) > MaxUDPErrorMessageBytes || !utf8.ValidString(message) {
		return false
	}
	for _, character := range message {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
