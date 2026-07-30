package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
	"neproto.local/chameleon/internal/socks5"
)

const (
	defaultMaxConnections  = 128
	defaultDialTimeout     = 10 * time.Second
	defaultUDPIdleTimeout  = 60 * time.Second
	MaxCatalogPayload      = 256 << 10
	MaxClusterStatePayload = 4 << 20
	MaxGeoDataPayload      = 16 << 10
)

var (
	ErrInvalidConfig       = errors.New("invalid proxy configuration")
	ErrUDPAssociationLimit = errors.New("UDP association limit reached")
)

type Connector struct {
	Mux           *session.Mux
	Datagrams     *session.DatagramMux
	MaxUDPPayload uint64
}

func (c Connector) Connect(ctx context.Context, request socks5.Request) (io.ReadWriteCloser, error) {
	if ctx == nil || c.Mux == nil {
		return nil, &socks5.ReplyError{Code: socks5.ReplyGeneralFailure}
	}
	metadata, err := EncodeTarget(Target{Host: request.Host, Port: request.Port})
	if err != nil {
		return nil, &socks5.ReplyError{Code: socks5.ReplyAddressNotSupported}
	}
	stream, err := c.Mux.Open(ctx, metadata)
	if err == nil {
		return stream, nil
	}
	var rejection *session.RejectError
	if errors.As(err, &rejection) && rejection.Code >= socks5.ReplyGeneralFailure &&
		rejection.Code <= socks5.ReplyAddressNotSupported {
		return nil, &socks5.ReplyError{Code: rejection.Code}
	}
	return nil, &socks5.ReplyError{Code: socks5.ReplyGeneralFailure}
}

func (c Connector) AssociateUDP(ctx context.Context) (socks5.UDPAssociation, error) {
	if ctx == nil || c.Mux == nil || c.MaxUDPPayload < 1200 ||
		c.MaxUDPPayload > MaxUDPDatagramPayload {
		return nil, &socks5.ReplyError{Code: socks5.ReplyCommandNotSupported}
	}
	metadata, err := EncodeOpenRequest(OpenRequest{Command: CommandUDPAssociate})
	if err != nil {
		return nil, &socks5.ReplyError{Code: socks5.ReplyGeneralFailure}
	}
	stream, err := c.Mux.Open(ctx, metadata)
	if err != nil {
		return nil, connectorReplyError(err)
	}
	var endpoint *session.DatagramEndpoint
	if c.Datagrams != nil && c.Datagrams.Enabled() {
		endpoint, err = c.Datagrams.OpenEndpoint(stream.ID())
		if err != nil {
			_ = stream.Close()
			return nil, &socks5.ReplyError{Code: socks5.ReplyGeneralFailure}
		}
	}
	var association *UDPAssociation
	if endpoint != nil {
		association, err = NewUDPAssociationWithDatagrams(
			stream, CommandUDPAssociate, Target{}, c.MaxUDPPayload, endpoint,
		)
	} else {
		association, err = NewUDPAssociation(stream, CommandUDPAssociate, Target{}, c.MaxUDPPayload)
	}
	if err != nil {
		_ = stream.Close()
		return nil, &socks5.ReplyError{Code: socks5.ReplyGeneralFailure}
	}
	return &socksUDPAssociation{inner: association}, nil
}

func connectorReplyError(err error) error {
	var rejection *session.RejectError
	if errors.As(err, &rejection) && rejection.Code >= socks5.ReplyGeneralFailure &&
		rejection.Code <= socks5.ReplyAddressNotSupported {
		return &socks5.ReplyError{Code: rejection.Code}
	}
	return &socks5.ReplyError{Code: socks5.ReplyGeneralFailure}
}

type socksUDPAssociation struct {
	inner *UDPAssociation
}

func (a *socksUDPAssociation) WriteDatagram(payload []byte, target socks5.Request) error {
	if a == nil || a.inner == nil {
		return net.ErrClosed
	}
	destination := Target{Host: target.Host, Port: target.Port}
	return a.inner.WriteDatagram(payload, &destination)
}

func (a *socksUDPAssociation) ReadDatagram() ([]byte, socks5.Request, error) {
	if a == nil || a.inner == nil {
		return nil, socks5.Request{}, net.ErrClosed
	}
	payload, target, err := a.inner.ReadDatagram()
	if err != nil {
		return nil, socks5.Request{}, err
	}
	return payload, socks5.Request{Host: target.Host, Port: target.Port}, nil
}

