package continuity

import (
	"crypto/hmac"
	cryptorand "crypto/rand"
	"io"
	"sync"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

const (
	LeaseTicketSize   = 32
	MaxLeaseTickets   = 4096
	MinLeaseTicketTTL = 5 * time.Second
	MaxLeaseTicketTTL = 10 * time.Minute
	maxTicketAttempts = 8
)

type PrincipalID [32]byte
type TranscriptID [32]byte
type LeaseTicket [LeaseTicketSize]byte

type TicketRegistryConfig struct {
	MaxTickets int
	TTL        time.Duration
	Now        func() time.Time
	Random     io.Reader
}

type TicketGrant struct {
	ConstellationID  protocol.ContinuityID
	IssuerTranscript TranscriptID
	ExpiresAt        time.Time
}

type ticketRecord struct {
	principal        PrincipalID
	constellationID  protocol.ContinuityID
	issuerTranscript TranscriptID
	expiresAt        time.Time
}

type TicketRegistry struct {
	mu sync.Mutex

	maxTickets int
	ttl        time.Duration
	now        func() time.Time
	random     io.Reader
	tickets    map[LeaseTicket]ticketRecord
	closed     bool
}

func NewTicketRegistry(config TicketRegistryConfig) (*TicketRegistry, error) {
	if config.MaxTickets <= 0 || config.MaxTickets > MaxLeaseTickets ||
		config.TTL < MinLeaseTicketTTL || config.TTL > MaxLeaseTicketTTL {
		return nil, ErrLeaseTicketConfig
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	random := config.Random
	if random == nil {
		random = cryptorand.Reader
	}
	return &TicketRegistry{
		maxTickets: config.MaxTickets,
		ttl:        config.TTL,
		now:        now,
		random:     random,
		tickets:    make(map[LeaseTicket]ticketRecord, config.MaxTickets),
	}, nil
}

func (r *TicketRegistry) Issue(
	principal PrincipalID,
	constellationID protocol.ContinuityID,
	issuerTranscript TranscriptID,
) (LeaseTicket, error) {
	if r == nil {
		return LeaseTicket{}, ErrLeaseTicketConfig
	}
	if principal == (PrincipalID{}) || constellationID == (protocol.ContinuityID{}) ||
		issuerTranscript == (TranscriptID{}) {
		return LeaseTicket{}, ErrLeaseTicketBinding
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return LeaseTicket{}, ErrLeaseTicketClosed
	}
	now := r.now()
	r.cleanupLocked(now)
	if len(r.tickets) >= r.maxTickets {
		return LeaseTicket{}, ErrLeaseTicketCapacity
	}

	ticket, err := r.newTicketLocked()
	if err != nil {
		return LeaseTicket{}, err
	}
	r.tickets[ticket] = ticketRecord{
		principal: principal, constellationID: constellationID,
		issuerTranscript: issuerTranscript, expiresAt: now.Add(r.ttl),
	}
	return ticket, nil
}

func (r *TicketRegistry) Consume(
	ticket LeaseTicket,
	principal PrincipalID,
	constellationID protocol.ContinuityID,
) (TicketGrant, error) {
	if r == nil {
		return TicketGrant{}, ErrLeaseTicketConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return TicketGrant{}, ErrLeaseTicketClosed
	}
	r.cleanupLocked(r.now())
	record, exists := r.tickets[ticket]
	if !exists || ticket == (LeaseTicket{}) || principal == (PrincipalID{}) ||
		constellationID == (protocol.ContinuityID{}) ||
		!hmac.Equal(record.principal[:], principal[:]) ||
		record.constellationID != constellationID {
		return TicketGrant{}, ErrLeaseTicketInvalid
	}
	delete(r.tickets, ticket)
	return grantFromRecord(record), nil
}

// Rotate atomically consumes the attach ticket and issues its successor. If
// successor generation fails, the current ticket remains valid.
func (r *TicketRegistry) Rotate(
	ticket LeaseTicket,
	principal PrincipalID,
	constellationID protocol.ContinuityID,
	newIssuerTranscript TranscriptID,
) (LeaseTicket, TicketGrant, error) {
	if r == nil {
		return LeaseTicket{}, TicketGrant{}, ErrLeaseTicketConfig
	}
	if newIssuerTranscript == (TranscriptID{}) {
		return LeaseTicket{}, TicketGrant{}, ErrLeaseTicketBinding
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return LeaseTicket{}, TicketGrant{}, ErrLeaseTicketClosed
	}
	now := r.now()
	r.cleanupLocked(now)
	record, exists := r.tickets[ticket]
	if !exists || ticket == (LeaseTicket{}) || principal == (PrincipalID{}) ||
		constellationID == (protocol.ContinuityID{}) ||
		!hmac.Equal(record.principal[:], principal[:]) ||
		record.constellationID != constellationID {
		return LeaseTicket{}, TicketGrant{}, ErrLeaseTicketInvalid
	}
	next, err := r.newTicketLocked()
	if err != nil {
		return LeaseTicket{}, TicketGrant{}, err
	}
	delete(r.tickets, ticket)
	r.tickets[next] = ticketRecord{
		principal: principal, constellationID: constellationID,
		issuerTranscript: newIssuerTranscript, expiresAt: now.Add(r.ttl),
	}
	return next, grantFromRecord(record), nil
}

// RollbackRotate restores a consumed ticket when the successor could not be
// delivered to the authenticated peer. It succeeds only while the exact
// successor is still unused and bound to the same principal/constellation.
func (r *TicketRegistry) RollbackRotate(
	current LeaseTicket,
	next LeaseTicket,
	principal PrincipalID,
	constellationID protocol.ContinuityID,
	currentGrant TicketGrant,
) error {
	if r == nil {
		return ErrLeaseTicketConfig
	}
	if current == (LeaseTicket{}) || next == (LeaseTicket{}) || principal == (PrincipalID{}) ||
		constellationID == (protocol.ContinuityID{}) || currentGrant.IssuerTranscript == (TranscriptID{}) ||
		currentGrant.ConstellationID != constellationID {
		return ErrLeaseTicketBinding
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrLeaseTicketClosed
	}
	now := r.now()
	r.cleanupLocked(now)
	nextRecord, exists := r.tickets[next]
	_, currentExists := r.tickets[current]
	if !exists || currentExists || !hmac.Equal(nextRecord.principal[:], principal[:]) ||
		nextRecord.constellationID != constellationID || !now.Before(currentGrant.ExpiresAt) {
		return ErrLeaseTicketInvalid
	}
	delete(r.tickets, next)
	r.tickets[current] = ticketRecord{
		principal: principal, constellationID: constellationID,
		issuerTranscript: currentGrant.IssuerTranscript, expiresAt: currentGrant.ExpiresAt,
	}
	return nil
}

// Revoke removes an undelivered freshly issued ticket without exposing whether
// another principal owns a similarly shaped token.
func (r *TicketRegistry) Revoke(
	ticket LeaseTicket,
	principal PrincipalID,
	constellationID protocol.ContinuityID,
) bool {
	if r == nil || ticket == (LeaseTicket{}) || principal == (PrincipalID{}) ||
		constellationID == (protocol.ContinuityID{}) {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.cleanupLocked(r.now())
	record, exists := r.tickets[ticket]
	if !exists || !hmac.Equal(record.principal[:], principal[:]) || record.constellationID != constellationID {
		return false
	}
	delete(r.tickets, ticket)
	return true
}

func (r *TicketRegistry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0
	}
	r.cleanupLocked(r.now())
	return len(r.tickets)
}

func (r *TicketRegistry) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	clear(r.tickets)
	r.tickets = nil
	r.closed = true
}

func (r *TicketRegistry) cleanupLocked(now time.Time) {
	for ticket, record := range r.tickets {
		if !now.Before(record.expiresAt) {
			delete(r.tickets, ticket)
		}
	}
}

func (r *TicketRegistry) newTicketLocked() (LeaseTicket, error) {
	for range maxTicketAttempts {
		var ticket LeaseTicket
		if _, err := io.ReadFull(r.random, ticket[:]); err != nil {
			return LeaseTicket{}, ErrLeaseTicketEntropy
		}
		if ticket == (LeaseTicket{}) {
			continue
		}
		if _, collision := r.tickets[ticket]; collision {
			continue
		}
		return ticket, nil
	}
	return LeaseTicket{}, ErrLeaseTicketEntropy
}

func grantFromRecord(record ticketRecord) TicketGrant {
	return TicketGrant{
		ConstellationID:  record.constellationID,
		IssuerTranscript: record.issuerTranscript,
		ExpiresAt:        record.expiresAt,
	}
}
