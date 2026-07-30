package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/grammar"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/socks5"
	"neproto.local/chameleon/internal/tunstack"
)

const (
	desktopConstellationControlTimeout = 5 * time.Second
	desktopConstellationRetryDelay     = 2 * time.Second
	desktopConstellationPollInterval   = time.Second
)

type desktopConstellationRoute struct {
	id            uint64
	authenticated *session.Authenticated
	control       *constellation.ControlChannel
	lastPayload   uint64
	lastSample    time.Time
	lastActivity  time.Time
	grammarLease  grammar.Lease
}

// desktopConstellationRuntime owns the complete desktop carrier pool. TCP
// flows use logical continuity, while new UDP associations select any healthy
// authenticated route. Losing one physical carrier therefore does not stop the
// local SOCKS service or redial an already admitted TCP target.
type desktopConstellationRuntime struct {
	ctx    context.Context
	cancel context.CancelFunc

	clientConfig config.Client
	connect      runtimeClientConnect
	control      *constellation.ClientControl
	router       *tunstack.ClientContinuityRouter
	grammar      *grammar.Driver
	target       int

	mu           sync.Mutex
	routes       map[uint64]*desktopConstellationRoute
	nextRouteID  uint64
	routeChanged chan struct{}
	terminalErr  error
	closed       bool

	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once
	wait      sync.WaitGroup
}

