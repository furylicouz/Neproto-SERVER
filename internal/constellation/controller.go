package constellation

import (
	"crypto/hmac"
	"errors"
	"io"
	"reflect"
	"sync"

	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
)

var (
	ErrControllerConfig = errors.New("invalid constellation controller configuration")
	ErrControllerClosed = errors.New("constellation controller closed")
	ErrLeaseBinding     = errors.New("lease authentication binding mismatch")
	ErrLeaseDuplicate   = errors.New("duplicate constellation lease")
	ErrLeaseCapacity    = errors.New("constellation lease capacity exceeded")
	ErrLeaseNotFound    = errors.New("constellation lease not found")
	ErrLeaseState       = errors.New("invalid constellation lease state")
	ErrNoActiveLease    = errors.New("no active constellation lease")
)

type LeaseID uint64

type LeaseState uint8

const (
	LeaseActive LeaseState = iota + 1
	LeaseDraining
)

type LeaseCandidate struct {
	Key             protocol.ContinuityID
	Principal       continuity.PrincipalID
	ConstellationID protocol.ContinuityID
	Carrier         protocol.CarrierKind
	Resource        io.Closer
}

type ControllerConfig struct {
	Principal       continuity.PrincipalID
	ConstellationID protocol.ContinuityID
	MaxActive       int
	MaxDraining     int
}

type LeaseSnapshot struct {
	ID      LeaseID
	Key     protocol.ContinuityID
	Carrier protocol.CarrierKind
	State   LeaseState
	Load    uint64
}

type ControllerState struct {
	PrimaryID LeaseID
	Active    int
	Draining  int
	Closed    bool
}

type leaseRecord struct {
	candidate LeaseCandidate
	state     LeaseState
	load      uint64
}

type Controller struct {
	mu sync.Mutex

	principal       continuity.PrincipalID
	constellationID protocol.ContinuityID
	maxActive       int
	maxDraining     int
	nextID          LeaseID
	primaryID       LeaseID
	tieCursor       uint64
	leases          map[LeaseID]*leaseRecord
	order           []LeaseID
	closed          bool
}

func NewController(config ControllerConfig, primary LeaseCandidate) (*Controller, error) {
	if config.Principal == (continuity.PrincipalID{}) ||
		config.ConstellationID == (protocol.ContinuityID{}) ||
		config.MaxActive <= 0 || config.MaxActive > 8 ||
		config.MaxDraining <= 0 || config.MaxDraining > 8 {
		return nil, ErrControllerConfig
	}
	controller := &Controller{
		principal: config.Principal, constellationID: config.ConstellationID,
		maxActive: config.MaxActive, maxDraining: config.MaxDraining,
		nextID: 1, leases: make(map[LeaseID]*leaseRecord, config.MaxActive+config.MaxDraining),
	}
	if err := controller.validateCandidate(primary); err != nil {
		return nil, err
	}
	id := controller.addLocked(primary)
	controller.primaryID = id
	return controller, nil
}

func (c *Controller) Add(candidate LeaseCandidate) (LeaseID, error) {
	if c == nil {
		return 0, ErrControllerConfig
	}
	if err := c.validateCandidate(candidate); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, ErrControllerClosed
	}
	for _, record := range c.leases {
		if record.candidate.Key == candidate.Key {
			return 0, ErrLeaseDuplicate
		}
	}
	if c.countStateLocked(LeaseActive) >= c.maxActive {
		return 0, ErrLeaseCapacity
	}
	id := c.addLocked(candidate)
	if c.primaryID == 0 {
		c.primaryID = id
	}
	return id, nil
}

func (c *Controller) UpdateLoad(id LeaseID, load uint64) error {
	if c == nil {
		return ErrControllerConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrControllerClosed
	}
	record, exists := c.leases[id]
	if !exists {
		return ErrLeaseNotFound
	}
	record.load = load
	return nil
}

func (c *Controller) Select() (LeaseSnapshot, error) {
	if c == nil {
		return LeaseSnapshot{}, ErrControllerConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return LeaseSnapshot{}, ErrControllerClosed
	}
	minimum := ^uint64(0)
	ties := make([]*leaseRecordWithID, 0, c.maxActive)
	for _, id := range c.order {
		record := c.leases[id]
		if record == nil || record.state != LeaseActive {
			continue
		}
		if record.load < minimum {
			minimum = record.load
			ties = append(ties[:0], &leaseRecordWithID{id: id, record: record})
		} else if record.load == minimum {
			ties = append(ties, &leaseRecordWithID{id: id, record: record})
		}
	}
	if len(ties) == 0 {
		return LeaseSnapshot{}, ErrNoActiveLease
	}
	selected := ties[c.tieCursor%uint64(len(ties))]
	c.tieCursor++
	return snapshotLease(selected.id, selected.record), nil
}

