package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

const (
	MaxInitialWindow    = 16 * 1024 * 1024
	MaxOpenMetadata     = 1024
	minRetiredStreams   = 1024
	retiredStreamFactor = 8
)

var (
	ErrClosed        = errors.New("session closed")
	ErrProtocol      = errors.New("session protocol violation")
	ErrRejected      = errors.New("stream rejected")
	ErrReset         = errors.New("stream reset")
	ErrStreamLimit   = errors.New("stream limit reached")
	ErrPingLimit     = errors.New("session ping limit reached")
	ErrInvalidConfig = errors.New("invalid session configuration")
)

type RejectError struct {
	Code byte
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("%v (code %d)", ErrRejected, e.Code)
}

func (e *RejectError) Unwrap() error {
	return ErrRejected
}

type Role uint8

const (
	RoleClient Role = iota + 1
	RoleServer
)

type Config struct {
	Role          Role
	Carrier       carrier.Carrier
	TypeMap       protocol.TypeMap
	InitialWindow uint64
	MaxStreams    int
}

type Mux struct {
	role          Role
	carrier       carrier.Carrier
	typeMap       protocol.TypeMap
	initialWindow uint64
	maxStreams    int

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu           sync.Mutex
	streams      map[uint64]*Stream
	retired      map[uint64]struct{}
	retiredOrder []uint64
	retiredLimit int
	nextID       uint64
	incoming     chan *Incoming
	extensions   chan protocol.ExtensionEnvelope
	err          error

	extensionMu        sync.Mutex
	extensionSeen      map[uint64][32]byte
	extensionOrder     []uint64
	highestExtensionID uint64

	pingMu       sync.Mutex
	pendingPings map[[16]byte]chan struct{}

	sendMu    sync.Mutex
	closeOnce sync.Once
	wait      sync.WaitGroup
	stats     muxStats
}

func (m *Mux) CarrierKind() protocol.CarrierKind {
	if m == nil || m.carrier == nil {
		return 0
	}
	return m.carrier.Kind()
}

func (m *Mux) MaxStreams() uint64 {
	if m == nil || m.maxStreams <= 0 {
		return 0
	}
	return uint64(m.maxStreams)
}

