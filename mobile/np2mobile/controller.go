// Package np2mobile exposes the bounded NP/2 client lifecycle to Apple
// NetworkExtension targets through a gomobile-generated XCFramework.
package np2mobile

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/tunstack"
)

var (
	ErrAlreadyRunning       = errors.New("NP/2 client is already running")
	ErrStartTimeout         = errors.New("NP/2 client start timed out")
	ErrNotConnected         = errors.New("NP/2 client is not connected")
	ErrTunnelAlreadyStarted = errors.New("NP/2 packet tunnel is already running")
	ErrInvalidTunnelFD      = errors.New("invalid packet tunnel file descriptor")
	ErrMigrationInProgress  = errors.New("NP/2 carrier migration is already running")
	ErrCarrierPoolMismatch  = errors.New("NP/2 carrier pool member does not match the primary HTTPS carrier")
)

const (
	mobileMigrationTimeout     = 20 * time.Second
	carrierPoolHighRate        = int64(2 * 1024 * 1024)
	carrierPoolHighStreams     = uint64(4)
	carrierPoolPollInterval    = time.Second
	carrierPoolIdleTimeout     = 30 * time.Second
	carrierPoolRetryDelay      = 10 * time.Second
	carrierPoolWarmDelayMin    = 250 * time.Millisecond
	carrierPoolWarmDelaySpread = 500 * time.Millisecond
	mobileContinuityMaxFlows   = 128
	mobileContinuityJournal    = 256 * 1024
	mobileContinuityAckEvery   = 64 * 1024
	mobileContinuityMigration  = 15 * time.Second
	mobileContinuityControl    = 3 * time.Second
)

type poolActivity struct {
	BytesPerSecond int64
	ActiveStreams  uint64
}

type mobileRuntime interface {
	StartTunnel(int) error
	Wait(context.Context) error
	Close() error
	ServerAddresses() string
	CarrierName() string
	TrafficStats() trafficStats
	NetworkChanged(context.Context) (migrationResult, error)
}

type catalogRuntime interface {
	CatalogJSON(context.Context) (string, error)
}

type clientRouteRuntime interface {
	SetClientRoutes(*tunstack.ClientRoutePolicy) error
}

type migrationResult struct {
	NativePath  bool
	Reconnected bool
	Migrated    bool
}

type trafficStats struct {
	UploadBytesPerSecond    int64
	DownloadBytesPerSecond  int64
	UploadTotalBytes        int64
	DownloadTotalBytes      int64
	UDPMode                 string
	QUICFallbacks           int64
	CarrierPoolTarget       int64
	CarrierPoolHealthy      int64
	CarrierPoolAssignments  int64
	CarrierPoolScaleUps     int64
	CarrierPoolFailures     int64
	DNSAttributionQueries   int64
	DNSAttributionResponses int64
	DNSAttributionHits      int64
	DNSAttributionMisses    int64
	DNSAttributionCached    int64
	FirstFlightDomainHits   int64
	FirstFlightFallbacks    int64
}

type clientConnector func(context.Context, config.Client) (mobileRuntime, error)

type controller struct {
	mu               sync.Mutex
	connect          clientConnector
	state            string
	lastErr          string
	cancel           context.CancelFunc
	done             chan struct{}
	runtime          mobileRuntime
	serverAddr       string
	carrier          string
	generation       uint64
	migrationCancel  context.CancelFunc
	migrationTimeout time.Duration
	networkChanges   uint64
	reconnects       uint64
	migrations       uint64
	clientRoutes     *tunstack.ClientRoutePolicy
}

func newController(connect clientConnector) *controller {
	return &controller{connect: connect, state: "stopped"}
}

func (c *controller) start(raw []byte, secret string) error {
	loaded, err := config.ParseMobileClientBytes(raw, secret)
	if err != nil {
		return fmt.Errorf("validate mobile profile: %w", err)
	}

	c.mu.Lock()
	if c.state == "starting" || c.state == "connected" || c.state == "running" ||
		c.state == "migrating" || c.state == "stopping" {
		c.mu.Unlock()
		return ErrAlreadyRunning
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.generation++
	generation := c.generation
	c.state = "starting"
	c.lastErr = ""
	c.cancel = cancel
	c.done = done
	c.runtime = nil
	c.serverAddr = ""
	c.carrier = ""
	c.migrationTimeout = mobileConnectDeadline(loaded)
	clientRoutes := c.clientRoutes
	c.mu.Unlock()

	type connectionResult struct {
		runtime mobileRuntime
		err     error
	}
	result := make(chan connectionResult, 1)
	go func() {
		runtime, connectErr := c.connect(ctx, loaded)
		result <- connectionResult{runtime: runtime, err: connectErr}
	}()

	timeout := mobileConnectDeadline(loaded)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case connected := <-result:
		if connected.err != nil {
			c.failStart(generation, connected.err, done)
			return connected.err
		}
		if connected.runtime == nil {
			c.failStart(generation, ErrNotConnected, done)
			return ErrNotConnected
		}
		if clientRoutes != nil {
			routed, ok := connected.runtime.(clientRouteRuntime)
			if !ok {
				_ = connected.runtime.Close()
				c.failStart(generation, tunstack.ErrInvalidClientRoutes, done)
				return tunstack.ErrInvalidClientRoutes
			}
			if err := routed.SetClientRoutes(clientRoutes); err != nil {
				_ = connected.runtime.Close()
				c.failStart(generation, err, done)
				return err
			}
		}
		c.mu.Lock()
		if c.generation != generation || c.state != "starting" {
			c.mu.Unlock()
			_ = connected.runtime.Close()
			return context.Canceled
		}
		c.runtime = connected.runtime
		c.serverAddr = connected.runtime.ServerAddresses()
		c.carrier = connected.runtime.CarrierName()
		if c.serverAddr == "" || c.carrier == "" {
			c.mu.Unlock()
			_ = connected.runtime.Close()
			c.failStart(generation, ErrNotConnected, done)
			return ErrNotConnected
		}
		c.state = "connected"
		c.mu.Unlock()
		go c.monitor(generation, ctx, connected.runtime, done)
		return nil
	case <-timer.C:
		cancel()
		c.failStart(generation, ErrStartTimeout, done)
		go func() {
			connected := <-result
			if connected.runtime != nil {
				_ = connected.runtime.Close()
			}
		}()
		return ErrStartTimeout
	case <-ctx.Done():
		c.failStart(generation, ctx.Err(), done)
		return ctx.Err()
	}
}