func (a *socksUDPAssociation) Close() error {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.Close()
}

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type CatalogHandler func(context.Context, string) ([]byte, error)
type CatalogRelayHandler func(context.Context, string, string) ([]byte, error)
type CredentialSyncHandler func(context.Context, string, cluster.CredentialSyncRequest) error
type ClusterStateHandler func(context.Context, string) ([]byte, error)
type GeoDataControlHandler func(context.Context, string, cluster.GeoDataRequest) ([]byte, error)

type DuplexStream interface {
	io.ReadWriteCloser
	CloseWrite() error
}

type TCPRouteHandler func(context.Context, string, Target) (DuplexStream, bool, error)
type UDPRouteHandler func(context.Context, string, Target) (DuplexStream, bool, error)
type ClientTCPRouteHandler func(context.Context, string, Target, cluster.ClientRouteRequest) (DuplexStream, bool, error)
type ClientUDPRouteHandler func(context.Context, string, Target, cluster.ClientRouteRequest) (DuplexStream, bool, error)
type ClusterRelayHandler func(context.Context, string, cluster.RelayRequest) (DuplexStream, error)

type Server struct {
	Mux                *session.Mux
	Policy             DestinationPolicy
	Resolver           Resolver
	Dialer             Dialer
	PacketListener     PacketListener
	AuthorizeUDP       func(context.Context) (uint64, bool)
	DialTimeout        time.Duration
	UDPIdleTimeout     time.Duration
	MaxConnections     int
	MaxUDPAssociations int
	MaxUDPPayload      uint64
	UDPStats           *UDPStatistics
	Datagrams          *session.DatagramMux
	ResourceLimiter    *ResourceLimiter
	CredentialID       string
	Continuity         *ContinuityRuntime
	ContinuityLease    ContinuityLease
	Catalog            CatalogHandler
	CatalogRelay       CatalogRelayHandler
	CredentialSync     CredentialSyncHandler
	ClusterState       ClusterStateHandler
	GeoDataControl     GeoDataControlHandler
	RouteTCP           TCPRouteHandler
	RouteUDP           UDPRouteHandler
	RouteClientTCP     ClientTCPRouteHandler
	RouteClientUDP     ClientUDPRouteHandler
	ClusterRelay       ClusterRelayHandler

	udpSemaphore chan struct{}
}

func (s Server) Serve(ctx context.Context) error {
	if ctx == nil || s.Mux == nil || s.DialTimeout < 0 || s.UDPIdleTimeout < 0 ||
		s.MaxConnections < 0 || s.MaxUDPAssociations < 0 || (s.MaxUDPPayload != 0 &&
		(s.MaxUDPPayload < 1200 || s.MaxUDPPayload > MaxUDPDatagramPayload)) ||
		(s.ResourceLimiter != nil && s.CredentialID == "") {
		return ErrInvalidConfig
	}
	if s.Continuity != nil && (s.ContinuityLease.Principal == ([32]byte{}) ||
		s.ContinuityLease.ConstellationID == (protocol.ContinuityID{}) ||
		s.ContinuityLease.LeaseKey == (protocol.ContinuityID{}) || s.ContinuityLease.Control == nil ||
		s.ContinuityLease.Mux == nil) {
		return ErrInvalidConfig
	}
	maximum := s.MaxConnections
	if maximum == 0 {
		maximum = defaultMaxConnections
	}
	maximumUDP := s.MaxUDPAssociations
	if maximumUDP == 0 || maximumUDP > maximum {
		maximumUDP = maximum
	}
	runtimeServer := s
	runtimeServer.udpSemaphore = make(chan struct{}, maximumUDP)
	serveContext, cancelServe := context.WithCancel(ctx)
	semaphore := make(chan struct{}, maximum)
	var wait sync.WaitGroup
	defer func() {
		cancelServe()
		wait.Wait()
	}()
	for {
		incoming, err := s.Mux.Accept(serveContext)
		if err != nil {
			if serveContext.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case semaphore <- struct{}{}:
			wait.Add(1)
			go func() {
				defer wait.Done()
				defer func() { <-semaphore }()
				_ = runtimeServer.handleIncoming(serveContext, incoming)
			}()
		case <-serveContext.Done():
			_ = incoming.Reject(socks5.ReplyGeneralFailure)
			return nil
		}
	}
}

