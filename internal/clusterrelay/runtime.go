package clusterrelay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/netip"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/proxy"
)

var (
	ErrInvalidConfig     = errors.New("invalid cluster relay configuration")
	ErrRelayUnauthorized = errors.New("cluster relay peer is not authorized")
	ErrRouteBlocked      = errors.New("cluster route blocked")
	ErrPeerUnavailable   = errors.New("cluster relay peer is unavailable")
)

type StateLoader func() (cluster.State, error)
type PeerOpener func(context.Context, string, cluster.RelayRequest) (proxy.DuplexStream, error)
type TargetDialer func(context.Context, proxy.Target) (proxy.DuplexStream, error)
type UDPTargetDialer func(context.Context, proxy.Target) (proxy.DuplexStream, error)

type GeoMatcher interface {
	Match(context.Context, cluster.RouteMatch, cluster.Target) bool
}

type Runtime struct {
	NodeID         string
	MasterNodeID   string
	PeerPrincipals map[string]string
	LoadState      StateLoader
	OpenPeer       PeerOpener
	DialTarget     TargetDialer
	DialUDP        UDPTargetDialer
	GeoMatcher     GeoMatcher
	Random         io.Reader
}

func (runtime *Runtime) Validate() error {
	if runtime == nil || !identifier(runtime.NodeID) || !identifier(runtime.MasterNodeID) ||
		runtime.OpenPeer == nil || runtime.DialTarget == nil || runtime.DialUDP == nil || len(runtime.PeerPrincipals) == 0 {
		return ErrInvalidConfig
	}
	if runtime.NodeID == runtime.MasterNodeID && runtime.LoadState == nil {
		return ErrInvalidConfig
	}
	for credentialID, nodeID := range runtime.PeerPrincipals {
		if credentialID == "" || len(credentialID) > 128 || !identifier(nodeID) || nodeID == runtime.NodeID {
			return ErrInvalidConfig
		}
	}
	return nil
}

func (runtime *Runtime) RouteTCP(ctx context.Context, userID string, target proxy.Target) (proxy.DuplexStream, bool, error) {
	return runtime.route(ctx, userID, target, cluster.ProtocolTCP)
}

func (runtime *Runtime) RouteUDP(ctx context.Context, userID string, target proxy.Target) (proxy.DuplexStream, bool, error) {
	return runtime.route(ctx, userID, target, cluster.ProtocolUDP)
}

func (runtime *Runtime) RouteClientTCP(ctx context.Context, userID string, target proxy.Target, request cluster.ClientRouteRequest) (proxy.DuplexStream, bool, error) {
	return runtime.routeClient(ctx, userID, target, cluster.ProtocolTCP, request)
}

func (runtime *Runtime) RouteClientUDP(ctx context.Context, userID string, target proxy.Target, request cluster.ClientRouteRequest) (proxy.DuplexStream, bool, error) {
	return runtime.routeClient(ctx, userID, target, cluster.ProtocolUDP, request)
}

func (runtime *Runtime) routeClient(
	ctx context.Context,
	userID string,
	target proxy.Target,
	protocol cluster.NetworkProtocol,
	request cluster.ClientRouteRequest,
) (proxy.DuplexStream, bool, error) {
	if ctx == nil || userID == "" || target == (proxy.Target{}) || cluster.ValidateClientRouteRequest(request) != nil {
		return nil, true, ErrInvalidConfig
	}
	if err := runtime.Validate(); err != nil {
		return nil, true, err
	}
	if _, isPeer := runtime.PeerPrincipals[userID]; isPeer {
		return nil, true, ErrRelayUnauthorized
	}
	if runtime.NodeID != runtime.MasterNodeID && runtime.LoadState == nil {
		relay, err := runtime.newRequest("resolve", userID, target, protocol, []string{runtime.NodeID})
		if err != nil {
			return nil, true, err
		}
		relay.ClientRoute = &request
		peer, err := runtime.OpenPeer(ctx, runtime.MasterNodeID, relay)
		return peer, true, err
	}
	action, routeID, err := runtime.resolveClient(ctx, userID, target, protocol, request)
	if err != nil {
		return nil, true, err
	}
	return runtime.applyAction(ctx, action, routeID, userID, target, protocol, nil, "")
}

