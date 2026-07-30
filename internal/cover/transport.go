package cover

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

const dummyRequestQueue = 64

var ErrTransportClosed = errors.New("cover transport closed")

type TransportConfig struct {
	Carrier     carrier.Carrier
	TypeMap     protocol.TypeMap
	Engine      *Engine
	PaddingSeed [32]byte
}

type TransportStats struct {
	RealMessages       uint64
	DummyMessages      uint64
	RealWireBytes      uint64
	PaddingBytes       uint64
	DummyWireBytes     uint64
	MosaicEnabled      bool
	TrafficClass       TrafficClass
	ActiveProfile      ProfileID
	ProfileTransitions uint64
}

func (s TransportStats) ActualOverheadBytes() uint64 {
	return saturatingAdd(s.PaddingBytes, s.DummyWireBytes)
}

type Transport struct {
	carrier carrier.Carrier
	typeMap protocol.TypeMap
	engine  *Engine
	random  *paddingRandom

	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	dummies  chan struct{}
	planMu   sync.Mutex
	sendMu   sync.Mutex
	wait     sync.WaitGroup
	stopOnce sync.Once

	realMessages   atomic.Uint64
	dummyMessages  atomic.Uint64
	realWireBytes  atomic.Uint64
	paddingBytes   atomic.Uint64
	dummyWireBytes atomic.Uint64
	dummySequence  atomic.Uint64
	dummiesPaused  atomic.Bool
}

var _ carrier.Carrier = (*Transport)(nil)

func NewTransport(config TransportConfig) (*Transport, error) {
	if config.Carrier == nil || config.Engine == nil || config.PaddingSeed == ([32]byte{}) {
		return nil, ErrInvalidConfig
	}
	if _, err := config.TypeMap.EncodeKind(protocol.CellDummy); err != nil {
		return nil, errors.Join(ErrInvalidConfig, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	transport := &Transport{
		carrier: config.Carrier, typeMap: config.TypeMap, engine: config.Engine,
		random: newPaddingRandom(config.PaddingSeed),
		ctx:    ctx, cancel: cancel, done: make(chan struct{}), dummies: make(chan struct{}, dummyRequestQueue),
	}
	transport.wait.Add(1)
	go transport.runDummies()
	return transport, nil
}

func (t *Transport) Send(ctx context.Context, raw []byte) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	t.planMu.Lock()
	defer t.planMu.Unlock()
	decision, err := t.engine.PlanReal(time.Now(), len(raw))
	if err != nil {
		t.stop()
		return err
	}
	padded, paddingBytes, err := t.addPadding(raw, decision.PaddingBytes)
	if err != nil {
		t.stop()
		return err
	}
	if err := waitUntil(ctx, t.done, decision.SendAt); err != nil {
		t.stop()
		return err
	}
	if err := t.sendRaw(ctx, padded); err != nil {
		t.stop()
		return err
	}
	t.realMessages.Add(1)
	t.realWireBytes.Add(uint64(len(raw)))
	t.paddingBytes.Add(uint64(paddingBytes))
	if decision.ScheduleDummy {
		select {
		case t.dummies <- struct{}{}:
		default:
		}
	}
	return nil
}

func (t *Transport) Receive(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	return t.carrier.Receive(ctx)
}

func (t *Transport) Close() error {
	t.stop()
	t.wait.Wait()
	return nil
}

func (t *Transport) Kind() protocol.CarrierKind {
	return t.carrier.Kind()
}

func (t *Transport) Stats() TransportStats {
	schedule := t.engine.Stats()
	return TransportStats{
		RealMessages: t.realMessages.Load(), DummyMessages: t.dummyMessages.Load(),
		RealWireBytes: t.realWireBytes.Load(), PaddingBytes: t.paddingBytes.Load(),
		DummyWireBytes: t.dummyWireBytes.Load(), MosaicEnabled: schedule.MosaicEnabled,
		TrafficClass: schedule.TrafficClass, ActiveProfile: schedule.ActiveProfile,
		ProfileTransitions: schedule.ProfileTransitions,
	}
}

// EnableMosaic activates the negotiated adaptive scheduler. Quiet profiles
// remain fixed and report false.
func (t *Transport) EnableMosaic() bool {
	return t != nil && t.engine.EnableMosaic()
}

// PauseDummies temporarily suppresses background cover cells while a caller
// performs a record-layer key transition. Real protocol cells keep flowing so
// the authenticated rekey exchange itself can complete.
func (t *Transport) PauseDummies() {
	if t != nil {
		t.dummiesPaused.Store(true)
	}
}

// ResumeDummies re-enables background cover cells after a key transition.
func (t *Transport) ResumeDummies() {
	if t != nil {
		t.dummiesPaused.Store(false)
	}
}

func (t *Transport) runDummies() {
	defer t.wait.Done()
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-t.dummies:
			if t.dummiesPaused.Load() {
				continue
			}
			t.planMu.Lock()
			if t.ctx.Err() != nil {
				t.planMu.Unlock()
				return
			}
			decision := t.engine.PlanDummy(time.Now())
			t.planMu.Unlock()
			if !decision.Scheduled {
				continue
			}
			raw, err := t.makeDummy(decision.Bytes)
			if err != nil {
				continue
			}
			if err := waitUntil(t.ctx, t.done, decision.SendAt); err != nil {
				return
			}
			if t.dummiesPaused.Load() {
				continue
			}
			if err := t.sendRaw(t.ctx, raw); err != nil {
				if t.ctx.Err() != nil {
					return
				}
				continue
			}
			t.dummyMessages.Add(1)
			t.dummyWireBytes.Add(uint64(len(raw)))
		}
	}
}

