package cluster

import "errors"

const ClientRouteVersion = 1

var ErrClientRouteUnauthorized = errors.New("client cluster route is not authorized")

type ClientRouteRequest struct {
	Version int         `json:"version"`
	RouteID string      `json:"route_id"`
	Action  RouteAction `json:"action"`
}

func ValidateClientRouteRequest(request ClientRouteRequest) error {
	if request.Version != ClientRouteVersion || !validIdentifier(request.RouteID, 64) {
		return ErrInvalidState
	}
	switch request.Action.Kind {
	case RouteActionNode:
		if len(request.Action.NodeIDs) != 1 || !validIdentifier(request.Action.NodeIDs[0], 64) {
			return ErrInvalidState
		}
	case RouteActionChain:
		if len(request.Action.NodeIDs) < 2 || len(request.Action.NodeIDs) > MaxRouteHops || hasDuplicates(request.Action.NodeIDs) {
			return ErrInvalidState
		}
		for _, nodeID := range request.Action.NodeIDs {
			if !validIdentifier(nodeID, 64) {
				return ErrInvalidState
			}
		}
	case RouteActionCurrent, RouteActionDirect, RouteActionAuto:
		if len(request.Action.NodeIDs) != 0 {
			return ErrInvalidState
		}
	default:
		// Block is enforced locally before a stream is opened and therefore is
		// deliberately not a valid server hint.
		return ErrInvalidState
	}
	return nil
}

// ResolveClientRouteForUser preserves mandatory administrator policy and then
// authorizes a client-selected action only within the user's signed node set.
func ResolveClientRouteForUser(
	state State,
	userID string,
	target Target,
	request ClientRouteRequest,
) (RouteAction, string, error) {
	return ResolveClientRouteForUserWithMatcher(state, userID, target, request, nil)
}

func ResolveClientRouteForUserWithMatcher(
	state State,
	userID string,
	target Target,
	request ClientRouteRequest,
	matcher GeoMatchEvaluator,
) (RouteAction, string, error) {
	if ValidateState(state) != nil || ValidateClientRouteRequest(request) != nil ||
		!validOpaqueID(userID, 128) {
		return RouteAction{}, "", ErrInvalidState
	}
	var access *UserAccess
	for index := range state.Access {
		if state.Access[index].UserID == userID {
			access = &state.Access[index]
			break
		}
	}
	if access == nil || !access.AllowClientRoutes {
		return RouteAction{}, "", ErrClientRouteUnauthorized
	}
	allowedRoutes := make(map[string]struct{}, len(access.AllowedRouteIDs))
	for _, routeID := range access.AllowedRouteIDs {
		allowedRoutes[routeID] = struct{}{}
	}
	for _, route := range EffectiveRoutes(state.Routes, nil, false) {
		_, permitted := allowedRoutes[route.ID]
		if permitted && route.Mandatory && route.MatchesWith(target, matcher) {
			return route.Action, route.ID, nil
		}
	}
	if request.Action.Kind == RouteActionAuto {
		return ResolveRouteForUserWithMatcher(state, userID, target, matcher)
	}
	allowedNodes := make(map[string]struct{}, len(access.AllowedNodeIDs))
	for _, nodeID := range access.AllowedNodeIDs {
		allowedNodes[nodeID] = struct{}{}
	}
	for _, nodeID := range request.Action.NodeIDs {
		if _, allowed := allowedNodes[nodeID]; !allowed {
			return RouteAction{}, "", ErrClientRouteUnauthorized
		}
	}
	return request.Action, request.RouteID, nil
}