func (c *controller) setClientRoutesJSON(raw string) error {
	if len(raw) == 0 || len(raw) > proxy.MaxCatalogPayload {
		return tunstack.ErrInvalidClientRoutes
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	var routes []cluster.Route
	if err := decoder.Decode(&routes); err != nil {
		return tunstack.ErrInvalidClientRoutes
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return tunstack.ErrInvalidClientRoutes
	}
	policy, err := tunstack.NewClientRoutePolicy(routes)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == "starting" || c.state == "connected" || c.state == "running" ||
		c.state == "migrating" || c.state == "stopping" {
		return ErrAlreadyRunning
	}
	c.clientRoutes = policy
	return nil
}

func (c *controller) startTunnel(fileDescriptor int) error {
	if fileDescriptor < 0 {
		return ErrInvalidTunnelFD
	}
	c.mu.Lock()
	if c.state == "running" {
		c.mu.Unlock()
		return ErrTunnelAlreadyStarted
	}
	if c.state != "connected" || c.runtime == nil {
		c.mu.Unlock()
		return ErrNotConnected
	}
	runtime := c.runtime
	generation := c.generation
	c.mu.Unlock()

	if err := runtime.StartTunnel(fileDescriptor); err != nil {
		c.mu.Lock()
		if c.generation == generation {
			c.state = "failed"
			c.lastErr = err.Error()
		}
		cancel := c.cancel
		c.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		_ = runtime.Close()
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation || c.state != "connected" {
		_ = runtime.Close()
		return context.Canceled
	}
	c.state = "running"
	return nil
}

func (c *controller) failStart(generation uint64, err error, done chan struct{}) {
	c.mu.Lock()
	if c.generation == generation {
		c.state = "failed"
		c.lastErr = err.Error()
		c.cancel = nil
	}
	c.mu.Unlock()
	close(done)
}

func (c *controller) monitor(generation uint64, ctx context.Context, runtime mobileRuntime, done chan struct{}) {
	runErr := runtime.Wait(ctx)
	c.mu.Lock()
	if c.generation == generation {
		c.cancel = nil
		c.runtime = nil
		c.serverAddr = ""
		c.carrier = ""
		if c.state != "failed" {
			if runErr != nil && ctx.Err() == nil && !errors.Is(runErr, context.Canceled) {
				c.state = "failed"
				c.lastErr = runErr.Error()
			} else {
				c.state = "stopped"
				c.lastErr = ""
			}
		}
	}
	c.mu.Unlock()
	_ = runtime.Close()
	close(done)
}

func (c *controller) stop() {
	c.mu.Lock()
	cancel := c.cancel
	migrationCancel := c.migrationCancel
	runtime := c.runtime
	if cancel == nil && runtime == nil {
		c.state = "stopped"
		c.lastErr = ""
		c.migrationTimeout = 0
		c.mu.Unlock()
		return
	}
	// Invalidate this generation before beginning potentially slow carrier and
	// userspace-stack cleanup. NetworkExtension requires stopTunnel to complete
	// promptly; the OS owns the original utun descriptor and will tear it down
	// after the provider acknowledges the stop request.
	c.generation++
	c.state = "stopped"
	c.lastErr = ""
	c.cancel = nil
	c.migrationCancel = nil
	c.done = nil
	c.runtime = nil
	c.serverAddr = ""
	c.carrier = ""
	c.migrationTimeout = 0
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if migrationCancel != nil {
		migrationCancel()
	}
	if runtime != nil {
		go func() { _ = runtime.Close() }()
	}
}

func (c *controller) networkChanged() error {
	app.ResetClientCarrierPreferences()
	c.mu.Lock()
	c.networkChanges++
	if c.state == "migrating" {
		c.mu.Unlock()
		return ErrMigrationInProgress
	}
	if c.state != "running" || c.runtime == nil {
		c.mu.Unlock()
		return ErrNotConnected
	}
	runtime := c.runtime
	generation := c.generation
	migrationTimeout := c.migrationTimeout
	if migrationTimeout <= 0 {
		migrationTimeout = mobileMigrationTimeout
	}
	c.state = "migrating"
	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	c.migrationCancel = cancel
	c.mu.Unlock()

	result, err := runtime.NetworkChanged(ctx)
	cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation || c.runtime != runtime {
		return context.Canceled
	}
	c.migrationCancel = nil
	if c.state != "migrating" {
		return err
	}
	if err != nil {
		// A failed reconnect does not tear down a carrier that still passed the
		// runtime's liveness checks. If it was already dead, monitor() moves the
		// controller to failed instead of reaching this branch.
		c.state = "running"
		c.lastErr = "carrier migration: " + err.Error()
		return err
	}
	c.state = "running"
	c.lastErr = ""
	c.serverAddr = runtime.ServerAddresses()
	c.carrier = runtime.CarrierName()
	if result.Reconnected {
		c.reconnects++
	}
	if result.Migrated || result.NativePath {
		c.migrations++
	}
	return nil
}

func (c *controller) stateName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *controller) lastError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *controller) serverAddresses() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverAddr
}

func (c *controller) carrierName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.carrier
}

func (c *controller) trafficStats() trafficStats {
	c.mu.Lock()
	runtime := c.runtime
	state := c.state
	c.mu.Unlock()
	if runtime == nil || (state != "connected" && state != "running" && state != "migrating") {
		return trafficStats{}
	}
	return runtime.TrafficStats()
}

func (c *controller) catalogJSON(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrNotConnected
	}
	c.mu.Lock()
	runtime := c.runtime
	state := c.state
	c.mu.Unlock()
	if runtime == nil || (state != "connected" && state != "running" && state != "migrating") {
		return "", ErrNotConnected
	}
	provider, ok := runtime.(catalogRuntime)
	if !ok {
		return "", ErrNotConnected
	}
	return provider.CatalogJSON(ctx)
}

func (c *controller) networkChangeCount() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.networkChanges
}

func (c *controller) reconnectCount() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reconnects
}

func (c *controller) migrationCount() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.migrations
}

