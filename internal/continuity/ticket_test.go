package continuity

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

func TestTicketRegistryIssuesConsumesAndRejectsReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	registry, err := NewTicketRegistry(TicketRegistryConfig{
		MaxTickets: 4, TTL: time.Minute,
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{7}, 128)),
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	transcript := ticketTestTranscript(33)
	ticket, err := registry.Issue(principal, constellationID, transcript)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if ticket == (LeaseTicket{}) || registry.Count() != 1 {
		t.Fatalf("ticket=%x count=%d", ticket, registry.Count())
	}
	grant, err := registry.Consume(ticket, principal, constellationID)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if grant.ConstellationID != constellationID || grant.IssuerTranscript != transcript || !grant.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("unexpected grant: %+v", grant)
	}
	if registry.Count() != 0 {
		t.Fatalf("ticket not consumed: count=%d", registry.Count())
	}
	if _, err := registry.Consume(ticket, principal, constellationID); !errors.Is(err, ErrLeaseTicketInvalid) {
		t.Fatalf("replay error=%v", err)
	}
}

func TestTicketRegistryWrongBindingDoesNotConsumeTicket(t *testing.T) {
	registry := newTicketTestRegistry(t, 4, bytes.Repeat([]byte{9}, 128))
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	ticket, err := registry.Issue(principal, constellationID, ticketTestTranscript(33))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Consume(ticket, ticketTestPrincipal(2), constellationID); !errors.Is(err, ErrLeaseTicketInvalid) {
		t.Fatalf("wrong principal error=%v", err)
	}
	if _, err := registry.Consume(ticket, principal, ticketTestContinuityID(18)); !errors.Is(err, ErrLeaseTicketInvalid) {
		t.Fatalf("wrong constellation error=%v", err)
	}
	if registry.Count() != 1 {
		t.Fatalf("invalid attempts consumed ticket: count=%d", registry.Count())
	}
	if _, err := registry.Consume(ticket, principal, constellationID); err != nil {
		t.Fatalf("legitimate consume after invalid attempts: %v", err)
	}
}

func TestTicketRegistryRotatesAtomicallyAfterAttach(t *testing.T) {
	firstBytes := bytes.Repeat([]byte{3}, LeaseTicketSize)
	secondBytes := bytes.Repeat([]byte{4}, LeaseTicketSize)
	registry := newTicketTestRegistry(t, 2, append(firstBytes, secondBytes...))
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	first, err := registry.Issue(principal, constellationID, ticketTestTranscript(33))
	if err != nil {
		t.Fatal(err)
	}
	second, grant, err := registry.Rotate(
		first, principal, constellationID, ticketTestTranscript(34),
	)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if first == second || grant.IssuerTranscript != ticketTestTranscript(33) || registry.Count() != 1 {
		t.Fatalf("first=%x second=%x grant=%+v count=%d", first, second, grant, registry.Count())
	}
	if _, err := registry.Consume(first, principal, constellationID); !errors.Is(err, ErrLeaseTicketInvalid) {
		t.Fatalf("rotated ticket replay error=%v", err)
	}
	rotatedGrant, err := registry.Consume(second, principal, constellationID)
	if err != nil || rotatedGrant.IssuerTranscript != ticketTestTranscript(34) {
		t.Fatalf("consume rotated ticket grant=%+v err=%v", rotatedGrant, err)
	}
}

func TestTicketRegistryFailedRotationPreservesCurrentTicket(t *testing.T) {
	registry := newTicketTestRegistry(t, 2, bytes.Repeat([]byte{6}, LeaseTicketSize))
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	ticket, err := registry.Issue(principal, constellationID, ticketTestTranscript(33))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Rotate(ticket, principal, constellationID, ticketTestTranscript(34)); !errors.Is(err, ErrLeaseTicketEntropy) {
		t.Fatalf("rotation entropy error=%v", err)
	}
	if registry.Count() != 1 {
		t.Fatalf("failed rotation changed count=%d", registry.Count())
	}
	if _, err := registry.Consume(ticket, principal, constellationID); err != nil {
		t.Fatalf("failed rotation consumed current ticket: %v", err)
	}
}

func TestTicketRegistryRollsBackUndeliveredRotation(t *testing.T) {
	firstBytes := bytes.Repeat([]byte{10}, LeaseTicketSize)
	secondBytes := bytes.Repeat([]byte{11}, LeaseTicketSize)
	registry := newTicketTestRegistry(t, 2, append(firstBytes, secondBytes...))
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	first, err := registry.Issue(principal, constellationID, ticketTestTranscript(33))
	if err != nil {
		t.Fatal(err)
	}
	second, grant, err := registry.Rotate(first, principal, constellationID, ticketTestTranscript(34))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.RollbackRotate(first, second, principal, constellationID, grant); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := registry.Consume(second, principal, constellationID); !errors.Is(err, ErrLeaseTicketInvalid) {
		t.Fatalf("undelivered successor remained valid: %v", err)
	}
	restored, err := registry.Consume(first, principal, constellationID)
	if err != nil || restored.IssuerTranscript != ticketTestTranscript(33) {
		t.Fatalf("restored grant=%+v err=%v", restored, err)
	}
}

