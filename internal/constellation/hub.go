package constellation

import (
	"crypto/hmac"
	"errors"
	"io"
	"sync"
	"time"

	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
)

const (
	MinConstellationTTL = 5 * time.Second
	MaxConstellationTTL = time.Hour
	MaxConstellations   = 4096
)

var (
	ErrHubConfig    = errors.New("invalid constellation hub configuration")
	ErrHubClosed    = errors.New("constellation hub closed")
	ErrHubCapacity  = errors.New("constellation hub capacity exceeded")
	ErrHubDuplicate = errors.New("constellation already exists")
	ErrHubBinding   = errors.New("constellation authentication binding mismatch")
)

type HubConfig struct {
	MaxConstellations int
	MaxLeases         int
	MaxDraining       int
	InactiveTTL       time.Duration
	Now               func() time.Time
	TicketConfig      continuity.TicketRegistryConfig
}

type CreateRequest struct {
	Principal       continuity.PrincipalID
	ConstellationID protocol.ContinuityID
	Transcript      continuity.TranscriptID
	LeaseKey        protocol.ContinuityID
	Carrier         protocol.CarrierKind
	Resource        io.Closer
}

type AttachRequest struct {
	Ticket          continuity.LeaseTicket
	Principal       continuity.PrincipalID
	ConstellationID protocol.ContinuityID
	Transcript      continuity.TranscriptID
	LeaseKey        protocol.ContinuityID
	Carrier         protocol.CarrierKind
	Resource        io.Closer
}

type Attachment struct {
	ConstellationID      protocol.ContinuityID
	LeaseID              LeaseID
	LeaseKey             protocol.ContinuityID
	Ticket               continuity.LeaseTicket
	Primary              bool
	ControlNextMessageID uint64

	hub         *Hub
	admissionMu sync.Mutex
	admission   *ticketAdmission
	closeOnce   sync.Once
	closeErr    error
}

type ticketAdmission struct {
	kind      uint8
	principal continuity.PrincipalID
	current   continuity.LeaseTicket
	next      continuity.LeaseTicket
	grant     continuity.TicketGrant
}

const (
	ticketAdmissionIssue uint8 = iota + 1
	ticketAdmissionRotate
)

type hubRecord struct {
	principal  continuity.PrincipalID
	controller *Controller
	lastSeen   time.Time
}

type Hub struct {
	mu sync.Mutex

	maxConstellations int
	maxLeases         int
	maxDraining       int
	inactiveTTL       time.Duration
	now               func() time.Time
	tickets           *continuity.TicketRegistry
	records           map[protocol.ContinuityID]*hubRecord
	closed            bool
}

