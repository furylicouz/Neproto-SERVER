package cluster

import (
	"errors"
	"testing"
	"time"
)

func TestRelayRequestRejectsLoopsAndHopBudgetViolations(t *testing.T) {
	valid := RelayRequest{Version: RelayVersion, RouteID: "media", UserID: "alice", RemainingHops: 2, VisitedNodeIDs: []string{"master"}, RemainingNodeIDs: []string{"edge-02"}, TraceID: "0123456789abcdef", TargetHost: "example.com", TargetPort: 443, Protocol: ProtocolTCP}
	if err := ValidateRelayRequest(valid, "edge"); err != nil {
		t.Fatalf("ValidateRelayRequest() error = %v", err)
	}

	loop := valid
	loop.VisitedNodeIDs = []string{"master", "edge"}
	if err := ValidateRelayRequest(loop, "edge"); !errors.Is(err, ErrRelayLoop) {
		t.Fatalf("ValidateRelayRequest(loop) error = %v", err)
	}

	exhausted := valid
	exhausted.RemainingHops = 0
	if err := ValidateRelayRequest(exhausted, "edge"); !errors.Is(err, ErrRelayHopLimit) {
		t.Fatalf("ValidateRelayRequest(exhausted) error = %v", err)
	}
}

func TestResolveRouteForUserHonorsAssignedAdminRoutes(t *testing.T) {
	now := time.Now().UTC()
	state := testState(now)
	state.Nodes = append(state.Nodes, Node{
		ID: "edge-01", Name: "Edge", Region: "Finland", Roles: []NodeRole{RoleEgress},
		PublicIdentity: "edge.example.com", PublicAddresses: []string{"1.1.1.1"}, NP2Endpoint: "edge.example.com:443",
		Enabled: true, ClientVisible: true, CredentialID: "peer-edge", HostKeySHA256: "SHA256:edge",
		ProvisionedAt: now, UpdatedAt: now,
	})
	state.Routes = []Route{{
		ID: "media", Name: "Media", Priority: 10, Enabled: true, Source: RouteSourceAdmin,
		Match:  RouteMatch{DomainSuffixes: []string{"youtube.com"}, Protocols: []NetworkProtocol{ProtocolTCP}},
		Action: RouteAction{Kind: RouteActionNode, NodeIDs: []string{"edge-01"}},
	}}
	state.Access = []UserAccess{{UserID: "alice", AllowedNodeIDs: []string{"master", "edge-01"}, AllowedRouteIDs: []string{"media"}, Revision: 1}}
	action, routeID, err := ResolveRouteForUser(state, "alice", Target{Domain: "www.youtube.com", Port: 443, Protocol: ProtocolTCP})
	if err != nil || routeID != "media" || action.Kind != RouteActionNode || len(action.NodeIDs) != 1 || action.NodeIDs[0] != "edge-01" {
		t.Fatalf("resolved action=%+v route=%q err=%v", action, routeID, err)
	}
	action, routeID, err = ResolveRouteForUser(state, "alice", Target{Domain: "example.org", Port: 443, Protocol: ProtocolTCP})
	if err != nil || routeID != "" || action.Kind != RouteActionCurrent {
		t.Fatalf("default action=%+v route=%q err=%v", action, routeID, err)
	}
}
