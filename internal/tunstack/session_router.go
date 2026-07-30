package tunstack

import (
	"context"
	"sync"
	"sync/atomic"

	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
)

// SessionRouter atomically selects the authenticated NP/2 session used for
// new TUN flows. A stream that was already returned keeps its original Mux,
// allowing the mobile runtime to drain it after a carrier reconnect.
type SessionRouter struct {
	mu            sync.RWMutex
	active        sessionRoute
	routes        []sessionRoute
	nextRouteID   uint64
	tieCursor     uint64
	assignments   atomic.Uint64
	quicFallbacks atomic.Uint64
	continuity    *ClientContinuityRouter
	clientRoutes  *ClientRoutePolicy
}

func (r *SessionRouter) EnableContinuity(router *ClientContinuityRouter) error {
	if r == nil || router == nil {
		return ErrInvalidStackConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.continuity != nil {
		return ErrInvalidStackConfig
	}
	r.continuity = router
	return nil
}

func (r *SessionRouter) SetClientRoutes(policy *ClientRoutePolicy) error {
	if r == nil || policy == nil {
		return ErrInvalidClientRoutes
	}
	r.mu.Lock()
	r.clientRoutes = policy
	r.mu.Unlock()
	return nil
}

type sessionRoute struct {
	id            uint64
	open          streamOpenFunc
	activeStreams func() uint64
	datagrams     *session.DatagramMux
	maxUDPPayload uint64
}

func NewSessionRouter(
	mux *session.Mux,
	maxUDPPayload uint64,
	datagrams *session.DatagramMux,
) (*SessionRouter, error) {
	route, err := makeSessionRoute(mux, maxUDPPayload, datagrams)
	if err != nil {
		return nil, err
	}
	route.id = 1
	return &SessionRouter{active: route, routes: []sessionRoute{route}, nextRouteID: 2}, nil
}

func (r *SessionRouter) Switch(
	mux *session.Mux,
	maxUDPPayload uint64,
	datagrams *session.DatagramMux,
) error {
	route, err := makeSessionRoute(mux, maxUDPPayload, datagrams)
	if err != nil {
		return err
	}
	return r.switchRoute(route)
}

// Add admits one independently authenticated NP/2 session into the bounded
// TCP flow pool. UDP remains pinned to the primary route installed by New or
// Switch.
func (r *SessionRouter) Add(
	mux *session.Mux,
	maxUDPPayload uint64,
	datagrams *session.DatagramMux,
) (uint64, error) {
	route, err := makeSessionRoute(mux, maxUDPPayload, datagrams)
	if err != nil {
		return 0, err
	}
	return r.addRoute(route)
}

// Remove withdraws a secondary route from future TCP flow selection. Existing
// streams keep their original Mux and are closed by the runtime that owns it.
func (r *SessionRouter) Remove(id uint64) bool {
	return r.removeRoute(id)
}

// Promote makes an existing healthy pool member the primary route used for
// UDP and future route snapshots without discarding the remaining carrier
// pool. Existing streams keep the route on which they were opened.
func (r *SessionRouter) Promote(id uint64) error {
	if r == nil || id == 0 {
		return ErrInvalidStackConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, route := range r.routes {
		if route.id == id && route.open != nil {
			r.active = route
			return nil
		}
	}
	return ErrInvalidStackConfig
}

func makeSessionRoute(
	mux *session.Mux,
	maxUDPPayload uint64,
	datagrams *session.DatagramMux,
) (sessionRoute, error) {
	if mux == nil || (maxUDPPayload != 0 &&
		(maxUDPPayload < 1200 || maxUDPPayload > proxy.MaxUDPDatagramPayload)) ||
		(datagrams != nil && (maxUDPPayload == 0 || !datagrams.Enabled())) {
		return sessionRoute{}, ErrInvalidStackConfig
	}
	return sessionRoute{
		open: func(ctx context.Context, metadata []byte) (streamConnection, error) {
			return mux.Open(ctx, metadata)
		},
		activeStreams: func() uint64 { return mux.Stats().ActiveStreams },
		datagrams:     datagrams, maxUDPPayload: maxUDPPayload,
	}, nil
}

func (r *SessionRouter) switchRoute(route sessionRoute) error {
	if r == nil || route.open == nil || (route.maxUDPPayload != 0 &&
		(route.maxUDPPayload < 1200 || route.maxUDPPayload > proxy.MaxUDPDatagramPayload)) ||
		(route.datagrams != nil && (route.maxUDPPayload == 0 || !route.datagrams.Enabled())) {
		return ErrInvalidStackConfig
	}
	r.mu.Lock()
	route.id = 1
	r.active = route
	r.routes = []sessionRoute{route}
	r.nextRouteID = 2
	r.tieCursor = 0
	r.mu.Unlock()
	return nil
}

func (r *SessionRouter) openStream(ctx context.Context, metadata []byte) (streamConnection, error) {
	opener, err := r.pinStreamOpener()
	if err != nil {
		return nil, err
	}
	return opener(ctx, metadata)
}

func (r *SessionRouter) pinStreamOpener() (streamOpenFunc, error) {
	r.mu.RLock()
	continuityRouter := r.continuity
	clientRoutes := r.clientRoutes
	r.mu.RUnlock()
	if continuityRouter != nil {
		r.assignments.Add(1)
		return func(ctx context.Context, metadata []byte) (streamConnection, error) {
			if clientRoutes != nil {
				var err error
				metadata, err = clientRoutes.rewrite(metadata)
				if err != nil {
					return nil, err
				}
			}
			return continuityRouter.openStream(ctx, metadata)
		}, nil
	}
	route, err := r.selectLeastLoaded()
	if err != nil {
		return nil, err
	}
	r.assignments.Add(1)
	return func(ctx context.Context, metadata []byte) (streamConnection, error) {
		if clientRoutes != nil {
			var err error
			metadata, err = clientRoutes.rewrite(metadata)
			if err != nil {
				return nil, err
			}
		}
		return route.open(ctx, metadata)
	}, nil
}

func (r *SessionRouter) addRoute(route sessionRoute) (uint64, error) {
	if r == nil || route.open == nil || (route.maxUDPPayload != 0 &&
		(route.maxUDPPayload < 1200 || route.maxUDPPayload > proxy.MaxUDPDatagramPayload)) ||
		(route.datagrams != nil && (route.maxUDPPayload == 0 || !route.datagrams.Enabled())) {
		return 0, ErrInvalidStackConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.routes) == 0 {
		if r.active.open == nil {
			return 0, ErrInvalidStackConfig
		}
		r.active.id = 1
		r.routes = []sessionRoute{r.active}
		r.nextRouteID = 2
	}
	if len(r.routes) >= 3 {
		return 0, ErrCarrierPoolFull
	}
	if r.nextRouteID < 2 {
		r.nextRouteID = 2
	}
	route.id = r.nextRouteID
	r.nextRouteID++
	r.routes = append(r.routes, route)
	return route.id, nil
}

func (r *SessionRouter) removeRoute(id uint64) bool {
	if r == nil || id == 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for index, route := range r.routes {
		if route.id != id || route.id == r.active.id {
			continue
		}
		r.routes = append(r.routes[:index], r.routes[index+1:]...)
		return true
	}
	return false
}

func (r *SessionRouter) selectLeastLoaded() (sessionRoute, error) {
	if r == nil {
		return sessionRoute{}, ErrInvalidStackConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	routes := r.routes
	if len(routes) == 0 && r.active.open != nil {
		routes = []sessionRoute{r.active}
	}
	if len(routes) == 0 {
		return sessionRoute{}, ErrInvalidStackConfig
	}
	minimum := ^uint64(0)
	ties := make([]sessionRoute, 0, len(routes))
	for _, route := range routes {
		load := uint64(0)
		if route.activeStreams != nil {
			load = route.activeStreams()
		}
		if load < minimum {
			minimum = load
			ties = append(ties[:0], route)
		} else if load == minimum {
			ties = append(ties, route)
		}
	}
	selected := ties[r.tieCursor%uint64(len(ties))]
	r.tieCursor++
	return selected, nil
}

// PoolStats reports bounded, destination-free routing diagnostics.
func (r *SessionRouter) PoolStats() (healthy int, assignments uint64) {
	if r == nil {
		return 0, 0
	}
	r.mu.RLock()
	healthy = len(r.routes)
	if healthy == 0 && r.active.open != nil {
		healthy = 1
	}
	r.mu.RUnlock()
	return healthy, r.assignments.Load()
}

func (r *SessionRouter) openUDP(
	ctx context.Context,
	metadata []byte,
) (streamConnection, *session.DatagramEndpoint, uint64, error) {
	r.mu.RLock()
	clientRoutes := r.clientRoutes
	r.mu.RUnlock()
	if clientRoutes != nil {
		var err error
		metadata, err = clientRoutes.rewrite(metadata)
		if err != nil {
			return nil, nil, 0, err
		}
	}
	route, err := r.snapshot()
	if err != nil {
		return nil, nil, 0, err
	}
	if route.maxUDPPayload == 0 {
		return nil, nil, 0, ErrUDPUnsupported
	}
	request, err := proxy.DecodeOpenRequest(metadata)
	if err != nil {
		return nil, nil, 0, err
	}
	// HTTP/3 application traffic sent through a reliable-only carrier becomes
	// QUIC-over-TCP. A lost outer TCP segment then stalls both reliability
	// layers and is materially slower than the application's normal HTTPS/TCP
	// fallback. Reject only QUIC's standard destination port before opening an
	// NP/2 stream; DNS, calls, games, and every other UDP destination remain
	// available. Fast carrier datagrams preserve native QUIC semantics.
	if route.datagrams == nil &&
		(request.Command == proxy.CommandUDPFixed || request.Command == proxy.CommandUDPClientRoute) &&
		request.Target.Port == 443 {
		r.quicFallbacks.Add(1)
		return nil, nil, 0, ErrReliableQUICFallback
	}
	stream, err := route.open(ctx, metadata)
	if err != nil {
		return nil, nil, 0, err
	}
	if route.datagrams == nil {
		return stream, nil, route.maxUDPPayload, nil
	}
	identified, ok := stream.(interface{ ID() uint64 })
	if !ok || identified.ID() == 0 {
		_ = stream.Close()
		return nil, nil, 0, session.ErrInvalidConfig
	}
	endpoint, err := route.datagrams.OpenEndpoint(identified.ID())
	if err != nil {
		_ = stream.Close()
		return nil, nil, 0, err
	}
	return stream, endpoint, route.maxUDPPayload, nil
}

// UDPMode reports the active packet treatment using a bounded diagnostic
// value suitable for the iOS provider UI and production telemetry.
func (r *SessionRouter) UDPMode() string {
	route, err := r.snapshot()
	if err != nil || route.maxUDPPayload == 0 {
		return "disabled"
	}
	if route.datagrams != nil {
		return "fast-datagram"
	}
	return "reliable-stream-quic-fallback"
}

// QUICFallbackCount is the cumulative number of UDP/443 flows rejected so
// applications can retry over HTTPS/TCP without QUIC-over-TCP head-of-line
// blocking.
func (r *SessionRouter) QUICFallbackCount() uint64 {
	if r == nil {
		return 0
	}
	return r.quicFallbacks.Load()
}

func (r *SessionRouter) snapshot() (sessionRoute, error) {
	if r == nil {
		return sessionRoute{}, ErrInvalidStackConfig
	}
	r.mu.RLock()
	route := r.active
	r.mu.RUnlock()
	if route.open == nil {
		return sessionRoute{}, ErrInvalidStackConfig
	}
	return route, nil
}
