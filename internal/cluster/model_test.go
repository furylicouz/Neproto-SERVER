package cluster

import (
	"net/netip"
	"testing"
	"time"
)

func TestValidateStateAcceptsBoundedSingleMasterCluster(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	state := testState(now)

	if err := ValidateState(state); err != nil {
		t.Fatalf("ValidateState() error = %v", err)
	}
}

func TestValidateStateRejectsDuplicateNodeAndUnknownRouteHop(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	state := testState(now)
	state.Nodes = append(state.Nodes, state.Nodes[0])
	if err := ValidateState(state); err == nil {
		t.Fatal("ValidateState() accepted duplicate node")
	}

	state = testState(now)
	state.Routes[0].Action.NodeIDs = []string{"missing"}
	if err := ValidateState(state); err == nil {
		t.Fatal("ValidateState() accepted unknown route hop")
	}
}

func TestRouteMatchesDomainBoundaryCIDRPortAndProtocol(t *testing.T) {
	route := Route{
		ID: "media", Name: "Media", Priority: 10, Enabled: true,
		Source: RouteSourceAdmin,
		Match: RouteMatch{
			DomainSuffixes: []string{"youtube.com"},
			CIDRs:          []string{"203.0.113.0/24"},
			PortRanges:     []PortRange{{From: 443, To: 443}},
			Protocols:      []NetworkProtocol{ProtocolTCP},
		},
		Action: RouteAction{Kind: RouteActionNode, NodeIDs: []string{"edge"}},
	}
	if err := ValidateRoute(route); err != nil {
		t.Fatalf("ValidateRoute() error = %v", err)
	}

	cases := []struct {
		name   string
		target Target
		want   bool
	}{
		{"domain", Target{Domain: "www.youtube.com", Port: 443, Protocol: ProtocolTCP}, true},
		{"domain boundary", Target{Domain: "notyoutube.com", Port: 443, Protocol: ProtocolTCP}, false},
		{"cidr", Target{Address: netip.MustParseAddr("203.0.113.7"), Port: 443, Protocol: ProtocolTCP}, true},
		{"wrong port", Target{Domain: "youtube.com", Port: 80, Protocol: ProtocolTCP}, false},
		{"wrong protocol", Target{Domain: "youtube.com", Port: 443, Protocol: ProtocolUDP}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := route.Matches(test.target); got != test.want {
				t.Fatalf("Matches() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRouteMatchesGeoIPAndGeoSiteMetadata(t *testing.T) {
	route := Route{
		ID: "geo-media", Name: "Geo media", Priority: 10, Enabled: true, Source: RouteSourceAdmin,
		Match: RouteMatch{
			GeoIPCountries:    []string{"nl"},
			GeoSiteCategories: []string{"youtube"},
			Protocols:         []NetworkProtocol{ProtocolTCP},
		},
		Action: RouteAction{Kind: RouteActionNode, NodeIDs: []string{"edge"}},
	}
	if err := ValidateRoute(route); err != nil {
		t.Fatalf("ValidateRoute() error = %v", err)
	}
	matcher := func(match RouteMatch, target Target) bool {
		return (target.Domain == "www.youtube.com" && match.GeoSiteCategories[0] == "youtube") ||
			(target.Address.String() == "203.0.113.8" && match.GeoIPCountries[0] == "nl")
	}
	if !route.MatchesWith(Target{Domain: "www.youtube.com", Port: 443, Protocol: ProtocolTCP}, matcher) {
		t.Fatal("geosite route did not match")
	}
	if !route.MatchesWith(Target{Address: netip.MustParseAddr("203.0.113.8"), Port: 443, Protocol: ProtocolTCP}, matcher) {
		t.Fatal("geoip route did not match")
	}
	if route.Matches(Target{Domain: "www.youtube.com", Port: 443, Protocol: ProtocolTCP}) {
		t.Fatal("geo route matched without a geodata evaluator")
	}
}

func TestEffectiveRoutesKeepsMandatoryAdminAheadOfLocalRules(t *testing.T) {
	admin := []Route{
		{ID: "admin-optional", Name: "Optional", Priority: 50, Enabled: true, Source: RouteSourceAdmin, Action: RouteAction{Kind: RouteActionCurrent}},
		{ID: "admin-mandatory", Name: "Mandatory", Priority: 100, Enabled: true, Source: RouteSourceAdmin, Mandatory: true, Action: RouteAction{Kind: RouteActionBlock}},
	}
	local := []Route{
		{ID: "local-fast", Name: "Local", Priority: 1, Enabled: true, Source: RouteSourceClient, Action: RouteAction{Kind: RouteActionDirect}},
	}

	effective := EffectiveRoutes(admin, local, true)
	want := []string{"admin-mandatory", "local-fast", "admin-optional"}
	if len(effective) != len(want) {
		t.Fatalf("len(EffectiveRoutes()) = %d, want %d", len(effective), len(want))
	}
	for index, id := range want {
		if effective[index].ID != id {
			t.Fatalf("EffectiveRoutes()[%d].ID = %q, want %q", index, effective[index].ID, id)
		}
	}

	withoutLocal := EffectiveRoutes(admin, local, false)
	if len(withoutLocal) != 2 {
		t.Fatalf("client routes were not removed: %+v", withoutLocal)
	}
}

func testState(now time.Time) State {
	return State{
		Version: StateVersion, ClusterID: "cluster-01", Revision: 1,
		Nodes: []Node{
			{ID: "master", Name: "Master", Region: "Moscow", Roles: []NodeRole{RoleMaster, RoleIngress}, PublicIdentity: "master.example.com", PublicAddresses: []string{"198.51.100.10"}, NP2Endpoint: "master.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "node-master", HostKeySHA256: "SHA256:master", ProvisionedAt: now, UpdatedAt: now},
			{ID: "edge", Name: "Edge", Region: "Helsinki", Roles: []NodeRole{RoleRelay, RoleEgress}, PublicIdentity: "edge.example.com", PublicAddresses: []string{"203.0.113.10"}, NP2Endpoint: "edge.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "node-edge", HostKeySHA256: "SHA256:edge", ProvisionedAt: now, UpdatedAt: now},
		},
		Routes:    []Route{{ID: "media", Name: "Media", Priority: 10, Enabled: true, Source: RouteSourceAdmin, Match: RouteMatch{DomainSuffixes: []string{"youtube.com"}, Protocols: []NetworkProtocol{ProtocolTCP}}, Action: RouteAction{Kind: RouteActionNode, NodeIDs: []string{"edge"}}}},
		Access:    []UserAccess{{UserID: "alice", AllowedNodeIDs: []string{"master", "edge"}, AllowedRouteIDs: []string{"media"}, AllowAutoSelection: true, AllowClientRoutes: true, Revision: 1}},
		UpdatedAt: now,
	}
}
