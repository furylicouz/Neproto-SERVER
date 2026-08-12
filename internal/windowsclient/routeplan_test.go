package windowsclient

import "testing"

func TestBuildEndpointRoutePlanContainsNoTunnelSideEffects(t *testing.T) {
	plan, err := BuildEndpointRoutePlan([]EndpointRoute{{
		Address: "37.252.23.223", InterfaceIndex: 6, NextHop: "10.0.0.1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Apply) != 1 || plan.Apply[0].Kind != RouteCommandEndpointExclusion {
		t.Fatalf("unexpected endpoint plan: %#v", plan.Apply)
	}
	if len(plan.Rollback) != 1 || plan.Rollback[0].Kind != RouteCommandEndpointExclusion || plan.Rollback[0].Add {
		t.Fatalf("unexpected endpoint rollback: %#v", plan.Rollback)
	}
}

func TestBuildRoutePlanExcludesEndpointsBeforeTunnelRoutes(t *testing.T) {
	plan, err := BuildRoutePlan("NeProto", 42, []EndpointRoute{
		{Address: "104.171.136.10", InterfaceIndex: 7, NextHop: "192.168.1.1"},
		{Address: "2001:4860:4860::8888", InterfaceIndex: 9, NextHop: "fe80::1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Apply) < 6 || plan.Apply[0].Kind != RouteCommandEndpointExclusion {
		t.Fatalf("unexpected apply plan: %#v", plan.Apply)
	}
	firstTunnel := -1
	lastExclusion := -1
	for i, command := range plan.Apply {
		switch command.Kind {
		case RouteCommandEndpointExclusion:
			lastExclusion = i
		case RouteCommandTunnelRoute:
			if firstTunnel < 0 {
				firstTunnel = i
			}
		}
	}
	if lastExclusion < 0 || firstTunnel <= lastExclusion {
		t.Fatalf("unsafe order: %#v", plan.Apply)
	}
	if len(plan.Rollback) == 0 || plan.Rollback[0].Kind != RouteCommandTunnelRoute {
		t.Fatalf("rollback must remove tunnel routes first: %#v", plan.Rollback)
	}
}

func TestBuildRoutePlanRejectsUnsafeInput(t *testing.T) {
	for _, endpoint := range []EndpointRoute{
		{Address: "127.0.0.1", InterfaceIndex: 7, NextHop: "192.168.1.1"},
		{Address: "1.1.1.1", InterfaceIndex: 0, NextHop: "192.168.1.1"},
		{Address: "1.1.1.1", InterfaceIndex: 7, NextHop: "not-an-ip"},
	} {
		if _, err := BuildRoutePlan("NeProto", 42, []EndpointRoute{endpoint}); err == nil {
			t.Fatalf("accepted unsafe endpoint: %+v", endpoint)
		}
	}
}
