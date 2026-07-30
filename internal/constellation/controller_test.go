package constellation

import (
	"errors"
	"testing"

	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
)

func TestControllerSelectsLeastLoadedActiveLease(t *testing.T) {
	principal := controllerPrincipal(1)
	constellationID := controllerID(17)
	primaryResource := &controllerCloser{}
	controller, err := NewController(ControllerConfig{
		Principal: principal, ConstellationID: constellationID,
		MaxActive: 3, MaxDraining: 2,
	}, controllerCandidate(principal, constellationID, 1, protocol.CarrierHTTPS, primaryResource))
	if err != nil {
		t.Fatal(err)
	}
	secondaryID, err := controller.Add(controllerCandidate(
		principal, constellationID, 2, protocol.CarrierHTTP3, &controllerCloser{},
	))
	if err != nil {
		t.Fatal(err)
	}
	thirdID, err := controller.Add(controllerCandidate(
		principal, constellationID, 3, protocol.CarrierWebRTC, &controllerCloser{},
	))
	if err != nil {
		t.Fatal(err)
	}
	state := controller.State()
	if err := controller.UpdateLoad(state.PrimaryID, 8); err != nil {
		t.Fatal(err)
	}
	if err := controller.UpdateLoad(secondaryID, 2); err != nil {
		t.Fatal(err)
	}
	if err := controller.UpdateLoad(thirdID, 2); err != nil {
		t.Fatal(err)
	}
	first, err := controller.Select()
	if err != nil || first.ID != secondaryID {
		t.Fatalf("first selection=%+v err=%v", first, err)
	}
	second, err := controller.Select()
	if err != nil || second.ID != thirdID {
		t.Fatalf("second selection=%+v err=%v", second, err)
	}
}