func newDesktopConstellationRuntime(
	ctx context.Context,
	clientConfig config.Client,
	initial *session.Authenticated,
	connect runtimeClientConnect,
) (*desktopConstellationRuntime, error) {
	if ctx == nil || initial == nil || initial.Mux == nil || connect == nil ||
		!clientConfig.EnableConstellation || clientConfig.MaxParallelCarriers < 1 ||
		clientConfig.MaxParallelCarriers > productionConstellationMaxLeases {
		return nil, config.ErrInvalidConfig
	}
	runtimeContext, cancel := context.WithCancel(ctx)
	clientControl, err := constellation.NewClientControl(nil)
	if err != nil {
		cancel()
		return nil, err
	}
	controlContext, controlCancel := context.WithTimeout(runtimeContext, desktopConstellationControlTimeout)
	err = clientControl.Create(controlContext, initial)
	controlCancel()
	if err != nil {
		cancel()
		return nil, err
	}
	state := clientControl.State()
	maximumFlows := min(max(clientConfig.MaxStreams, 1), productionConstellationMaxFlows)
	primaryControl, err := constellation.NewControlChannel(runtimeContext, constellation.ControlChannelConfig{
		Mux: initial.Mux, ConstellationID: state.ConstellationID,
		FirstMessageID: state.NextMessageID, MaxFlows: maximumFlows,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	router, err := tunstack.NewClientContinuityRouter(tunstack.ClientContinuityRouterConfig{
		Context: runtimeContext,
		Initial: tunstack.ContinuityRoute{
			ID: 1, Mux: initial.Mux, Control: primaryControl,
			ConstellationID: state.ConstellationID, LeaseKey: state.LeaseKey,
			SupportsDatagram: sessionSupportsDatagrams(initial),
		},
		MaxFlows: maximumFlows, JournalBytes: productionConstellationJournalBytes,
		AckEveryBytes:    productionConstellationAckEveryBytes,
		MigrationTimeout: productionConstellationMigration,
		ControlTimeout:   desktopConstellationControlTimeout,
	})
	if err != nil {
		_ = primaryControl.Close()
		cancel()
		return nil, err
	}
	now := time.Now()
	grammarDriver, err := grammar.NewDriver(grammar.DefaultManifest(), nil)
	if err != nil {
		_ = router.Close()
		_ = primaryControl.Close()
		cancel()
		return nil, err
	}
	primaryGrammarLease, err := grammarDriver.Acquire(initial.Carrier, now)
	if err != nil {
		_ = router.Close()
		_ = primaryControl.Close()
		cancel()
		return nil, err
	}
	runtime := &desktopConstellationRuntime{
		ctx: runtimeContext, cancel: cancel, clientConfig: clientConfig, connect: connect,
		control: clientControl, router: router, grammar: grammarDriver,
		target: clientConfig.MaxParallelCarriers,
		routes: map[uint64]*desktopConstellationRoute{1: {
			id: 1, authenticated: initial, control: primaryControl,
			lastSample: now, lastActivity: now, grammarLease: primaryGrammarLease,
		}},
		nextRouteID: 2, routeChanged: make(chan struct{}), done: make(chan struct{}),
	}
	runtime.wait.Add(2)
	go runtime.watchRoute(runtime.routes[1])
	go runtime.maintainPool()
	return runtime, nil
}

func (r *desktopConstellationRuntime) Connect(
	ctx context.Context,
	request socks5.Request,
) (io.ReadWriteCloser, error) {
	if r == nil || r.router == nil || ctx == nil {
		return nil, &socks5.ReplyError{Code: socks5.ReplyGeneralFailure}
	}
	metadata, err := proxy.EncodeTarget(proxy.Target{Host: request.Host, Port: request.Port})
	if err != nil {
		return nil, &socks5.ReplyError{Code: socks5.ReplyAddressNotSupported}
	}
	connection, err := r.router.OpenStream(ctx, metadata)
	if err == nil {
		return connection, nil
	}
	var rejection *session.RejectError
	if errors.As(err, &rejection) && rejection.Code >= socks5.ReplyGeneralFailure &&
		rejection.Code <= socks5.ReplyAddressNotSupported {
		return nil, &socks5.ReplyError{Code: rejection.Code}
	}
	return nil, &socks5.ReplyError{Code: socks5.ReplyGeneralFailure}
}

func (r *desktopConstellationRuntime) AssociateUDP(
	ctx context.Context,
) (socks5.UDPAssociation, error) {
	if r == nil || ctx == nil {
		return nil, &socks5.ReplyError{Code: socks5.ReplyCommandNotSupported}
	}
	r.mu.Lock()
	var selected *session.Authenticated
	selectedID := ^uint64(0)
	for id, route := range r.routes {
		if id < selectedID && route != nil && route.authenticated != nil &&
			route.authenticated.Mux != nil && route.authenticated.Mux.Err() == nil &&
			sessionSupportsReliableUDP(route.authenticated) {
			selected = route.authenticated
			selectedID = id
		}
	}
	r.mu.Unlock()
	if selected == nil {
		return nil, &socks5.ReplyError{Code: socks5.ReplyCommandNotSupported}
	}
	parameters, _ := selected.Extensions()
	connector := proxy.Connector{Mux: selected.Mux, MaxUDPPayload: parameters.MaxUDPPayload}
	if sessionSupportsDatagrams(selected) {
		connector.Datagrams = selected.Datagrams
	}
	return connector.AssociateUDP(ctx)
}

func (r *desktopConstellationRuntime) Wait(ctx context.Context) error {
	if r == nil || ctx == nil {
		return config.ErrInvalidConfig
	}
	select {
	case <-r.done:
		r.mu.Lock()
		err := r.terminalErr
		r.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *desktopConstellationRuntime) Close() error {
	if r == nil {
		return nil
	}
	var closeErrors []error
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		routes := make([]*desktopConstellationRoute, 0, len(r.routes))
		for _, route := range r.routes {
			routes = append(routes, route)
		}
		clear(r.routes)
		r.signalRouteChangedLocked()
		r.mu.Unlock()
		r.cancel()
		closeErrors = append(closeErrors, r.router.Close())
		for _, route := range routes {
			r.grammar.Release(route.grammarLease.ID)
			if route.control != nil {
				closeErrors = append(closeErrors, route.control.Close())
			}
			if route.authenticated != nil && route.authenticated.Mux != nil {
				closeErrors = append(closeErrors, route.authenticated.Mux.Close())
			}
		}
		r.wait.Wait()
		r.doneOnce.Do(func() { close(r.done) })
	})
	return errors.Join(closeErrors...)
}

func (r *desktopConstellationRuntime) maintainPool() {
	defer r.wait.Done()
	ticker := time.NewTicker(desktopConstellationPollInterval)
	defer ticker.Stop()
	var emptySince time.Time
	for {
		r.refreshMetrics()
		if r.rotateExpired(time.Now()) {
			continue
		}
		r.mu.Lock()
		count := len(r.routes)
		closed := r.closed
		notify := r.routeChanged
		r.mu.Unlock()
		if closed {
			return
		}
		if count == 0 {
			if emptySince.IsZero() {
				emptySince = time.Now()
			} else if time.Since(emptySince) >= productionConstellationMigration {
				r.terminate(session.ErrCarrierLost)
				return
			}
		} else {
			emptySince = time.Time{}
		}
		if count < r.target {
			if err := r.addRoute(); err == nil {
				continue
			}
			timer := time.NewTimer(desktopConstellationRetryDelay)
			select {
			case <-timer.C:
			case <-notify:
				stopDesktopTimer(timer)
			case <-r.ctx.Done():
				stopDesktopTimer(timer)
				return
			}
			continue
		}
		select {
		case <-ticker.C:
		case <-notify:
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *desktopConstellationRuntime) addRoute() error {
	authenticated, err := r.connect(r.ctx, r.clientConfig)
	if err != nil {
		return err
	}
	attachContext, cancel := context.WithTimeout(r.ctx, desktopConstellationControlTimeout)
	err = r.control.Attach(attachContext, authenticated)
	cancel()
	if err != nil {
		_ = authenticated.Mux.Close()
		return err
	}
	state := r.control.State()
	maximumFlows := min(max(r.clientConfig.MaxStreams, 1), productionConstellationMaxFlows)
	control, err := constellation.NewControlChannel(r.ctx, constellation.ControlChannelConfig{
		Mux: authenticated.Mux, ConstellationID: state.ConstellationID,
		FirstMessageID: state.NextMessageID, MaxFlows: maximumFlows,
	})
	if err != nil {
		_ = authenticated.Mux.Close()
		return err
	}
	now := time.Now()
	grammarLease, err := r.grammar.Acquire(authenticated.Carrier, now)
	if err != nil {
		_ = control.Close()
		_ = authenticated.Mux.Close()
		return err
	}
	r.mu.Lock()
	if r.closed || len(r.routes) >= r.target {
		r.mu.Unlock()
		r.grammar.Release(grammarLease.ID)
		_ = control.Close()
		_ = authenticated.Mux.Close()
		if r.closed {
			return context.Canceled
		}
		return tunstack.ErrCarrierPoolFull
	}
	routeID := r.nextRouteID
	r.nextRouteID++
	route := &desktopConstellationRoute{
		id: routeID, authenticated: authenticated, control: control,
		lastSample: now, lastActivity: now, grammarLease: grammarLease,
	}
	err = r.router.AddRoute(tunstack.ContinuityRoute{
		ID: routeID, Mux: authenticated.Mux, Control: control,
		ConstellationID: state.ConstellationID, LeaseKey: state.LeaseKey,
		SupportsDatagram: sessionSupportsDatagrams(authenticated),
	})
	if err == nil {
		r.routes[routeID] = route
		r.signalRouteChangedLocked()
		r.wait.Add(1)
	}
	r.mu.Unlock()
	if err != nil {
		r.grammar.Release(grammarLease.ID)
		_ = control.Close()
		_ = authenticated.Mux.Close()
		return err
	}
	go r.watchRoute(route)
	return nil
}

func (r *desktopConstellationRuntime) watchRoute(route *desktopConstellationRoute) {
	defer r.wait.Done()
	err := route.authenticated.Mux.Wait(context.Background())
	r.mu.Lock()
	current := r.routes[route.id]
	if r.closed || current != route {
		r.mu.Unlock()
		return
	}
	delete(r.routes, route.id)
	r.signalRouteChangedLocked()
	r.mu.Unlock()
	r.grammar.Release(route.grammarLease.ID)
	r.router.RemoveRoute(route.id)
	_ = route.control.Close()
	_ = route.authenticated.Mux.Close()
	if err != nil && !errors.Is(err, context.Canceled) {
		// The pool manager reconnects during the bounded continuity window.
		return
	}
}

func (r *desktopConstellationRuntime) refreshMetrics() {
	now := time.Now()
	r.mu.Lock()
	updates := make([]struct {
		id      uint64
		metrics tunstack.ContinuityRouteMetrics
	}, 0, len(r.routes))
	for _, route := range r.routes {
		stats := route.authenticated.Mux.Stats()
		total := stats.SentCellPayloadBytes + stats.ReceivedPayloadBytes
		elapsed := now.Sub(route.lastSample)
		throughput := uint64(0)
		if elapsed > 0 && total >= route.lastPayload {
			throughput = uint64(float64(total-route.lastPayload) * 8 / elapsed.Seconds())
		}
		if total != route.lastPayload {
			route.lastActivity = now
		}
		route.lastPayload = total
		route.lastSample = now
		updates = append(updates, struct {
			id      uint64
			metrics tunstack.ContinuityRouteMetrics
		}{id: route.id, metrics: tunstack.ContinuityRouteMetrics{ThroughputBPS: throughput}})
	}
	r.mu.Unlock()
	for _, update := range updates {
		_ = r.router.UpdateRouteMetrics(update.id, update.metrics)
	}
}

func (r *desktopConstellationRuntime) rotateExpired(now time.Time) bool {
	if r == nil || r.grammar == nil || now.IsZero() {
		return false
	}
	r.mu.Lock()
	if r.closed || len(r.routes) <= 1 {
		r.mu.Unlock()
		return false
	}
	var expired *desktopConstellationRoute
	for _, route := range r.routes {
		if route != nil && route.grammarLease.ShouldRotate(now, route.lastActivity) {
			if expired == nil || route.id < expired.id {
				expired = route
			}
		}
	}
	if expired == nil {
		r.mu.Unlock()
		return false
	}
	delete(r.routes, expired.id)
	r.signalRouteChangedLocked()
	r.mu.Unlock()
	r.grammar.Release(expired.grammarLease.ID)
	r.router.RemoveRoute(expired.id)
	_ = expired.control.Close()
	_ = expired.authenticated.Mux.Close()
	return true
}

func (r *desktopConstellationRuntime) terminate(err error) {
	r.mu.Lock()
	if !r.closed && r.terminalErr == nil {
		r.terminalErr = err
	}
	r.mu.Unlock()
	r.doneOnce.Do(func() { close(r.done) })
}

func (r *desktopConstellationRuntime) signalRouteChangedLocked() {
	close(r.routeChanged)
	r.routeChanged = make(chan struct{})
}

func sessionSupportsReliableUDP(authenticated *session.Authenticated) bool {
	if authenticated == nil || authenticated.Mux == nil {
		return false
	}
	parameters, negotiated := authenticated.Extensions()
	return negotiated && parameters.Capabilities&protocol.CapabilityReliableUDP != 0 &&
		parameters.MaxUDPPayload >= 1200 && parameters.MaxUDPPayload <= proxy.MaxUDPDatagramPayload
}

func sessionSupportsDatagrams(authenticated *session.Authenticated) bool {
	if !sessionSupportsReliableUDP(authenticated) || authenticated.Datagrams == nil ||
		!authenticated.Datagrams.Enabled() {
		return false
	}
	parameters, _ := authenticated.Extensions()
	return parameters.Capabilities&protocol.CapabilityUnreliableDatagrams != 0
}

func stopDesktopTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