func TestTicketRegistryRevokesOnlyMatchingUndeliveredIssue(t *testing.T) {
	registry := newTicketTestRegistry(t, 2, bytes.Repeat([]byte{12}, 128))
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	ticket, err := registry.Issue(principal, constellationID, ticketTestTranscript(33))
	if err != nil {
		t.Fatal(err)
	}
	if registry.Revoke(ticket, ticketTestPrincipal(2), constellationID) {
		t.Fatal("wrong principal revoked ticket")
	}
	if !registry.Revoke(ticket, principal, constellationID) || registry.Count() != 0 {
		t.Fatalf("matching revoke failed, count=%d", registry.Count())
	}
}

func TestTicketRegistryExpiresAndReclaimsCapacity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	random := append(bytes.Repeat([]byte{1}, LeaseTicketSize), bytes.Repeat([]byte{2}, LeaseTicketSize)...)
	registry, err := NewTicketRegistry(TicketRegistryConfig{
		MaxTickets: 1, TTL: MinLeaseTicketTTL,
		Now: func() time.Time { return now }, Random: bytes.NewReader(random),
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	first, err := registry.Issue(principal, constellationID, ticketTestTranscript(33))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Issue(principal, constellationID, ticketTestTranscript(34)); !errors.Is(err, ErrLeaseTicketCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
	now = now.Add(MinLeaseTicketTTL + time.Nanosecond)
	second, err := registry.Issue(principal, constellationID, ticketTestTranscript(34))
	if err != nil {
		t.Fatalf("issue after expiry: %v", err)
	}
	if first == second || registry.Count() != 1 {
		t.Fatalf("first=%x second=%x count=%d", first, second, registry.Count())
	}
	if _, err := registry.Consume(first, principal, constellationID); !errors.Is(err, ErrLeaseTicketInvalid) {
		t.Fatalf("expired ticket error=%v", err)
	}
}

func TestTicketRegistryValidatesConfigurationAndBindings(t *testing.T) {
	valid := TicketRegistryConfig{MaxTickets: 1, TTL: MinLeaseTicketTTL, Random: bytes.NewReader(make([]byte, 64))}
	tests := []TicketRegistryConfig{
		{MaxTickets: 0, TTL: MinLeaseTicketTTL},
		{MaxTickets: MaxLeaseTickets + 1, TTL: MinLeaseTicketTTL},
		{MaxTickets: 1, TTL: MinLeaseTicketTTL - time.Nanosecond},
		{MaxTickets: 1, TTL: MaxLeaseTicketTTL + time.Nanosecond},
	}
	for _, config := range tests {
		if _, err := NewTicketRegistry(config); !errors.Is(err, ErrLeaseTicketConfig) {
			t.Fatalf("config=%+v error=%v", config, err)
		}
	}
	registry, err := NewTicketRegistry(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Issue(PrincipalID{}, ticketTestContinuityID(1), ticketTestTranscript(1)); !errors.Is(err, ErrLeaseTicketBinding) {
		t.Fatalf("zero principal error=%v", err)
	}
	if _, err := registry.Issue(ticketTestPrincipal(1), protocol.ContinuityID{}, ticketTestTranscript(1)); !errors.Is(err, ErrLeaseTicketBinding) {
		t.Fatalf("zero constellation error=%v", err)
	}
	if _, err := registry.Issue(ticketTestPrincipal(1), ticketTestContinuityID(1), TranscriptID{}); !errors.Is(err, ErrLeaseTicketBinding) {
		t.Fatalf("zero transcript error=%v", err)
	}
}

func TestTicketRegistryCloseClearsAndRejectsUse(t *testing.T) {
	registry := newTicketTestRegistry(t, 2, bytes.Repeat([]byte{5}, 128))
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	ticket, err := registry.Issue(principal, constellationID, ticketTestTranscript(33))
	if err != nil {
		t.Fatal(err)
	}
	registry.Close()
	registry.Close()
	if registry.Count() != 0 {
		t.Fatalf("closed registry count=%d", registry.Count())
	}
	if _, err := registry.Issue(principal, constellationID, ticketTestTranscript(34)); !errors.Is(err, ErrLeaseTicketClosed) {
		t.Fatalf("issue after close error=%v", err)
	}
	if _, err := registry.Consume(ticket, principal, constellationID); !errors.Is(err, ErrLeaseTicketClosed) {
		t.Fatalf("consume after close error=%v", err)
	}
}

func newTicketTestRegistry(t *testing.T, maxTickets int, random []byte) *TicketRegistry {
	t.Helper()
	registry, err := NewTicketRegistry(TicketRegistryConfig{
		MaxTickets: maxTickets, TTL: time.Minute,
		Now:    func() time.Time { return time.Unix(1_700_000_000, 0) },
		Random: bytes.NewReader(random),
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func ticketTestPrincipal(seed byte) PrincipalID {
	var id PrincipalID
	for index := range id {
		id[index] = seed + byte(index)
	}
	return id
}

func ticketTestTranscript(seed byte) TranscriptID {
	var id TranscriptID
	for index := range id {
		id[index] = seed + byte(index)
	}
	return id
}

func ticketTestContinuityID(seed byte) protocol.ContinuityID {
	var id protocol.ContinuityID
	for index := range id {
		id[index] = seed + byte(index)
	}
	return id
}