func (s Server) handleIncoming(ctx context.Context, incoming *session.Incoming) error {
	metadata := incoming.Metadata()
	if protocol.IsContinuityOpenMetadata(metadata) {
		return s.handleContinuityIncoming(ctx, incoming, metadata)
	}
	request, err := DecodeOpenRequest(metadata)
	if err != nil {
		_ = incoming.Reject(socks5.ReplyAddressNotSupported)
		return err
	}
	switch request.Command {
	case CommandTCPConnect:
		return s.handleTCPIncoming(ctx, incoming, request.Target, nil)
	case CommandTCPClientRoute:
		return s.handleTCPIncoming(ctx, incoming, request.Target, request.ClientRoute)
	case CommandUDPFixed, CommandUDPAssociate:
		return s.handleUDPIncoming(ctx, incoming, request)
	case CommandUDPClientRoute:
		request.Command = CommandUDPFixed
		return s.handleUDPIncoming(ctx, incoming, request)
	case CommandClusterCatalog:
		return s.handleCatalogIncoming(ctx, incoming)
	case CommandClusterRelay:
		return s.handleClusterRelayIncoming(ctx, incoming, request.Relay)
	case CommandClusterCatalogRelay:
		return s.handleCatalogRelayIncoming(ctx, incoming, request.CatalogUserID)
	case CommandClusterCredentialSync:
		return s.handleCredentialSyncIncoming(ctx, incoming, request.CredentialSync)
	case CommandClusterState:
		return s.handleClusterStateIncoming(ctx, incoming)
	case CommandClusterGeoData:
		return s.handleGeoDataIncoming(ctx, incoming, request.GeoData)
	default:
		_ = incoming.Reject(socks5.ReplyCommandNotSupported)
		return ErrInvalidTarget
	}
}

func (s Server) handleGeoDataIncoming(ctx context.Context, incoming *session.Incoming, request *cluster.GeoDataRequest) error {
	if s.GeoDataControl == nil || s.CredentialID == "" || request == nil {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return ErrInvalidConfig
	}
	payload, err := s.GeoDataControl(ctx, s.CredentialID, *request)
	if err != nil || len(payload) == 0 || len(payload) > MaxGeoDataPayload {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		if err != nil {
			return err
		}
		return ErrInvalidConfig
	}
	stream, err := incoming.Accept()
	if err != nil {
		return err
	}
	defer stream.Close()
	return writeBoundedPayload(stream, payload)
}

func (s Server) handleClusterStateIncoming(ctx context.Context, incoming *session.Incoming) error {
	if s.ClusterState == nil || s.CredentialID == "" {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return ErrInvalidConfig
	}
	payload, err := s.ClusterState(ctx, s.CredentialID)
	if err != nil || len(payload) == 0 || len(payload) > MaxClusterStatePayload {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		if err != nil {
			return err
		}
		return ErrInvalidConfig
	}
	stream, err := incoming.Accept()
	if err != nil {
		return err
	}
	defer stream.Close()
	return writeBoundedPayload(stream, payload)
}

func (s Server) handleCredentialSyncIncoming(ctx context.Context, incoming *session.Incoming, request *cluster.CredentialSyncRequest) error {
	if s.CredentialSync == nil || s.CredentialID == "" || request == nil {
		slog.Warn("NP/2 cluster credential sync rejected", "event", "cluster_credential_sync_rejected", "reason", "handler_unavailable")
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return ErrInvalidConfig
	}
	if err := s.CredentialSync(ctx, s.CredentialID, *request); err != nil {
		slog.Warn("NP/2 cluster credential sync rejected", "event", "cluster_credential_sync_rejected", "reason", err.Error())
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return err
	}
	stream, err := incoming.Accept()
	if err != nil {
		return err
	}
	defer stream.Close()
	if _, err := stream.Write([]byte{1}); err != nil {
		return err
	}
	return stream.CloseWrite()
}

func (s Server) handleCatalogRelayIncoming(ctx context.Context, incoming *session.Incoming, userID string) error {
	if s.CatalogRelay == nil || s.CredentialID == "" || userID == "" {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return ErrInvalidConfig
	}
	payload, err := s.CatalogRelay(ctx, s.CredentialID, userID)
	if err != nil || len(payload) == 0 || len(payload) > MaxCatalogPayload {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return ErrInvalidConfig
	}
	stream, err := incoming.Accept()
	if err != nil {
		return err
	}
	defer stream.Close()
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeCatalogBytes(stream, header[:]); err != nil {
		return err
	}
	if err := writeCatalogBytes(stream, payload); err != nil {
		return err
	}
	return stream.CloseWrite()
}

func (s Server) handleClusterRelayIncoming(ctx context.Context, incoming *session.Incoming, request *cluster.RelayRequest) error {
	if s.ClusterRelay == nil || s.CredentialID == "" || request == nil {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return ErrInvalidConfig
	}
	peer, err := s.ClusterRelay(ctx, s.CredentialID, *request)
	if err != nil {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return err
	}
	stream, err := incoming.Accept()
	if err != nil {
		_ = peer.Close()
		return err
	}
	return relayDuplex(ctx, stream, peer)
}

