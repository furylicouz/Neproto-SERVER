package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"neproto.local/chameleon/internal/protocol"
)

const maxUDPAssociationRecordBytes = MaxUDPDatagramPayload + 1024
const udpAssociationReceiveQueue = 64

type udpAssociationStream interface {
	io.ReadWriteCloser
	CloseWrite() error
}

type UDPDatagramEndpoint interface {
	Send(context.Context, []byte) error
	Receive(context.Context) ([]byte, error)
	MaxPayload() int
	Close() error
}

type UDPAssociation struct {
	stream     udpAssociationStream
	reader     *bufio.Reader
	command    OpenCommand
	fixed      Target
	maxPayload uint64
	fast       UDPDatagramEndpoint
	ctx        context.Context
	cancel     context.CancelFunc
	records    chan udpAssociationResult

	readMu     sync.Mutex
	deliveryMu sync.Mutex
	writeMu    sync.Mutex
	closeOnce  sync.Once
	replay     udpAssociationReplay
	sendID     uint64
}

type udpAssociationResult struct {
	record UDPRecord
	err    error
}

type UDPRemoteError struct {
	Code    UDPErrorCode
	Message string
}

func (e *UDPRemoteError) Error() string {
	return fmt.Sprintf("UDP peer error code=%d: %s", e.Code, e.Message)
}

func NewUDPAssociation(
	stream udpAssociationStream,
	command OpenCommand,
	fixed Target,
	maxPayload uint64,
) (*UDPAssociation, error) {
	return NewUDPAssociationWithDatagrams(stream, command, fixed, maxPayload, nil)
}

func NewUDPAssociationWithDatagrams(
	stream udpAssociationStream,
	command OpenCommand,
	fixed Target,
	maxPayload uint64,
	fast UDPDatagramEndpoint,
) (*UDPAssociation, error) {
	if stream == nil || maxPayload < 1200 || maxPayload > MaxUDPDatagramPayload {
		return nil, ErrInvalidUDPRecord
	}
	switch command {
	case CommandUDPFixed:
		if _, err := EncodeTarget(fixed); err != nil {
			return nil, ErrInvalidUDPRecord
		}
	case CommandUDPAssociate:
		if fixed != (Target{}) {
			return nil, ErrInvalidUDPRecord
		}
	default:
		return nil, ErrInvalidUDPRecord
	}
	if fast != nil && fast.MaxPayload() <= 0 {
		return nil, ErrInvalidUDPRecord
	}
	ctx, cancel := context.WithCancel(context.Background())
	association := &UDPAssociation{
		stream: stream, reader: bufio.NewReader(stream), command: command,
		fixed: fixed, maxPayload: maxPayload, fast: fast, ctx: ctx, cancel: cancel,
	}
	if fast != nil {
		association.records = make(chan udpAssociationResult, udpAssociationReceiveQueue)
		go association.readReliableLoop()
		go association.readFastLoop()
	}
	return association, nil
}

func (a *UDPAssociation) WriteDatagram(payload []byte, target *Target) error {
	if a == nil || uint64(len(payload)) > a.maxPayload {
		return ErrInvalidUDPRecord
	}
	record := UDPRecord{Type: UDPRecordDatagram, Payload: payload}
	switch a.command {
	case CommandUDPFixed:
		if target != nil && *target != a.fixed {
			return ErrInvalidUDPRecord
		}
	case CommandUDPAssociate:
		if target == nil {
			return ErrInvalidUDPRecord
		}
		record.HasTarget = true
		record.Target = *target
	default:
		return ErrInvalidUDPRecord
	}
	return a.writeRecord(record, true)
}

func (a *UDPAssociation) ReadDatagram() ([]byte, Target, error) {
	if a == nil {
		return nil, Target{}, ErrInvalidUDPRecord
	}
	var record UDPRecord
	var err error
	if a.fast == nil {
		a.readMu.Lock()
		defer a.readMu.Unlock()
		record, err = a.readRecordLocked()
	} else {
		a.deliveryMu.Lock()
		defer a.deliveryMu.Unlock()
		select {
		case result := <-a.records:
			record, err = result.record, result.err
		case <-a.ctx.Done():
			err = io.EOF
		}
	}
	if err != nil {
		return nil, Target{}, err
	}
	switch record.Type {
	case UDPRecordDatagram:
		if uint64(len(record.Payload)) > a.maxPayload {
			return nil, Target{}, ErrInvalidUDPRecord
		}
		if a.command == CommandUDPFixed {
			if record.HasTarget {
				return nil, Target{}, ErrInvalidUDPRecord
			}
			return record.Payload, a.fixed, nil
		}
		if !record.HasTarget {
			return nil, Target{}, ErrInvalidUDPRecord
		}
		return record.Payload, record.Target, nil
	case UDPRecordError:
		return nil, Target{}, &UDPRemoteError{Code: record.ErrorCode, Message: record.ErrorMessage}
	case UDPRecordClose:
		return nil, Target{}, io.EOF
	default:
		return nil, Target{}, ErrInvalidUDPRecord
	}
}

