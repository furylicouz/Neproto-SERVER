package tunstack

import (
	"errors"
	"net/netip"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/proxy"
)

var (
	ErrInvalidClientRoutes = errors.New("invalid client route policy")
	ErrClientRouteBlocked  = errors.New("client route blocked target")
)

type ClientRoutePolicy struct {
	routes []cluster.Route
}

func NewClientRoutePolicy(routes []cluster.Route) (*ClientRoutePolicy, error) {
	if len(routes) > cluster.MaxRoutes {
		return nil, ErrInvalidClientRoutes
	}
	validated := make([]cluster.Route, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if cluster.ValidateRoute(route) != nil || route.Source != cluster.RouteSourceClient || route.Mandatory {
			return nil, ErrInvalidClientRoutes
		}
		if _, duplicate := seen[route.ID]; duplicate {
			return nil, ErrInvalidClientRoutes
		}
		seen[route.ID] = struct{}{}
		validated = append(validated, route)
	}
	return &ClientRoutePolicy{routes: cluster.EffectiveRoutes(nil, validated, true)}, nil
}

func (policy *ClientRoutePolicy) rewrite(metadata []byte) ([]byte, error) {
	if policy == nil || len(policy.routes) == 0 {
		return metadata, nil
	}
	request, err := proxy.DecodeOpenRequest(metadata)
	if err != nil || (request.Command != proxy.CommandTCPConnect && request.Command != proxy.CommandUDPFixed) {
		return nil, ErrInvalidClientRoutes
	}
	networkProtocol := cluster.ProtocolTCP
	routedCommand := proxy.CommandTCPClientRoute
	if request.Command == proxy.CommandUDPFixed {
		networkProtocol = cluster.ProtocolUDP
		routedCommand = proxy.CommandUDPClientRoute
	}
	target := cluster.Target{Port: request.Target.Port, Protocol: networkProtocol}
	if address, err := netip.ParseAddr(request.Target.Host); err == nil {
		target.Address = address.Unmap()
	} else {
		target.Domain = request.Target.Host
	}
	for _, route := range policy.routes {
		if !route.Matches(target) {
			continue
		}
		if route.Action.Kind == cluster.RouteActionBlock {
			return nil, ErrClientRouteBlocked
		}
		return proxy.EncodeOpenRequest(proxy.OpenRequest{
			Command: routedCommand, Target: request.Target,
			ClientRoute: &cluster.ClientRouteRequest{
				Version: cluster.ClientRouteVersion, RouteID: route.ID, Action: route.Action,
			},
		})
	}
	return metadata, nil
}
