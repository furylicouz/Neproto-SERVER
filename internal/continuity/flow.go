package continuity

import (
	"crypto/hmac"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"math"
	"reflect"
	"sync"

	"neproto.local/chameleon/internal/protocol"
)

const (
	MaxLogicalFlows   = 65_536
	maxFlowIDAttempts = 8
)

type FlowRegistryConfig struct {
	MaxFlows             int
	MaxFlowsPerPrincipal int
	Random               io.Reader
}

type FlowCreateRequest struct {
	Principal       PrincipalID
	ConstellationID protocol.ContinuityID
	FlowID          protocol.ContinuityID
	LeaseKey        protocol.ContinuityID
	Upstream        io.ReadWriteCloser
}

type FlowAttachRequest struct {
	Principal       PrincipalID
	ConstellationID protocol.ContinuityID
	FlowID          protocol.ContinuityID
	LeaseKey        protocol.ContinuityID
	Epoch           uint64
}

// Flow is an immutable point-in-time view. Upstream remains the same object for
// every accepted lease epoch and is owned by the registry until Release/Close.
type Flow struct {
	ID              protocol.ContinuityID
	ConstellationID protocol.ContinuityID
	LeaseKey        protocol.ContinuityID
	Epoch           uint64
	Upstream        io.ReadWriteCloser
}

type flowRecord struct {
	principal       PrincipalID
	constellationID protocol.ContinuityID
	leaseKey        protocol.ContinuityID
	epoch           uint64
	upstream        io.ReadWriteCloser
}

type FlowRegistry struct {
	mu sync.Mutex

	maxFlows             int
	maxFlowsPerPrincipal int
	random               io.Reader
	flows                map[protocol.ContinuityID]*flowRecord
	principalCounts      map[PrincipalID]int
	closed               bool
}

func NewFlowRegistry(config FlowRegistryConfig) (*FlowRegistry, error) {
	if config.MaxFlows <= 0 || config.MaxFlows > MaxLogicalFlows ||
		config.MaxFlowsPerPrincipal <= 0 || config.MaxFlowsPerPrincipal > config.MaxFlows {
		return nil, ErrFlowRegistryConfig
	}
	random := config.Random
	if random == nil {
		random = cryptorand.Reader
	}
	return &FlowRegistry{
		maxFlows: config.MaxFlows, maxFlowsPerPrincipal: config.MaxFlowsPerPrincipal,
		random: random, flows: make(map[protocol.ContinuityID]*flowRecord, config.MaxFlows),
		principalCounts: make(map[PrincipalID]int),
	}, nil
}

func (r *FlowRegistry) Create(request FlowCreateRequest) (Flow, error) {
	if r == nil {
		return Flow{}, ErrFlowRegistryConfig
	}
	if request.Principal == (PrincipalID{}) || request.ConstellationID == (protocol.ContinuityID{}) ||
		request.LeaseKey == (protocol.ContinuityID{}) || nilReadWriteCloser(request.Upstream) {
		return Flow{}, ErrFlowBinding
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Flow{}, ErrFlowRegistryClosed
	}
	if len(r.flows) >= r.maxFlows || r.principalCounts[request.Principal] >= r.maxFlowsPerPrincipal {
		return Flow{}, ErrFlowCapacity
	}
	flowID := request.FlowID
	if flowID == (protocol.ContinuityID{}) {
		var err error
		flowID, err = r.newFlowIDLocked()
		if err != nil {
			return Flow{}, err
		}
	} else if _, collision := r.flows[flowID]; collision {
		return Flow{}, ErrFlowConflict
	}
	record := &flowRecord{
		principal: request.Principal, constellationID: request.ConstellationID,
		leaseKey: request.LeaseKey, epoch: 1, upstream: request.Upstream,
	}
	r.flows[flowID] = record
	r.principalCounts[request.Principal]++
	return snapshotFlow(flowID, record), nil
}

