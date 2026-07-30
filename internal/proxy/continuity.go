package proxy

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

const (
	MinContinuityJournalBytes = 64 * 1024
	defaultControlTimeout     = 2 * time.Second
	defaultMigrationTimeout   = 15 * time.Second
)

var (
	ErrContinuityRuntimeConfig = errors.New("invalid proxy continuity runtime configuration")
	ErrContinuityRuntimeClosed = errors.New("proxy continuity runtime closed")
	ErrContinuityLeaseBinding  = errors.New("proxy continuity lease binding mismatch")
)

type ContinuityRuntimeConfig struct {
	Context              context.Context
	MaxFlows             int
	MaxFlowsPerPrincipal int
	JournalBytes         int
	AckEveryBytes        uint64
	ControlTimeout       time.Duration
	MigrationTimeout     time.Duration
}

type ContinuityLease struct {
	Principal       continuity.PrincipalID
	ConstellationID protocol.ContinuityID
	LeaseKey        protocol.ContinuityID
	Control         *constellation.ControlChannel
	Mux             *session.Mux
}

type ContinuityRuntime struct {
	ctx      context.Context
	cancel   context.CancelFunc
	registry *continuity.FlowRegistry

	journalBytes     int
	ackEveryBytes    uint64
	controlTimeout   time.Duration
	migrationTimeout time.Duration

	mu     sync.Mutex
	flows  map[protocol.ContinuityID]*serverContinuityFlow
	closed bool
	wait   sync.WaitGroup
	close  sync.Once
}

type serverContinuityFlow struct {
	runtime         *ContinuityRuntime
	id              protocol.ContinuityID
	principal       continuity.PrincipalID
	constellationID protocol.ContinuityID
	stream          *continuity.ResumableStream
	target          io.ReadWriteCloser

	mu                  sync.Mutex
	control             *constellation.ControlChannel
	leaseKey            protocol.ContinuityID
	migrationTimer      *time.Timer
	migrationGeneration uint64

	releaseOnce sync.Once
	release     func()
	finishOnce  sync.Once
}

type flowControlBinding struct {
	flow     *serverContinuityFlow
	leaseKey protocol.ContinuityID
}

func NewContinuityRuntime(config ContinuityRuntimeConfig) (*ContinuityRuntime, error) {
	if config.Context == nil || config.MaxFlows <= 0 || config.MaxFlows > continuity.MaxLogicalFlows ||
		config.MaxFlowsPerPrincipal <= 0 || config.MaxFlowsPerPrincipal > config.MaxFlows ||
		config.JournalBytes < MinContinuityJournalBytes || config.JournalBytes > continuity.MaxJournalCapacity ||
		config.AckEveryBytes == 0 || config.AckEveryBytes > uint64(config.JournalBytes) ||
		config.ControlTimeout < 0 || config.ControlTimeout > 30*time.Second ||
		config.MigrationTimeout < 0 || config.MigrationTimeout > 5*time.Minute {
		return nil, ErrContinuityRuntimeConfig
	}
	registry, err := continuity.NewFlowRegistry(continuity.FlowRegistryConfig{
		MaxFlows: config.MaxFlows, MaxFlowsPerPrincipal: config.MaxFlowsPerPrincipal,
	})
	if err != nil {
		return nil, errors.Join(ErrContinuityRuntimeConfig, err)
	}
	controlTimeout := config.ControlTimeout
	if controlTimeout == 0 {
		controlTimeout = defaultControlTimeout
	}
	migrationTimeout := config.MigrationTimeout
	if migrationTimeout == 0 {
		migrationTimeout = defaultMigrationTimeout
	}
	runtimeContext, cancel := context.WithCancel(config.Context)
	return &ContinuityRuntime{
		ctx: runtimeContext, cancel: cancel, registry: registry,
		journalBytes: config.JournalBytes, ackEveryBytes: config.AckEveryBytes,
		controlTimeout: controlTimeout, migrationTimeout: migrationTimeout,
		flows: make(map[protocol.ContinuityID]*serverContinuityFlow, config.MaxFlows),
	}, nil
}