func New(config Config) (*Mux, error) {
	if config.Carrier == nil || (config.Role != RoleClient && config.Role != RoleServer) ||
		config.InitialWindow == 0 || config.InitialWindow > MaxInitialWindow || config.MaxStreams <= 0 {
		return nil, ErrInvalidConfig
	}
	if _, err := config.TypeMap.EncodeKind(protocol.CellOpen); err != nil {
		return nil, fmt.Errorf("%w: type map: %v", ErrInvalidConfig, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	nextID := uint64(1)
	if config.Role == RoleServer {
		nextID = 2
	}
	mux := &Mux{
		role:          config.Role,
		carrier:       config.Carrier,
		typeMap:       config.TypeMap,
		initialWindow: config.InitialWindow,
		maxStreams:    config.MaxStreams,
		ctx:           ctx,
		cancel:        cancel,
		done:          make(chan struct{}),
		streams:       make(map[uint64]*Stream),
		retired:       make(map[uint64]struct{}),
		retiredLimit:  max(minRetiredStreams, config.MaxStreams*retiredStreamFactor),
		nextID:        nextID,
		incoming:      make(chan *Incoming, config.MaxStreams),
		extensions:    make(chan protocol.ExtensionEnvelope, maxQueuedExtensionMessages),
		extensionSeen: make(map[uint64][32]byte),
		pendingPings:  make(map[[16]byte]chan struct{}),
	}
	mux.wait.Add(1)
	go mux.readLoop()
	return mux, nil
}

func (m *Mux) Open(ctx context.Context, metadata []byte) (*Stream, error) {
	if ctx == nil || len(metadata) > MaxOpenMetadata {
		return nil, ErrProtocol
	}

	m.mu.Lock()
	if m.err != nil {
		err := m.err
		m.mu.Unlock()
		return nil, err
	}
	if len(m.streams) >= m.maxStreams {
		m.mu.Unlock()
		return nil, ErrStreamLimit
	}
	if m.nextID > protocol.MaxStreamID {
		m.mu.Unlock()
		return nil, ErrStreamLimit
	}
	id := m.nextID
	m.nextID += 2
	stream := newStream(m, id, 0, m.initialWindow, true)
	m.streams[id] = stream
	m.stats.locallyOpenedStreams.Add(1)
	m.mu.Unlock()

	payload := binary.AppendUvarint(nil, m.initialWindow)
	payload = append(payload, metadata...)
	if err := m.send(ctx, protocol.Cell{Kind: protocol.CellOpen, StreamID: id, Sequence: 0, Payload: payload}); err != nil {
		m.removeStream(id, stream)
		stream.fail(err)
		return nil, err
	}

	select {
	case err := <-stream.openResult:
		if err != nil {
			m.removeStream(id, stream)
			return nil, err
		}
		return stream, nil
	case <-ctx.Done():
		_ = stream.sendReset(1)
		stream.fail(ctx.Err())
		m.removeStream(id, stream)
		return nil, ctx.Err()
	case <-m.done:
		return nil, m.sessionError()
	}
}

func (m *Mux) Accept(ctx context.Context) (*Incoming, error) {
	if ctx == nil {
		return nil, ErrProtocol
	}
	select {
	case incoming := <-m.incoming:
		return incoming, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.done:
		return nil, m.sessionError()
	}
}

func (m *Mux) Close() error {
	m.terminate(ErrClosed)
	m.wait.Wait()
	return nil
}

func (m *Mux) Wait(ctx context.Context) error {
	if ctx == nil {
		return ErrProtocol
	}
	select {
	case <-m.done:
		return m.sessionError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Err reports the terminal transport/session error without blocking. A nil
// result means the Mux is still alive. Higher-level continuity code uses this
// to distinguish graceful per-stream EOF from loss of the whole carrier.
func (m *Mux) Err() error {
	if m == nil {
		return ErrInvalidConfig
	}
	select {
	case <-m.done:
		return m.sessionError()
	default:
		return nil
	}
}

// Ping completes only after the authenticated peer echoes an unpredictable
// connection-level nonce. It is used to prove bidirectional carrier liveness
// after a network-path change before a reconnect is attempted.
func (m *Mux) Ping(ctx context.Context) error {
	if ctx == nil {
		return ErrProtocol
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("generate ping nonce: %w", err)
	}
	wait := make(chan struct{})
	m.pingMu.Lock()
	if len(m.pendingPings) >= 16 {
		m.pingMu.Unlock()
		return ErrPingLimit
	}
	m.pendingPings[nonce] = wait
	m.pingMu.Unlock()
	defer func() {
		m.pingMu.Lock()
		delete(m.pendingPings, nonce)
		m.pingMu.Unlock()
	}()
	if err := m.send(ctx, protocol.Cell{Kind: protocol.CellPing, Payload: nonce[:]}); err != nil {
		return err
	}
	select {
	case <-wait:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.done:
		return m.sessionError()
	}
}

func (m *Mux) readLoop() {
	defer m.wait.Done()
	for {
		raw, err := m.carrier.Receive(m.ctx)
		if err != nil {
			if m.ctx.Err() == nil {
				m.terminate(err)
			}
			return
		}
		cell, err := protocol.DecodeCell(m.typeMap, raw)
		if err != nil {
			m.stats.protocolErrors.Add(1)
			m.terminate(fmt.Errorf("%w: decode cell: %v", ErrProtocol, err))
			return
		}
		m.stats.receivedCells.Add(1)
		m.stats.receivedPayloadBytes.Add(uint64(len(cell.Payload)))
		if cell.Kind == protocol.CellWindowUpdate {
			m.stats.windowUpdatesReceived.Add(1)
		}
		if err := m.handleCell(cell); err != nil {
			if errors.Is(err, ErrProtocol) {
				m.stats.protocolErrors.Add(1)
				m.terminate(err)
				return
			}
		}
	}
}

func (m *Mux) handleCell(cell protocol.Cell) error {
	if cell.StreamID == 0 {
		switch cell.Kind {
		case protocol.CellDummy:
			return nil
		case protocol.CellPong:
			m.handlePong(cell.Payload)
			return nil
		case protocol.CellProfile:
			return m.handleExtensionPayload(cell.Payload)
		case protocol.CellPing:
			return m.send(m.ctx, protocol.Cell{Kind: protocol.CellPong, Payload: cell.Payload})
		case protocol.CellGoAway:
			m.terminate(io.EOF)
			return nil
		default:
			return fmt.Errorf("%w: invalid connection-level cell", ErrProtocol)
		}
	}
	if cell.Kind == protocol.CellOpen {
		return m.handleOpen(cell)
	}

	m.mu.Lock()
	stream := m.streams[cell.StreamID]
	_, retired := m.retired[cell.StreamID]
	m.mu.Unlock()
	if stream == nil {
		if retired {
			return nil
		}
		return fmt.Errorf(
			"%w: unknown stream %d kind=%d sequence=%d",
			ErrProtocol, cell.StreamID, cell.Kind, cell.Sequence,
		)
	}
	if err := stream.handleCell(cell); err != nil {
		stream.fail(err)
		if errors.Is(err, ErrProtocol) {
			m.stats.protocolErrors.Add(1)
			_ = stream.sendReset(2)
			m.removeStream(stream.id, stream)
		}
		return nil
	}
	if cell.Kind == protocol.CellReset {
		m.removeStream(stream.id, stream)
	}
	return nil
}

func (m *Mux) handlePong(payload []byte) {
	if len(payload) != 16 {
		return
	}
	var nonce [16]byte
	copy(nonce[:], payload)
	m.pingMu.Lock()
	wait := m.pendingPings[nonce]
	if wait != nil {
		delete(m.pendingPings, nonce)
		close(wait)
	}
	m.pingMu.Unlock()
}

func (m *Mux) handleOpen(cell protocol.Cell) error {
	if cell.Sequence != 0 || !m.validRemoteStreamID(cell.StreamID) {
		return fmt.Errorf("%w: invalid OPEN", ErrProtocol)
	}
	window, metadata, err := parseOpenPayload(cell.Payload)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.err != nil {
		m.mu.Unlock()
		return m.err
	}
	if _, exists := m.streams[cell.StreamID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("%w: duplicate stream", ErrProtocol)
	}
	if _, retired := m.retired[cell.StreamID]; retired {
		m.mu.Unlock()
		return fmt.Errorf("%w: reused stream", ErrProtocol)
	}
	if len(m.streams) >= m.maxStreams {
		m.mu.Unlock()
		return m.send(m.ctx, protocol.Cell{Kind: protocol.CellOpenFail, StreamID: cell.StreamID, Sequence: 0, Payload: []byte{1}})
	}
	stream := newStream(m, cell.StreamID, window, m.initialWindow, false)
	stream.recvSequence = 1
	m.streams[cell.StreamID] = stream
	m.stats.remotelyOpenedStreams.Add(1)
	m.mu.Unlock()

	incoming := &Incoming{mux: m, stream: stream, metadata: append([]byte(nil), metadata...)}
	select {
	case m.incoming <- incoming:
		return nil
	case <-m.done:
		return m.sessionError()
	}
}

func (m *Mux) validRemoteStreamID(id uint64) bool {
	if id == 0 || id > protocol.MaxStreamID {
		return false
	}
	if m.role == RoleServer {
		return id%2 == 1
	}
	return id%2 == 0
}

func (m *Mux) send(ctx context.Context, cell protocol.Cell) error {
	raw, err := protocol.EncodeCell(m.typeMap, cell)
	if err != nil {
		return err
	}
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	select {
	case <-m.done:
		return m.sessionError()
	default:
	}
	if err := m.carrier.Send(ctx, raw); err != nil {
		m.terminate(err)
		return err
	}
	m.stats.sentCells.Add(1)
	m.stats.sentCellPayloadBytes.Add(uint64(len(cell.Payload)))
	if cell.Kind == protocol.CellWindowUpdate {
		m.stats.windowUpdatesSent.Add(1)
	}
	return nil
}

func (m *Mux) terminate(err error) {
	if err == nil {
		err = ErrClosed
	}
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.err = err
		streams := make([]*Stream, 0, len(m.streams))
		for _, stream := range m.streams {
			streams = append(streams, stream)
		}
		m.mu.Unlock()
		close(m.done)
		m.cancel()
		_ = m.carrier.Close()
		for _, stream := range streams {
			stream.fail(err)
		}
	})
}

func (m *Mux) sessionError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	return ErrClosed
}

func (m *Mux) removeStream(id uint64, stream *Stream) {
	m.mu.Lock()
	if m.streams[id] == stream {
		delete(m.streams, id)
		m.retireStreamLocked(id)
		m.stats.retiredStreams.Add(1)
	}
	m.mu.Unlock()
}

func (m *Mux) retireStreamLocked(id uint64) {
	if _, exists := m.retired[id]; exists {
		return
	}
	m.retired[id] = struct{}{}
	m.retiredOrder = append(m.retiredOrder, id)
	if len(m.retiredOrder) <= m.retiredLimit {
		return
	}
	oldest := m.retiredOrder[0]
	m.retiredOrder[0] = 0
	m.retiredOrder = m.retiredOrder[1:]
	delete(m.retired, oldest)
}

func parseOpenPayload(payload []byte) (uint64, []byte, error) {
	window, consumed, err := readCanonicalUvarint(payload)
	if err != nil || window == 0 || window > MaxInitialWindow || len(payload)-consumed > MaxOpenMetadata {
		return 0, nil, fmt.Errorf("%w: invalid OPEN payload", ErrProtocol)
	}
	return window, payload[consumed:], nil
}

func readCanonicalUvarint(raw []byte) (uint64, int, error) {
	value, consumed := binary.Uvarint(raw)
	if consumed <= 0 {
		return 0, 0, ErrProtocol
	}
	var canonical [binary.MaxVarintLen64]byte
	length := binary.PutUvarint(canonical[:], value)
	if consumed != length || !bytes.Equal(raw[:consumed], canonical[:length]) {
		return 0, 0, ErrProtocol
	}
	return value, consumed, nil
}

func parseCredit(payload []byte) (uint64, error) {
	credit, consumed, err := readCanonicalUvarint(payload)
	if err != nil || consumed != len(payload) || credit == 0 || credit > math.MaxInt64 {
		return 0, fmt.Errorf("%w: invalid flow-control credit", ErrProtocol)
	}
	return credit, nil
}

type Incoming struct {
	mux      *Mux
	stream   *Stream
	metadata []byte

	mu      sync.Mutex
	handled bool
}

func (i *Incoming) Metadata() []byte {
	return append([]byte(nil), i.metadata...)
}

func (i *Incoming) Accept() (*Stream, error) {
	if !i.claim() {
		return nil, ErrClosed
	}
	payload := binary.AppendUvarint(nil, i.mux.initialWindow)
	if err := i.mux.send(i.mux.ctx, protocol.Cell{Kind: protocol.CellOpenOK, StreamID: i.stream.id, Sequence: 0, Payload: payload}); err != nil {
		i.stream.fail(err)
		i.mux.removeStream(i.stream.id, i.stream)
		return nil, err
	}
	return i.stream, nil
}

func (i *Incoming) AcceptWithDatagrams(datagrams *DatagramMux) (*Stream, *DatagramEndpoint, error) {
	if datagrams == nil {
		return nil, nil, ErrDatagramUnavailable
	}
	endpoint, err := datagrams.OpenEndpoint(i.stream.id)
	if err != nil {
		return nil, nil, err
	}
	if !i.claim() {
		_ = endpoint.Close()
		return nil, nil, ErrClosed
	}
	payload := binary.AppendUvarint(nil, i.mux.initialWindow)
	if err := i.mux.send(i.mux.ctx, protocol.Cell{Kind: protocol.CellOpenOK, StreamID: i.stream.id, Sequence: 0, Payload: payload}); err != nil {
		_ = endpoint.Close()
		i.stream.fail(err)
		i.mux.removeStream(i.stream.id, i.stream)
		return nil, nil, err
	}
	return i.stream, endpoint, nil
}

func (i *Incoming) Reject(code byte) error {
	if !i.claim() {
		return ErrClosed
	}
	err := i.mux.send(i.mux.ctx, protocol.Cell{Kind: protocol.CellOpenFail, StreamID: i.stream.id, Sequence: 0, Payload: []byte{code}})
	i.stream.fail(ErrRejected)
	i.mux.removeStream(i.stream.id, i.stream)
	return err
}

func (i *Incoming) claim() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.handled {
		return false
	}
	i.handled = true
	return true
}