func NewHub(config HubConfig) (*Hub, error) {
	if config.MaxConstellations <= 0 || config.MaxConstellations > MaxConstellations ||
		config.MaxLeases <= 0 || config.MaxLeases > 8 ||
		config.MaxDraining <= 0 || config.MaxDraining > 8 ||
		config.InactiveTTL < MinConstellationTTL || config.InactiveTTL > MaxConstellationTTL ||
		config.InactiveTTL < config.TicketConfig.TTL {
		return nil, ErrHubConfig
	}
	tickets, err := continuity.NewTicketRegistry(config.TicketConfig)
	if err != nil {
		return nil, errors.Join(ErrHubConfig, err)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Hub{
		maxConstellations: config.MaxConstellations,
		maxLeases:         config.MaxLeases, maxDraining: config.MaxDraining,
		inactiveTTL: config.InactiveTTL, now: now, tickets: tickets,
		records: make(map[protocol.ContinuityID]*hubRecord, config.MaxConstellations),
	}, nil
}

func (h *Hub) Create(request CreateRequest) (*Attachment, error) {
	return h.create(request, false)
}

func (h *Hub) createPending(request CreateRequest) (*Attachment, error) {
	return h.create(request, true)
}

func (h *Hub) create(request CreateRequest, pending bool) (*Attachment, error) {
	if h == nil {
		return nil, ErrHubConfig
	}
	if err := validateHubLease(
		request.Principal, request.ConstellationID, request.Transcript,
		request.LeaseKey, request.Carrier, request.Resource,
	); err != nil {
		return nil, err
	}
	h.cleanup()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrHubClosed
	}
	if _, exists := h.records[request.ConstellationID]; exists {
		return nil, ErrHubDuplicate
	}
	if len(h.records) >= h.maxConstellations {
		return nil, ErrHubCapacity
	}
	controller, err := NewController(ControllerConfig{
		Principal: request.Principal, ConstellationID: request.ConstellationID,
		MaxActive: h.maxLeases, MaxDraining: h.maxDraining,
	}, LeaseCandidate{
		Key: request.LeaseKey, Principal: request.Principal,
		ConstellationID: request.ConstellationID, Carrier: request.Carrier,
		Resource: request.Resource,
	})
	if err != nil {
		return nil, err
	}
	ticket, err := h.tickets.Issue(request.Principal, request.ConstellationID, request.Transcript)
	if err != nil {
		_ = controller.Stop()
		return nil, err
	}
	state := controller.State()
	h.records[request.ConstellationID] = &hubRecord{
		principal: request.Principal, controller: controller, lastSeen: h.now(),
	}
	attachment := &Attachment{
		ConstellationID: request.ConstellationID, LeaseID: state.PrimaryID,
		LeaseKey: request.LeaseKey, Ticket: ticket, Primary: true, hub: h,
	}
	if pending {
		attachment.admission = &ticketAdmission{
			kind: ticketAdmissionIssue, principal: request.Principal, next: ticket,
		}
	}
	return attachment, nil
}

func (h *Hub) Attach(request AttachRequest) (*Attachment, error) {
	return h.attach(request, false)
}

func (h *Hub) attachPending(request AttachRequest) (*Attachment, error) {
	return h.attach(request, true)
}

func (h *Hub) attach(request AttachRequest, pending bool) (*Attachment, error) {
	if h == nil {
		return nil, ErrHubConfig
	}
	if err := validateHubLease(
		request.Principal, request.ConstellationID, request.Transcript,
		request.LeaseKey, request.Carrier, request.Resource,
	); err != nil {
		return nil, err
	}
	h.cleanup()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrHubClosed
	}
	record, exists := h.records[request.ConstellationID]
	if !exists || !hmac.Equal(record.principal[:], request.Principal[:]) {
		return nil, ErrHubBinding
	}
	leaseID, err := record.controller.Add(LeaseCandidate{
		Key: request.LeaseKey, Principal: request.Principal,
		ConstellationID: request.ConstellationID, Carrier: request.Carrier,
		Resource: request.Resource,
	})
	if err != nil {
		return nil, err
	}
	nextTicket, currentGrant, err := h.tickets.Rotate(
		request.Ticket, request.Principal, request.ConstellationID, request.Transcript,
	)
	if err != nil {
		_ = record.controller.Fail(leaseID)
		return nil, err
	}
	record.lastSeen = h.now()
	attachment := &Attachment{
		ConstellationID: request.ConstellationID, LeaseID: leaseID,
		LeaseKey: request.LeaseKey, Ticket: nextTicket, hub: h,
	}
	if pending {
		attachment.admission = &ticketAdmission{
			kind: ticketAdmissionRotate, principal: request.Principal,
			current: request.Ticket, next: nextTicket, grant: currentGrant,
		}
	}
	return attachment, nil
}

func (h *Hub) State(id protocol.ContinuityID) (ControllerState, bool) {
	if h == nil || id == (protocol.ContinuityID{}) {
		return ControllerState{}, false
	}
	h.mu.Lock()
	record, exists := h.records[id]
	closed := h.closed
	h.mu.Unlock()
	if !exists || closed {
		return ControllerState{}, false
	}
	return record.controller.State(), true
}

