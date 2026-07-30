package tunstack

import (
	"errors"
	"reflect"
	"testing"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/proxy"
)

func TestClientRoutePolicyRewritesTCPAndUDPToAuthorizedHints(t *testing.T) {
	policy, err := NewClientRoutePolicy([]cluster.Route{{
		ID: "local-media", Name: "Media", Priority: 10, Enabled: true, Source: cluster.RouteSourceClient,
		Match:  cluster.RouteMatch{CIDRs: []string{"203.0.113.0/24"}},
		Action: cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{"edge-01"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, commands := range [][2]proxy.OpenCommand{
		{proxy.CommandTCPConnect, proxy.CommandTCPClientRoute},
		{proxy.CommandUDPFixed, proxy.CommandUDPClientRoute},
	} {
		raw, _ := proxy.EncodeOpenRequest(proxy.OpenRequest{
			Command: commands[0], Target: proxy.Target{Host: "203.0.113.20", Port: 443},
		})
		rewritten, err := policy.rewrite(raw)
		if err != nil {
			t.Fatal(err)
		}
		request, err := proxy.DecodeOpenRequest(rewritten)
		if err != nil || request.Command != commands[1] || request.ClientRoute == nil ||
			request.ClientRoute.RouteID != "local-media" ||
			!reflect.DeepEqual(request.ClientRoute.Action.NodeIDs, []string{"edge-01"}) {
			t.Fatalf("request=%+v err=%v", request, err)
		}
	}
}

func TestClientRoutePolicyBlocksLocallyAndLeavesUnmatchedTargetUnchanged(t *testing.T) {
	policy, err := NewClientRoutePolicy([]cluster.Route{{
		ID: "local-block", Name: "Block", Priority: 1, Enabled: true, Source: cluster.RouteSourceClient,
		Match: cluster.RouteMatch{CIDRs: []string{"198.51.100.0/24"}}, Action: cluster.RouteAction{Kind: cluster.RouteActionBlock},
	}})
	if err != nil {
		t.Fatal(err)
	}
	blocked, _ := proxy.EncodeOpenRequest(proxy.OpenRequest{Command: proxy.CommandTCPConnect, Target: proxy.Target{Host: "198.51.100.10", Port: 443}})
	if _, err := policy.rewrite(blocked); !errors.Is(err, ErrClientRouteBlocked) {
		t.Fatalf("block error=%v", err)
	}
	unmatched, _ := proxy.EncodeOpenRequest(proxy.OpenRequest{Command: proxy.CommandTCPConnect, Target: proxy.Target{Host: "8.8.8.8", Port: 443}})
	got, err := policy.rewrite(unmatched)
	if err != nil || !reflect.DeepEqual(got, unmatched) {
		t.Fatalf("unmatched changed=%v err=%v", !reflect.DeepEqual(got, unmatched), err)
	}
}

func TestClientRoutePolicyRewritesAttributedDomain(t *testing.T) {
	policy, err := NewClientRoutePolicy([]cluster.Route{{
		ID: "local-domain", Name: "Domain", Priority: 1, Enabled: true, Source: cluster.RouteSourceClient,
		Match:  cluster.RouteMatch{DomainSuffixes: []string{"2ip.ru"}},
		Action: cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{"edge-01"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := proxy.EncodeOpenRequest(proxy.OpenRequest{
		Command: proxy.CommandTCPConnect, Target: proxy.Target{Host: "www.2ip.ru", Port: 443},
	})
	rewritten, err := policy.rewrite(raw)
	if err != nil {
		t.Fatal(err)
	}
	request, err := proxy.DecodeOpenRequest(rewritten)
	if err != nil || request.Command != proxy.CommandTCPClientRoute || request.ClientRoute == nil ||
		request.ClientRoute.RouteID != "local-domain" {
		t.Fatalf("request=%+v err=%v", request, err)
	}
}
