package cluster

import (
	"errors"
	"testing"
	"time"
)

func TestClientRouteIsBoundToAllowedNodesAndMandatoryAdminPolicy(t *testing.T) {
	state := testState(time.Now().UTC())
	state.Access[0].AllowClientRoutes = true
	request := ClientRouteRequest{
		Version: ClientRouteVersion, RouteID: "local-media",
		Action: RouteAction{Kind: RouteActionNode, NodeIDs: []string{"edge"}},
	}
	target := Target{Domain: "example.org", Port: 443, Protocol: ProtocolTCP}
	action, routeID, err := ResolveClientRouteForUser(state, "alice", target, request)
	if err != nil || routeID != request.RouteID || action.Kind != RouteActionNode {
		t.Fatalf("action=%+v route=%q err=%v", action, routeID, err)
	}
	request.Action.NodeIDs = []string{"missing"}
	if _, _, err := ResolveClientRouteForUser(state, "alice", target, request); !errors.Is(err, ErrClientRouteUnauthorized) {
		t.Fatalf("unauthorized node error=%v", err)
	}

	state.Routes[0].Mandatory = true
	state.Routes[0].Match.DomainSuffixes = []string{"example.org"}
	state.Routes[0].Action = RouteAction{Kind: RouteActionBlock}
	request.Action.NodeIDs = []string{"edge"}
	action, routeID, err = ResolveClientRouteForUser(state, "alice", target, request)
	if err != nil || routeID != state.Routes[0].ID || action.Kind != RouteActionBlock {
		t.Fatalf("mandatory action=%+v route=%q err=%v", action, routeID, err)
	}
}

func TestClientRouteRequestRejectsBlockAndMalformedChains(t *testing.T) {
	for _, request := range []ClientRouteRequest{
		{Version: 1, RouteID: "local", Action: RouteAction{Kind: RouteActionBlock}},
		{Version: 1, RouteID: "local", Action: RouteAction{Kind: RouteActionChain, NodeIDs: []string{"edge"}}},
		{Version: 1, RouteID: "bad/route", Action: RouteAction{Kind: RouteActionCurrent}},
	} {
		if err := ValidateClientRouteRequest(request); err == nil {
			t.Fatalf("accepted request=%+v", request)
		}
	}
}