type np2Runtime struct {
	mu                   sync.Mutex
	clientConfig         config.Client
	connect              func(context.Context, config.Client) (*session.Authenticated, error)
	session              *session.Authenticated
	router               *tunstack.SessionRouter
	stack                *tunstack.Stack
	closed               bool
	serverAddresses      string
	carrierName          string
	generation           uint64
	primaryRouteID       uint64
	retired              map[uint64]*session.Authenticated
	nextRetiredID        uint64
	done                 chan struct{}
	doneOnce             sync.Once
	terminalErr          error
	migrationInProgress  bool
	pendingTerminal      error
	nativeProbeTimeout   time.Duration
	drainTimeout         time.Duration
	poolContext          context.Context
	poolCancel           context.CancelFunc
	poolConfiguredTarget int
	poolTarget           int
	poolSessions         map[uint64]*session.Authenticated
	constellationControl *constellation.ClientControl
	continuityRouter     *tunstack.ClientContinuityRouter
	continuityMaxFlows   int
	controls             map[*session.Authenticated]*constellation.ControlChannel
	poolScaleUps         uint64
	poolFailures         uint64
}

func (r *np2Runtime) SetClientRoutes(policy *tunstack.ClientRoutePolicy) error {
	if r == nil || r.router == nil {
		return tunstack.ErrInvalidClientRoutes
	}
	return r.router.SetClientRoutes(policy)
}

func (r *np2Runtime) CatalogJSON(ctx context.Context) (string, error) {
	if r == nil || ctx == nil {
		return "", ErrNotConnected
	}
	r.mu.Lock()
	authenticated := r.session
	closed := r.closed
	r.mu.Unlock()
	if closed || authenticated == nil || authenticated.Mux == nil {
		return "", ErrNotConnected
	}
	raw, err := proxy.FetchCatalog(ctx, authenticated.Mux)
	if err != nil {
		return "", fmt.Errorf("fetch cluster catalog: %w", err)
	}
	return string(raw), nil
}

func connectRuntime(ctx context.Context, clientConfig config.Client) (mobileRuntime, error) {
	authenticated, err := app.ConnectClientHTTPSFirst(ctx, clientConfig)
	if err != nil {
		return nil, err
	}
	return newNP2Runtime(clientConfig, authenticated, app.ConnectClientHTTPSFirst)
}

func newNP2Runtime(
	clientConfig config.Client,
	authenticated *session.Authenticated,
	connect func(context.Context, config.Client) (*session.Authenticated, error),
) (*np2Runtime, error) {
	if authenticated == nil || authenticated.Mux == nil || connect == nil {
		return nil, ErrNotConnected
	}
	addresses := append([]netip.Addr(nil), clientConfig.ServerAddresses...)
	addresses = append(addresses, authenticated.CarrierRemoteAddresses...)
	routes, err := canonicalRouteAddresses(addresses)
	if err != nil {
		_ = authenticated.Mux.Close()
		return nil, err
	}
	carrierName, err := mobileCarrierName(authenticated.Carrier)
	if err != nil {
		_ = authenticated.Mux.Close()
		return nil, err
	}
	maximumPayload, datagrams, err := authenticatedSessionRoute(authenticated)
	if err != nil {
		_ = authenticated.Mux.Close()
		return nil, err
	}
	router, err := tunstack.NewSessionRouter(authenticated.Mux, maximumPayload, datagrams)
	if err != nil {
		_ = authenticated.Mux.Close()
		return nil, err
	}
	poolContext, poolCancel := context.WithCancel(context.Background())
	continuityMaxFlows := min(max(clientConfig.MaxStreams, 1), mobileContinuityMaxFlows)
	constellationControl, continuityRouter, primaryControl, err := newMobileContinuity(
		poolContext, clientConfig, authenticated, router, continuityMaxFlows,
	)
	if err != nil {
		poolCancel()
		_ = authenticated.Mux.Close()
		return nil, err
	}
	poolConfiguredTarget := clientConfig.MaxParallelCarriers
	if poolConfiguredTarget < 1 {
		poolConfiguredTarget = 1
	}
	if poolConfiguredTarget > 3 {
		poolConfiguredTarget = 3
	}
	poolTarget := poolConfiguredTarget
	if carrierName != "https" {
		poolTarget = 1
	}
	runtime := &np2Runtime{
		clientConfig: clientConfig, connect: connect, session: authenticated, router: router,
		serverAddresses: routes, carrierName: carrierName, generation: 1,
		primaryRouteID: 1,
		retired:        make(map[uint64]*session.Authenticated), nextRetiredID: 1,
		done:               make(chan struct{}),
		nativeProbeTimeout: 1500 * time.Millisecond, drainTimeout: 30 * time.Second,
		poolContext: poolContext, poolCancel: poolCancel,
		poolConfiguredTarget: poolConfiguredTarget, poolTarget: poolTarget,
		poolSessions:         make(map[uint64]*session.Authenticated),
		constellationControl: constellationControl, continuityRouter: continuityRouter,
		continuityMaxFlows: continuityMaxFlows,
		controls:           make(map[*session.Authenticated]*constellation.ControlChannel),
	}
	if primaryControl != nil {
		runtime.controls[authenticated] = primaryControl
	}
	go runtime.watchSession(1, authenticated)
	return runtime, nil
}