func (t *Transport) stop() {
	t.stopOnce.Do(func() {
		close(t.done)
		t.cancel()
		_ = t.carrier.Close()
	})
}

func (t *Transport) sendRaw(ctx context.Context, raw []byte) error {
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	select {
	case <-t.done:
		return ErrTransportClosed
	default:
	}
	return t.carrier.Send(ctx, raw)
}

func (t *Transport) addPadding(raw []byte, budget int) ([]byte, int, error) {
	if budget <= 0 {
		return raw, 0, nil
	}
	cell, err := protocol.DecodeCell(t.typeMap, raw)
	if err != nil {
		return nil, 0, err
	}
	existing := len(cell.Padding)
	maximumPadding := min(existing+budget, protocol.MaxCellPaddingSize)
	targetWireSize := min(len(raw)+budget, protocol.MaxCellSize)
	paddingLength := maximumPadding
	for paddingLength > existing && cellWireSize(cell, paddingLength) > targetWireSize {
		paddingLength--
	}
	if paddingLength == existing {
		return raw, 0, nil
	}
	padding := make([]byte, paddingLength)
	copy(padding, cell.Padding)
	t.random.Fill(padding[existing:])
	cell.Padding = padding
	padded, err := protocol.EncodeCell(t.typeMap, cell)
	if err != nil {
		return nil, 0, err
	}
	return padded, len(padded) - len(raw), nil
}

func (t *Transport) makeDummy(targetBytes int) ([]byte, error) {
	sequence := t.dummySequence.Add(1) - 1
	if sequence > protocol.MaxSequence {
		return nil, protocol.ErrInvalidCell
	}
	cell := protocol.Cell{Kind: protocol.CellDummy, Sequence: sequence}
	maximumPadding := min(targetBytes, protocol.MaxCellPaddingSize)
	paddingLength := maximumPadding
	for paddingLength > 0 && cellWireSize(cell, paddingLength) > targetBytes {
		paddingLength--
	}
	if paddingLength == 0 {
		return nil, ErrInvalidCellSize
	}
	cell.Padding = make([]byte, paddingLength)
	t.random.Fill(cell.Padding)
	return protocol.EncodeCell(t.typeMap, cell)
}

func cellWireSize(cell protocol.Cell, paddingLength int) int {
	return 1 + uvarintSize(cell.StreamID) + uvarintSize(cell.Sequence) +
		uvarintSize(uint64(len(cell.Payload))) + uvarintSize(uint64(paddingLength)) +
		len(cell.Payload) + paddingLength
}

func uvarintSize(value uint64) int {
	var buffer [binary.MaxVarintLen64]byte
	return binary.PutUvarint(buffer[:], value)
}

func waitUntil(ctx context.Context, closed <-chan struct{}, target time.Time) error {
	delay := time.Until(target)
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-closed:
			return ErrTransportClosed
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-closed:
		return ErrTransportClosed
	}
}

type paddingRandom struct {
	mu      sync.Mutex
	key     [32]byte
	counter uint64
}

func newPaddingRandom(seed [32]byte) *paddingRandom {
	return &paddingRandom{key: seed}
}

func (r *paddingRandom) Fill(destination []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(destination) > 0 {
		mac := hmac.New(sha256.New, r.key[:])
		_, _ = mac.Write([]byte("NP2 padding bytes"))
		var counter [8]byte
		binary.BigEndian.PutUint64(counter[:], r.counter)
		_, _ = mac.Write(counter[:])
		block := mac.Sum(nil)
		copied := copy(destination, block)
		destination = destination[copied:]
		r.counter++
	}
}