func (r *ContinuityRuntime) AdmitNew(
	metadata protocol.ContinuityOpenMetadata,
	lease ContinuityLease,
	physical *session.Stream,
	target io.ReadWriteCloser,
	release func(),
) error {
	if r == nil || metadata.Mode != protocol.ContinuityOpenNew ||
		!validContinuityLease(lease, metadata) || physical == nil || target == nil {
		closeContinuityInputs(physical, target, release)
		return ErrContinuityLeaseBinding
	}
	flow := &serverContinuityFlow{
		runtime: r, id: metadata.FlowID, principal: lease.Principal,
		constellationID: lease.ConstellationID, target: target,
		control: lease.Control, leaseKey: lease.LeaseKey, release: release,
	}
	transport, err := session.NewTransportStream(lease.Mux, physical)
	if err != nil {
		closeContinuityInputs(physical, target, release)
		return err
	}
	stream, err := continuity.NewResumableStream(continuity.ResumableStreamConfig{
		Context: r.ctx, Initial: transport, JournalBytes: r.journalBytes,
		AckEveryBytes: r.ackEveryBytes, OnReceiveOffset: flow.sendAck,
		RecoverableReadError:  recoverableTransportError,
		RecoverableWriteError: recoverableTransportError,
	})
	if err != nil {
		closeContinuityInputs(physical, target, release)
		return err
	}
	flow.stream = stream
	if _, err := r.registry.Create(continuity.FlowCreateRequest{
		Principal: lease.Principal, ConstellationID: lease.ConstellationID,
		FlowID: metadata.FlowID, LeaseKey: lease.LeaseKey, Upstream: target,
	}); err != nil {
		_ = stream.Close()
		_ = target.Close()
		flow.runRelease()
		return err
	}
	binding := &flowControlBinding{flow: flow, leaseKey: lease.LeaseKey}
	if err := lease.Control.Register(metadata.FlowID, binding); err != nil {
		_ = stream.Close()
		_ = r.registry.Release(lease.Principal, lease.ConstellationID, metadata.FlowID)
		flow.runRelease()
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		lease.Control.Unregister(metadata.FlowID)
		_ = stream.Close()
		_ = r.registry.Release(lease.Principal, lease.ConstellationID, metadata.FlowID)
		flow.runRelease()
		return ErrContinuityRuntimeClosed
	}
	if _, exists := r.flows[metadata.FlowID]; exists {
		r.mu.Unlock()
		lease.Control.Unregister(metadata.FlowID)
		_ = stream.Close()
		_ = r.registry.Release(lease.Principal, lease.ConstellationID, metadata.FlowID)
		flow.runRelease()
		return continuity.ErrFlowConflict
	}
	r.flows[metadata.FlowID] = flow
	r.wait.Add(1)
	r.mu.Unlock()
	go flow.run()
	return nil
}

func (r *ContinuityRuntime) Resume(
	metadata protocol.ContinuityOpenMetadata,
	lease ContinuityLease,
	physical *session.Stream,
) error {
	if r == nil || metadata.Mode != protocol.ContinuityOpenResume ||
		!validContinuityLease(lease, metadata) || physical == nil {
		if physical != nil {
			_ = physical.Close()
		}
		return ErrContinuityLeaseBinding
	}
	r.mu.Lock()
	flow := r.flows[metadata.FlowID]
	closed := r.closed
	r.mu.Unlock()
	if closed {
		_ = physical.Close()
		return ErrContinuityRuntimeClosed
	}
	if flow == nil {
		_ = physical.Close()
		return continuity.ErrFlowNotFound
	}
	_, err := r.registry.Attach(continuity.FlowAttachRequest{
		Principal: lease.Principal, ConstellationID: lease.ConstellationID,
		FlowID: metadata.FlowID, LeaseKey: lease.LeaseKey, Epoch: metadata.Epoch,
	})
	if err != nil {
		_ = physical.Close()
		if errors.Is(err, continuity.ErrFlowConflict) {
			flow.finish()
		}
		return err
	}
	return flow.replace(metadata, lease, physical)
}

func (r *ContinuityRuntime) Count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.flows)
}

func (r *ContinuityRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.close.Do(func() {
		r.cancel()
		r.mu.Lock()
		r.closed = true
		flows := make([]*serverContinuityFlow, 0, len(r.flows))
		for _, flow := range r.flows {
			flows = append(flows, flow)
		}
		r.mu.Unlock()
		for _, flow := range flows {
			flow.finish()
		}
		r.wait.Wait()
		_ = r.registry.Close()
	})
	return nil
}