func newMobileContinuity(
	runtimeContext context.Context,
	clientConfig config.Client,
	authenticated *session.Authenticated,
	router *tunstack.SessionRouter,
	maxFlows int,
) (
	*constellation.ClientControl,
	*tunstack.ClientContinuityRouter,
	*constellation.ControlChannel,
	error,
) {
	if !clientConfig.EnableConstellation {
		return nil, nil, nil, nil
	}
	if runtimeContext == nil || authenticated == nil || authenticated.Mux == nil || router == nil || maxFlows <= 0 {
		return nil, nil, nil, session.ErrInvalidConfig
	}
	clientControl, err := constellation.NewClientControl(nil)
	if err != nil {
		return nil, nil, nil, err
	}
	controlContext, cancel := context.WithTimeout(runtimeContext, mobileContinuityControl)
	err = clientControl.Create(controlContext, authenticated)
	cancel()
	if err != nil {
		return nil, nil, nil, err
	}
	state := clientControl.State()
	if !state.Ready {
		return nil, nil, nil, constellation.ErrContinuityState
	}
	control, err := constellation.NewControlChannel(runtimeContext, constellation.ControlChannelConfig{
		Mux: authenticated.Mux, ConstellationID: state.ConstellationID,
		FirstMessageID: state.NextMessageID, MaxFlows: maxFlows,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	continuityRouter, err := tunstack.NewClientContinuityRouter(tunstack.ClientContinuityRouterConfig{
		Context: runtimeContext,
		Initial: tunstack.ContinuityRoute{
			ID: 1, Mux: authenticated.Mux, Control: control,
			ConstellationID: state.ConstellationID, LeaseKey: state.LeaseKey,
			SupportsDatagram: authenticated.Datagrams != nil && authenticated.Datagrams.Enabled(),
		},
		MaxFlows: maxFlows, JournalBytes: mobileContinuityJournal,
		AckEveryBytes:    mobileContinuityAckEvery,
		MigrationTimeout: mobileContinuityMigration, ControlTimeout: mobileContinuityControl,
	})
	if err != nil {
		_ = control.Close()
		return nil, nil, nil, err
	}
	if err := router.EnableContinuity(continuityRouter); err != nil {
		_ = continuityRouter.Close()
		_ = control.Close()
		return nil, nil, nil, err
	}
	return clientControl, continuityRouter, control, nil
}

func (r *np2Runtime) attachContinuity(
	ctx context.Context,
	authenticated *session.Authenticated,
) (*constellation.ControlChannel, constellation.ClientControlState, error) {
	if r == nil || ctx == nil || authenticated == nil || authenticated.Mux == nil {
		return nil, constellation.ClientControlState{}, session.ErrInvalidConfig
	}
	r.mu.Lock()
	clientControl := r.constellationControl
	runtimeContext := r.poolContext
	maxFlows := r.continuityMaxFlows
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil, constellation.ClientControlState{}, context.Canceled
	}
	if clientControl == nil {
		return nil, constellation.ClientControlState{}, nil
	}
	attachContext, cancel := context.WithTimeout(ctx, mobileContinuityControl)
	stopRuntimeCancellation := context.AfterFunc(runtimeContext, cancel)
	err := clientControl.Attach(attachContext, authenticated)
	stopRuntimeCancellation()
	cancel()
	if err != nil {
		return nil, constellation.ClientControlState{}, err
	}
	state := clientControl.State()
	if !state.Ready {
		return nil, constellation.ClientControlState{}, constellation.ErrContinuityState
	}
	control, err := constellation.NewControlChannel(runtimeContext, constellation.ControlChannelConfig{
		Mux: authenticated.Mux, ConstellationID: state.ConstellationID,
		FirstMessageID: state.NextMessageID, MaxFlows: maxFlows,
	})
	if err != nil {
		return nil, constellation.ClientControlState{}, err
	}
	return control, state, nil
}

func mobileConnectDeadline(clientConfig config.Client) time.Duration {
	deadline := clientConfig.HTTPSTimeout.Duration + clientConfig.WebRTCTimeout.Duration
	if clientConfig.HTTP3Configured() {
		deadline += clientConfig.HTTP3Timeout.Duration
	}
	return deadline + 3*time.Second
}

func mobileCarrierName(kind protocol.CarrierKind) (string, error) {
	switch kind {
	case protocol.CarrierHTTP3:
		return "http3", nil
	case protocol.CarrierWebRTC:
		return "webrtc", nil
	case protocol.CarrierHTTPS:
		return "https", nil
	default:
		return "", errors.New("unsupported NP/2 carrier")
	}
}

func (r *np2Runtime) StartTunnel(fileDescriptor int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.session == nil || r.router == nil {
		return ErrNotConnected
	}
	if r.stack != nil {
		return ErrTunnelAlreadyStarted
	}
	stack, err := tunstack.StartWithSessionRouter(fileDescriptor, 1500, r.router)
	if err != nil {
		return err
	}
	r.stack = stack
	if r.poolConfiguredTarget > 1 {
		go r.runCarrierPool()
	}
	return nil
}

func (r *np2Runtime) Wait(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
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

func (r *np2Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.poolCancel()
	stack := r.stack
	continuityRouter := r.continuityRouter
	controls := make([]*constellation.ControlChannel, 0, len(r.controls))
	for _, control := range r.controls {
		if control != nil {
			controls = append(controls, control)
		}
	}
	clear(r.controls)
	sessions := make([]runtimeCloser, 0, 1+len(r.poolSessions)+len(r.retired))
	if r.session != nil && r.session.Mux != nil {
		sessions = append(sessions, r.session.Mux)
	}
	for _, pooled := range r.poolSessions {
		if pooled != nil && pooled.Mux != nil {
			sessions = append(sessions, pooled.Mux)
		}
	}
	clear(r.poolSessions)
	for _, retired := range r.retired {
		if retired != nil && retired.Mux != nil {
			sessions = append(sessions, retired.Mux)
		}
	}
	r.doneOnce.Do(func() { close(r.done) })
	r.mu.Unlock()
	var closeErrors []error
	if continuityRouter != nil {
		closeErrors = append(closeErrors, continuityRouter.Close())
	}
	for _, control := range controls {
		closeErrors = append(closeErrors, control.Close())
	}
	for _, current := range sessions {
		closeErrors = append(closeErrors, current.Close())
	}
	closeErrors = append(closeErrors, stack.Close())
	return errors.Join(closeErrors...)
}

func (r *np2Runtime) warmCarrierPool(ctx context.Context, desired int) error {
	if ctx == nil {
		return context.Canceled
	}
	r.mu.Lock()
	target := min(max(desired, 1), r.poolTarget)
	poolContext := r.poolContext
	if r.migrationInProgress {
		r.mu.Unlock()
		return ErrMigrationInProgress
	}
	r.mu.Unlock()
	attemptContext, cancel := context.WithCancel(ctx)
	stopPoolCancellation := context.AfterFunc(poolContext, cancel)
	defer func() {
		stopPoolCancellation()
		cancel()
	}()

	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return context.Canceled
		}
		healthy := 1 + len(r.poolSessions)
		connect := r.connect
		clientConfig := r.clientConfig
		primaryCarrier := r.carrierName
		r.mu.Unlock()
		if healthy >= target {
			return nil
		}
		if primaryCarrier != "https" {
			return ErrCarrierPoolMismatch
		}

		authenticated, err := connect(attemptContext, clientConfig)
		if err != nil {
			r.recordPoolFailure()
			return err
		}
		if authenticated == nil || authenticated.Mux == nil || authenticated.Carrier != protocol.CarrierHTTPS {
			if authenticated != nil && authenticated.Mux != nil {
				_ = authenticated.Mux.Close()
			}
			r.recordPoolFailure()
			return ErrCarrierPoolMismatch
		}
		maximumPayload, datagrams, err := authenticatedSessionRoute(authenticated)
		if err != nil {
			_ = authenticated.Mux.Close()
			r.recordPoolFailure()
			return err
		}
		addresses := append([]netip.Addr(nil), clientConfig.ServerAddresses...)
		addresses = append(addresses, authenticated.CarrierRemoteAddresses...)
		routes, routeErr := canonicalRouteAddresses(addresses)
		if routeErr != nil || !routeSetContains(r.serverAddresses, routes) {
			_ = authenticated.Mux.Close()
			if routeErr == nil {
				routeErr = errors.New("pooled carrier needs a new route exclusion")
			}
			r.recordPoolFailure()
			return routeErr
		}
		control, continuityState, err := r.attachContinuity(attemptContext, authenticated)
		if err != nil {
			_ = authenticated.Mux.Close()
			r.recordPoolFailure()
			return err
		}
		r.mu.Lock()
		if r.closed || r.migrationInProgress || r.carrierName != primaryCarrier ||
			r.poolTarget < target {
			r.mu.Unlock()
			if control != nil {
				_ = control.Close()
			}
			_ = authenticated.Mux.Close()
			if r.closed {
				return context.Canceled
			}
			return ErrMigrationInProgress
		}
		routeID, err := r.router.Add(authenticated.Mux, maximumPayload, datagrams)
		if err == nil && r.continuityRouter != nil {
			err = r.continuityRouter.AddRoute(tunstack.ContinuityRoute{
				ID: routeID, Mux: authenticated.Mux, Control: control,
				ConstellationID: continuityState.ConstellationID, LeaseKey: continuityState.LeaseKey,
				SupportsDatagram: authenticated.Datagrams != nil && authenticated.Datagrams.Enabled(),
			})
			if err != nil {
				r.router.Remove(routeID)
			}
		}
		if err == nil {
			r.poolSessions[routeID] = authenticated
			if control != nil {
				r.controls[authenticated] = control
			}
			r.poolScaleUps++
		}
		r.mu.Unlock()
		if err != nil {
			if control != nil {
				_ = control.Close()
			}
			_ = authenticated.Mux.Close()
			r.recordPoolFailure()
			return err
		}
		go r.watchPoolSession(routeID, authenticated)
	}
}