func (c *Controller) Promote(id LeaseID) error {
	if c == nil {
		return ErrControllerConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrControllerClosed
	}
	target, exists := c.leases[id]
	if !exists {
		return ErrLeaseNotFound
	}
	if target.state != LeaseActive {
		return ErrLeaseState
	}
	if c.primaryID == id {
		return nil
	}
	current := c.leases[c.primaryID]
	if current != nil && current.state == LeaseActive {
		if c.countStateLocked(LeaseDraining) >= c.maxDraining {
			return ErrLeaseCapacity
		}
		current.state = LeaseDraining
	}
	c.primaryID = id
	return nil
}

func (c *Controller) BeginDrain(id LeaseID) error {
	if c == nil {
		return ErrControllerConfig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrControllerClosed
	}
	record, exists := c.leases[id]
	if !exists {
		return ErrLeaseNotFound
	}
	if record.state != LeaseActive {
		return ErrLeaseState
	}
	if c.countStateLocked(LeaseDraining) >= c.maxDraining {
		return ErrLeaseCapacity
	}
	if id == c.primaryID && c.countStateLocked(LeaseActive) <= 1 {
		return ErrNoActiveLease
	}
	record.state = LeaseDraining
	if id == c.primaryID {
		c.primaryID = c.firstActiveLocked()
	}
	return nil
}

func (c *Controller) CompleteDrain(id LeaseID) error {
	return c.removeAndClose(id, true)
}

func (c *Controller) Fail(id LeaseID) error {
	return c.removeAndClose(id, false)
}

func (c *Controller) State() ControllerState {
	if c == nil {
		return ControllerState{Closed: true}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return ControllerState{
		PrimaryID: c.primaryID,
		Active:    c.countStateLocked(LeaseActive), Draining: c.countStateLocked(LeaseDraining),
		Closed: c.closed,
	}
}

func (c *Controller) Stop() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	resources := make([]io.Closer, 0, len(c.leases))
	for _, id := range c.order {
		if record := c.leases[id]; record != nil {
			resources = append(resources, record.candidate.Resource)
		}
	}
	clear(c.leases)
	c.order = nil
	c.primaryID = 0
	c.mu.Unlock()

	closeErrors := make([]error, 0, len(resources))
	for _, resource := range resources {
		closeErrors = append(closeErrors, resource.Close())
	}
	return errors.Join(closeErrors...)
}

type leaseRecordWithID struct {
	id     LeaseID
	record *leaseRecord
}

func (c *Controller) removeAndClose(id LeaseID, requireDraining bool) error {
	if c == nil {
		return ErrControllerConfig
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrControllerClosed
	}
	record, exists := c.leases[id]
	if !exists {
		c.mu.Unlock()
		return ErrLeaseNotFound
	}
	if requireDraining && record.state != LeaseDraining {
		c.mu.Unlock()
		return ErrLeaseState
	}
	delete(c.leases, id)
	c.removeOrderLocked(id)
	if c.primaryID == id {
		c.primaryID = c.firstActiveLocked()
	}
	resource := record.candidate.Resource
	c.mu.Unlock()
	return resource.Close()
}

func (c *Controller) validateCandidate(candidate LeaseCandidate) error {
	if candidate.Key == (protocol.ContinuityID{}) || nilCloser(candidate.Resource) ||
		!validCarrier(candidate.Carrier) ||
		!hmac.Equal(candidate.Principal[:], c.principal[:]) ||
		candidate.ConstellationID != c.constellationID {
		return ErrLeaseBinding
	}
	return nil
}

func (c *Controller) addLocked(candidate LeaseCandidate) LeaseID {
	id := c.nextID
	if id == 0 {
		id = 1
	}
	c.nextID = id + 1
	c.leases[id] = &leaseRecord{candidate: candidate, state: LeaseActive}
	c.order = append(c.order, id)
	return id
}

func (c *Controller) countStateLocked(state LeaseState) int {
	count := 0
	for _, record := range c.leases {
		if record.state == state {
			count++
		}
	}
	return count
}

func (c *Controller) firstActiveLocked() LeaseID {
	for _, id := range c.order {
		if record := c.leases[id]; record != nil && record.state == LeaseActive {
			return id
		}
	}
	return 0
}

func (c *Controller) removeOrderLocked(id LeaseID) {
	for index, current := range c.order {
		if current == id {
			c.order = append(c.order[:index], c.order[index+1:]...)
			return
		}
	}
}

func snapshotLease(id LeaseID, record *leaseRecord) LeaseSnapshot {
	return LeaseSnapshot{
		ID: id, Key: record.candidate.Key, Carrier: record.candidate.Carrier,
		State: record.state, Load: record.load,
	}
}

func validCarrier(kind protocol.CarrierKind) bool {
	return kind == protocol.CarrierHTTPS || kind == protocol.CarrierHTTP3 || kind == protocol.CarrierWebRTC
}

func nilCloser(resource io.Closer) bool {
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