func (f *serverContinuityFlow) replace(
	metadata protocol.ContinuityOpenMetadata,
	lease ContinuityLease,
	physical *session.Stream,
) error {
	transport, err := session.NewTransportStream(lease.Mux, physical)
	if err != nil {
		_ = physical.Close()
		return err
	}
	f.mu.Lock()
	oldControl := f.control
	oldLeaseKey := f.leaseKey
	f.mu.Unlock()
	if oldControl != lease.Control {
		if err := lease.Control.Register(f.id, &flowControlBinding{flow: f, leaseKey: lease.LeaseKey}); err != nil {
			_ = physical.Close()
			return err
		}
	}
	f.mu.Lock()
	f.control = lease.Control
	f.leaseKey = lease.LeaseKey
	f.mu.Unlock()
	if err := f.stream.DetachPhysical(); err != nil {
		f.mu.Lock()
		f.control = oldControl
		f.leaseKey = oldLeaseKey
		f.mu.Unlock()
		if oldControl != lease.Control {
			lease.Control.Unregister(f.id)
		}
		_ = physical.Close()
		return err
	}
	if err := f.stream.Replace(transport, continuity.ResumeState{
		PeerReceiveOffset: metadata.ReceiveOffset,
		ReceiveOffset:     metadata.SendOffset,
	}); err != nil {
		f.mu.Lock()
		f.control = oldControl
		f.leaseKey = oldLeaseKey
		f.mu.Unlock()
		if oldControl != lease.Control {
			lease.Control.Unregister(f.id)
		}
		return err
	}
	f.cancelMigration()
	if oldControl != lease.Control {
		oldControl.Unregister(f.id)
	}
	return nil
}

func (f *serverContinuityFlow) sendAck(offset uint64) error {
	f.mu.Lock()
	control := f.control
	f.mu.Unlock()
	if control == nil {
		return ErrContinuityRuntimeClosed
	}
	ctx, cancel := context.WithTimeout(f.runtime.ctx, f.runtime.controlTimeout)
	defer cancel()
	return control.SendAck(ctx, f.id, offset)
}

func (f *serverContinuityFlow) run() {
	defer f.runtime.wait.Done()
	_ = relayOpenTarget(f.runtime.ctx, f.stream, f.target)
	f.finish()
}

func (f *serverContinuityFlow) finish() {
	f.finishOnce.Do(func() {
		f.runtime.mu.Lock()
		delete(f.runtime.flows, f.id)
		f.runtime.mu.Unlock()
		f.mu.Lock()
		if f.migrationTimer != nil {
			f.migrationTimer.Stop()
			f.migrationTimer = nil
		}
		f.migrationGeneration++
		control := f.control
		f.control = nil
		f.mu.Unlock()
		if control != nil {
			control.Unregister(f.id)
		}
		_ = f.stream.Close()
		_ = f.runtime.registry.Release(f.principal, f.constellationID, f.id)
		f.runRelease()
	})
}

func (f *serverContinuityFlow) runRelease() {
	f.releaseOnce.Do(func() {
		if f.release != nil {
			f.release()
		}
	})
}

func (b *flowControlBinding) Acknowledge(offset uint64) error {
	if b == nil || b.flow == nil {
		return ErrContinuityRuntimeClosed
	}
	return b.flow.stream.Ack(offset)
}

func (b *flowControlBinding) Abort(err error) {
	if b == nil || b.flow == nil {
		return
	}
	if errors.Is(err, constellation.ErrControlChannelClosed) {
		b.flow.beginMigration(b.leaseKey)
		return
	}
	b.flow.mu.Lock()
	current := b.flow.leaseKey
	b.flow.mu.Unlock()
	if current != b.leaseKey {
		return
	}
	b.flow.finish()
}

func (f *serverContinuityFlow) beginMigration(lostLease protocol.ContinuityID) {
	f.mu.Lock()
	if f.leaseKey != lostLease {
		f.mu.Unlock()
		return
	}
	if f.migrationTimer != nil {
		f.migrationTimer.Stop()
	}
	f.migrationGeneration++
	generation := f.migrationGeneration
	f.migrationTimer = time.AfterFunc(f.runtime.migrationTimeout, func() {
		f.mu.Lock()
		expired := f.leaseKey == lostLease && f.migrationGeneration == generation
		f.mu.Unlock()
		if expired {
			f.finish()
		}
	})
	f.mu.Unlock()
}

func (f *serverContinuityFlow) cancelMigration() {
	f.mu.Lock()
	if f.migrationTimer != nil {
		f.migrationTimer.Stop()
		f.migrationTimer = nil
	}
	f.migrationGeneration++
	f.mu.Unlock()
}

func validContinuityLease(lease ContinuityLease, metadata protocol.ContinuityOpenMetadata) bool {
	return lease.Principal != (continuity.PrincipalID{}) && lease.Control != nil && lease.Mux != nil &&
		lease.ConstellationID != (protocol.ContinuityID{}) && lease.LeaseKey != (protocol.ContinuityID{}) &&
		metadata.ConstellationID == lease.ConstellationID && metadata.LeaseKey == lease.LeaseKey
}

func recoverableTransportError(err error) bool {
	return err != nil && errors.Is(err, session.ErrCarrierLost)
}

func closeContinuityInputs(physical *session.Stream, target io.ReadWriteCloser, release func()) {
	if physical != nil {
		_ = physical.Close()
	}
	if target != nil {
		_ = target.Close()
	}
	if release != nil {
		release()
	}
}
