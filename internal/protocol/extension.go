package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
)

const (
	ExtensionEnvelopeVersion = 1
	MaxExtensionTLVs         = 32
	MaxExtensionTLVValueSize = 4096
	ExtensionMandatoryFlag   = uint64(1) << 62
	MaxExtensionTLVType      = (uint64(1) << 63) - 1
)

var ErrInvalidExtension = errors.New("invalid extension envelope")
var ErrUnsupportedExtension = errors.New("unsupported extension parameters")

type ExtensionMessageType uint8

const (
	ExtensionOffer ExtensionMessageType = iota + 1
	ExtensionAccept
	ExtensionUpdate
)

func (t ExtensionMessageType) valid() bool {
	return t >= ExtensionOffer && t <= ExtensionUpdate
}

type ExtensionCapability uint64

const (
	CapabilityReliableUDP ExtensionCapability = 1 << iota
	CapabilityUnreliableDatagrams
	CapabilityAdaptiveWindow
	CapabilityCarrierMigration
	CapabilityMosaicCover
)

const (
	ExtensionTLVCapabilities           uint64 = 1
	ExtensionTLVMaxUDPAssociations     uint64 = 2
	ExtensionTLVMaxUDPPayload          uint64 = 3
	ExtensionTLVUDPIdleTimeoutMS       uint64 = 4
	ExtensionTLVMaxSessionReceiveBytes uint64 = 5
	ExtensionTLVMaxStreamWindowBytes   uint64 = 6
	ExtensionTLVUnreliableDatagramSize uint64 = 7
)

var extensionMagic = [4]byte{'N', 'P', 'E', 'X'}

func IsExtensionEnvelope(raw []byte) bool {
	return len(raw) >= len(extensionMagic) && bytes.Equal(raw[:len(extensionMagic)], extensionMagic[:])
}

type ExtensionTLV struct {
	Type  uint64
	Value []byte
}

func (t ExtensionTLV) Mandatory() bool {
	return t.Type&ExtensionMandatoryFlag != 0
}

func (t ExtensionTLV) BaseType() uint64 {
	return t.Type &^ ExtensionMandatoryFlag
}

func NewExtensionCapabilitiesTLV(capabilities ExtensionCapability) ExtensionTLV {
	value := make([]byte, 8)
	binary.BigEndian.PutUint64(value, uint64(capabilities))
	return ExtensionTLV{Type: ExtensionTLVCapabilities, Value: value}
}

func (t ExtensionTLV) Capabilities() (ExtensionCapability, error) {
	if t.BaseType() != ExtensionTLVCapabilities || len(t.Value) != 8 {
		return 0, ErrInvalidExtension
	}
	return ExtensionCapability(binary.BigEndian.Uint64(t.Value)), nil
}

func NewExtensionUvarintTLV(tlvType, value uint64) (ExtensionTLV, error) {
	if !validExtensionTLVType(tlvType) {
		return ExtensionTLV{}, ErrInvalidExtension
	}
	return ExtensionTLV{Type: tlvType, Value: binary.AppendUvarint(nil, value)}, nil
}

func (t ExtensionTLV) Uvarint() (uint64, error) {
	value, consumed, err := readCanonicalUvarint(t.Value)
	if err != nil || consumed != len(t.Value) {
		return 0, ErrInvalidExtension
	}
	return value, nil
}

type ExtensionEnvelope struct {
	Type      ExtensionMessageType
	MessageID uint64
	TLVs      []ExtensionTLV
}

func (e ExtensionEnvelope) MarshalBinary() ([]byte, error) {
	if !e.Type.valid() || e.MessageID == 0 || e.MessageID > MaxSequence ||
		len(e.TLVs) == 0 || len(e.TLVs) > MaxExtensionTLVs {
		return nil, ErrInvalidExtension
	}
	raw := make([]byte, 0, 64)
	raw = append(raw, extensionMagic[:]...)
	raw = append(raw, ExtensionEnvelopeVersion, byte(e.Type))
	raw = binary.AppendUvarint(raw, e.MessageID)
	raw = binary.AppendUvarint(raw, uint64(len(e.TLVs)))
	previousType := uint64(0)
	for _, tlv := range e.TLVs {
		if !validExtensionTLVType(tlv.Type) || tlv.Type <= previousType ||
			len(tlv.Value) > MaxExtensionTLVValueSize {
			return nil, ErrInvalidExtension
		}
		previousType = tlv.Type
		raw = binary.AppendUvarint(raw, tlv.Type)
		raw = binary.AppendUvarint(raw, uint64(len(tlv.Value)))
		raw = append(raw, tlv.Value...)
		if len(raw) > MaxCellPayloadSize {
			return nil, ErrInvalidExtension
		}
	}
	return raw, nil
}

func ParseExtensionEnvelope(raw []byte) (ExtensionEnvelope, error) {
	if len(raw) < 8 || len(raw) > MaxCellPayloadSize ||
		!bytes.Equal(raw[:4], extensionMagic[:]) || raw[4] != ExtensionEnvelopeVersion {
		return ExtensionEnvelope{}, ErrInvalidExtension
	}
	messageType := ExtensionMessageType(raw[5])
	if !messageType.valid() {
		return ExtensionEnvelope{}, ErrInvalidExtension
	}
	cursor := 6
	messageID, consumed, err := readCanonicalUvarint(raw[cursor:])
	if err != nil || messageID == 0 || messageID > MaxSequence {
		return ExtensionEnvelope{}, ErrInvalidExtension
	}
	cursor += consumed
	count, consumed, err := readCanonicalUvarint(raw[cursor:])
	if err != nil || count == 0 || count > MaxExtensionTLVs {
		return ExtensionEnvelope{}, ErrInvalidExtension
	}
	cursor += consumed

	tlvs := make([]ExtensionTLV, 0, int(count))
	previousType := uint64(0)
	for range count {
		if cursor >= len(raw) {
			return ExtensionEnvelope{}, ErrInvalidExtension
		}
		tlvType, typeBytes, parseErr := readCanonicalUvarint(raw[cursor:])
		if parseErr != nil || !validExtensionTLVType(tlvType) || tlvType <= previousType {
			return ExtensionEnvelope{}, ErrInvalidExtension
		}
		cursor += typeBytes
		length, lengthBytes, parseErr := readCanonicalUvarint(raw[cursor:])
		if parseErr != nil || length > MaxExtensionTLVValueSize {
			return ExtensionEnvelope{}, ErrInvalidExtension
		}
		cursor += lengthBytes
		if length > uint64(len(raw)-cursor) {
			return ExtensionEnvelope{}, ErrInvalidExtension
		}
		end := cursor + int(length)
		tlvs = append(tlvs, ExtensionTLV{
			Type: tlvType, Value: append([]byte(nil), raw[cursor:end]...),
		})
		cursor = end
		previousType = tlvType
	}
	if cursor != len(raw) {
		return ExtensionEnvelope{}, ErrInvalidExtension
	}
	return ExtensionEnvelope{Type: messageType, MessageID: messageID, TLVs: tlvs}, nil
}

func validExtensionTLVType(tlvType uint64) bool {
	return tlvType != 0 && tlvType <= MaxExtensionTLVType &&
		tlvType&^ExtensionMandatoryFlag != 0
}
