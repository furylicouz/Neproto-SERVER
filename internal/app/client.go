package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/carrier/http3wt"
	"neproto.local/chameleon/internal/carrier/httpsws"
	"neproto.local/chameleon/internal/carrier/hybrid"
	rtc "neproto.local/chameleon/internal/carrier/webrtc"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/socks5"
)

type ProbeMode uint8

const (
	ProbeAuto ProbeMode = iota + 1
	ProbeWebRTC
	ProbeHTTPS
	ProbeHTTP3
)

type ProbeResult struct {
	Kind                  protocol.CarrierKind
	UsedFallback          bool
	MosaicEnabled         bool
	CoverClass            string
	CoverTransitions      uint64
	ConstellationEnabled  bool
	ForwardSecrecyEnabled bool
}

func RunClient(ctx context.Context, config config.Client) error {
	return RunClientReady(ctx, config, nil)
}

// RunClientReady runs the client and invokes ready with the actual loopback
// SOCKS listener address after it is bound. Mobile tunnel providers use the
// callback to avoid routing device traffic before the local adapter can accept
// connections. The actual address matters when the profile requests port 0.
func RunClientReady(ctx context.Context, config config.Client, ready func(string)) error {
	if ctx == nil {
		return errors.New("nil client context")
	}
	runtimeConnect, err := clientConnectorForRun(
		config, ConnectClientHTTPSFirst, ConnectClient,
	)
	if err != nil {
		return err
	}
	authenticated, err := runtimeConnect(ctx, config)
	if err != nil {
		return err
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	var connect socks5.ConnectFunc
	var associateUDP socks5.AssociateUDPFunc
	var waitRuntime func(context.Context) error
	var closeRuntime func() error
	if config.EnableConstellation {
		constellationRuntime, runtimeErr := newDesktopConstellationRuntime(
			runContext, config, authenticated, runtimeConnect,
		)
		if runtimeErr != nil {
			_ = authenticated.Mux.Close()
			return fmt.Errorf("start constellation runtime: %w", runtimeErr)
		}
		connect = constellationRuntime.Connect
		associateUDP = constellationRuntime.AssociateUDP
		waitRuntime = constellationRuntime.Wait
		closeRuntime = constellationRuntime.Close
	} else {
		connector := proxy.Connector{Mux: authenticated.Mux}
		if extensions, negotiated := authenticated.Extensions(); negotiated &&
			extensions.Capabilities&protocol.CapabilityReliableUDP != 0 {
			connector.MaxUDPPayload = extensions.MaxUDPPayload
			if extensions.Capabilities&protocol.CapabilityUnreliableDatagrams != 0 &&
				authenticated.Datagrams != nil && authenticated.Datagrams.Enabled() {
				connector.Datagrams = authenticated.Datagrams
			}
			associateUDP = connector.AssociateUDP
		}
		connect = connector.Connect
		waitRuntime = authenticated.Mux.Wait
		closeRuntime = authenticated.Mux.Close
	}
	defer closeRuntime()

	listener, err := net.Listen("tcp", config.SOCKSListen)
	if err != nil {
		return fmt.Errorf("listen SOCKS: %w", err)
	}
	defer listener.Close()
	if ready != nil {
		ready(listener.Addr().String())
	}
	results := make(chan error, 2)
	go func() {
		results <- (socks5.Server{
			Connect: connect, AssociateUDP: associateUDP,
			MaxConnections: config.MaxSOCKSConnections,
		}).Serve(runContext, listener)
	}()
	go func() { results <- waitRuntime(runContext) }()

	var result error
	select {
	case result = <-results:
	case <-ctx.Done():
		result = nil
	}
	cancel()
	_ = listener.Close()
	_ = closeRuntime()
	select {
	case <-results:
	case <-ctx.Done():
	}
	if ctx.Err() != nil || result == nil || errors.Is(result, context.Canceled) {
		return nil
	}
	return result
}

type runtimeClientConnect func(context.Context, config.Client) (*session.Authenticated, error)

func connectClientForRun(
	ctx context.Context,
	clientConfig config.Client,
	performance runtimeClientConnect,
	udpFirst runtimeClientConnect,
) (*session.Authenticated, error) {
	if ctx == nil {
		return nil, config.ErrInvalidConfig
	}
	selected, err := clientConnectorForRun(clientConfig, performance, udpFirst)
	if err != nil {
		return nil, err
	}
	return selected(ctx, clientConfig)
}

func clientConnectorForRun(
	clientConfig config.Client,
	compatibility runtimeClientConnect,
	adaptive runtimeClientConnect,
) (runtimeClientConnect, error) {
	if compatibility == nil || adaptive == nil {
		return nil, config.ErrInvalidConfig
	}
	switch clientConfig.CarrierPolicy {
	case "", config.CarrierPolicyPerformance:
		return adaptive, nil
	case config.CarrierPolicyUDPFirst:
		return adaptive, nil
	case config.CarrierPolicyHTTP3Only:
		return adaptive, nil
	default:
		return nil, config.ErrInvalidConfig
	}
}

// ConnectClient establishes one authenticated NP/2 session without starting
// a local application adapter. Mobile packet tunnels use the returned
// multiplexer directly.
func ConnectClient(ctx context.Context, clientConfig config.Client) (*session.Authenticated, error) {
	authenticated, _, err := connectClient(ctx, clientConfig, primaryClientProbeMode(clientConfig))
	return authenticated, err
}

func primaryClientProbeMode(clientConfig config.Client) ProbeMode {
	if clientConfig.CarrierPolicy == config.CarrierPolicyHTTP3Only {
		return ProbeHTTP3
	}
	return ProbeAuto
}

// ConnectClientHTTPSFirst establishes the same authenticated NP/2 session as
// ConnectClient, but prefers the route-stable HTTPS carrier used by packet
// tunnels. HTTP/3 and WebRTC remain ordered fallbacks for networks where the
// HTTPS carrier is unavailable.
func ConnectClientHTTPSFirst(ctx context.Context, clientConfig config.Client) (*session.Authenticated, error) {
	return connectClientHTTPSFirst(ctx, clientConfig, connectClient)
}

// ConnectClientDatagramPreferred is used only after a compatibility carrier
// already provides connectivity. It gives HTTP/3 and then WebRTC their full
// bounded authentication attempts without allowing HTTPS to cancel the warm
// probe. The caller keeps the existing carrier until this function succeeds.
func ConnectClientDatagramPreferred(
	ctx context.Context,
	clientConfig config.Client,
) (*session.Authenticated, error) {
	return connectClientDatagramPreferred(ctx, clientConfig, connectClient)
}

type clientSessionDial func(
	context.Context, config.Client, ProbeMode,
) (*session.Authenticated, hybrid.Result, error)

func connectClientHTTPSFirst(
	ctx context.Context, clientConfig config.Client, dial clientSessionDial,
) (*session.Authenticated, error) {
	modes := make([]ProbeMode, 0, 3)
	if clientConfig.CarrierPolicy == config.CarrierPolicyHTTP3Only {
		modes = append(modes, ProbeHTTP3)
	} else {
		modes = append(modes, ProbeHTTPS)
		if clientConfig.HTTP3Configured() {
			modes = append(modes, ProbeHTTP3)
		}
		modes = append(modes, ProbeWebRTC)
	}

	failures := make([]error, 0, len(modes))
	for _, mode := range modes {
		authenticated, _, err := dial(ctx, clientConfig, mode)
		if err == nil {
			return authenticated, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", mobileCarrierAttemptLabel(mode), err))
	}
	return nil, errors.Join(failures...)
}

func connectClientDatagramPreferred(
	ctx context.Context, clientConfig config.Client, dial clientSessionDial,
) (*session.Authenticated, error) {
	if ctx == nil || dial == nil {
		return nil, config.ErrInvalidConfig
	}
	modes := make([]ProbeMode, 0, 2)
	if clientConfig.HTTP3Configured() {
		modes = append(modes, ProbeHTTP3)
	}
	if clientConfig.CarrierPolicy != config.CarrierPolicyHTTP3Only {
		modes = append(modes, ProbeWebRTC)
	}
	failures := make([]error, 0, len(modes))
	for _, mode := range modes {
		authenticated, _, err := dial(ctx, clientConfig, mode)
		if err == nil {
			return authenticated, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", mobileCarrierAttemptLabel(mode), err))
	}
	return nil, errors.Join(failures...)
}

func mobileCarrierAttemptLabel(mode ProbeMode) string {
	switch mode {
	case ProbeHTTPS:
		return "HTTPS carrier"
	case ProbeHTTP3:
		return "HTTP/3 carrier"
	case ProbeWebRTC:
		return "WebRTC fallback"
	default:
		return "NP/2 carrier"
	}
}

func ProbeClient(ctx context.Context, config config.Client, mode ProbeMode) (ProbeResult, error) {
	if ctx == nil {
		return ProbeResult{}, errors.New("nil client context")
	}
	authenticated, selected, err := connectClient(ctx, config, mode)
	if err != nil {
		return ProbeResult{}, err
	}
	coverStats := authenticated.Cover.Stats()
	extensions, negotiated := authenticated.Extensions()
	coverClass := "fixed-" + config.Profile
	if coverStats.MosaicEnabled {
		coverClass = coverStats.TrafficClass.String()
	}
	_ = authenticated.Mux.Close()
	return ProbeResult{
		Kind: selected.Kind, UsedFallback: selected.UsedFallback,
		MosaicEnabled: coverStats.MosaicEnabled, CoverClass: coverClass,
		CoverTransitions:      coverStats.ProfileTransitions,
		ConstellationEnabled:  negotiated && extensions.Capabilities&protocol.CapabilityConstellationContinuity != 0,
		ForwardSecrecyEnabled: negotiated && extensions.Capabilities&protocol.CapabilityForwardSecrecy != 0,
	}, nil
}

func connectClient(ctx context.Context, config config.Client, mode ProbeMode) (*session.Authenticated, hybrid.Result, error) {
	if ctx == nil {
		return nil, hybrid.Result{}, errors.New("nil client context")
	}
	if mode == ProbeAuto {
		return raceClientCarriers(ctx, config, connectClientSingle, defaultCarrierPreferences)
	}
	return connectClientSingle(ctx, config, mode)
}

func connectClientSingle(ctx context.Context, config config.Client, mode ProbeMode) (*session.Authenticated, hybrid.Result, error) {
	selected, err := selectClientCarrier(ctx, config, mode)
	if err != nil {
		return nil, hybrid.Result{}, fmt.Errorf("select carrier: %w", err)
	}
	authenticated, err := AuthenticateClientCarrier(ctx, config, selected.Carrier)
	if err != nil {
		return nil, hybrid.Result{}, err
	}
	return authenticated, selected, nil
}

// AuthenticateClientCarrier applies the shared NP/2 feature and extension
// contract to an already established carrier. The caller selects the carrier;
// strict candidate code passes only HTTP/3 WebTransport here.
func AuthenticateClientCarrier(
	ctx context.Context,
	clientConfig config.Client,
	connection carrier.Carrier,
) (*session.Authenticated, error) {
	if ctx == nil || connection == nil {
		if connection != nil {
			_ = connection.Close()
		}
		return nil, config.ErrInvalidConfig
	}
	features, err := clientFeatures(clientConfig.Profile)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	if !clientConfig.DeviceID.IsZero() {
		features |= protocol.FeatureDeviceIdentity
	}
	extensionRequest := productionClientExtensionRequest(clientConfig, connection)
	requiredExtensions := requiredClientExtensions(clientConfig)
	authenticated, err := session.ConnectClient(ctx, connection, session.AuthenticatedConfig{
		RootSecret: clientConfig.Secret.Bytes(), ServerIdentity: clientConfig.ServerIdentity,
		DeviceID: clientConfig.DeviceID,
		Features: features, InitialWindow: clientConfig.InitialWindowBytes, MaxStreams: clientConfig.MaxStreams,
		MaxCoverOverheadPercent: clientConfig.MaxCoverOverheadPercent,
		ExtensionRequest:        &extensionRequest, RequiredExtensions: requiredExtensions,
		ExtensionTimeout:     500 * time.Millisecond,
		EnableForwardSecrecy: clientConfig.EnableForwardSecrecy,
	})
	if err != nil {
		return nil, fmt.Errorf("authenticate session: %w", err)
	}
	return authenticated, nil
}

func requiredClientExtensions(clientConfig config.Client) protocol.ExtensionCapability {
	var required protocol.ExtensionCapability
	if clientConfig.RequireDatagrams {
		required |= protocol.CapabilityReliableUDP | protocol.CapabilityUnreliableDatagrams
	}
	if clientConfig.EnableForwardSecrecy {
		required |= protocol.CapabilityForwardSecrecy
	}
	return required
}

func selectClientCarrier(ctx context.Context, config config.Client, mode ProbeMode) (hybrid.Result, error) {
	if mode == ProbeHTTP3 {
		if !config.HTTP3Configured() {
			return hybrid.Result{}, errors.New("HTTP/3 carrier is not configured")
		}
		attempt, cancel := context.WithTimeout(ctx, config.HTTP3Timeout.Duration)
		defer cancel()
		connection, err := http3wt.Dial(attempt, http3wt.DialConfig{
			URL: config.HTTP3URL, ServerAddresses: config.ServerAddresses,
			HandshakeIdleTimeout: config.HTTP3Timeout.Duration,
		})
		if err != nil {
			return hybrid.Result{}, err
		}
		return hybrid.Result{Carrier: connection, Kind: protocol.CarrierHTTP3}, nil
	}
	if mode == ProbeWebRTC {
		attempt, cancel := context.WithTimeout(ctx, config.WebRTCTimeout.Duration)
		defer cancel()
		connection, err := rtc.Dial(attempt, rtc.DialConfig{
			SignalingURL: config.WebRTCSignalingURL, GatherTimeout: config.WebRTCTimeout.Duration,
			ConnectTimeout: config.WebRTCTimeout.Duration, ServerAddresses: config.ServerAddresses,
		})
		if err != nil {
			return hybrid.Result{}, err
		}
		return hybrid.Result{Carrier: connection, Kind: protocol.CarrierWebRTC}, nil
	}
	if mode == ProbeHTTPS {
		attempt, cancel := context.WithTimeout(ctx, config.HTTPSTimeout.Duration)
		defer cancel()
		connection, err := httpsws.Dial(attempt, httpsws.DialConfig{
			URL: config.HTTPSURL, ServerAddresses: config.ServerAddresses,
		})
		if err != nil {
			return hybrid.Result{}, err
		}
		return hybrid.Result{Carrier: connection, Kind: protocol.CarrierHTTPS}, nil
	}
	return hybrid.Result{}, errors.New("invalid probe mode")
}

func clientFeatures(profile string) (protocol.FeatureSet, error) {
	features := protocol.FeatureMultiplex | protocol.FeatureCellAEAD
	switch profile {
	case "quiet":
		return features | protocol.FeatureProfileQuiet, nil
	case "web":
		return features | protocol.FeatureProfileWeb, nil
	case "interactive":
		return features | protocol.FeatureProfileInteractive, nil
	default:
		return 0, config.ErrInvalidConfig
	}
}
