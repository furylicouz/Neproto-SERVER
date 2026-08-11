package tunstack

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"sync"
	"time"

	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

const MaxClientContinuityFlows = 4096

var (
	ErrContinuityRouterConfig = errors.New("invalid client continuity router configuration")
	ErrContinuityRouterClosed = errors.New("client continuity router closed")
	ErrContinuityRoute        = errors.New("invalid client continuity route")
)

type ContinuityRoute struct {
	ID               uint64
	Mux              *session.Mux
	Control          *constellation.ControlChannel
	ConstellationID  protocol.ContinuityID
	LeaseKey         protocol.ContinuityID
	SupportsDatagram bool
	QueueBytes       uint64
	RTT              time.Duration
	LossPPM          uint32
	ThroughputBPS    uint64
	Standby          bool
}

type ClientContinuityRouterConfig struct {
	Context          context.Context
	Initial          ContinuityRoute
	MaxFlows         int
	JournalBytes     int
	AckEveryBytes    uint64
	MigrationTimeout time.Duration
	ControlTimeout   time.Duration
	Random           io.Reader
	Scheduler        constellation.Scheduler
}

type ContinuityRouteMetrics struct {
	QueueBytes    uint64
	RTT           time.Duration
	LossPPM       uint32
	ThroughputBPS uint64
}

type ClientContinuityRouter struct {
	ctx    context.Context
	cancel context.CancelFunc

	maxFlows         int
	journalBytes     int
	ackEveryBytes    uint64
	migrationTimeout time.Duration
	controlTimeout   time.Duration
	random           io.Reader
	scheduler        constellation.Scheduler

	mu          sync.Mutex
	routes      map[uint64]ContinuityRoute
	flows       map[protocol.ContinuityID]*clientContinuityFlow
	pending     map[protocol.ContinuityID]struct{}
	routeNotify chan struct{}
	closed      bool
	migrations  chan *clientContinuityFlow
	wait        sync.WaitGroup
	closeOnce   sync.Once
}

type clientContinuityFlow struct {
	router *ClientContinuityRouter
	id     protocol.ContinuityID
	stream *continuity.ResumableStream

	mu        sync.Mutex
	control   *constellation.ControlChannel
	leaseKey  protocol.ContinuityID
	routeID   uint64
	epoch     uint64
	migrating bool
	finished  bool
	closeOnce sync.Once
}

type clientFlowControlBinding struct {
	flow     *clientContinuityFlow
	leaseKey protocol.ContinuityID
}

type clientFlowConnection struct {
	flow *clientContinuityFlow
}

func NewClientContinuityRouter(config ClientContinuityRouterConfig) (*ClientContinuityRouter, error) {
	if config.Context == nil || !validClientContinuityRoute(config.Initial) ||
		config.MaxFlows <= 0 || config.MaxFlows > MaxClientContinuityFlows ||
		config.JournalBytes <= 0 || config.JournalBytes > continuity.MaxJournalCapacity ||
		config.AckEveryBytes == 0 || config.AckEveryBytes > uint64(config.JournalBytes) ||
		config.MigrationTimeout <= 0 || config.MigrationTimeout > 5*time.Minute ||
		config.ControlTimeout <= 0 || config.ControlTimeout > 30*time.Second {
		return nil, ErrContinuityRouterConfig
	}
	random := config.Random
	if random == nil {
		random = cryptorand.Reader
	}
	ctx, cancel := context.WithCancel(config.Context)
	router := &ClientContinuityRouter{
		ctx: ctx, cancel: cancel, maxFlows: config.MaxFlows,
		journalBytes: config.JournalBytes, ackEveryBytes: config.AckEveryBytes,
		migrationTimeout: config.MigrationTimeout, controlTimeout: config.ControlTimeout,
		random: random, scheduler: config.Scheduler,
		routes:      map[uint64]ContinuityRoute{config.Initial.ID: config.Initial},
		flows:       make(map[protocol.ContinuityID]*clientContinuityFlow, config.MaxFlows),
		pending:     make(map[protocol.ContinuityID]struct{}, config.MaxFlows),
		routeNotify: make(chan struct{}), migrations: make(chan *clientContinuityFlow, config.MaxFlows),
	}
	router.wait.Add(1)
	go router.migrationWorker()
	return router, nil
}