func (r *np2Runtime) watchPoolSession(routeID uint64, authenticated *session.Authenticated) {
	err := authenticated.Mux.Wait(context.Background())
	r.mu.Lock()
	if r.poolSessions[routeID] != authenticated {
		r.mu.Unlock()
		return
	}
	delete(r.poolSessions, routeID)
	control := r.controls[authenticated]
	delete(r.controls, authenticated)
	continuityRouter := r.continuityRouter
	if !r.closed && err != nil && !errors.Is(err, context.Canceled) {
		r.poolFailures++
	}
	r.mu.Unlock()
	if continuityRouter != nil {
		continuityRouter.RemoveRoute(routeID)
	}
	r.router.Remove(routeID)
	if control != nil {
		_ = control.Close()
	}
	_ = authenticated.Mux.Close()
}

func (r *np2Runtime) recordPoolFailure() {
	r.mu.Lock()
	if !r.closed {
		r.poolFailures++
	}
	r.mu.Unlock()
}

func (r *np2Runtime) carrierPoolStats() (
	target, healthy int,
	assignments, scaleUps, failures uint64,
) {
	healthy, assignments = r.router.PoolStats()
	r.mu.Lock()
	target, scaleUps, failures = r.poolTarget, r.poolScaleUps, r.poolFailures
	r.mu.Unlock()
	return target, healthy, assignments, scaleUps, failures
}

func (r *np2Runtime) reconcileCarrierPool(ctx context.Context, activity poolActivity) error {
	r.mu.Lock()
	target := r.poolTarget
	r.mu.Unlock()
	desired := 1
	if target > 1 {
		desired = 2
	}
	if target > 2 && (activity.BytesPerSecond >= carrierPoolHighRate ||
		activity.ActiveStreams >= carrierPoolHighStreams) {
		desired = 3
	}
	return r.warmCarrierPool(ctx, desired)
}

func (r *np2Runtime) retireIdlePoolMember() bool {
	r.mu.Lock()
	if r.closed || len(r.poolSessions) <= 1 {
		r.mu.Unlock()
		return false
	}
	var selectedID uint64
	var selected *session.Authenticated
	for routeID, authenticated := range r.poolSessions {
		if authenticated == nil || authenticated.Mux == nil ||
			authenticated.Mux.Stats().ActiveStreams != 0 || routeID <= selectedID {
			continue
		}
		selectedID, selected = routeID, authenticated
	}
	if selected == nil {
		r.mu.Unlock()
		return false
	}
	delete(r.poolSessions, selectedID)
	control := r.controls[selected]
	delete(r.controls, selected)
	continuityRouter := r.continuityRouter
	r.mu.Unlock()
	if continuityRouter != nil {
		continuityRouter.RemoveRoute(selectedID)
	}
	r.router.Remove(selectedID)
	if control != nil {
		_ = control.Close()
	}
	_ = selected.Mux.Close()
	return true
}

func (r *np2Runtime) runCarrierPool() {
	timer := time.NewTimer(carrierPoolWarmDelay())
	select {
	case <-timer.C:
	case <-r.poolContext.Done():
		timer.Stop()
		return
	}
	ticker := time.NewTicker(carrierPoolPollInterval)
	defer ticker.Stop()
	lastHighLoad := time.Now()
	retryAt := time.Time{}
	for {
		activity := r.currentPoolActivity()
		now := time.Now()
		highLoad := activity.BytesPerSecond >= carrierPoolHighRate ||
			activity.ActiveStreams >= carrierPoolHighStreams
		if highLoad {
			lastHighLoad = now
		}
		if retryAt.IsZero() || !now.Before(retryAt) {
			if err := r.reconcileCarrierPool(r.poolContext, activity); err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				if errors.Is(err, ErrMigrationInProgress) {
					retryAt = time.Time{}
				} else {
					retryAt = now.Add(carrierPoolRetryDelay)
				}
			} else {
				retryAt = time.Time{}
			}
		}
		if !highLoad && now.Sub(lastHighLoad) >= carrierPoolIdleTimeout {
			if r.retireIdlePoolMember() {
				lastHighLoad = now
			}
		}
		select {
		case <-ticker.C:
		case <-r.poolContext.Done():
			return
		}
	}
}

