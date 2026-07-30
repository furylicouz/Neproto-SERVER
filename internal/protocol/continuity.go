package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
)

const (
	ContinuityEnvelopeVersion = 1
	MinContinuityTokenSize    = 32
	MaxContinuityTokenSize    = 512

	CapabilityConstellationContinuity ExtensionCapability = 1 << 5
	CapabilityForwardSecrecy          ExtensionCapability = 1 << 6
	ExtensionTLVContinuity            uint64              = 8
	ExtensionTLVForwardSecretKeyShare uint64              = 9
	ExtensionTLVForwardSecretConfirm  uint64              = 10
	ExtensionTLVForwardSecretAck      uint64              = 11
)

var (
	ErrInvalidContinuity = errors.New("invalid continuity envelope")
	continuityMagic      = [4]byte{'N', 'P', 'C', 'T'}
)

type ContinuityMessageType uint8

const (
	ContinuityLeaseIssue ContinuityMessageType = iota + 1
	ContinuityLeaseAttach
	ContinuityLeaseAccept
	ContinuityFlowResume
	ContinuityFlowAck
	ContinuityFlowAbort
	ContinuityConstellationCreate
)

func (t ContinuityMessageType) valid() bool {
	return t >= ContinuityLeaseIssue && t <= ContinuityConstellationCreate
}

func (t ContinuityMessageType) String() string {
	switch t {
	case ContinuityLeaseIssue:
		return "lease_issue"
	case ContinuityLeaseAttach:
		return "lease_attach"
	case ContinuityLeaseAccept:
		return "lease_accept"
	case ContinuityFlowResume:
		return "flow_resume"
	case ContinuityFlowAck:
		return "flow_ack"
	case ContinuityFlowAbort:
		return "flow_abort"
	case ContinuityConstellationCreate:
		return "constellation_create"
	default:
		return "invalid"
	}
}

type ContinuityID [16]byte

func (id ContinuityID) zero() bool {
	return id == ContinuityID{}
}

type ContinuityFrame struct {
	Type            ContinuityMessageType
	MessageID       uint64
	ConstellationID ContinuityID
	FlowID          ContinuityID
	SendOffset      uint64
	ReceiveOffset   uint64
	Token           []byte
}

func (f ContinuityFrame) MarshalBinary() ([]byte, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	raw := make([]byte, 0, 64+len(f.Token))
	raw = append(raw, continuityMagic[:]...)
	raw = append(raw, ContinuityEnvelopeVersion, byte(f.Type))
	raw = binary.AppendUvarint(raw, f.MessageID)
	raw = append(raw, f.ConstellationID[:]...)
	raw = append(raw, f.FlowID[:]...)
	raw = binary.AppendUvarint(raw, f.SendOffset)
	raw = binary.AppendUvarint(raw, f.ReceiveOffset)
	raw = binary.AppendUvarint(raw, uint64(len(f.Token)))
	raw = append(raw, f.Token...)
	return raw, nil
}

func ParseContinuityFrame(raw []byte) (ContinuityFrame, error) {
	const fixedFields = 4 + 1 + 1 + 16 + 16
	if len(raw) < fixedFields+4 ||
		!bytes.Equal(raw[:4], continuityMagic[:]) || raw[4] != ContinuityEnvelopeVersion {
		return ContinuityFrame{}, ErrInvalidContinuity
	}
	messageType := ContinuityMessageType(raw[5])
	cursor := 6
	messageID, consumed, err := readContinuityUvarint(raw[cursor:])
	if err != nil {
		return ContinuityFrame{}, err
	}
	cursor += consumed
	if len(raw)-cursor < 32 {
		return ContinuityFrame{}, ErrInvalidContinuity
	}
	var constellationID, flowID ContinuityID
	copy(constellationID[:], raw[cursor:cursor+16])
	cursor += 16
	copy(flowID[:], raw[cursor:cursor+16])
	cursor += 16
	sendOffset, consumed, err := readContinuityUvarint(raw[cursor:])
	if err != nil {
		return ContinuityFrame{}, err
	}
	cursor += consumed
	receiveOffset, consumed, err := readContinuityUvarint(raw[cursor:])
	if err != nil {
		return ContinuityFrame{}, err
	}
	cursor += consumed
	tokenLength, consumed, err := readContinuityUvarint(raw[cursor:])
	if err != nil || tokenLength > MaxContinuityTokenSize {
		return ContinuityFrame{}, ErrInvalidContinuity
	}
	cursor += consumed
	if tokenLength != uint64(len(raw)-cursor) {
		return ContinuityFrame{}, ErrInvalidContinuity
	}
	frame := ContinuityFrame{
		Type: messageType, MessageID: messageID,
		ConstellationID: constellationID, FlowID: flowID,
		SendOffset: sendOffset, ReceiveOffset: receiveOffset,
		Token: append([]byte(nil), raw[cursor:]...),
	}
	if err := frame.validate(); err != nil {
		return ContinuityFrame{}, err
	}
	return frame, nil
}

func NewContinuityTLV(frame ContinuityFrame) (ExtensionTLV, error) {
	raw, err := frame.MarshalBinary()
	if err != nil {
		return ExtensionTLV{}, err
	}
	return ExtensionTLV{Type: ExtensionTLVContinuity, Value: raw}, nil
}

func ParseContinuityTLV(tlv ExtensionTLV) (ContinuityFrame, error) {
	if tlv.Type != ExtensionTLVContinuity {
		return ContinuityFrame{}, ErrInvalidContinuity
	}
	return ParseContinuityFrame(tlv.Value)
}

func (f ContinuityFrame) validate() error {
	if !f.Type.valid() || f.MessageID == 0 || f.MessageID > MaxSequence ||
		f.ConstellationID.zero() || f.SendOffset > MaxSequence || f.ReceiveOffset > MaxSequence {
		return ErrInvalidContinuity
	}
	switch f.Type {
	case ContinuityLeaseIssue, ContinuityLeaseAttach, ContinuityLeaseAccept:
		if !f.FlowID.zero() || f.SendOffset != 0 || f.ReceiveOffset != 0 ||
			len(f.Token) < MinContinuityTokenSize || len(f.Token) > MaxContinuityTokenSize {
			return ErrInvalidContinuity
		}
	case ContinuityFlowResume, ContinuityFlowAck:
		if f.FlowID.zero() || len(f.Token) != 0 {
			return ErrInvalidContinuity
		}
	case ContinuityFlowAbort:
		if f.FlowID.zero() || f.SendOffset != 0 || f.ReceiveOffset != 0 || len(f.Token) != 0 {
			return ErrInvalidContinuity
		}
	case ContinuityConstellationCreate:
		if !f.FlowID.zero() || f.SendOffset != 0 || f.ReceiveOffset != 0 || len(f.Token) != 0 {
			return ErrInvalidContinuity
		}
	default:
		return ErrInvalidContinuity
	}
	return nil
}

func readContinuityUvarint(raw []byte) (uint64, int, error) {
	value, consumed, err := readCanonicalUvarint(raw)
	if err != nil {
		return 0, 0, ErrInvalidContinuity
	}
	return value, consumed, nil
}
