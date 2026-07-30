package protocol

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
)

const (
	MaxCellSize        = 65_535
	MaxCellPayloadSize = 32_768
	MaxCellPaddingSize = 16_384
	MaxStreamID        = (1 << 62) - 1
	MaxSequence        = (1 << 62) - 1
)

var (
	ErrCellTooLarge       = errors.New("cell too large")
	ErrInvalidCell        = errors.New("invalid cell")
	ErrInvalidTypeMap     = errors.New("invalid type map seed")
	ErrNonCanonicalVarint = errors.New("non-canonical varint")
	ErrUnknownCellType    = errors.New("unknown cell type")
)

type CellKind uint8

const (
	CellInvalid CellKind = iota
	CellOpen
	CellOpenOK
	CellOpenFail
	CellData
	CellFin
	CellReset
	CellWindowUpdate
	CellDummy
	CellProfile
	CellPing
	CellPong
	CellGoAway
	CellKindCount
)

func (k CellKind) valid() bool {
	return k > CellInvalid && k < CellKindCount
}

func (k CellKind) controlOnly() bool {
	switch k {
	case CellDummy, CellProfile, CellPing, CellPong, CellGoAway:
		return true
	default:
		return false
	}
}

type TypeMap struct {
	encode [CellKindCount]byte
	decode [256]CellKind
}

func NewTypeMap(seed [32]byte) (TypeMap, error) {
	if seed == ([32]byte{}) {
		return TypeMap{}, ErrInvalidTypeMap
	}
	codes := make([]byte, int(CellKindCount-1))
	for index := range codes {
		codes[index] = byte(index + 1)
	}
	random := newMapRandom(seed)
	for index := len(codes) - 1; index > 0; index-- {
		other := int(random.uniform(uint64(index + 1)))
		codes[index], codes[other] = codes[other], codes[index]
	}

	var result TypeMap
	for offset, wire := range codes {
		kind := CellKind(offset + 1)
		result.encode[kind] = wire
		result.decode[wire] = kind
	}
	return result, nil
}

func (m TypeMap) EncodeKind(kind CellKind) (byte, error) {
	if !kind.valid() {
		return 0, ErrUnknownCellType
	}
	wire := m.encode[kind]
	if wire == 0 {
		return 0, ErrUnknownCellType
	}
	return wire, nil
}

func (m TypeMap) DecodeKind(wire byte) (CellKind, error) {
	kind := m.decode[wire]
	if !kind.valid() {
		return CellInvalid, ErrUnknownCellType
	}
	return kind, nil
}

type Cell struct {
	Kind     CellKind
	StreamID uint64
	Sequence uint64
	Payload  []byte
	Padding  []byte
}

func EncodeCell(typeMap TypeMap, cell Cell) ([]byte, error) {
	if err := validateCell(cell); err != nil {
		return nil, err
	}
	wireType, err := typeMap.EncodeKind(cell.Kind)
	if err != nil {
		return nil, err
	}

	raw := make([]byte, 0, 1+4*binary.MaxVarintLen64+len(cell.Payload)+len(cell.Padding))
	raw = append(raw, wireType)
	raw = binary.AppendUvarint(raw, cell.StreamID)
	raw = binary.AppendUvarint(raw, cell.Sequence)
	raw = binary.AppendUvarint(raw, uint64(len(cell.Payload)))
	raw = binary.AppendUvarint(raw, uint64(len(cell.Padding)))
	raw = append(raw, cell.Payload...)
	raw = append(raw, cell.Padding...)
	if len(raw) > MaxCellSize {
		return nil, ErrCellTooLarge
	}
	return raw, nil
}