func (r *np2Runtime) currentPoolActivity() poolActivity {
	r.mu.Lock()
	stack := r.stack
	sessions := make([]*session.Authenticated, 0, 1+len(r.poolSessions))
	if r.session != nil {
		sessions = append(sessions, r.session)
	}
	for _, authenticated := range r.poolSessions {
		sessions = append(sessions, authenticated)
	}
	r.mu.Unlock()
	uploadRate, downloadRate, _, _ := stack.TrafficStats()
	activity := poolActivity{BytesPerSecond: uploadRate + downloadRate}
	for _, authenticated := range sessions {
		if authenticated != nil && authenticated.Mux != nil {
			activity.ActiveStreams += authenticated.Mux.Stats().ActiveStreams
		}
	}
	return activity
}

func carrierPoolWarmDelay() time.Duration {
	var sample [1]byte
	if _, err := rand.Read(sample[:]); err != nil {
		return carrierPoolWarmDelayMin + carrierPoolWarmDelaySpread/2
	}
	return carrierPoolWarmDelayMin +
		time.Duration(sample[0])*carrierPoolWarmDelaySpread/time.Duration(255)
}

func (r *np2Runtime) NetworkChanged(ctx context.Context) (migrationResult, error) {
	if ctx == nil {
		return migrationResult{}, context.Canceled
	}
	r.mu.Lock()
	if r.closed || r.session == nil {
		r.mu.Unlock()
		return migrationResult{}, ErrNotConnected
	}
	if r.migrationInProgress {
		r.mu.Unlock()
		return migrationResult{}, ErrMigrationInProgress
	}
	r.migrationInProgress = true
	current := r.session
	currentGeneration := r.generation
	r.pendingTerminal = nil
	probeTimeout := r.nativeProbeTimeout
	r.mu.Unlock()

	if probeTimeout <= 0 {
		probeTimeout = 1500 * time.Millisecond
	}
	probeContext, cancelProbe := context.WithTimeout(ctx, probeTimeout)
	probeErr := current.Mux.Ping(probeContext)
	cancelProbe()
	if probeErr == nil {
		r.mu.Lock()
		if !r.closed && r.generation == currentGeneration && r.session == current {
			r.migrationInProgress = false
			r.pendingTerminal = nil
			r.mu.Unlock()
			return migrationResult{NativePath: true, Migrated: true}, nil
		}
		r.mu.Unlock()
		return migrationResult{}, context.Canceled
	}

	replacement, err := r.connect(ctx, r.clientConfig)
	if err != nil {
		r.finishMigrationFailure(currentGeneration, current, err)
		return migrationResult{}, fmt.Errorf("reconnect authenticated session: %w", err)
	}
	maximumPayload, datagrams, err := authenticatedSessionRoute(replacement)
	if err != nil {
		_ = replacement.Mux.Close()
		r.finishMigrationFailure(currentGeneration, current, err)
		return migrationResult{}, err
	}
	carrierName, err := mobileCarrierName(replacement.Carrier)
	if err != nil {
		_ = replacement.Mux.Close()
		r.finishMigrationFailure(currentGeneration, current, err)
		return migrationResult{}, err
	}
	replacementAddresses := append([]netip.Addr(nil), r.clientConfig.ServerAddresses...)
	replacementAddresses = append(replacementAddresses, replacement.CarrierRemoteAddresses...)
	replacementRoutes, err := canonicalRouteAddresses(replacementAddresses)
	if err != nil || !routeSetContains(r.serverAddresses, replacementRoutes) {
		_ = replacement.Mux.Close()
		if err == nil {
			err = errors.New("replacement carrier needs a new route exclusion")
		}
		r.finishMigrationFailure(currentGeneration, current, err)
		return migrationResult{}, err
	}
	replacementControl, continuityState, err := r.attachContinuity(ctx, replacement)
	if err != nil {
		_ = replacement.Mux.Close()
		r.finishMigrationFailure(currentGeneration, current, err)
		return migrationResult{}, err
	}

	r.mu.Lock()
	if r.closed || r.generation != currentGeneration || r.session != current {
		r.migrationInProgress = false
		r.mu.Unlock()
		if replacementControl != nil {
			_ = replacementControl.Close()
		}
		_ = replacement.Mux.Close()
		return migrationResult{}, context.Canceled
	}
	if err := r.router.Switch(replacement.Mux, maximumPayload, datagrams); err != nil {
		r.migrationInProgress = false
		r.mu.Unlock()
		if replacementControl != nil {
			_ = replacementControl.Close()
		}
		_ = replacement.Mux.Close()
		r.finishMigrationFailure(currentGeneration, current, err)
		return migrationResult{}, err
	}
	if r.continuityRouter != nil {
		if err := r.continuityRouter.SwitchRoute(tunstack.ContinuityRoute{
			ID: 1, Mux: replacement.Mux, Control: replacementControl,
			ConstellationID: continuityState.ConstellationID, LeaseKey: continuityState.LeaseKey,
			SupportsDatagram: replacement.Datagrams != nil && replacement.Datagrams.Enabled(),
		}); err != nil {
			r.migrationInProgress = false
			r.mu.Unlock()
			if replacementControl != nil {
				_ = replacementControl.Close()
			}
			_ = replacement.Mux.Close()
			r.finishMigrationFailure(currentGeneration, current, err)
			return migrationResult{}, err
		}
	}
	retired := make([]struct {
		id            uint64
		authenticated *session.Authenticated
	}, 0, 1+len(r.poolSessions))
	retired = append(retired, struct {
		id            uint64
		authenticated *session.Authenticated
	}{r.retireSessionLocked(current), current})
	for _, pooled := range r.poolSessions {
		if pooled == nil || pooled.Mux == nil {
			continue
		}
		retired = append(retired, struct {
			id            uint64
			authenticated *session.Authenticated
		}{r.retireSessionLocked(pooled), pooled})
	}
	clear(r.poolSessions)
	r.generation++
	replacementGeneration := r.generation
	r.session = replacement
	r.primaryRouteID = 1
	if replacementControl != nil {
		r.controls[replacement] = replacementControl
	}
	r.carrierName = carrierName
	r.poolTarget = r.poolConfiguredTarget
	if carrierName != "https" {
		r.poolTarget = 1
	}
	r.migrationInProgress = false
	r.pendingTerminal = nil
	r.mu.Unlock()

	go r.watchSession(replacementGeneration, replacement)
	for _, retiredSession := range retired {
		go r.drainRetiredSession(retiredSession.id, retiredSession.authenticated)
	}
	return migrationResult{Reconnected: true, Migrated: true}, nil
}

