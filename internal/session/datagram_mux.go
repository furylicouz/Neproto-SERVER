package session

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/chacha20poly1305"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

const (
	datagramEnvelopeVersion    = byte(1)
	datagramAssociationMaxSize = binary.MaxVarintLen64
	datagramCounterSize        = 8
	datagramReceiveQueue       = 64
	minimumFastDatagramPayload = 512
	datagramReplayWindow       = 1024
)

var (
	ErrDatagramUnavailable       = errors.New("NP/2 unreliable datagrams unavailable")
	ErrDatagramTooLarge          = errors.New("NP/2 unreliable datagram too large")
	ErrDatagramAssociationExists = errors.New("NP/2 datagram association already exists")
	ErrDatagramEndpointClosed    = errors.New("NP/2 datagram endpoint closed")
)

type DatagramStats struct {
	Sent                    uint64
	Received                uint64
	AuthenticationDrops     uint64
	ReplayDrops             uint64
	UnknownAssociationDrops uint64
	QueueDrops              uint64
}

type datagramCounters struct {
	sent                    atomic.Uint64
	received                atomic.Uint64
	authenticationDrops     atomic.Uint64
	replayDrops             atomic.Uint64
	unknownAssociationDrops atomic.Uint64
	queueDrops              atomic.Uint64
}

type DatagramMux struct {
	inner carrier.DatagramCarrier
	ctx   context.Context

	sendAEAD      cipherAEAD
	receiveAEAD   cipherAEAD
	sendPrefix    [4]byte
	receivePrefix [4]byte
	sendDirection byte
	recvDirection byte

	sendMu      sync.Mutex
	receiveMu   sync.Mutex
	sendCounter uint64
	replay      datagramReplay

	mu        sync.RWMutex
	endpoints map[uint64]*DatagramEndpoint
	enabled   atomic.Bool
	maximum   atomic.Int64
	stats     datagramCounters
}