func (r *ClientContinuityRouter) AddRoute(route ContinuityRoute) error {
	if r == nil || !validClientContinuityRoute(route) {
		return ErrContinuityRoute
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrContinuityRouterClosed
	}
	if len(r.routes) >= 3 {
		return ErrCarrierPoolFull
	}
	for _, existing := range r.routes {
		if existing.ID == route.ID || existing.LeaseKey == route.LeaseKey ||
			existing.ConstellationID != route.ConstellationID {
			return ErrContinuityRoute
		}
	}
	r.routes[route.ID] = route
	r.signalRoutesLocked()
	return nil
}

func (r *ClientContinuityRouter) UpdateRouteMetrics(
	id uint64,
	metrics ContinuityRouteMetrics,
) error {
	if r == nil || id == 0 || metrics.RTT < 0 || metrics.RTT > time.Minute ||
		metrics.LossPPM > 1_000_000 {
		return ErrContinuityRoute
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrContinuityRouterClosed
	}
	route, exists := r.routes[id]
	if !exists {
		return ErrContinuityRoute
	}
	route.QueueBytes = metrics.QueueBytes
	route.RTT = metrics.RTT
	route.LossPPM = metrics.LossPPM
	route.ThroughputBPS = metrics.ThroughputBPS
	r.routes[id] = route
	return nil
}

// SetStandby excludes a healthy compatibility route from ordinary flow
// scheduling while keeping it available when no primary route remains.
func (r *ClientContinuityRouter) SetStandby(id uint64, standby bool) bool {
	if r == nil || id == 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	route, exists := r.routes[id]
	if !exists {
		return false
	}
	route.Standby = standby
	r.routes[id] = route
	r.signalRoutesLocked()
	return true
}

func (r *ClientContinuityRouter) SwitchRoute(route ContinuityRoute) error {
	if r == nil || !validClientContinuityRoute(route) {
		return ErrContinuityRoute
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrContinuityRouterClosed
	}
	r.routes = map[uint64]ContinuityRoute{route.ID: route}
	flows := make([]*clientContinuityFlow, 0, len(r.flows))
	for _, flow := range r.flows {
		flows = append(flows, flow)
	}
	r.signalRoutesLocked()
	r.mu.Unlock()
	for _, flow := range flows {
		flow.requestMigration()
	}
	return nil
}