func (r *np2Runtime) retireSessionLocked(authenticated *session.Authenticated) uint64 {
	retiredID := r.nextRetiredID
	if retiredID == 0 {
		retiredID = 1
	}
	r.nextRetiredID = retiredID + 1
	r.retired[retiredID] = authenticated
	return retiredID
}

func authenticatedSessionRoute(authenticated *session.Authenticated) (
	uint64,
	*session.DatagramMux,
	error,
) {
	if authenticated == nil || authenticated.Mux == nil {
		return 0, nil, ErrNotConnected
	}
	extensions, negotiated := authenticated.Extensions()
	if !negotiated || extensions.Capabilities&protocol.CapabilityReliableUDP == 0 {
		return 0, nil, nil
	}
	if extensions.MaxUDPPayload < 1200 {
		return 0, nil, session.ErrInvalidConfig
	}
	if extensions.Capabilities&protocol.CapabilityUnreliableDatagrams != 0 {
		if authenticated.Datagrams == nil || !authenticated.Datagrams.Enabled() {
			return 0, nil, session.ErrInvalidConfig
		}
		return extensions.MaxUDPPayload, authenticated.Datagrams, nil
	}
	return extensions.MaxUDPPayload, nil, nil
}

func (r *np2Runtime) finishMigrationFailure(generation uint64, current *session.Authenticated, _ error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.generation != generation || r.session != current {
		return
	}
	r.migrationInProgress = false
	if r.pendingTerminal != nil {
		r.terminalErr = r.pendingTerminal
		r.doneOnce.Do(func() { close(r.done) })
	}
}

func (r *np2Runtime) watchSession(generation uint64, authenticated *session.Authenticated) {
	err := authenticated.Mux.Wait(context.Background())
	if r.promoteWarmCarrier(generation, authenticated) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || generation != r.generation || authenticated != r.session {
		return
	}
	if r.migrationInProgress {
		r.pendingTerminal = err
		return
	}
	r.terminalErr = err
	r.doneOnce.Do(func() { close(r.done) })
}

func (r *np2Runtime) promoteWarmCarrier(
	generation uint64,
	authenticated *session.Authenticated,
) bool {
	r.mu.Lock()
	if r.closed || r.migrationInProgress || generation != r.generation ||
		authenticated != r.session || len(r.poolSessions) == 0 {
		r.mu.Unlock()
		return false
	}
	var selectedID uint64
	var selected *session.Authenticated
	for routeID, candidate := range r.poolSessions {
		if candidate == nil || candidate.Mux == nil || candidate.Mux.Err() != nil ||
			(selected != nil && routeID >= selectedID) {
			continue
		}
		selectedID, selected = routeID, candidate
	}
	if selected == nil || r.router.Promote(selectedID) != nil {
		r.mu.Unlock()
		return false
	}
	oldRouteID := r.primaryRouteID
	oldControl := r.controls[authenticated]
	delete(r.controls, authenticated)
	delete(r.poolSessions, selectedID)
	r.generation++
	newGeneration := r.generation
	r.session = selected
	r.primaryRouteID = selectedID
	r.pendingTerminal = nil
	if name, err := mobileCarrierName(selected.Carrier); err == nil {
		r.carrierName = name
	}
	r.poolTarget = r.poolConfiguredTarget
	if r.carrierName != "https" {
		r.poolTarget = 1
	}
	continuityRouter := r.continuityRouter
	r.mu.Unlock()

	if continuityRouter != nil {
		continuityRouter.RemoveRoute(oldRouteID)
	}
	r.router.Remove(oldRouteID)
	if oldControl != nil {
		_ = oldControl.Close()
	}
	_ = authenticated.Mux.Close()
	go r.watchSession(newGeneration, selected)
	return true
}

func (r *np2Runtime) drainRetiredSession(generation uint64, authenticated *session.Authenticated) {
	timeout := r.drainTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for authenticated.Mux.Stats().ActiveStreams != 0 {
		select {
		case <-ticker.C:
		case <-timer.C:
			r.closeControlForSession(authenticated)
			_ = authenticated.Mux.Close()
			r.removeRetired(generation, authenticated)
			return
		case <-r.done:
			r.closeControlForSession(authenticated)
			_ = authenticated.Mux.Close()
			return
		}
	}
	r.closeControlForSession(authenticated)
	_ = authenticated.Mux.Close()
	r.removeRetired(generation, authenticated)
}

func (r *np2Runtime) closeControlForSession(authenticated *session.Authenticated) {
	if r == nil || authenticated == nil {
		return
	}
	r.mu.Lock()
	control := r.controls[authenticated]
	delete(r.controls, authenticated)
	r.mu.Unlock()
	if control != nil {
		_ = control.Close()
	}
}

func (r *np2Runtime) removeRetired(generation uint64, authenticated *session.Authenticated) {
	r.mu.Lock()
	if r.retired[generation] == authenticated {
		delete(r.retired, generation)
	}
	r.mu.Unlock()
}

func routeSetContains(current, candidate string) bool {
	allowed := make(map[string]struct{})
	for _, value := range strings.Split(current, ",") {
		if value != "" {
			allowed[value] = struct{}{}
		}
	}
	for _, value := range strings.Split(candidate, ",") {
		if _, ok := allowed[value]; !ok {
			return false
		}
	}
	return candidate != ""
}

type runtimeCloser interface {
	Close() error
}

func closeRuntimeResources(stack, np2Session runtimeCloser) error {
	var sessionError error
	if np2Session != nil {
		sessionError = np2Session.Close()
	}
	var stackError error
	if stack != nil {
		stackError = stack.Close()
	}
	return errors.Join(stackError, sessionError)
}

func (r *np2Runtime) ServerAddresses() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.serverAddresses
}

func (r *np2Runtime) CarrierName() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.carrierName
}