// cipherAEAD keeps the implementation independent of a concrete AEAD while
// exposing only the operations used by the datagram record layer.
type cipherAEAD interface {
	NonceSize() int
	Overhead() int
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

type DatagramEndpoint struct {
	mux      *DatagramMux
	streamID uint64
	receive  chan []byte
	done     chan struct{}
	once     sync.Once
}

func CarrierDatagramPayloadLimit(inner carrier.DatagramCarrier) int {
	if inner == nil {
		return 0
	}
	overhead := 1 + datagramAssociationMaxSize + datagramCounterSize + chacha20poly1305.Overhead
	maximum := inner.MaxDatagramPayload() - overhead
	if maximum < minimumFastDatagramPayload {
		return 0
	}
	return maximum
}

func newDatagramMux(
	ctx context.Context,
	inner carrier.DatagramCarrier,
	role Role,
	keys protocol.SessionKeys,
) (*DatagramMux, error) {
	if ctx == nil || inner == nil || (role != RoleClient && role != RoleServer) ||
		keys.Control == ([32]byte{}) || CarrierDatagramPayloadLimit(inner) == 0 {
		return nil, ErrInvalidConfig
	}
	clientKey, err := deriveDatagramBytes(keys.Control, "NP2 datagram c2s key", 32)
	if err != nil {
		return nil, err
	}
	serverKey, err := deriveDatagramBytes(keys.Control, "NP2 datagram s2c key", 32)
	if err != nil {
		return nil, err
	}
	clientNonce, err := deriveDatagramBytes(keys.Control, "NP2 datagram c2s nonce", 4)
	if err != nil {
		return nil, err
	}
	serverNonce, err := deriveDatagramBytes(keys.Control, "NP2 datagram s2c nonce", 4)
	if err != nil {
		return nil, err
	}
	sendKey, receiveKey := clientKey, serverKey
	sendNonce, receiveNonce := clientNonce, serverNonce
	sendDirection, receiveDirection := recordDirectionClientToServer, recordDirectionServerToClient
	if role == RoleServer {
		sendKey, receiveKey = receiveKey, sendKey
		sendNonce, receiveNonce = receiveNonce, sendNonce
		sendDirection, receiveDirection = receiveDirection, sendDirection
	}
	sendAEAD, err := chacha20poly1305.New(sendKey)
	if err != nil {
		return nil, err
	}
	receiveAEAD, err := chacha20poly1305.New(receiveKey)
	if err != nil {
		return nil, err
	}
	mux := &DatagramMux{
		inner: inner, ctx: ctx, sendAEAD: sendAEAD, receiveAEAD: receiveAEAD,
		sendDirection: sendDirection, recvDirection: receiveDirection,
		endpoints: make(map[uint64]*DatagramEndpoint),
	}
	copy(mux.sendPrefix[:], sendNonce)
	copy(mux.receivePrefix[:], receiveNonce)
	go mux.receiveLoop()
	return mux, nil
}

func (m *DatagramMux) Enable(maximum int) bool {
	if m == nil || maximum < minimumFastDatagramPayload {
		return false
	}
	carrierMaximum := CarrierDatagramPayloadLimit(m.inner)
	if maximum > carrierMaximum {
		maximum = carrierMaximum
	}
	if maximum < minimumFastDatagramPayload {
		return false
	}
	m.maximum.Store(int64(maximum))
	m.enabled.Store(true)
	return true
}

func (m *DatagramMux) Enabled() bool { return m != nil && m.enabled.Load() }

func (m *DatagramMux) MaxPayload() int {
	if m == nil || !m.enabled.Load() {
		return 0
	}
	return int(m.maximum.Load())
}

func (m *DatagramMux) OpenEndpoint(streamID uint64) (*DatagramEndpoint, error) {
	if m == nil || !m.enabled.Load() || streamID == 0 || streamID > protocol.MaxStreamID {
		return nil, ErrDatagramUnavailable
	}
	select {
	case <-m.ctx.Done():
		return nil, ErrDatagramUnavailable
	default:
	}
	endpoint := &DatagramEndpoint{
		mux: m, streamID: streamID, receive: make(chan []byte, datagramReceiveQueue), done: make(chan struct{}),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.endpoints[streamID]; exists {
		return nil, ErrDatagramAssociationExists
	}
	m.endpoints[streamID] = endpoint
	return endpoint, nil
}

func (m *DatagramMux) Stats() DatagramStats {
	if m == nil {
		return DatagramStats{}
	}
	return DatagramStats{
		Sent: m.stats.sent.Load(), Received: m.stats.received.Load(),
		AuthenticationDrops: m.stats.authenticationDrops.Load(), ReplayDrops: m.stats.replayDrops.Load(),
		UnknownAssociationDrops: m.stats.unknownAssociationDrops.Load(), QueueDrops: m.stats.queueDrops.Load(),
	}
}

func (e *DatagramEndpoint) Send(ctx context.Context, plaintext []byte) error {
	if e == nil || e.mux == nil || ctx == nil {
		return ErrDatagramUnavailable
	}
	select {
	case <-e.done:
		return ErrDatagramEndpointClosed
	default:
	}
	maximum := e.MaxPayload()
	if len(plaintext) > maximum {
		return ErrDatagramTooLarge
	}
	return e.mux.send(ctx, e.streamID, plaintext)
}

func (e *DatagramEndpoint) Receive(ctx context.Context) ([]byte, error) {
	if e == nil || e.mux == nil || ctx == nil {
		return nil, ErrDatagramUnavailable
	}
	select {
	case <-e.done:
		return nil, ErrDatagramEndpointClosed
	default:
	}
	select {
	case raw := <-e.receive:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, ErrDatagramEndpointClosed
	case <-e.mux.ctx.Done():
		return nil, ErrDatagramUnavailable
	}
}

func (e *DatagramEndpoint) MaxPayload() int {
	if e == nil || e.mux == nil {
		return 0
	}
	return int(e.mux.maximum.Load())
}

func (e *DatagramEndpoint) Close() error {
	if e == nil || e.mux == nil {
		return nil
	}
	e.once.Do(func() {
		e.mux.mu.Lock()
		if e.mux.endpoints[e.streamID] == e {
			delete(e.mux.endpoints, e.streamID)
		}
		e.mux.mu.Unlock()
		close(e.done)
	})
	return nil
}

func (m *DatagramMux) send(ctx context.Context, streamID uint64, plaintext []byte) error {
	if !m.enabled.Load() || len(plaintext) > int(m.maximum.Load()) {
		return ErrDatagramTooLarge
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.ctx.Done():
		return ErrDatagramUnavailable
	default:
	}
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	if m.sendCounter == math.MaxUint64 {
		return ErrRecordSequenceExhausted
	}
	m.sendCounter++
	counter := m.sendCounter
	header := []byte{datagramEnvelopeVersion}
	header = binary.AppendUvarint(header, streamID)
	header = binary.BigEndian.AppendUint64(header, counter)
	nonce := recordNonce(m.sendPrefix, counter)
	aad := datagramAssociatedData(m.inner.Kind(), m.sendDirection, streamID, counter)
	sealed := m.sendAEAD.Seal(header, nonce[:], plaintext, aad)
	if len(sealed) > m.inner.MaxDatagramPayload() {
		return ErrDatagramTooLarge
	}
	if err := m.inner.SendDatagram(ctx, sealed); err != nil {
		return err
	}
	m.stats.sent.Add(1)
	return nil
}

func (m *DatagramMux) rekey(keys protocol.SessionKeys) error {
	if m == nil || keys.Control == ([32]byte{}) {
		return ErrInvalidConfig
	}
	clientKey, err := deriveDatagramBytes(keys.Control, "NP2 datagram c2s key", 32)
	if err != nil {
		return err
	}
	serverKey, err := deriveDatagramBytes(keys.Control, "NP2 datagram s2c key", 32)
	if err != nil {
		return err
	}
	clientNonce, err := deriveDatagramBytes(keys.Control, "NP2 datagram c2s nonce", 4)
	if err != nil {
		return err
	}
	serverNonce, err := deriveDatagramBytes(keys.Control, "NP2 datagram s2c nonce", 4)
	if err != nil {
		return err
	}
	sendKey, receiveKey := clientKey, serverKey
	sendNonce, receiveNonce := clientNonce, serverNonce
	if m.sendDirection == recordDirectionServerToClient {
		sendKey, receiveKey = receiveKey, sendKey
		sendNonce, receiveNonce = receiveNonce, sendNonce
	}
	sendAEAD, err := chacha20poly1305.New(sendKey)
	if err != nil {
		return err
	}
	receiveAEAD, err := chacha20poly1305.New(receiveKey)
	if err != nil {
		return err
	}
	m.sendMu.Lock()
	m.receiveMu.Lock()
	m.sendAEAD, m.receiveAEAD = sendAEAD, receiveAEAD
	copy(m.sendPrefix[:], sendNonce)
	copy(m.receivePrefix[:], receiveNonce)
	m.sendCounter = 0
	m.replay.Reset()
	m.receiveMu.Unlock()
	m.sendMu.Unlock()
	return nil
}

func (m *DatagramMux) receiveLoop() {
	defer m.closeEndpoints()
	for {
		raw, err := m.inner.ReceiveDatagram(m.ctx)
		if err != nil {
			return
		}
		if !m.enabled.Load() {
			continue
		}
		m.receiveMu.Lock()
		streamID, counter, ciphertext, ok := parseDatagramEnvelope(raw, m.receiveAEAD.Overhead())
		if !ok {
			m.receiveMu.Unlock()
			m.stats.authenticationDrops.Add(1)
			continue
		}
		nonce := recordNonce(m.receivePrefix, counter)
		aad := datagramAssociatedData(m.inner.Kind(), m.recvDirection, streamID, counter)
		plaintext, err := m.receiveAEAD.Open(nil, nonce[:], ciphertext, aad)
		if err != nil || len(plaintext) > int(m.maximum.Load()) {
			m.receiveMu.Unlock()
			m.stats.authenticationDrops.Add(1)
			continue
		}
		if !m.replay.Accept(counter) {
			m.receiveMu.Unlock()
			m.stats.replayDrops.Add(1)
			continue
		}
		m.receiveMu.Unlock()
		m.mu.RLock()
		endpoint := m.endpoints[streamID]
		if endpoint == nil {
			m.mu.RUnlock()
			m.stats.unknownAssociationDrops.Add(1)
			continue
		}
		select {
		case endpoint.receive <- plaintext:
			m.stats.received.Add(1)
		default:
			m.stats.queueDrops.Add(1)
		}
		m.mu.RUnlock()
	}
}

func (m *DatagramMux) closeEndpoints() {
	m.mu.Lock()
	endpoints := make([]*DatagramEndpoint, 0, len(m.endpoints))
	for _, endpoint := range m.endpoints {
		endpoints = append(endpoints, endpoint)
	}
	m.mu.Unlock()
	for _, endpoint := range endpoints {
		_ = endpoint.Close()
	}
}

func parseDatagramEnvelope(raw []byte, aeadOverhead int) (uint64, uint64, []byte, bool) {
	if len(raw) < 1+1+datagramCounterSize+aeadOverhead || raw[0] != datagramEnvelopeVersion {
		return 0, 0, nil, false
	}
	streamID, consumed := binary.Uvarint(raw[1:])
	if consumed <= 0 || streamID == 0 || streamID > protocol.MaxStreamID {
		return 0, 0, nil, false
	}
	canonical := binary.AppendUvarint(nil, streamID)
	if consumed != len(canonical) || !bytes.Equal(canonical, raw[1:1+consumed]) {
		return 0, 0, nil, false
	}
	cursor := 1 + consumed
	if len(raw)-cursor < datagramCounterSize+aeadOverhead {
		return 0, 0, nil, false
	}
	counter := binary.BigEndian.Uint64(raw[cursor : cursor+datagramCounterSize])
	if counter == 0 {
		return 0, 0, nil, false
	}
	return streamID, counter, raw[cursor+datagramCounterSize:], true
}

func datagramAssociatedData(kind protocol.CarrierKind, direction byte, streamID, counter uint64) []byte {
	aad := []byte("Neproto NP/2 datagram")
	aad = append(aad, datagramEnvelopeVersion, byte(kind), direction)
	aad = binary.AppendUvarint(aad, streamID)
	aad = binary.BigEndian.AppendUint64(aad, counter)
	return aad
}

func deriveDatagramBytes(key [32]byte, label string, size int) ([]byte, error) {
	return hkdf.Key(sha256.New, key[:], []byte("Neproto NP/2 datagram"), label, size)
}

type datagramReplay struct {
	mu      sync.Mutex
	highest uint64
	slots   [datagramReplayWindow]uint64
}

func (r *datagramReplay) Accept(counter uint64) bool {
	if counter == 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.highest >= counter && r.highest-counter >= datagramReplayWindow {
		return false
	}
	index := counter % datagramReplayWindow
	if r.slots[index] == counter {
		return false
	}
	r.slots[index] = counter
	if counter > r.highest {
		r.highest = counter
	}
	return true
}

func (r *datagramReplay) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.highest = 0
	clear(r.slots[:])
	r.mu.Unlock()
}
