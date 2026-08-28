package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/carrier/http3wt"
	"neproto.local/chameleon/internal/carrier/httpsws"
	rtc "neproto.local/chameleon/internal/carrier/webrtc"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/constellation"
	credentialstore "neproto.local/chameleon/internal/credentials"
	"neproto.local/chameleon/internal/observability"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/usage"
)

type carrierAcceptor interface {
	Accept(context.Context) (carrier.Carrier, error)
}

func RunServer(ctx context.Context, config config.Server) error {
	if ctx == nil {
		return errors.New("nil server context")
	}
	resourceLimiter, err := newProductionResourceLimiter(config)
	if err != nil {
		return fmt.Errorf("create resource limiter: %w", err)
	}
	var usageTracker *usage.Tracker
	if shouldTrackUserSessions(config) {
		usageTracker, err = usage.New(usage.Config{
			PolicyPath: config.UserPolicyFile, StatePath: config.UsageStateFile, Now: time.Now,
		})
		if err != nil {
			return fmt.Errorf("create user usage tracker: %w", err)
		}
	}
	catalogHandler, err := newClusterCatalogHandler(
		config.ClusterDirectory, config.ClusterCatalogTTL.Duration, time.Now,
	)
	if err != nil {
		return fmt.Errorf("create cluster catalog handler: %w", err)
	}
	clusterRelay, err := newClusterRelayServices(config, catalogHandler)
	if err != nil {
		return fmt.Errorf("create cluster relay: %w", err)
	}
	defer clusterRelay.Close()
	if clusterRelay != nil {
		catalogHandler = clusterRelay.catalog
	}
	httpsAcceptor, err := httpsws.NewAcceptor(httpsws.AcceptorConfig{
		Path: config.HTTPSPath, Backlog: config.MaxSessions, AllowLoopbackProxy: true,
	})
	if err != nil {
		return fmt.Errorf("create HTTPS acceptor: %w", err)
	}
	defer httpsAcceptor.Close()
	webRTCServer, err := rtc.NewServer(rtc.ServerConfig{
		Path: config.WebRTCPath, MaxPeers: config.MaxWebRTCPeers,
		GatherTimeout: config.GatherTimeout.Duration, ConnectTimeout: config.ConnectTimeout.Duration,
		Engine: rtc.EngineConfig{UDPPortMin: config.UDPPortMin, UDPPortMax: config.UDPPortMax},
	})
	if err != nil {
		return fmt.Errorf("create WebRTC server: %w", err)
	}
	defer webRTCServer.Close()

	var http3Server *http3wt.Server
	var http3Packet net.PacketConn
	if config.EnableHTTP3 {
		certificate, certificateErr := tls.LoadX509KeyPair(config.HTTP3CertFile, config.HTTP3KeyFile)
		if certificateErr != nil {
			return fmt.Errorf("load HTTP/3 certificate: %w", certificateErr)
		}
		http3Server, err = http3wt.NewServer(http3wt.ServerConfig{
			Path: config.HTTP3Path, TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
			},
			Backlog: config.MaxHTTP3Sessions, MaxSessions: config.MaxHTTP3Sessions,
			FirstStreamTimeout:   config.HTTP3HandshakeTimeout.Duration,
			HandshakeIdleTimeout: config.HTTP3HandshakeTimeout.Duration,
			IdleTimeout:          config.HTTP3IdleTimeout.Duration,
		})
		if err != nil {
			return fmt.Errorf("create HTTP/3 server: %w", err)
		}
		defer http3Server.Close()
		http3Packet, err = net.ListenPacket("udp", config.HTTP3Listen)
		if err != nil {
			return fmt.Errorf("listen HTTP/3: %w", err)
		}
		defer http3Packet.Close()
	}

	httpsHandler := httpsAcceptor.Handler()
	webRTCHandler := webRTCServer.Handler()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case config.HTTPSPath:
			httpsHandler.ServeHTTP(writer, request)
		case config.WebRTCPath:
			webRTCHandler.ServeHTTP(writer, request)
		default:
			http.NotFound(writer, request)
		}
	})
	listener, err := net.Listen("tcp", config.Listen)
	if err != nil {
		return fmt.Errorf("listen backend: %w", err)
	}
	defer listener.Close()
	runtimeMetrics := observability.NewServerMetrics()
	runtimeMetrics.AttachResourceLimiter(resourceLimiter)
	var metricsListener net.Listener
	var metricsServer *http.Server
	if config.MetricsListen != "" {
		metricsListener, err = net.Listen("tcp", config.MetricsListen)
		if err != nil {
			return fmt.Errorf("listen metrics: %w", err)
		}
		defer metricsListener.Close()
		metricsServer = &http.Server{
			Handler:           runtimeMetrics,
			ReadHeaderTimeout: 2 * time.Second,
			IdleTimeout:       15 * time.Second,
			MaxHeaderBytes:    4 * 1024,
		}
	}
	httpServer := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024,
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	if usageTracker != nil {
		go usageTracker.Run(runContext.Done(), 5*time.Second)
	}
	constellationRuntime, err := newConstellationServices(runContext, config)
	if err != nil {
		return fmt.Errorf("create constellation runtime: %w", err)
	}
	defer constellationRuntime.Close()
	if constellationRuntime != nil {
		runtimeMetrics.AttachContinuityRuntime(constellationRuntime.runtime)
	}
	errorsChannel := make(chan error, 8)
	sessions := make(chan struct{}, config.MaxSessions)
	var sessionWait sync.WaitGroup
	startAcceptLoop := func(acceptor carrierAcceptor) {
		go func() {
			for {
				connection, acceptErr := acceptor.Accept(runContext)
				if acceptErr != nil {
					if runContext.Err() == nil {
						errorsChannel <- acceptErr
					}
					return
				}
				runtimeMetrics.CarrierAccepted(connection.Kind())
				select {
				case sessions <- struct{}{}:
					sessionWait.Add(1)
					go func() {
						defer sessionWait.Done()
						defer func() { <-sessions }()
						serveAuthenticatedCarrier(
							runContext, config, connection, runtimeMetrics, resourceLimiter, usageTracker,
							constellationRuntime, catalogHandler, clusterRelay,
						)
					}()
				default:
					runtimeMetrics.SessionRejected()
					_ = connection.Close()
				}
			}
		}()
	}
	startAcceptLoop(httpsAcceptor)
	startAcceptLoop(webRTCServer)
	if http3Server != nil {
		startAcceptLoop(http3Server)
		go func() {
			serveErr := http3Server.Serve(http3Packet)
			if serveErr != nil && runContext.Err() == nil && !errors.Is(serveErr, http3wt.ErrServerClosed) {
				errorsChannel <- serveErr
			}
		}()
	}
	go func() {
		serveErr := httpServer.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errorsChannel <- serveErr
		}
	}()
	if metricsServer != nil {
		go func() {
			serveErr := metricsServer.Serve(metricsListener)
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				errorsChannel <- serveErr
			}
		}()
	}

	var result error
	select {
	case <-ctx.Done():
	case result = <-errorsChannel:
	}
	cancel()
	_ = httpsAcceptor.Close()
	_ = webRTCServer.Close()
	if http3Server != nil {
		_ = http3Server.Close()
		_ = http3Packet.Close()
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), config.ShutdownTimeout.Duration)
	_ = httpServer.Shutdown(shutdownContext)
	if metricsServer != nil {
		_ = metricsServer.Shutdown(shutdownContext)
	}
	shutdownCancel()
	_ = listener.Close()
	sessionWait.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return result
}

