package windowsclient

import (
	"errors"
	"net/netip"
	"regexp"
)

var ErrInvalidRoutePlan = errors.New("invalid Windows route plan")

type EndpointRoute struct {
	Address        string `json:"address"`
	InterfaceIndex int    `json:"interface_index"`
	NextHop        string `json:"next_hop"`
}

type RouteCommandKind string

const (
	RouteCommandConfigureAdapter  RouteCommandKind = "configure-adapter"
	RouteCommandEndpointExclusion RouteCommandKind = "endpoint-exclusion"
	RouteCommandTunnelRoute       RouteCommandKind = "tunnel-route"
)

type RouteCommand struct {
	Kind           RouteCommandKind `json:"kind"`
	Add            bool             `json:"add"`
	Family         int              `json:"family"`
	Destination    string           `json:"destination,omitempty"`
	InterfaceIndex int              `json:"interface_index"`
	NextHop        string           `json:"next_hop,omitempty"`
	AdapterName    string           `json:"adapter_name,omitempty"`
}

type RoutePlan struct {
	Apply    []RouteCommand `json:"apply"`
	Rollback []RouteCommand `json:"rollback"`
}

var adapterNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

func BuildRoutePlan(adapterName string, adapterIndex int, endpoints []EndpointRoute) (RoutePlan, error) {
	if !adapterNamePattern.MatchString(adapterName) || adapterIndex <= 0 || len(endpoints) == 0 || len(endpoints) > 16 {
		return RoutePlan{}, ErrInvalidRoutePlan
	}
	plan := RoutePlan{Apply: []RouteCommand{{Kind: RouteCommandConfigureAdapter, Add: true, AdapterName: adapterName, InterfaceIndex: adapterIndex}}}
	seen := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		address, err := netip.ParseAddr(endpoint.Address)
		nextHop, nextHopErr := netip.ParseAddr(endpoint.NextHop)
		address = address.Unmap()
		if err != nil || nextHopErr != nil || endpoint.InterfaceIndex <= 0 || !address.IsGlobalUnicast() ||
			address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || nextHop.IsMulticast() ||
			address.Is4() != nextHop.Is4() {
			return RoutePlan{}, ErrInvalidRoutePlan
		}
		if _, duplicate := seen[address.String()]; duplicate {
			continue
		}
		seen[address.String()] = struct{}{}
		bits := 128
		family := 6
		if address.Is4() {
			bits, family = 32, 4
		}
		plan.Apply = append(plan.Apply, RouteCommand{
			Kind: RouteCommandEndpointExclusion, Add: true, Family: family,
			Destination: netip.PrefixFrom(address, bits).String(), InterfaceIndex: endpoint.InterfaceIndex,
			NextHop: nextHop.String(),
		})
	}
	for _, route := range []struct {
		family               int
		destination, nextHop string
	}{
		{4, "0.0.0.0/1", "0.0.0.0"}, {4, "128.0.0.0/1", "0.0.0.0"},
		{6, "::/1", "::"}, {6, "8000::/1", "::"},
	} {
		plan.Apply = append(plan.Apply, RouteCommand{Kind: RouteCommandTunnelRoute, Add: true, Family: route.family,
			Destination: route.destination, InterfaceIndex: adapterIndex, NextHop: route.nextHop})
	}
	for index := len(plan.Apply) - 1; index >= 0; index-- {
		command := plan.Apply[index]
		command.Add = false
		plan.Rollback = append(plan.Rollback, command)
	}
	return plan, nil
}