func (s Server) handleCatalogIncoming(ctx context.Context, incoming *session.Incoming) error {
	if s.Catalog == nil || s.CredentialID == "" {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return ErrInvalidConfig
	}
	payload, err := s.Catalog(ctx, s.CredentialID)
	if err != nil || len(payload) == 0 || len(payload) > MaxCatalogPayload {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		if err != nil {
			return err
		}
		return ErrInvalidConfig
	}
	stream, err := incoming.Accept()
	if err != nil {
		return err
	}
	defer stream.Close()
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeCatalogBytes(stream, header[:]); err != nil {
		return err
	}
	if err := writeCatalogBytes(stream, payload); err != nil {
		return err
	}
	return stream.CloseWrite()
}

func writeCatalogBytes(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(payload) {
			return io.ErrUnexpectedEOF
		}
		payload = payload[written:]
	}
	return nil
}

func FetchCatalog(ctx context.Context, mux *session.Mux) ([]byte, error) {
	if ctx == nil || mux == nil {
		return nil, ErrInvalidConfig
	}
	metadata, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterCatalog})
	if err != nil {
		return nil, err
	}
	return fetchCatalogWithMetadata(ctx, mux, metadata)
}

func FetchRelayedCatalog(ctx context.Context, mux *session.Mux, userID string) ([]byte, error) {
	if ctx == nil || mux == nil {
		return nil, ErrInvalidConfig
	}
	metadata, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterCatalogRelay, CatalogUserID: userID})
	if err != nil {
		return nil, err
	}
	return fetchCatalogWithMetadata(ctx, mux, metadata)
}

func FetchClusterState(ctx context.Context, mux *session.Mux) ([]byte, error) {
	if ctx == nil || mux == nil {
		return nil, ErrInvalidConfig
	}
	metadata, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterState})
	if err != nil {
		return nil, err
	}
	return fetchBoundedPayload(ctx, mux, metadata, MaxClusterStatePayload)
}

func FetchGeoDataControl(ctx context.Context, mux *session.Mux, request cluster.GeoDataRequest) ([]byte, error) {
	if ctx == nil || mux == nil || cluster.ValidateGeoDataRequest(request) != nil {
		return nil, ErrInvalidConfig
	}
	metadata, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterGeoData, GeoData: &request})
	if err != nil {
		return nil, err
	}
	return fetchBoundedPayload(ctx, mux, metadata, MaxGeoDataPayload)
}

func fetchCatalogWithMetadata(ctx context.Context, mux *session.Mux, metadata []byte) ([]byte, error) {
	return fetchBoundedPayload(ctx, mux, metadata, MaxCatalogPayload)
}

