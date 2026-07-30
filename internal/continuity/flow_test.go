package continuity

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"

	"neproto.local/chameleon/internal/protocol"
)

func TestFlowRegistryKeepsUpstreamAcrossLeaseEpochs(t *testing.T) {
	registry := newFlowTestRegistry(t, 4, 2, flowIDBytes(1, 2))
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	firstLease := ticketTestContinuityID(33)
	upstream := &flowTestResource{}
	requestedFlowID := ticketTestContinuityID(49)

	flow, err := registry.Create(FlowCreateRequest{
		Principal: principal, ConstellationID: constellationID,
		FlowID: requestedFlowID, LeaseKey: firstLease, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	if flow.ID != requestedFlowID || flow.Epoch != 1 || flow.Upstream != upstream {
		t.Fatalf("created flow=%+v", flow)
	}

	secondLease := ticketTestContinuityID(34)
	resumed, err := registry.Attach(FlowAttachRequest{
		Principal: principal, ConstellationID: constellationID, FlowID: flow.ID,
		LeaseKey: secondLease, Epoch: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Epoch != 2 || resumed.LeaseKey != secondLease || resumed.Upstream != upstream {
		t.Fatalf("resumed flow=%+v", resumed)
	}
	idempotent, err := registry.Attach(FlowAttachRequest{
		Principal: principal, ConstellationID: constellationID, FlowID: flow.ID,
		LeaseKey: secondLease, Epoch: 2,
	})
	if err != nil || idempotent.Epoch != resumed.Epoch || idempotent.Upstream != resumed.Upstream {
		t.Fatalf("duplicate attach flow=%+v err=%v", idempotent, err)
	}
	if upstream.closes() != 0 {
		t.Fatalf("stable upstream closed=%d", upstream.closes())
	}
}

func TestFlowRegistryRejectsClientIDCollisionWithoutClosingExistingFlow(t *testing.T) {
	registry := newFlowTestRegistry(t, 2, 2, flowIDBytes(1, 2))
	flowID := ticketTestContinuityID(49)
	firstResource := &flowTestResource{}
	request := FlowCreateRequest{
		Principal: ticketTestPrincipal(1), ConstellationID: ticketTestContinuityID(17),
		FlowID: flowID, LeaseKey: ticketTestContinuityID(33), Upstream: firstResource,
	}
	if _, err := registry.Create(request); err != nil {
		t.Fatal(err)
	}
	secondResource := &flowTestResource{}
	request.Upstream = secondResource
	request.LeaseKey = ticketTestContinuityID(34)
	if _, err := registry.Create(request); !errors.Is(err, ErrFlowConflict) {
		t.Fatalf("collision error=%v", err)
	}
	if firstResource.closes() != 0 || secondResource.closes() != 0 || registry.Count() != 1 {
		t.Fatalf("collision changed ownership first=%d second=%d count=%d", firstResource.closes(), secondResource.closes(), registry.Count())
	}
}

func TestFlowRegistryConflictClosesOnlyAffectedFlow(t *testing.T) {
	registry := newFlowTestRegistry(t, 4, 4, flowIDBytes(1, 2, 3))
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	firstResource := &flowTestResource{}
	first, err := registry.Create(FlowCreateRequest{
		Principal: principal, ConstellationID: constellationID,
		LeaseKey: ticketTestContinuityID(33), Upstream: firstResource,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondResource := &flowTestResource{}
	second, err := registry.Create(FlowCreateRequest{
		Principal: principal, ConstellationID: constellationID,
		LeaseKey: ticketTestContinuityID(34), Upstream: secondResource,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = registry.Attach(FlowAttachRequest{
		Principal: principal, ConstellationID: constellationID, FlowID: first.ID,
		LeaseKey: ticketTestContinuityID(99), Epoch: 1,
	})
	if !errors.Is(err, ErrFlowConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	if firstResource.closes() != 1 || secondResource.closes() != 0 || registry.Count() != 1 {
		t.Fatalf("first closes=%d second closes=%d count=%d", firstResource.closes(), secondResource.closes(), registry.Count())
	}
	if snapshot, ok := registry.State(second.ID); !ok || snapshot.Epoch != 1 {
		t.Fatalf("unrelated state=%+v ok=%v", snapshot, ok)
	}
}

func TestFlowRegistryWrongBindingCannotAbortFlow(t *testing.T) {
	registry := newFlowTestRegistry(t, 2, 2, flowIDBytes(1, 2))
	principal := ticketTestPrincipal(1)
	constellationID := ticketTestContinuityID(17)
	upstream := &flowTestResource{}
	flow, err := registry.Create(FlowCreateRequest{
		Principal: principal, ConstellationID: constellationID,
		LeaseKey: ticketTestContinuityID(33), Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []FlowAttachRequest{
		{Principal: ticketTestPrincipal(2), ConstellationID: constellationID, FlowID: flow.ID, LeaseKey: ticketTestContinuityID(34), Epoch: 2},
		{Principal: principal, ConstellationID: ticketTestContinuityID(18), FlowID: flow.ID, LeaseKey: ticketTestContinuityID(34), Epoch: 2},
	} {
		if _, err := registry.Attach(request); !errors.Is(err, ErrFlowBinding) {
			t.Fatalf("binding error=%v", err)
		}
	}
	if upstream.closes() != 0 || registry.Count() != 1 {
		t.Fatalf("unauthorized attach changed flow: closes=%d count=%d", upstream.closes(), registry.Count())
	}
}

func TestFlowRegistryEnforcesGlobalAndPerPrincipalLimits(t *testing.T) {
	registry := newFlowTestRegistry(t, 3, 1, flowIDBytes(1, 2, 3, 4))
	create := func(principal PrincipalID, seed byte) (Flow, error) {
		return registry.Create(FlowCreateRequest{
			Principal: principal, ConstellationID: ticketTestContinuityID(seed),
			LeaseKey: ticketTestContinuityID(seed + 20), Upstream: &flowTestResource{},
		})
	}
	if _, err := create(ticketTestPrincipal(1), 11); err != nil {
		t.Fatal(err)
	}
	if _, err := create(ticketTestPrincipal(1), 12); !errors.Is(err, ErrFlowCapacity) {
		t.Fatalf("per-principal capacity error=%v", err)
	}
	if _, err := create(ticketTestPrincipal(2), 13); err != nil {
		t.Fatal(err)
	}
	if _, err := create(ticketTestPrincipal(3), 14); err != nil {
		t.Fatal(err)
	}
	if _, err := create(ticketTestPrincipal(4), 15); !errors.Is(err, ErrFlowCapacity) {
		t.Fatalf("global capacity error=%v", err)
	}
}

func TestFlowRegistryRetriesIDCollisionsAndOwnsClose(t *testing.T) {
	registry := newFlowTestRegistry(t, 2, 2, flowIDBytes(7, 7, 8))
	firstResource := &flowTestResource{}
	first, err := registry.Create(FlowCreateRequest{
		Principal: ticketTestPrincipal(1), ConstellationID: ticketTestContinuityID(17),
		LeaseKey: ticketTestContinuityID(33), Upstream: firstResource,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondResource := &flowTestResource{}
	second, err := registry.Create(FlowCreateRequest{
		Principal: ticketTestPrincipal(2), ConstellationID: ticketTestContinuityID(18),
		LeaseKey: ticketTestContinuityID(34), Upstream: secondResource,
	})
	if err != nil || second.ID == first.ID {
		t.Fatalf("collision retry second=%+v err=%v", second, err)
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	if firstResource.closes() != 1 || secondResource.closes() != 1 || registry.Count() != 0 {
		t.Fatalf("close ownership first=%d second=%d count=%d", firstResource.closes(), secondResource.closes(), registry.Count())
	}
}

func TestFlowRegistryRejectsInvalidConfigurationAndRequests(t *testing.T) {
	for _, config := range []FlowRegistryConfig{
		{},
		{MaxFlows: 1, MaxFlowsPerPrincipal: 2},
		{MaxFlows: MaxLogicalFlows + 1, MaxFlowsPerPrincipal: 1},
	} {
		if _, err := NewFlowRegistry(config); !errors.Is(err, ErrFlowRegistryConfig) {
			t.Fatalf("config=%+v error=%v", config, err)
		}
	}
	registry := newFlowTestRegistry(t, 1, 1, flowIDBytes(1))
	if _, err := registry.Create(FlowCreateRequest{}); !errors.Is(err, ErrFlowBinding) {
		t.Fatalf("invalid create error=%v", err)
	}
}

func newFlowTestRegistry(t *testing.T, maxFlows, perPrincipal int, random []byte) *FlowRegistry {
	t.Helper()
	registry, err := NewFlowRegistry(FlowRegistryConfig{
		MaxFlows: maxFlows, MaxFlowsPerPrincipal: perPrincipal,
		Random: bytes.NewReader(random),
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func flowIDBytes(seeds ...byte) []byte {
	var raw []byte
	for _, seed := range seeds {
		raw = append(raw, bytes.Repeat([]byte{seed}, len(protocol.ContinuityID{}))...)
	}
	return raw
}

type flowTestResource struct {
	mu     sync.Mutex
	closed int
}

func (r *flowTestResource) Read([]byte) (int, error)    { return 0, io.EOF }
func (r *flowTestResource) Write(p []byte) (int, error) { return len(p), nil }
func (r *flowTestResource) Close() error {
	r.mu.Lock()
	r.closed++
	r.mu.Unlock()
	return nil
}
func (r *flowTestResource) closes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}