func (runtime *Runtime) route(ctx context.Context, userID string, target proxy.Target, protocol cluster.NetworkProtocol) (proxy.DuplexStream, bool, error) {
	if ctx == nil || userID == "" || target == (proxy.Target{}) {
		return nil, true, ErrInvalidConfig
	}
	if err := runtime.Validate(); err != nil {
		return nil, true, err
	}
	if _, isPeer := runtime.PeerPrincipals[userID]; isPeer {
		return nil, true, ErrRelayUnauthorized
	}
	if runtime.NodeID != runtime.MasterNodeID && runtime.LoadState == nil {
		request, err := runtime.newRequest("resolve", userID, target, protocol, []string{runtime.NodeID})
		if err != nil {
			return nil, true, err
		}
		peer, err := runtime.OpenPeer(ctx, runtime.MasterNodeID, request)
		return peer, true, err
	}
	action, routeID, err := runtime.resolve(ctx, userID, target, protocol)
	if err != nil {
		return nil, true, err
	}
	return runtime.applyAction(ctx, action, routeID, userID, target, protocol, nil, "")
}

func (runtime *Runtime) HandleRelay(ctx context.Context, credentialID string, request cluster.RelayRequest) (proxy.DuplexStream, error) {
	if err := runtime.Validate(); err != nil {
		return nil, err
	}
	peerNodeID, authorized := runtime.PeerPrincipals[credentialID]
	if !authorized || len(request.VisitedNodeIDs) == 0 || request.VisitedNodeIDs[len(request.VisitedNodeIDs)-1] != peerNodeID {
		return nil, ErrRelayUnauthorized
	}
	if err := cluster.ValidateRelayRequest(request, runtime.NodeID); err != nil {
		return nil, err
	}
	target := proxy.Target{Host: request.TargetHost, Port: request.TargetPort}
	visited := append(append([]string(nil), request.VisitedNodeIDs...), runtime.NodeID)
	if request.RouteID == "resolve" {
		if runtime.NodeID != runtime.MasterNodeID {
			return nil, ErrRelayUnauthorized
		}
		var action cluster.RouteAction
		var routeID string
		var err error
		if request.ClientRoute != nil {
			action, routeID, err = runtime.resolveClient(
				ctx, request.UserID, target, request.Protocol, *request.ClientRoute,
			)
		} else {
			action, routeID, err = runtime.resolve(ctx, request.UserID, target, request.Protocol)
		}
		if err != nil {
			return nil, err
		}
		stream, handled, err := runtime.applyAction(ctx, action, routeID, request.UserID, target, request.Protocol, visited, request.TraceID)
		if err != nil {
			return nil, err
		}
		if !handled {
			return runtime.dialTerminal(ctx, target, request.Protocol)
		}
		return stream, nil
	}
	if len(request.RemainingNodeIDs) == 0 {
		return runtime.dialTerminal(ctx, target, request.Protocol)
	}
	next := request.RemainingNodeIDs[0]
	forward := request
	forward.VisitedNodeIDs = visited
	forward.RemainingNodeIDs = append([]string(nil), request.RemainingNodeIDs[1:]...)
	forward.RemainingHops = uint8(len(forward.RemainingNodeIDs) + 1)
	return runtime.OpenPeer(ctx, next, forward)
}

func (runtime *Runtime) resolveClient(
	ctx context.Context,
	userID string,
	target proxy.Target,
	protocol cluster.NetworkProtocol,
	request cluster.ClientRouteRequest,
) (cluster.RouteAction, string, error) {
	state, err := runtime.LoadState()
	if err != nil {
		return cluster.RouteAction{}, "", err
	}
	clusterTarget := cluster.Target{Domain: target.Host, Port: target.Port, Protocol: protocol}
	if address, parseErr := netip.ParseAddr(target.Host); parseErr == nil {
		clusterTarget.Domain = ""
		clusterTarget.Address = address.Unmap()
	}
	matcher := runtime.geoEvaluator(ctx)
	return cluster.ResolveClientRouteForUserWithMatcher(state, userID, clusterTarget, request, matcher)
}

func (runtime *Runtime) resolve(ctx context.Context, userID string, target proxy.Target, protocol cluster.NetworkProtocol) (cluster.RouteAction, string, error) {
	state, err := runtime.LoadState()
	if err != nil {
		return cluster.RouteAction{}, "", err
	}
	clusterTarget := cluster.Target{Domain: target.Host, Port: target.Port, Protocol: protocol}
	if address, parseErr := netip.ParseAddr(target.Host); parseErr == nil {
		clusterTarget.Domain = ""
		clusterTarget.Address = address.Unmap()
	}
	return cluster.ResolveRouteForUserWithMatcher(state, userID, clusterTarget, runtime.geoEvaluator(ctx))
}