func (r *ClientContinuityRouter) RemoveRoute(id uint64) bool {
	if r == nil || id == 0 {
		return false
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	_, exists := r.routes[id]
	if !exists {
		r.mu.Unlock()
		return false
	}
	delete(r.routes, id)
	flows := make([]*clientContinuityFlow, 0)
	for _, flow := range r.flows {
		flow.mu.Lock()
		usesRoute := flow.routeID == id
		flow.mu.Unlock()
		if usesRoute {
			flows = append(flows, flow)
		}
	}
	r.signalRoutesLocked()
	r.mu.Unlock()
	for _, flow := range flows {
		flow.requestMigration()
	}
	return true
}

func (r *ClientContinuityRouter) openStream(ctx context.Context, inner []byte) (streamConnection, error) {
	if r == nil || ctx == nil || len(inner) == 0 {
		return nil, ErrContinuityRouterConfig
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrContinuityRouterClosed
	}
	if len(r.flows)+len(r.pending) >= r.maxFlows {
		r.mu.Unlock()
		return nil, continuity.ErrFlowCapacity
	}
	route, err := r.selectRouteLocked(protocol.ContinuityID{}, constellation.FlowInteractive)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	flowID, err := r.newFlowIDLocked()
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.pending[flowID] = struct{}{}
	r.mu.Unlock()
	reserved := true
	defer func() {
		if !reserved {
			return
		}
		r.mu.Lock()
		delete(r.pending, flowID)
		r.mu.Unlock()
	}()
	metadata, err := (protocol.ContinuityOpenMetadata{
		Mode: protocol.ContinuityOpenNew, ConstellationID: route.ConstellationID,
		FlowID: flowID, LeaseKey: route.LeaseKey, Epoch: 1, Inner: append([]byte(nil), inner...),
	}).MarshalBinary()
	if err != nil {
		return nil, err
	}
	physical, err := route.Mux.Open(ctx, metadata)
	if err != nil {
		return nil, err
	}
	transport, err := session.NewTransportStream(route.Mux, physical)
	if err != nil {
		_ = physical.Close()
		return nil, err
	}
	flow := &clientContinuityFlow{
		router: r, id: flowID, control: route.Control,
		leaseKey: route.LeaseKey, routeID: route.ID, epoch: 1,
	}
	stream, err := continuity.NewResumableStream(continuity.ResumableStreamConfig{
		Context: r.ctx, Initial: transport, JournalBytes: r.journalBytes,
		AckEveryBytes: r.ackEveryBytes, OnReceiveOffset: flow.sendAck,
		OnUnavailable:         func(error) { flow.requestMigration() },
		RecoverableReadError:  clientRecoverableTransportError,
		RecoverableWriteError: clientRecoverableTransportError,
	})
	if err != nil {
		_ = physical.Close()
		return nil, err
	}
	flow.stream = stream
	if err := route.Control.Register(flowID, &clientFlowControlBinding{flow: flow, leaseKey: route.LeaseKey}); err != nil {
		_ = stream.Close()
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		route.Control.Unregister(flowID)
		_ = stream.Close()
		return nil, ErrContinuityRouterClosed
	}
	if _, ok := r.pending[flowID]; !ok {
		r.mu.Unlock()
		route.Control.Unregister(flowID)
		_ = stream.Close()
		return nil, continuity.ErrFlowConflict
	}
	if _, collision := r.flows[flowID]; collision {
		r.mu.Unlock()
		route.Control.Unregister(flowID)
		_ = stream.Close()
		return nil, continuity.ErrFlowConflict
	}
	delete(r.pending, flowID)
	r.flows[flowID] = flow
	reserved = false
	r.mu.Unlock()
	return &clientFlowConnection{flow: flow}, nil
}

// OpenStream opens one logical TCP flow whose target metadata uses the NP/2
// proxy encoding. The returned connection remains usable while the router
// migrates its physical stream between authenticated carrier sessions.
func (r *ClientContinuityRouter) OpenStream(
	ctx context.Context,
	inner []byte,
) (io.ReadWriteCloser, error) {
	return r.openStream(ctx, inner)
}

func (r *ClientContinuityRouter) Count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.flows)
}

func (r *ClientContinuityRouter) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.cancel()
		r.mu.Lock()
		r.closed = true
		flows := make([]*clientContinuityFlow, 0, len(r.flows))
		for _, flow := range r.flows {
			flows = append(flows, flow)
		}
		r.signalRoutesLocked()
		r.mu.Unlock()
		for _, flow := range flows {
			flow.finish(false)
		}
		r.wait.Wait()
	})
	return nil
}