func serveAuthenticatedCarrier(
	ctx context.Context,
	config config.Server,
	connection carrier.Carrier,
	metrics *observability.ServerMetrics,
	resourceLimiter *proxy.ResourceLimiter,
	usageTracker *usage.Tracker,
	constellationRuntime *constellationServices,
	catalogHandler proxy.CatalogHandler,
	clusterRelay *clusterRelayServices,
) {
	if config.CredentialDirectory != "" {
		loaded, err := credentialstore.LoadActiveDirectory(config.CredentialDirectory)
		if err != nil {
			slog.Error("NP/2 credential reload failed", "event", "credential_reload_failed")
			return
		}
		config.Credentials = loaded
	}
	carrierName := "https"
	switch connection.Kind() {
	case protocol.CarrierWebRTC:
		carrierName = "webrtc"
	case protocol.CarrierHTTP3:
		carrierName = "http3"
	}
	serverCredentials := make([]session.ServerCredential, 0, len(config.Credentials))
	for _, credential := range config.Credentials {
		serverCredentials = append(serverCredentials, session.ServerCredential{
			ID: credential.ID, RootSecret: credential.Secret,
		})
	}
	extensionOffer := productionServerExtensionOffer(config, connection)
	authenticationStarted := time.Now()
	authenticated, err := session.AcceptServer(ctx, connection, session.AuthenticatedConfig{
		RootSecret: config.Secret.Bytes(), ServerIdentity: config.ServerIdentity,
		Credentials: serverCredentials,
		Features: protocol.FeatureMultiplex | protocol.FeatureCellAEAD | protocol.FeatureProfileQuiet |
			protocol.FeatureProfileWeb | protocol.FeatureProfileInteractive | protocol.FeatureDeviceIdentity,
		InitialWindow: config.InitialWindowBytes, MaxStreams: config.MaxStreams,
		MaxCoverOverheadPercent: config.MaxCoverOverheadPercent,
		DisableCover:            !config.CoverEnabled(),
		EnablePulse:             config.PulseCoverEnabled(),
		ExtensionOffer:          &extensionOffer,
		EnableForwardSecrecy:    config.EnableForwardSecrecy,
	})
	if err != nil {
		metrics.AuthenticationFailed(connection.Kind(), time.Since(authenticationStarted))
		slog.Warn("NP/2 session authentication failed",
			"event", "session_authentication_failed", "carrier", carrierName,
			"reason", string(observability.TerminalAuthentication))
		return
	}
	if config.EnableForwardSecrecy {
		// The server offers forward secrecy but does not require it, preserving
		// server-first rollout for legacy clients. Waiting closes the short
		// rekey barrier before proxy application cells are accepted.
		_, _, _ = authenticated.WaitExtensions(ctx)
	}
	if !resourceLimiter.AcquireSession(authenticated.CredentialID) {
		metrics.SessionRejected()
		_ = authenticated.Mux.Close()
		slog.Warn("NP/2 session rejected by per-user limit",
			"event", "session_resource_rejected", "carrier", carrierName,
			"reason", "per_user_session_limit")
		return
	}
	defer resourceLimiter.ReleaseSession(authenticated.CredentialID)
	var usageSession *usage.Session
	if usageTracker != nil {
		usageSession, err = usageTracker.Admit(
			authenticated.CredentialID,
			authenticated.DeviceID,
			func() usage.Counters {
				stats := authenticated.Mux.Stats()
				return usage.Counters{
					UploadBytes: stats.ReceivedPayloadBytes, DownloadBytes: stats.SentCellPayloadBytes,
				}
			},
		)
		if err != nil && rejectUsageAdmission(err, authenticated.CredentialID, clusterRelay) {
			metrics.SessionRejected()
			_ = authenticated.Mux.Close()
			slog.Warn("NP/2 session rejected by device policy",
				"event", "session_device_rejected", "carrier", carrierName,
				"reason", usageRejectionReason(err))
			return
		}
	}
	var continuityRuntime *proxy.ContinuityRuntime
	var continuityLease proxy.ContinuityLease
	if constellationRuntime != nil {
		parameters, negotiated, _ := authenticated.WaitExtensions(ctx)
		if negotiated && parameters.Capabilities&protocol.CapabilityConstellationContinuity != 0 {
			admitTimeout := min(config.ConnectTimeout.Duration, 5*time.Second)
			admitContext, admitCancel := context.WithTimeout(ctx, admitTimeout)
			attachment, admitErr := constellationRuntime.control.Admit(admitContext, authenticated)
			admitCancel()
			if admitErr != nil {
				metrics.ConstellationRejected()
				metrics.SessionRejected()
				_ = authenticated.Mux.Close()
				slog.Warn("NP/2 constellation admission failed",
					"event", "constellation_admission_failed", "carrier", carrierName)
				return
			}
			defer attachment.Close()
			metrics.ConstellationAdmitted()
			defer metrics.ConstellationDetached()
			principal, principalErr := constellation.PrincipalFromCredentialID(authenticated.CredentialID)
			if principalErr != nil {
				_ = authenticated.Mux.Close()
				return
			}
			control, controlErr := constellation.NewControlChannel(ctx, constellation.ControlChannelConfig{
				Mux: authenticated.Mux, ConstellationID: attachment.ConstellationID,
				FirstMessageID: attachment.ControlNextMessageID,
				MaxFlows:       productionConstellationMaxFlows,
			})
			if controlErr != nil {
				_ = authenticated.Mux.Close()
				return
			}
			defer control.Close()
			continuityRuntime = constellationRuntime.runtime
			continuityLease = proxy.ContinuityLease{
				Principal: principal, ConstellationID: attachment.ConstellationID,
				LeaseKey: attachment.LeaseKey, Control: control, Mux: authenticated.Mux,
			}
		}
	}
	metrics.SessionStarted(connection.Kind(), time.Since(authenticationStarted))
	slog.Info("NP/2 session connected", "event", "session_connected", "carrier", carrierName)
	var terminalError error
	defer func() {
		stats := authenticated.Mux.Stats()
		reason := observability.ClassifyTerminalError(terminalError)
		if ctx.Err() != nil {
			reason = observability.TerminalShutdown
		}
		metrics.SessionEnded(connection.Kind(), stats, authenticated.CoverStats(), reason)
		if usageSession != nil {
			_ = usageSession.Close()
		}
		_ = authenticated.Mux.Close()
		slog.Info("NP/2 session stopped", "event", "session_stopped", "carrier", carrierName,
			"reason", string(reason))
	}()
	proxyServer := proxy.Server{
		Mux: authenticated.Mux, Policy: proxy.DestinationPolicy{},
		Datagrams:   authenticated.Datagrams,
		UDPStats:    &metrics.UDP,
		DialTimeout: config.DialTimeout.Duration, MaxConnections: config.MaxTargetConnections,
		MaxUDPAssociations: int(extensionOffer.MaxUDPAssociations),
		ResourceLimiter:    resourceLimiter, CredentialID: authenticated.CredentialID,
		Continuity: continuityRuntime, ContinuityLease: continuityLease,
		Catalog: catalogHandler,
		AuthorizeUDP: func(authorizeContext context.Context) (uint64, bool) {
			parameters, negotiated, waitErr := authenticated.WaitExtensions(authorizeContext)
			return parameters.MaxUDPPayload, waitErr == nil && negotiated &&
				parameters.Capabilities&protocol.CapabilityReliableUDP != 0
		},
		UDPIdleTimeout: time.Duration(extensionOffer.UDPIdleTimeoutMS) * time.Millisecond,
	}
	if clusterRelay != nil {
		proxyServer.RouteTCP = clusterRelay.runtime.RouteTCP
		proxyServer.RouteUDP = clusterRelay.runtime.RouteUDP
		proxyServer.RouteClientTCP = clusterRelay.runtime.RouteClientTCP
		proxyServer.RouteClientUDP = clusterRelay.runtime.RouteClientUDP
		proxyServer.ClusterRelay = clusterRelay.runtime.HandleRelay
		proxyServer.CatalogRelay = clusterRelay.catalogRelay
		proxyServer.CredentialSync = clusterRelay.credentialSync
		proxyServer.ClusterState = clusterRelay.clusterState
		proxyServer.GeoDataControl = clusterRelay.geodataControl
	}
	terminalError = proxyServer.Serve(ctx)
}

func shouldTrackUserSessions(server config.Server) bool {
	if server.UserPolicyFile == "" || server.UsageStateFile == "" {
		return false
	}
	return server.ClusterNodeID == "" || server.ClusterNodeID == server.ClusterMasterNodeID
}

func rejectUsageAdmission(err error, credentialID string, clusterRelay *clusterRelayServices) bool {
	return err != nil && !(errors.Is(err, usage.ErrUserInactive) && clusterRelay.acceptsPeerCredential(credentialID))
}

func usageRejectionReason(err error) string {
	switch {
	case errors.Is(err, usage.ErrDeviceIdentityRequired):
		return "device_identity_required"
	case errors.Is(err, usage.ErrDeviceLimit):
		return "device_limit"
	case errors.Is(err, usage.ErrUserInactive):
		return "user_inactive"
	default:
		return "usage_state_unavailable"
	}
}
