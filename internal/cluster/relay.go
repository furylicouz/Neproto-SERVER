package cluster

import (
	"errors"
	"net/netip"
)

const RelayVersion = 1

var (
	ErrInvalidRelay  = errors.New("invalid cluster relay request")
	ErrRelayLoop     = errors.New("cluster relay loop")
	ErrRelayHopLimit = errors.New("cluster relay hop limit exceeded")
)

type RelayRequest struct {
	Version          int                 `json:"version"`
	RouteID          string              `json:"route_id"`
	UserID           string              `json:"user_id"`
	RemainingHops    uint8               `json:"remaining_hops"`
	VisitedNodeIDs   []string            `json:"visited_node_ids"`
	RemainingNodeIDs []string            `json:"remaining_node_ids,omitempty"`
	TraceID          string              `json:"trace_id"`
	TargetHost       string              `json:"target_host"`
	TargetPort       uint16              `json:"target_port"`
	Protocol         NetworkProtocol     `json:"protocol"`
	ClientRoute      *ClientRouteRequest `json:"client_route,omitempty"`
}

func ValidateRelayRequest(request RelayRequest, currentNodeID string) error {
	if request.Version != RelayVersion || !validIdentifier(request.RouteID, 64) || !validOpaqueID(request.UserID, 128) ||
		!validIdentifier(currentNodeID, 64) || request.TargetPort == 0 ||
		(request.Protocol != ProtocolTCP && request.Protocol != ProtocolUDP) || len(request.TraceID) != 16 ||
		len(request.VisitedNodeIDs) > MaxRouteHops || len(request.RemainingNodeIDs) > MaxRouteHops {
		return ErrInvalidRelay
	}
	if request.ClientRoute != nil && ValidateClientRouteRequest(*request.ClientRoute) != nil {
		return ErrInvalidRelay
	}
	if request.RemainingHops == 0 || request.RemainingHops > MaxRouteHops {
		return ErrRelayHopLimit
	}
	seen := make(map[string]struct{}, len(request.VisitedNodeIDs))
	for _, nodeID := range request.VisitedNodeIDs {
		if !validIdentifier(nodeID, 64) {
			return ErrInvalidRelay
		}
		if nodeID == currentNodeID {
			return ErrRelayLoop
		}
		if _, exists := seen[nodeID]; exists {
			return ErrRelayLoop
		}
		seen[nodeID] = struct{}{}
	}
	for _, nodeID := range request.RemainingNodeIDs {
		if !validIdentifier(nodeID, 64) || nodeID == currentNodeID {
			return ErrRelayLoop
		}
		if _, exists := seen[nodeID]; exists {
			return ErrRelayLoop
		}
		seen[nodeID] = struct{}{}
	}
	if int(request.RemainingHops) != len(request.RemainingNodeIDs)+1 {
		return ErrRelayHopLimit
	}
	if address, err := netip.ParseAddr(request.TargetHost); err != nil {
		if !validDomain(request.TargetHost) {
			return ErrInvalidRelay
		}
	} else if !address.IsGlobalUnicast() {
		return ErrInvalidRelay
	}
	return nil
}

func ResolveRouteForUser(state State, userID string, target Target) (RouteAction, string, error) {
	return ResolveRouteForUserWithMatcher(state, userID, target, nil)
}

func ResolveRouteForUserWithMatcher(
	state State,
	userID string,
	target Target,
	matcher GeoMatchEvaluator,
) (RouteAction, string, error) {
	if err := ValidateState(state); err != nil || !validOpaqueID(userID, 128) {
		return RouteAction{}, "", ErrInvalidState
	}
	var access *UserAccess
	for index := range state.Access {
		if state.Access[index].UserID == userID {
			access = &state.Access[index]
			break
		}
	}
	if access == nil {
		return RouteAction{}, "", ErrInvalidState
	}
	allowedRoutes := make(map[string]struct{}, len(access.AllowedRouteIDs))
	for _, routeID := range access.AllowedRouteIDs {
		allowedRoutes[routeID] = struct{}{}
	}
	allowedNodes := make(map[string]struct{}, len(access.AllowedNodeIDs))
	for _, nodeID := range access.AllowedNodeIDs {
		allowedNodes[nodeID] = struct{}{}
	}
	for _, route := range EffectiveRoutes(state.Routes, nil, false) {
		if _, allowed := allowedRoutes[route.ID]; allowed && route.MatchesWith(target, matcher) {
			for _, nodeID := range route.Action.NodeIDs {
				if _, permitted := allowedNodes[nodeID]; !permitted {
					return RouteAction{}, "", ErrInvalidState
				}
			}
			return route.Action, route.ID, nil
		}
	}
	return RouteAction{Kind: RouteActionCurrent}, "", nil
}