func (r *np2Runtime) TrafficStats() trafficStats {
	r.mu.Lock()
	stack := r.stack
	router := r.router
	closed := r.closed
	r.mu.Unlock()
	if closed || stack == nil {
		return trafficStats{}
	}
	uploadRate, downloadRate, uploadTotal, downloadTotal := stack.TrafficStats()
	dnsStats := stack.DNSAttributionStats()
	poolTarget, poolHealthy, assignments, scaleUps, failures := r.carrierPoolStats()
	return trafficStats{
		UploadBytesPerSecond: uploadRate, DownloadBytesPerSecond: downloadRate,
		UploadTotalBytes: uploadTotal, DownloadTotalBytes: downloadTotal,
		UDPMode: router.UDPMode(), QUICFallbacks: int64(router.QUICFallbackCount()),
		CarrierPoolTarget: int64(poolTarget), CarrierPoolHealthy: int64(poolHealthy),
		CarrierPoolAssignments: int64(assignments), CarrierPoolScaleUps: int64(scaleUps),
		CarrierPoolFailures:   int64(failures),
		DNSAttributionQueries: int64(dnsStats.Queries), DNSAttributionResponses: int64(dnsStats.Responses),
		DNSAttributionHits: int64(dnsStats.Hits), DNSAttributionMisses: int64(dnsStats.Misses),
		DNSAttributionCached:  int64(dnsStats.Cached),
		FirstFlightDomainHits: int64(dnsStats.FirstFlightDomainHits),
		FirstFlightFallbacks:  int64(dnsStats.FirstFlightFallbacks),
	}
}

func canonicalRouteAddresses(addresses []netip.Addr) (string, error) {
	unique := make(map[string]struct{}, len(addresses))
	benchmarkIPv4 := netip.MustParsePrefix("198.18.0.0/15")
	documentationIPv6 := netip.MustParsePrefix("2001:db8::/32")
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() ||
			address.IsMulticast() || address.IsLinkLocalUnicast() || address.IsPrivate() ||
			benchmarkIPv4.Contains(address) || documentationIPv6.Contains(address) {
			continue
		}
		unique[address.String()] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for address := range unique {
		result = append(result, address)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return "", errors.New("NP/2 server has no safe route exclusions")
	}
	return strings.Join(result, ","), nil
}

var defaultController = newController(connectRuntime)

// Validate checks an untrusted JSON profile and Keychain-provided secret.
func Validate(profileJSON, secret string) error {
	_, err := config.ParseMobileClientBytes([]byte(profileJSON), secret)
	return err
}

// Start establishes and authenticates an encrypted NP/2 session.
func Start(profileJSON, secret string) error {
	return defaultController.start([]byte(profileJSON), secret)
}

// SetClientRoutesJSON installs the immutable local route snapshot that will be
// applied to the next packet-tunnel start. Server authorization is still
// required for every node or chain action.
func SetClientRoutesJSON(routesJSON string) error {
	return defaultController.setClientRoutesJSON(routesJSON)
}

// StartPacketTunnel attaches a duplicated NetworkExtension utun descriptor to
// the direct userspace TCP/IP-to-NP/2 data plane.
func StartPacketTunnel(fileDescriptor int64) error {
	if fileDescriptor < 0 || fileDescriptor > int64(^uint(0)>>1) {
		return ErrInvalidTunnelFD
	}
	return defaultController.startTunnel(int(fileDescriptor))
}

// Stop invalidates the active session immediately and releases its userspace
// stack, streams, and carrier asynchronously.
func Stop() { defaultController.stop() }

// CatalogJSON returns the server-signed per-user cluster catalogue over the
// already authenticated NP/2 session. The caller must verify the signature
// against the public key pinned by the onboarding profile before applying it.
func CatalogJSON() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return defaultController.catalogJSON(ctx)
}

// NetworkChanged proves native carrier liveness and, when needed, reconnects
// and atomically routes new flows to a fresh authenticated session while old
// flows drain for a bounded interval.
func NetworkChanged() error { return defaultController.networkChanged() }

// State returns stopped, starting, connected, running, stopping, or failed.
func State() string { return defaultController.stateName() }

// LastError returns the last local lifecycle error without key material.
func LastError() string { return defaultController.lastError() }

// ServerAddresses returns numeric carrier endpoints that NetworkExtension must
// exclude from the utun default route to prevent a routing loop.
func ServerAddresses() string { return defaultController.serverAddresses() }

// Carrier returns http3, webrtc, or https for the active NP/2 session.
func Carrier() string { return defaultController.carrierName() }

// UploadBytesPerSecond returns application payload bytes sent during the last
// one-second statistics window.
func UploadBytesPerSecond() int64 { return defaultController.trafficStats().UploadBytesPerSecond }

// DownloadBytesPerSecond returns application payload bytes received during
// the last one-second statistics window.
func DownloadBytesPerSecond() int64 { return defaultController.trafficStats().DownloadBytesPerSecond }

func UploadTotalBytes() int64 { return defaultController.trafficStats().UploadTotalBytes }

func DownloadTotalBytes() int64 { return defaultController.trafficStats().DownloadTotalBytes }

// UDPMode returns disabled, fast-datagram, or
// reliable-stream-quic-fallback for the active packet tunnel.
func UDPMode() string { return defaultController.trafficStats().UDPMode }

// QUICFallbackCount reports UDP/443 flows rejected on a reliable-only carrier
// so applications can retry over HTTPS/TCP without QUIC-over-TCP blocking.
func QUICFallbackCount() int64 { return defaultController.trafficStats().QUICFallbacks }

func CarrierPoolTarget() int64 { return defaultController.trafficStats().CarrierPoolTarget }

func CarrierPoolHealthy() int64 { return defaultController.trafficStats().CarrierPoolHealthy }

func CarrierPoolAssignments() int64 {
	return defaultController.trafficStats().CarrierPoolAssignments
}

func CarrierPoolScaleUpCount() int64 { return defaultController.trafficStats().CarrierPoolScaleUps }

func CarrierPoolFailureCount() int64 { return defaultController.trafficStats().CarrierPoolFailures }

func DNSAttributionQueryCount() int64 { return defaultController.trafficStats().DNSAttributionQueries }

func DNSAttributionResponseCount() int64 {
	return defaultController.trafficStats().DNSAttributionResponses
}

func DNSAttributionHitCount() int64 { return defaultController.trafficStats().DNSAttributionHits }

func DNSAttributionMissCount() int64 { return defaultController.trafficStats().DNSAttributionMisses }

func DNSAttributionCachedCount() int64 { return defaultController.trafficStats().DNSAttributionCached }

func FirstFlightDomainHitCount() int64 { return defaultController.trafficStats().FirstFlightDomainHits }

func FirstFlightFallbackCount() int64 { return defaultController.trafficStats().FirstFlightFallbacks }

func NetworkChangeCount() int64 { return int64(defaultController.networkChangeCount()) }

func ReconnectCount() int64 { return int64(defaultController.reconnectCount()) }

func MigrationCount() int64 { return int64(defaultController.migrationCount()) }

// Version reports the embedded NP/2 core version.
func Version() string { return buildinfo.Version }