func (a *UDPAssociation) WriteError(code UDPErrorCode, message string) error {
	return a.writeRecord(UDPRecord{Type: UDPRecordError, ErrorCode: code, ErrorMessage: message}, false)
}

func (a *UDPAssociation) Close() error {
	if a == nil {
		return nil
	}
	var result error
	a.closeOnce.Do(func() {
		if err := a.writeRecord(UDPRecord{Type: UDPRecordClose}, false); err != nil {
			result = err
		}
		if err := a.stream.CloseWrite(); result == nil {
			result = err
		}
	})
	return result
}

func (a *UDPAssociation) Abort() error {
	if a == nil {
		return nil
	}
	a.cancel()
	if a.fast != nil {
		_ = a.fast.Close()
	}
	return a.stream.Close()
}

func (a *UDPAssociation) writeRecord(record UDPRecord, preferFast bool) error {
	if a == nil || a.stream == nil {
		return ErrInvalidUDPRecord
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if a.sendID == protocol.MaxSequence {
		return ErrInvalidUDPRecord
	}
	a.sendID++
	record.PacketID = a.sendID
	raw, err := EncodeUDPRecord(record)
	if err != nil {
		return err
	}
	if preferFast && a.fast != nil && len(raw) <= a.fast.MaxPayload() {
		if err := a.fast.Send(a.ctx, raw); err == nil {
			return nil
		}
	}
	framed := binary.AppendUvarint(nil, uint64(len(raw)))
	framed = append(framed, raw...)
	_, err = io.Copy(a.stream, bytes.NewReader(framed))
	return err
}

func (a *UDPAssociation) readRecordLocked() (UDPRecord, error) {
	length, err := readCanonicalAssociationUvarint(a.reader)
	if err != nil || length == 0 || length > maxUDPAssociationRecordBytes {
		if errors.Is(err, io.EOF) {
			return UDPRecord{}, io.EOF
		}
		return UDPRecord{}, ErrInvalidUDPRecord
	}
	raw := make([]byte, int(length))
	if _, err := io.ReadFull(a.reader, raw); err != nil {
		return UDPRecord{}, errors.Join(ErrInvalidUDPRecord, err)
	}
	record, err := DecodeUDPRecord(raw)
	if err != nil || !a.replay.Accept(record.PacketID) {
		return UDPRecord{}, ErrInvalidUDPRecord
	}
	return record, nil
}

func (a *UDPAssociation) readReliableLoop() {
	for {
		a.readMu.Lock()
		record, err := a.readRecordLocked()
		a.readMu.Unlock()
		select {
		case a.records <- udpAssociationResult{record: record, err: err}:
		case <-a.ctx.Done():
			return
		}
		if err != nil || record.Type == UDPRecordClose {
			return
		}
	}
}

func (a *UDPAssociation) readFastLoop() {
	for {
		raw, err := a.fast.Receive(a.ctx)
		if err != nil {
			return
		}
		record, err := DecodeUDPRecord(raw)
		if err != nil || record.Type != UDPRecordDatagram || uint64(len(record.Payload)) > a.maxPayload ||
			!a.replay.Accept(record.PacketID) {
			continue
		}
		select {
		case a.records <- udpAssociationResult{record: record}:
		default:
			// Unreliable datagrams are intentionally dropped under association
			// backpressure instead of growing memory or blocking the session.
		}
	}
}

type udpAssociationReplay struct {
	mu      sync.Mutex
	highest uint64
	slots   [1024]uint64
}

func (r *udpAssociationReplay) Accept(packetID uint64) bool {
	if packetID == 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.highest >= packetID && r.highest-packetID >= uint64(len(r.slots)) {
		return false
	}
	index := packetID % uint64(len(r.slots))
	if r.slots[index] == packetID {
		return false
	}
	r.slots[index] = packetID
	if packetID > r.highest {
		r.highest = packetID
	}
	return true
}

func readCanonicalAssociationUvarint(reader *bufio.Reader) (uint64, error) {
	var raw [binary.MaxVarintLen64]byte
	for index := range raw {
		value, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		raw[index] = value
		if value&0x80 != 0 {
			continue
		}
		parsed, consumed := binary.Uvarint(raw[:index+1])
		if consumed != index+1 {
			return 0, ErrInvalidUDPRecord
		}
		var canonical [binary.MaxVarintLen64]byte
		canonicalLength := binary.PutUvarint(canonical[:], parsed)
		if canonicalLength != consumed || !bytes.Equal(canonical[:canonicalLength], raw[:consumed]) {
			return 0, ErrInvalidUDPRecord
		}
		return parsed, nil
	}
	return 0, ErrInvalidUDPRecord
}