func DecodeCell(typeMap TypeMap, raw []byte) (Cell, error) {
	if len(raw) == 0 {
		return Cell{}, ErrInvalidCell
	}
	if len(raw) > MaxCellSize {
		return Cell{}, ErrCellTooLarge
	}
	kind, err := typeMap.DecodeKind(raw[0])
	if err != nil {
		return Cell{}, err
	}

	cursor := 1
	streamID, consumed, err := readCanonicalUvarint(raw[cursor:])
	if err != nil {
		return Cell{}, err
	}
	cursor += consumed
	sequence, consumed, err := readCanonicalUvarint(raw[cursor:])
	if err != nil {
		return Cell{}, err
	}
	cursor += consumed
	payloadLength, consumed, err := readCanonicalUvarint(raw[cursor:])
	if err != nil {
		return Cell{}, err
	}
	cursor += consumed
	paddingLength, consumed, err := readCanonicalUvarint(raw[cursor:])
	if err != nil {
		return Cell{}, err
	}
	cursor += consumed

	if payloadLength > MaxCellPayloadSize || paddingLength > MaxCellPaddingSize {
		return Cell{}, ErrCellTooLarge
	}
	remaining := uint64(len(raw) - cursor)
	if payloadLength+paddingLength != remaining {
		return Cell{}, ErrInvalidCell
	}
	payloadEnd := cursor + int(payloadLength)
	cell := Cell{
		Kind:     kind,
		StreamID: streamID,
		Sequence: sequence,
		Payload:  append([]byte(nil), raw[cursor:payloadEnd]...),
		Padding:  append([]byte(nil), raw[payloadEnd:]...),
	}
	if err := validateCell(cell); err != nil {
		return Cell{}, err
	}
	return cell, nil
}

func validateCell(cell Cell) error {
	if !cell.Kind.valid() {
		return ErrUnknownCellType
	}
	if len(cell.Payload) > MaxCellPayloadSize || len(cell.Padding) > MaxCellPaddingSize {
		return ErrCellTooLarge
	}
	if cell.StreamID > MaxStreamID || cell.Sequence > MaxSequence {
		return ErrInvalidCell
	}
	if cell.Kind.controlOnly() && cell.StreamID != 0 {
		return ErrInvalidCell
	}
	if !cell.Kind.controlOnly() && cell.StreamID == 0 {
		return ErrInvalidCell
	}
	return nil
}

func readCanonicalUvarint(raw []byte) (uint64, int, error) {
	value, consumed := binary.Uvarint(raw)
	if consumed == 0 {
		return 0, 0, ErrInvalidCell
	}
	if consumed < 0 {
		return 0, 0, ErrNonCanonicalVarint
	}
	var canonical [binary.MaxVarintLen64]byte
	canonicalLength := binary.PutUvarint(canonical[:], value)
	if consumed != canonicalLength || !bytes.Equal(raw[:consumed], canonical[:canonicalLength]) {
		return 0, 0, ErrNonCanonicalVarint
	}
	return value, consumed, nil
}

type mapRandom struct {
	key     [32]byte
	counter uint64
	block   [sha256.Size]byte
	offset  int
}

func newMapRandom(seed [32]byte) *mapRandom {
	return &mapRandom{key: seed, offset: sha256.Size}
}

func (r *mapRandom) uniform(limit uint64) uint64 {
	if limit == 0 {
		panic("protocol: zero uniform limit")
	}
	threshold := math.MaxUint64 - (math.MaxUint64 % limit)
	for {
		candidate := r.uint64()
		if candidate < threshold {
			return candidate % limit
		}
	}
}

func (r *mapRandom) uint64() uint64 {
	if r.offset+8 > len(r.block) {
		mac := hmac.New(sha256.New, r.key[:])
		_, _ = mac.Write([]byte("NP2 type map"))
		var counter [8]byte
		binary.BigEndian.PutUint64(counter[:], r.counter)
		_, _ = mac.Write(counter[:])
		copy(r.block[:], mac.Sum(nil))
		r.counter++
		r.offset = 0
	}
	value := binary.BigEndian.Uint64(r.block[r.offset : r.offset+8])
	r.offset += 8
	return value
}