func fetchBoundedPayload(ctx context.Context, mux *session.Mux, metadata []byte, maximum uint32) ([]byte, error) {
	stream, err := mux.Open(ctx, metadata)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var header [4]byte
	if _, err := io.ReadFull(stream, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maximum {
		return nil, ErrInvalidConfig
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(stream, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeBoundedPayload(stream DuplexStream, payload []byte) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeCatalogBytes(stream, header[:]); err != nil {
		return err
	}
	if err := writeCatalogBytes(stream, payload); err != nil {
		return err
	}
	return stream.CloseWrite()
}

func (s Server) handleContinuityIncoming(
	ctx context.Context,
	incoming *session.Incoming,
	raw []byte,
) error {
	metadata, err := protocol.ParseContinuityOpenMetadata(raw)
	if err != nil || s.Continuity == nil ||
		metadata.ConstellationID != s.ContinuityLease.ConstellationID ||
		metadata.LeaseKey != s.ContinuityLease.LeaseKey {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		if err != nil {
			return err
		}
		return ErrContinuityLeaseBinding
	}
	if metadata.Mode == protocol.ContinuityOpenResume {
		stream, err := incoming.Accept()
		if err != nil {
			return err
		}
		return s.Continuity.Resume(metadata, s.ContinuityLease, stream)
	}
	request, err := DecodeOpenRequest(metadata.Inner)
	if err != nil || (request.Command != CommandTCPConnect && request.Command != CommandTCPClientRoute) {
		_ = incoming.Reject(socks5.ReplyCommandNotSupported)
		if err != nil {
			return err
		}
		return ErrInvalidTarget
	}
	if s.ResourceLimiter != nil && !s.ResourceLimiter.AcquireTCP(s.CredentialID) {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return ErrResourceLimit
	}
	acquired := s.ResourceLimiter != nil
	release := func() {
		if acquired {
			s.ResourceLimiter.ReleaseTCP(s.CredentialID)
		}
	}
	peer, handled, err := s.routeTCP(ctx, request.Target, request.ClientRoute)
	var connection io.ReadWriteCloser = peer
	if err != nil {
		release()
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return err
	}
	if !handled {
		resolver := s.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		addresses, resolveErr := s.Policy.Resolve(ctx, request.Target, resolver)
		if resolveErr != nil {
			release()
			code := byte(socks5.ReplyHostUnreachable)
			if errors.Is(resolveErr, ErrDestinationDenied) {
				code = socks5.ReplyNotAllowed
			}
			_ = incoming.Reject(code)
			return resolveErr
		}
		connection, err = s.dialResolved(ctx, request.Target.Port, addresses)
		if err != nil {
			release()
			_ = incoming.Reject(dialReplyCode(err))
			return err
		}
	}
	stream, err := incoming.Accept()
	if err != nil {
		release()
		_ = connection.Close()
		return err
	}
	return s.Continuity.AdmitNew(metadata, s.ContinuityLease, stream, connection, release)
}

func (s Server) handleTCPIncoming(
	ctx context.Context,
	incoming *session.Incoming,
	target Target,
	clientRoute *cluster.ClientRouteRequest,
) error {
	if s.ResourceLimiter != nil {
		if !s.ResourceLimiter.AcquireTCP(s.CredentialID) {
			_ = incoming.Reject(socks5.ReplyNotAllowed)
			return ErrResourceLimit
		}
		defer s.ResourceLimiter.ReleaseTCP(s.CredentialID)
	}
	peer, handled, err := s.routeTCP(ctx, target, clientRoute)
	if handled {
		if err != nil || peer == nil {
			_ = incoming.Reject(socks5.ReplyNotAllowed)
			if err != nil {
				return err
			}
			return ErrInvalidConfig
		}
		stream, acceptErr := incoming.Accept()
		if acceptErr != nil {
			_ = peer.Close()
			return acceptErr
		}
		return relayDuplex(ctx, stream, peer)
	}
	if err != nil {
		_ = incoming.Reject(socks5.ReplyNotAllowed)
		return err
	}
	resolver := s.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := s.Policy.Resolve(ctx, target, resolver)
	if err != nil {
		code := byte(socks5.ReplyHostUnreachable)
		if errors.Is(err, ErrDestinationDenied) {
			code = socks5.ReplyNotAllowed
		}
		_ = incoming.Reject(code)
		return err
	}
	connection, err := s.dialResolved(ctx, target.Port, addresses)
	if err != nil {
		_ = incoming.Reject(dialReplyCode(err))
		return err
	}
	stream, err := incoming.Accept()
	if err != nil {
		_ = connection.Close()
		return err
	}
	return relayTarget(ctx, stream, connection)
}

func (s Server) routeTCP(
	ctx context.Context,
	target Target,
	clientRoute *cluster.ClientRouteRequest,
) (DuplexStream, bool, error) {
	if clientRoute != nil {
		if s.RouteClientTCP == nil {
			return nil, true, ErrInvalidConfig
		}
		return s.RouteClientTCP(ctx, s.CredentialID, target, *clientRoute)
	}
	if s.RouteTCP != nil {
		return s.RouteTCP(ctx, s.CredentialID, target)
	}
	return nil, false, nil
}

func (s Server) dialResolved(ctx context.Context, port uint16, addresses []netip.Addr) (net.Conn, error) {
	dialer := s.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	timeout := s.DialTimeout
	if timeout == 0 {
		timeout = defaultDialTimeout
	}
	dialContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastError error
	for _, address := range addresses {
		destination := netip.AddrPortFrom(address, port).String()
		connection, err := dialer.DialContext(dialContext, "tcp", destination)
		if err == nil {
			return connection, nil
		}
		lastError = err
		if dialContext.Err() != nil {
			break
		}
	}
	if lastError == nil {
		lastError = ErrResolution
	}
	return nil, fmt.Errorf("dial resolved target: %w", lastError)
}

func dialReplyCode(err error) byte {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return socks5.ReplyConnectionRefused
	}
	if errors.Is(err, syscall.ENETUNREACH) {
		return socks5.ReplyNetworkUnreachable
	}
	if os.IsTimeout(err) {
		return socks5.ReplyHostUnreachable
	}
	return socks5.ReplyHostUnreachable
}