func (r *ClientContinuityRouter) migrationWorker() {
	defer r.wait.Done()
	for {
		select {
		case flow := <-r.migrations:
			if flow != nil {
				r.migrate(flow)
			}
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *ClientContinuityRouter) migrate(flow *clientContinuityFlow) {
	ctx, cancel := context.WithTimeout(r.ctx, r.migrationTimeout)
	defer cancel()
	if err := flow.stream.DetachPhysical(); err != nil {
		if !errors.Is(err, continuity.ErrResumableClosed) {
			flow.finish(true)
		}
		return
	}
	for {
		flow.mu.Lock()
		if flow.finished {
			flow.mu.Unlock()
			return
		}
		oldControl, oldLeaseKey := flow.control, flow.leaseKey
		epoch := flow.epoch + 1
		flow.mu.Unlock()
		r.mu.Lock()
		class := constellation.FlowInteractive
		currentOffsets := flow.stream.Offsets()
		if currentOffsets.SendEnd >= 1024*1024 || currentOffsets.Receive >= 1024*1024 {
			class = constellation.FlowBulk
		}
		route, err := r.selectRouteLocked(oldLeaseKey, class)
		notify := r.routeNotify
		r.mu.Unlock()
		if err != nil {
			select {
			case <-notify:
				continue
			case <-ctx.Done():
				flow.finish(true)
				return
			}
		}
		offsets := flow.stream.Offsets()
		metadata, err := (protocol.ContinuityOpenMetadata{
			Mode: protocol.ContinuityOpenResume, ConstellationID: route.ConstellationID,
			FlowID: flow.id, LeaseKey: route.LeaseKey, Epoch: epoch,
			SendOffset: offsets.SendBase, ReceiveOffset: offsets.Receive,
		}).MarshalBinary()
		if err != nil {
			flow.finish(true)
			return
		}
		physical, err := route.Mux.Open(ctx, metadata)
		if err != nil {
			if !r.waitForRouteChange(ctx, notify) {
				flow.finish(true)
				return
			}
			continue
		}
		transport, err := session.NewTransportStream(route.Mux, physical)
		if err != nil {
			_ = physical.Close()
			flow.finish(true)
			return
		}
		if oldControl != route.Control {
			if err := route.Control.Register(flow.id, &clientFlowControlBinding{flow: flow, leaseKey: route.LeaseKey}); err != nil {
				_ = transport.Close()
				if !r.waitForRouteChange(ctx, notify) {
					flow.finish(true)
					return
				}
				continue
			}
		}
		flow.mu.Lock()
		flow.control = route.Control
		flow.leaseKey = route.LeaseKey
		flow.routeID = route.ID
		flow.mu.Unlock()
		if err := flow.stream.Replace(transport, continuity.ResumeState{
			PeerReceiveOffset: offsets.SendBase, ReceiveOffset: offsets.Receive,
		}); err != nil {
			flow.mu.Lock()
			flow.control = oldControl
			flow.leaseKey = oldLeaseKey
			flow.mu.Unlock()
			if oldControl != route.Control {
				route.Control.Unregister(flow.id)
			}
			if !r.waitForRouteChange(ctx, notify) {
				flow.finish(true)
				return
			}
			continue
		}
		flow.mu.Lock()
		flow.epoch = epoch
		flow.migrating = false
		flow.mu.Unlock()
		if oldControl != route.Control {
			oldControl.Unregister(flow.id)
		}
		return
	}
}

func (r *ClientContinuityRouter) waitForRouteChange(ctx context.Context, notify <-chan struct{}) bool {
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-notify:
		return true
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *ClientContinuityRouter) selectRouteLocked(
	exclude protocol.ContinuityID,
	class constellation.FlowClass,
) (ContinuityRoute, error) {
	candidates := make([]constellation.ScheduleCandidate, 0, len(r.routes))
	standbyCandidates := make([]constellation.ScheduleCandidate, 0, len(r.routes))
	for _, route := range r.routes {
		if route.LeaseKey == exclude || route.Mux.Err() != nil {
			continue
		}
		candidate := constellation.ScheduleCandidate{
			LeaseKey: route.LeaseKey, Carrier: route.Mux.CarrierKind(), Healthy: true,
			SupportsDatagram: route.SupportsDatagram,
			ActiveStreams:    route.Mux.Stats().ActiveStreams, MaxStreams: route.Mux.MaxStreams(),
			QueueBytes: route.QueueBytes, RTT: route.RTT, LossPPM: route.LossPPM,
			ThroughputBPS: route.ThroughputBPS,
		}
		if route.Standby {
			standbyCandidates = append(standbyCandidates, candidate)
		} else {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		candidates = standbyCandidates
	}
	selectedKey, err := r.scheduler.Select(class, protocol.ContinuityID{}, candidates)
	if err != nil {
		return ContinuityRoute{}, ErrContinuityRoute
	}
	for _, route := range r.routes {
		if route.LeaseKey == selectedKey {
			return route, nil
		}
	}
	return ContinuityRoute{}, ErrContinuityRoute
}

func (r *ClientContinuityRouter) newFlowIDLocked() (protocol.ContinuityID, error) {
	for range 8 {
		var id protocol.ContinuityID
		if _, err := io.ReadFull(r.random, id[:]); err != nil {
			return protocol.ContinuityID{}, continuity.ErrFlowEntropy
		}
		if id == (protocol.ContinuityID{}) {
			continue
		}
		_, active := r.flows[id]
		_, pending := r.pending[id]
		if !active && !pending {
			return id, nil
		}
	}
	return protocol.ContinuityID{}, continuity.ErrFlowEntropy
}

func (r *ClientContinuityRouter) signalRoutesLocked() {
	close(r.routeNotify)
	r.routeNotify = make(chan struct{})
}

func (f *clientContinuityFlow) requestMigration() {
	if f == nil || f.router == nil {
		return
	}
	f.mu.Lock()
	if f.finished || f.migrating {
		f.mu.Unlock()
		return
	}
	f.migrating = true
	f.mu.Unlock()
	select {
	case f.router.migrations <- f:
	case <-f.router.ctx.Done():
		f.finish(false)
	default:
		f.finish(true)
	}
}

func (f *clientContinuityFlow) sendAck(offset uint64) error {
	f.mu.Lock()
	control := f.control
	f.mu.Unlock()
	if control == nil {
		return ErrContinuityRouterClosed
	}
	ctx, cancel := context.WithTimeout(f.router.ctx, f.router.controlTimeout)
	defer cancel()
	return control.SendAck(ctx, f.id, offset)
}

func (f *clientContinuityFlow) finish(sendAbort bool) {
	if f == nil {
		return
	}
	f.closeOnce.Do(func() {
		f.mu.Lock()
		f.finished = true
		control := f.control
		f.control = nil
		f.mu.Unlock()
		if sendAbort && control != nil {
			ctx, cancel := context.WithTimeout(f.router.ctx, f.router.controlTimeout)
			_ = control.SendAbort(ctx, f.id)
			cancel()
		}
		if control != nil {
			control.Unregister(f.id)
		}
		_ = f.stream.Close()
		f.router.mu.Lock()
		delete(f.router.flows, f.id)
		f.router.mu.Unlock()
	})
}

func (b *clientFlowControlBinding) Acknowledge(offset uint64) error {
	if b == nil || b.flow == nil {
		return ErrContinuityRouterClosed
	}
	return b.flow.stream.Ack(offset)
}

func (b *clientFlowControlBinding) Abort(err error) {
	if b == nil || b.flow == nil {
		return
	}
	if errors.Is(err, constellation.ErrControlChannelClosed) {
		b.flow.requestMigration()
		return
	}
	b.flow.mu.Lock()
	current := b.flow.leaseKey
	b.flow.mu.Unlock()
	if current == b.leaseKey {
		b.flow.finish(false)
	}
}

func (c *clientFlowConnection) Read(destination []byte) (int, error) {
	if c == nil || c.flow == nil {
		return 0, ErrContinuityRouterClosed
	}
	return c.flow.stream.Read(destination)
}

func (c *clientFlowConnection) Write(payload []byte) (int, error) {
	if c == nil || c.flow == nil {
		return 0, ErrContinuityRouterClosed
	}
	return c.flow.stream.Write(payload)
}

func (c *clientFlowConnection) CloseWrite() error {
	if c == nil || c.flow == nil {
		return ErrContinuityRouterClosed
	}
	return c.flow.stream.CloseWrite()
}

func (c *clientFlowConnection) Close() error {
	if c == nil || c.flow == nil {
		return nil
	}
	c.flow.finish(true)
	return nil
}

func validClientContinuityRoute(route ContinuityRoute) bool {
	return route.ID != 0 && route.Mux != nil && route.Control != nil &&
		route.ConstellationID != (protocol.ContinuityID{}) && route.LeaseKey != (protocol.ContinuityID{})
}

func clientRecoverableTransportError(err error) bool {
	return err != nil && errors.Is(err, session.ErrCarrierLost)
}