func TestControllerRejectsWrongBindingDuplicateAndCapacity(t *testing.T) {
	principal := controllerPrincipal(1)
	constellationID := controllerID(17)
	primary := controllerCandidate(principal, constellationID, 1, protocol.CarrierHTTPS, &controllerCloser{})
	controller, err := NewController(ControllerConfig{
		Principal: principal, ConstellationID: constellationID,
		MaxActive: 2, MaxDraining: 1,
	}, primary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Add(controllerCandidate(
		controllerPrincipal(2), constellationID, 2, protocol.CarrierHTTPS, &controllerCloser{},
	)); !errors.Is(err, ErrLeaseBinding) {
		t.Fatalf("wrong principal error=%v", err)
	}
	if _, err := controller.Add(controllerCandidate(
		principal, controllerID(18), 2, protocol.CarrierHTTPS, &controllerCloser{},
	)); !errors.Is(err, ErrLeaseBinding) {
		t.Fatalf("wrong constellation error=%v", err)
	}
	if _, err := controller.Add(primary); !errors.Is(err, ErrLeaseDuplicate) {
		t.Fatalf("duplicate error=%v", err)
	}
	if _, err := controller.Add(controllerCandidate(
		principal, constellationID, 2, protocol.CarrierHTTPS, &controllerCloser{},
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Add(controllerCandidate(
		principal, constellationID, 3, protocol.CarrierHTTPS, &controllerCloser{},
	)); !errors.Is(err, ErrLeaseCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
}

func TestControllerPromotesReplacementAndDrainsOldPrimary(t *testing.T) {
	principal := controllerPrincipal(1)
	constellationID := controllerID(17)
	primaryResource := &controllerCloser{}
	replacementResource := &controllerCloser{}
	controller, err := NewController(ControllerConfig{
		Principal: principal, ConstellationID: constellationID,
		MaxActive: 2, MaxDraining: 1,
	}, controllerCandidate(principal, constellationID, 1, protocol.CarrierHTTPS, primaryResource))
	if err != nil {
		t.Fatal(err)
	}
	oldPrimary := controller.State().PrimaryID
	replacement, err := controller.Add(controllerCandidate(
		principal, constellationID, 2, protocol.CarrierHTTP3, replacementResource,
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Promote(replacement); err != nil {
		t.Fatalf("promote: %v", err)
	}
	state := controller.State()
	if state.PrimaryID != replacement || state.Active != 1 || state.Draining != 1 {
		t.Fatalf("promoted state=%+v", state)
	}
	selected, err := controller.Select()
	if err != nil || selected.ID != replacement {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
	if primaryResource.closed != 0 {
		t.Fatal("draining primary closed before completion")
	}
	if err := controller.CompleteDrain(oldPrimary); err != nil {
		t.Fatalf("complete drain: %v", err)
	}
	if primaryResource.closed != 1 || replacementResource.closed != 0 {
		t.Fatalf("primary closes=%d replacement closes=%d", primaryResource.closed, replacementResource.closed)
	}
}

func TestControllerFailurePromotesDeterministicReplacement(t *testing.T) {
	principal := controllerPrincipal(1)
	constellationID := controllerID(17)
	primaryResource := &controllerCloser{}
	controller, err := NewController(ControllerConfig{
		Principal: principal, ConstellationID: constellationID,
		MaxActive: 3, MaxDraining: 1,
	}, controllerCandidate(principal, constellationID, 1, protocol.CarrierHTTPS, primaryResource))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := controller.Add(controllerCandidate(principal, constellationID, 2, protocol.CarrierHTTP3, &controllerCloser{}))
	second, _ := controller.Add(controllerCandidate(principal, constellationID, 3, protocol.CarrierWebRTC, &controllerCloser{}))
	primary := controller.State().PrimaryID
	if err := controller.Fail(primary); err != nil {
		t.Fatal(err)
	}
	state := controller.State()
	if primaryResource.closed != 1 || state.PrimaryID != first || state.Active != 2 || state.Draining != 0 {
		t.Fatalf("state=%+v primary closes=%d first=%d second=%d", state, primaryResource.closed, first, second)
	}
}

func TestControllerStopClosesEveryLeaseExactlyOnce(t *testing.T) {
	principal := controllerPrincipal(1)
	constellationID := controllerID(17)
	first := &controllerCloser{}
	second := &controllerCloser{}
	controller, err := NewController(ControllerConfig{
		Principal: principal, ConstellationID: constellationID,
		MaxActive: 2, MaxDraining: 1,
	}, controllerCandidate(principal, constellationID, 1, protocol.CarrierHTTPS, first))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Add(controllerCandidate(
		principal, constellationID, 2, protocol.CarrierHTTP3, second,
	)); err != nil {
		t.Fatal(err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if first.closed != 1 || second.closed != 1 || !controller.State().Closed {
		t.Fatalf("first=%d second=%d state=%+v", first.closed, second.closed, controller.State())
	}
	if _, err := controller.Select(); !errors.Is(err, ErrControllerClosed) {
		t.Fatalf("select after stop error=%v", err)
	}
}

func TestControllerCannotDrainOnlyPrimary(t *testing.T) {
	principal := controllerPrincipal(1)
	constellationID := controllerID(17)
	controller, err := NewController(ControllerConfig{
		Principal: principal, ConstellationID: constellationID,
		MaxActive: 2, MaxDraining: 1,
	}, controllerCandidate(principal, constellationID, 1, protocol.CarrierHTTPS, &controllerCloser{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.BeginDrain(controller.State().PrimaryID); !errors.Is(err, ErrNoActiveLease) {
		t.Fatalf("drain only primary error=%v", err)
	}
}

type controllerCloser struct{ closed int }

func (c *controllerCloser) Close() error {
	c.closed++
	return nil
}

func controllerCandidate(
	principal continuity.PrincipalID,
	constellationID protocol.ContinuityID,
	keySeed byte,
	carrier protocol.CarrierKind,
	resource *controllerCloser,
) LeaseCandidate {
	return LeaseCandidate{
		Key: controllerID(keySeed), Principal: principal, ConstellationID: constellationID,
		Carrier: carrier, Resource: resource,
	}
}

func controllerPrincipal(seed byte) continuity.PrincipalID {
	var id continuity.PrincipalID
	for index := range id {
		id[index] = seed + byte(index)
	}
	return id
}

func controllerID(seed byte) protocol.ContinuityID {
	var id protocol.ContinuityID
	for index := range id {
		id[index] = seed + byte(index)
	}
	return id
}
