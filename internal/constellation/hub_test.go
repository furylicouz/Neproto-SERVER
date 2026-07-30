package constellation

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
)

func TestHubCreatesAttachesRotatesAndReleasesLeases(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hub := newTestHub(t, &now, 2, 3, ticketBytes(1, 2, 3, 4))
	principal := controllerPrincipal(1)
	constellationID := controllerID(17)
	firstResource := &controllerCloser{}
	first, err := hub.Create(CreateRequest{
		Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(33), LeaseKey: controllerID(49),
		Carrier: protocol.CarrierHTTPS, Resource: firstResource,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if first.Ticket == (continuity.LeaseTicket{}) || !first.Primary {
		t.Fatalf("first attachment=%+v", first)
	}
	secondResource := &controllerCloser{}
	second, err := hub.Attach(AttachRequest{
		Ticket: first.Ticket, Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(34), LeaseKey: controllerID(50),
		Carrier: protocol.CarrierHTTP3, Resource: secondResource,
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if second.Ticket == first.Ticket || second.Primary {
		t.Fatalf("second attachment=%+v first=%+v", second, first)
	}
	if state, ok := hub.State(constellationID); !ok || state.Active != 2 || state.PrimaryID != first.LeaseID {
		t.Fatalf("hub state=%+v ok=%v", state, ok)
	}
	if _, err := hub.Attach(AttachRequest{
		Ticket: first.Ticket, Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(35), LeaseKey: controllerID(51),
		Carrier: protocol.CarrierWebRTC, Resource: &controllerCloser{},
	}); !errors.Is(err, continuity.ErrLeaseTicketInvalid) {
		t.Fatalf("old ticket replay error=%v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if firstResource.closed != 1 {
		t.Fatalf("first resource closes=%d", firstResource.closed)
	}
	if state, ok := hub.State(constellationID); !ok || state.Active != 1 || state.PrimaryID != second.LeaseID {
		t.Fatalf("state after primary release=%+v ok=%v", state, ok)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if secondResource.closed != 1 {
		t.Fatalf("second resource closes=%d", secondResource.closed)
	}
}

func TestHubRollsBackUndeliveredControlAdmissions(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hub := newTestHub(t, &now, 2, 3, ticketBytes(1, 2, 3, 4, 5))
	principal := controllerPrincipal(1)
	undeliveredID := controllerID(17)
	undelivered, err := hub.createPending(CreateRequest{
		Principal: principal, ConstellationID: undeliveredID,
		Transcript: controllerTranscript(33), LeaseKey: controllerID(49),
		Carrier: protocol.CarrierHTTPS, Resource: &controllerCloser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := undelivered.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := hub.State(undeliveredID); ok {
		t.Fatal("undelivered create remained in hub")
	}

	constellationID := controllerID(18)
	first, err := hub.Create(CreateRequest{
		Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(34), LeaseKey: controllerID(50),
		Carrier: protocol.CarrierHTTPS, Resource: &controllerCloser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	undeliveredAttach, err := hub.attachPending(AttachRequest{
		Ticket: first.Ticket, Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(35), LeaseKey: controllerID(51),
		Carrier: protocol.CarrierHTTP3, Resource: &controllerCloser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := undeliveredAttach.Close(); err != nil {
		t.Fatalf("rollback attach: %v", err)
	}
	retry, err := hub.Attach(AttachRequest{
		Ticket: first.Ticket, Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(36), LeaseKey: controllerID(52),
		Carrier: protocol.CarrierWebRTC, Resource: &controllerCloser{},
	})
	if err != nil {
		t.Fatalf("retry old ticket after undelivered attach: %v", err)
	}
	if retry.Ticket == first.Ticket {
		t.Fatal("successful retry did not rotate ticket")
	}
}

func TestHubRejectsCrossPrincipalAttachWithoutConsumingTicket(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hub := newTestHub(t, &now, 2, 3, ticketBytes(1, 2, 3))
	principal := controllerPrincipal(1)
	constellationID := controllerID(17)
	first, err := hub.Create(CreateRequest{
		Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(33), LeaseKey: controllerID(49),
		Carrier: protocol.CarrierHTTPS, Resource: &controllerCloser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	rejected := &controllerCloser{}
	if _, err := hub.Attach(AttachRequest{
		Ticket: first.Ticket, Principal: controllerPrincipal(2), ConstellationID: constellationID,
		Transcript: controllerTranscript(34), LeaseKey: controllerID(50),
		Carrier: protocol.CarrierHTTP3, Resource: rejected,
	}); !errors.Is(err, ErrHubBinding) {
		t.Fatalf("cross-principal error=%v", err)
	}
	if rejected.closed != 0 {
		t.Fatal("hub took ownership of rejected resource")
	}
	accepted, err := hub.Attach(AttachRequest{
		Ticket: first.Ticket, Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(34), LeaseKey: controllerID(50),
		Carrier: protocol.CarrierHTTP3, Resource: &controllerCloser{},
	})
	if err != nil || accepted.Ticket == (continuity.LeaseTicket{}) {
		t.Fatalf("legitimate attach=%+v err=%v", accepted, err)
	}
}

func TestHubEnforcesLeaseAndConstellationCapacity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hub := newTestHub(t, &now, 1, 1, ticketBytes(1, 2, 3))
	principal := controllerPrincipal(1)
	constellationID := controllerID(17)
	first, err := hub.Create(CreateRequest{
		Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(33), LeaseKey: controllerID(49),
		Carrier: protocol.CarrierHTTPS, Resource: &controllerCloser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Create(CreateRequest{
		Principal: principal, ConstellationID: controllerID(18),
		Transcript: controllerTranscript(34), LeaseKey: controllerID(50),
		Carrier: protocol.CarrierHTTPS, Resource: &controllerCloser{},
	}); !errors.Is(err, ErrHubCapacity) {
		t.Fatalf("constellation capacity error=%v", err)
	}
	if _, err := hub.Attach(AttachRequest{
		Ticket: first.Ticket, Principal: principal, ConstellationID: constellationID,
		Transcript: controllerTranscript(34), LeaseKey: controllerID(50),
		Carrier: protocol.CarrierHTTP3, Resource: &controllerCloser{},
	}); !errors.Is(err, ErrLeaseCapacity) {
		t.Fatalf("lease capacity error=%v", err)
	}
}

func TestHubReclaimsInactiveConstellationAfterTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hub := newTestHub(t, &now, 1, 2, ticketBytes(1, 2, 3))
	principal := controllerPrincipal(1)
	attachment, err := hub.Create(CreateRequest{
		Principal: principal, ConstellationID: controllerID(17),
		Transcript: controllerTranscript(33), LeaseKey: controllerID(49),
		Carrier: protocol.CarrierHTTPS, Resource: &controllerCloser{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := attachment.Close(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(MinConstellationTTL + time.Nanosecond)
	if _, err := hub.Create(CreateRequest{
		Principal: principal, ConstellationID: controllerID(18),
		Transcript: controllerTranscript(34), LeaseKey: controllerID(50),
		Carrier: protocol.CarrierHTTPS, Resource: &controllerCloser{},
	}); err != nil {
		t.Fatalf("create after stale cleanup: %v", err)
	}
	if _, ok := hub.State(controllerID(17)); ok {
		t.Fatal("stale constellation remained registered")
	}
}

func TestHubCloseClosesOwnedLeasesAndRejectsUse(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hub := newTestHub(t, &now, 2, 2, ticketBytes(1, 2))
	resource := &controllerCloser{}
	_, err := hub.Create(CreateRequest{
		Principal: controllerPrincipal(1), ConstellationID: controllerID(17),
		Transcript: controllerTranscript(33), LeaseKey: controllerID(49),
		Carrier: protocol.CarrierHTTPS, Resource: resource,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	if resource.closed != 1 {
		t.Fatalf("resource closes=%d", resource.closed)
	}
	if _, err := hub.Create(CreateRequest{
		Principal: controllerPrincipal(1), ConstellationID: controllerID(18),
		Transcript: controllerTranscript(34), LeaseKey: controllerID(50),
		Carrier: protocol.CarrierHTTPS, Resource: &controllerCloser{},
	}); !errors.Is(err, ErrHubClosed) {
		t.Fatalf("create after close error=%v", err)
	}
}

func newTestHub(
	t *testing.T,
	now *time.Time,
	maxConstellations int,
	maxLeases int,
	random []byte,
) *Hub {
	t.Helper()
	hub, err := NewHub(HubConfig{
		MaxConstellations: maxConstellations, MaxLeases: maxLeases,
		MaxDraining: maxLeases, InactiveTTL: MinConstellationTTL,
		Now: func() time.Time { return *now },
		TicketConfig: continuity.TicketRegistryConfig{
			MaxTickets: maxConstellations * maxLeases,
			TTL:        continuity.MinLeaseTicketTTL,
			Now:        func() time.Time { return *now }, Random: bytes.NewReader(random),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return hub
}

func ticketBytes(seeds ...byte) []byte {
	var raw []byte
	for _, seed := range seeds {
		raw = append(raw, bytes.Repeat([]byte{seed}, continuity.LeaseTicketSize)...)
	}
	return raw
}

func controllerTranscript(seed byte) continuity.TranscriptID {
	var id continuity.TranscriptID
	for index := range id {
		id[index] = seed + byte(index)
	}
	return id
}