func (r *FlowRegistry) Attach(request FlowAttachRequest) (Flow, error) {
	if r == nil {
		return Flow{}, ErrFlowRegistryConfig
	}
	if request.Principal == (PrincipalID{}) || request.ConstellationID == (protocol.ContinuityID{}) ||
		request.FlowID == (protocol.ContinuityID{}) || request.LeaseKey == (protocol.ContinuityID{}) || request.Epoch == 0 {
		return Flow{}, ErrFlowBinding
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return Flow{}, ErrFlowRegistryClosed
	}
	record, exists := r.flows[request.FlowID]
	if !exists {
		r.mu.Unlock()
		return Flow{}, ErrFlowNotFound
	}
	if !hmac.Equal(record.principal[:], request.Principal[:]) || record.constellationID != request.ConstellationID {
		r.mu.Unlock()
		return Flow{}, ErrFlowBinding
	}
	if request.Epoch == record.epoch && request.LeaseKey == record.leaseKey {
		flow := snapshotFlow(request.FlowID, record)
		r.mu.Unlock()
		return flow, nil
	}
	if record.epoch != math.MaxUint64 && request.Epoch == record.epoch+1 {
		record.epoch = request.Epoch
		record.leaseKey = request.LeaseKey
		flow := snapshotFlow(request.FlowID, record)
		r.mu.Unlock()
		return flow, nil
	}
	delete(r.flows, request.FlowID)
	r.decrementPrincipalLocked(record.principal)
	upstream := record.upstream
	r.mu.Unlock()
	return Flow{}, errors.Join(ErrFlowConflict, upstream.Close())
}

func (r *FlowRegistry) State(id protocol.ContinuityID) (Flow, bool) {
	if r == nil || id == (protocol.ContinuityID{}) {
		return Flow{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Flow{}, false
	}
	record, exists := r.flows[id]
	if !exists {
		return Flow{}, false
	}
	return snapshotFlow(id, record), true
}

func (r *FlowRegistry) Release(
	principal PrincipalID,
	constellationID protocol.ContinuityID,
	flowID protocol.ContinuityID,
) error {
	if r == nil {
		return ErrFlowRegistryConfig
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrFlowRegistryClosed
	}
	record, exists := r.flows[flowID]
	if !exists {
		r.mu.Unlock()
		return ErrFlowNotFound
	}
	if principal == (PrincipalID{}) || constellationID == (protocol.ContinuityID{}) ||
		!hmac.Equal(record.principal[:], principal[:]) || record.constellationID != constellationID {
		r.mu.Unlock()
		return ErrFlowBinding
	}
	delete(r.flows, flowID)
	r.decrementPrincipalLocked(record.principal)
	upstream := record.upstream
	r.mu.Unlock()
	return upstream.Close()
}

func (r *FlowRegistry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.flows)
}

func (r *FlowRegistry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	upstreams := make([]io.ReadWriteCloser, 0, len(r.flows))
	for _, record := range r.flows {
		upstreams = append(upstreams, record.upstream)
	}
	clear(r.flows)
	clear(r.principalCounts)
	r.mu.Unlock()
	closeErrors := make([]error, 0, len(upstreams))
	for _, upstream := range upstreams {
		closeErrors = append(closeErrors, upstream.Close())
	}
	return errors.Join(closeErrors...)
}

func (r *FlowRegistry) newFlowIDLocked() (protocol.ContinuityID, error) {
	for range maxFlowIDAttempts {
		var id protocol.ContinuityID
		if _, err := io.ReadFull(r.random, id[:]); err != nil {
			return protocol.ContinuityID{}, errors.Join(ErrFlowEntropy, err)
		}
		if id == (protocol.ContinuityID{}) {
			continue
		}
		if _, collision := r.flows[id]; !collision {
			return id, nil
		}
	}
	return protocol.ContinuityID{}, ErrFlowEntropy
}

func (r *FlowRegistry) decrementPrincipalLocked(principal PrincipalID) {
	remaining := r.principalCounts[principal] - 1
	if remaining <= 0 {
		delete(r.principalCounts, principal)
		return
	}
	r.principalCounts[principal] = remaining
}

func snapshotFlow(id protocol.ContinuityID, record *flowRecord) Flow {
	return Flow{
		ID: id, ConstellationID: record.constellationID,
		LeaseKey: record.leaseKey, Epoch: record.epoch, Upstream: record.upstream,
	}
}

func nilReadWriteCloser(resource io.ReadWriteCloser) bool {
	if resource == nil {
		return true
	}
	value := reflect.ValueOf(resource)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