func (runtime *Runtime) geoEvaluator(ctx context.Context) cluster.GeoMatchEvaluator {
	if runtime == nil || runtime.GeoMatcher == nil {
		return nil
	}
	return func(match cluster.RouteMatch, target cluster.Target) bool {
		return runtime.GeoMatcher.Match(ctx, match, target)
	}
}

func (runtime *Runtime) applyAction(
	ctx context.Context,
	action cluster.RouteAction,
	routeID, userID string,
	target proxy.Target,
	protocol cluster.NetworkProtocol,
	visited []string,
	traceID string,
) (proxy.DuplexStream, bool, error) {
	switch action.Kind {
	case cluster.RouteActionBlock:
		return nil, true, ErrRouteBlocked
	case cluster.RouteActionCurrent, cluster.RouteActionDirect, cluster.RouteActionAuto:
		return nil, false, nil
	case cluster.RouteActionNode, cluster.RouteActionChain:
		path := append([]string(nil), action.NodeIDs...)
		for len(path) > 0 && path[0] == runtime.NodeID {
			path = path[1:]
		}
		if len(path) == 0 {
			return nil, false, nil
		}
		if len(path) > cluster.MaxRouteHops {
			return nil, true, cluster.ErrRelayHopLimit
		}
		if traceID == "" {
			generated, err := runtime.traceID()
			if err != nil {
				return nil, true, err
			}
			traceID = generated
		}
		nextNodeID := path[0]
		remainingNodeIDs := append([]string(nil), path[1:]...)
		remainingHops := len(path)
		// Edge nodes use a star topology and only hold an authenticated
		// connection to the master. The master forwards explicit routes to
		// another edge; default/current traffic never enters this branch.
		if runtime.NodeID != runtime.MasterNodeID && nextNodeID != runtime.MasterNodeID {
			nextNodeID = runtime.MasterNodeID
			remainingNodeIDs = append([]string(nil), path...)
			remainingHops++
		}
		if remainingHops > cluster.MaxRouteHops {
			return nil, true, cluster.ErrRelayHopLimit
		}
		request := cluster.RelayRequest{
			Version: cluster.RelayVersion, RouteID: routeID, UserID: userID,
			RemainingHops: uint8(remainingHops), VisitedNodeIDs: append([]string(nil), visited...),
			RemainingNodeIDs: remainingNodeIDs, TraceID: traceID,
			TargetHost: target.Host, TargetPort: target.Port, Protocol: protocol,
		}
		if len(request.VisitedNodeIDs) == 0 {
			request.VisitedNodeIDs = []string{runtime.NodeID}
		}
		stream, err := runtime.OpenPeer(ctx, nextNodeID, request)
		return stream, true, err
	default:
		return nil, true, ErrInvalidConfig
	}
}

func (runtime *Runtime) newRequest(routeID, userID string, target proxy.Target, protocol cluster.NetworkProtocol, visited []string) (cluster.RelayRequest, error) {
	traceID, err := runtime.traceID()
	if err != nil {
		return cluster.RelayRequest{}, err
	}
	return cluster.RelayRequest{
		Version: cluster.RelayVersion, RouteID: routeID, UserID: userID,
		RemainingHops: 1, VisitedNodeIDs: append([]string(nil), visited...), TraceID: traceID,
		TargetHost: target.Host, TargetPort: target.Port, Protocol: protocol,
	}, nil
}

func (runtime *Runtime) dialTerminal(ctx context.Context, target proxy.Target, protocol cluster.NetworkProtocol) (proxy.DuplexStream, error) {
	if protocol == cluster.ProtocolUDP {
		return runtime.DialUDP(ctx, target)
	}
	if protocol != cluster.ProtocolTCP {
		return nil, ErrInvalidConfig
	}
	return runtime.DialTarget(ctx, target)
}

func (runtime *Runtime) traceID() (string, error) {
	raw := make([]byte, 8)
	random := runtime.Random
	if random == nil {
		random = rand.Reader
	}
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func identifier(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || (index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}