func (h *Hub) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	controllers := make([]*Controller, 0, len(h.records))
	for _, record := range h.records {
		controllers = append(controllers, record.controller)
	}
	clear(h.records)
	h.mu.Unlock()
	h.tickets.Close()
	closeErrors := make([]error, 0, len(controllers))
	for _, controller := range controllers {
		closeErrors = append(closeErrors, controller.Stop())
	}
	return errors.Join(closeErrors...)
}

func (a *Attachment) Close() error {
	if a == nil || a.hub == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		undeliveredCreate, admissionErr := a.abortAdmission()
		if undeliveredCreate {
			a.closeErr = errors.Join(admissionErr, a.hub.discard(a.ConstellationID, a.LeaseID))
		} else {
			a.closeErr = errors.Join(admissionErr, a.hub.release(a.ConstellationID, a.LeaseID))
		}
	})
	return a.closeErr
}

func (a *Attachment) commitAdmission() {
	if a == nil {
		return
	}
	a.admissionMu.Lock()
	a.admission = nil
	a.admissionMu.Unlock()
}

func (a *Attachment) abortAdmission() (bool, error) {
	if a == nil || a.hub == nil {
		return false, nil
	}
	a.admissionMu.Lock()
	admission := a.admission
	a.admission = nil
	a.admissionMu.Unlock()
	if admission == nil {
		return false, nil
	}
	switch admission.kind {
	case ticketAdmissionIssue:
		a.hub.tickets.Revoke(admission.next, admission.principal, a.ConstellationID)
		return true, nil
	case ticketAdmissionRotate:
		return false, a.hub.tickets.RollbackRotate(
			admission.current, admission.next, admission.principal,
			a.ConstellationID, admission.grant,
		)
	default:
		return false, ErrHubBinding
	}
}

func (h *Hub) discard(id protocol.ContinuityID, leaseID LeaseID) error {
	h.mu.Lock()
	record, exists := h.records[id]
	if exists {
		state := record.controller.State()
		if state.PrimaryID != leaseID {
			exists = false
		} else {
			delete(h.records, id)
		}
	}
	closed := h.closed
	h.mu.Unlock()
	if !exists {
		if closed {
			return nil
		}
		return ErrHubBinding
	}
	return record.controller.Stop()
}

func (h *Hub) release(id protocol.ContinuityID, leaseID LeaseID) error {
	h.mu.Lock()
	record, exists := h.records[id]
	closed := h.closed
	h.mu.Unlock()
	if !exists {
		if closed {
			return nil
		}
		return ErrHubBinding
	}
	err := record.controller.Fail(leaseID)
	if errors.Is(err, ErrControllerClosed) || errors.Is(err, ErrLeaseNotFound) {
		err = nil
	}
	h.mu.Lock()
	if current := h.records[id]; current == record {
		current.lastSeen = h.now()
	}
	h.mu.Unlock()
	return err
}

func (h *Hub) cleanup() {
	now := h.now()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	stale := make([]*Controller, 0)
	for id, record := range h.records {
		state := record.controller.State()
		if state.Active == 0 && state.Draining == 0 &&
			!now.Before(record.lastSeen) && now.Sub(record.lastSeen) >= h.inactiveTTL {
			delete(h.records, id)
			stale = append(stale, record.controller)
		}
	}
	h.mu.Unlock()
	for _, controller := range stale {
		_ = controller.Stop()
	}
}

func validateHubLease(
	principal continuity.PrincipalID,
	constellationID protocol.ContinuityID,
	transcript continuity.TranscriptID,
	leaseKey protocol.ContinuityID,
	carrier protocol.CarrierKind,
	resource io.Closer,
) error {
	if principal == (continuity.PrincipalID{}) ||
		constellationID == (protocol.ContinuityID{}) ||
		transcript == (continuity.TranscriptID{}) ||
		leaseKey == (protocol.ContinuityID{}) || !validCarrier(carrier) || nilCloser(resource) {
		return ErrHubBinding
	}
	return nil
}
