package webrtc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	pion "github.com/pion/webrtc/v4"
	"neproto.local/chameleon/internal/carrier"
)

const (
	MaxSignalingBody      = 96 * 1024
	MaxSDPSize            = 64 * 1024
	defaultMaxPeers       = 32
	maxPeersLimit         = 4096
	defaultGatherTimeout  = 8 * time.Second
	defaultConnectTimeout = 12 * time.Second
	dataChannelLabel      = "updates"
	datagramChannelLabel  = "realtime"
)

var (
	ErrTLSRequired             = errors.New("HTTPS signaling is required")
	ErrTLSVerificationRequired = errors.New("TLS certificate verification is required")
	ErrTLS13Required           = errors.New("TLS 1.3 is required")
	ErrSignaling               = errors.New("WebRTC signaling failed")
	ErrServerClosed            = errors.New("WebRTC signaling server closed")
)

type ServerConfig struct {
	Path              string
	Engine            EngineConfig
	PeerConfiguration pion.Configuration
	MaxPeers          int
	GatherTimeout     time.Duration
	ConnectTimeout    time.Duration
}

type Server struct {
	path              string
	api               *pion.API
	peerConfiguration pion.Configuration
	gatherTimeout     time.Duration
	connectTimeout    time.Duration
	slots             chan struct{}
	connections       chan carrier.Carrier
	done              chan struct{}

	mu        sync.Mutex
	closed    bool
	peers     map[*pion.PeerConnection]func()
	closeOnce sync.Once
}

func NewServer(config ServerConfig) (*Server, error) {
	if !validRoute(config.Path) || config.MaxPeers < 0 || config.MaxPeers > maxPeersLimit ||
		config.GatherTimeout < 0 || config.ConnectTimeout < 0 {
		return nil, ErrInvalidConfig
	}
	api, err := newAPI(config.Engine)
	if err != nil {
		return nil, err
	}
	maximum := config.MaxPeers
	if maximum == 0 {
		maximum = defaultMaxPeers
	}
	gatherTimeout := config.GatherTimeout
	if gatherTimeout == 0 {
		gatherTimeout = defaultGatherTimeout
	}
	connectTimeout := config.ConnectTimeout
	if connectTimeout == 0 {
		connectTimeout = defaultConnectTimeout
	}
	return &Server{
		path: config.Path, api: api, peerConfiguration: config.PeerConfiguration,
		gatherTimeout: gatherTimeout, connectTimeout: connectTimeout,
		slots: make(chan struct{}, maximum), connections: make(chan carrier.Carrier, maximum),
		done: make(chan struct{}), peers: make(map[*pion.PeerConnection]func()),
	}, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mediaType, _, mediaTypeErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if request.Method != http.MethodPost || request.URL.Path != s.path ||
			mediaTypeErr != nil || mediaType != "application/json" {
			http.NotFound(writer, request)
			return
		}
		offer, err := decodeDescription(http.MaxBytesReader(writer, request.Body, MaxSignalingBody))
		if err != nil || offer.Type != pion.SDPTypeOffer {
			http.NotFound(writer, request)
			return
		}
		if !s.acquireSlot() {
			http.NotFound(writer, request)
			return
		}
		peer, err := s.api.NewPeerConnection(s.peerConfiguration)
		if err != nil {
			s.releaseSlot()
			http.NotFound(writer, request)
			return
		}
		release, registered := s.registerPeer(peer)
		if !registered {
			_ = peer.Close()
			s.releaseSlot()
			http.NotFound(writer, request)
			return
		}
		handedOff := false
		defer func() {
			if !handedOff {
				_ = peer.Close()
				release()
			}
		}()

		ready := make(chan struct{})
		var readyOnce sync.Once
		var channelMu sync.Mutex
		var connection *Conn
		var reliableChannel *pion.DataChannel
		var datagramChannel *pion.DataChannel
		peer.OnDataChannel(func(dataChannel *pion.DataChannel) {
			channelMu.Lock()
			defer channelMu.Unlock()
			switch dataChannel.Label() {
			case dataChannelLabel:
				if reliableChannel != nil || !dataChannel.Ordered() ||
					dataChannel.MaxRetransmits() != nil || dataChannel.MaxPacketLifeTime() != nil {
					_ = dataChannel.Close()
					return
				}
				reliableChannel = dataChannel
				connection = newConn(peer, dataChannel, datagramChannel, release, func(connection *Conn) {
					select {
					case s.connections <- connection:
						readyOnce.Do(func() { close(ready) })
					case <-s.done:
						connection.abort(ErrServerClosed)
					default:
						connection.abort(ErrBackpressure)
					}
				})
			case datagramChannelLabel:
				if datagramChannel != nil || dataChannel.Ordered() || dataChannel.MaxRetransmits() == nil ||
					*dataChannel.MaxRetransmits() != 0 || dataChannel.MaxPacketLifeTime() != nil {
					_ = dataChannel.Close()
					return
				}
				datagramChannel = dataChannel
				if connection != nil && !connection.attachDatagramChannel(dataChannel) {
					_ = dataChannel.Close()
				}
			default:
				_ = dataChannel.Close()
			}
		})
		peer.OnConnectionStateChange(func(state pion.PeerConnectionState) {
			if state == pion.PeerConnectionStateFailed || state == pion.PeerConnectionStateClosed {
				release()
			}
		})
		if err := peer.SetRemoteDescription(offer); err != nil {
			http.NotFound(writer, request)
			return
		}
		answer, err := peer.CreateAnswer(nil)
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		gathered := pion.GatheringCompletePromise(peer)
		if err := peer.SetLocalDescription(answer); err != nil {
			http.NotFound(writer, request)
			return
		}
		gatherContext, cancelGather := context.WithTimeout(request.Context(), s.gatherTimeout)
		defer cancelGather()
		select {
		case <-gathered:
		case <-gatherContext.Done():
			http.NotFound(writer, request)
			return
		case <-s.done:
			http.NotFound(writer, request)
			return
		}
		localDescription := peer.LocalDescription()
		if localDescription == nil || len(localDescription.SDP) > MaxSDPSize {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(writer).Encode(localDescription); err != nil {
			return
		}
		handedOff = true
		go s.enforceConnectDeadline(peer, release, ready)
	})
}

func (s *Server) Accept(ctx context.Context) (carrier.Carrier, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	select {
	case <-s.done:
		return nil, ErrServerClosed
	default:
	}
	select {
	case connection := <-s.connections:
		return connection, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, ErrServerClosed
	}
}

func (s *Server) ActivePeers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.peers)
}

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.done)
		peers := make(map[*pion.PeerConnection]func(), len(s.peers))
		for peer, release := range s.peers {
			peers[peer] = release
		}
		s.mu.Unlock()
		for peer, release := range peers {
			_ = peer.Close()
			release()
		}
	})
	return nil
}

func (s *Server) acquireSlot() bool {
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseSlot() {
	select {
	case <-s.slots:
	default:
	}
}

func (s *Server) registerPeer(peer *pion.PeerConnection) (func(), bool) {
	var once sync.Once
	release := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.peers, peer)
			s.mu.Unlock()
			s.releaseSlot()
		})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return release, false
	}
	s.peers[peer] = release
	return release, true
}

func (s *Server) enforceConnectDeadline(peer *pion.PeerConnection, release func(), ready <-chan struct{}) {
	timer := time.NewTimer(s.connectTimeout)
	defer timer.Stop()
	select {
	case <-ready:
		return
	case <-timer.C:
		_ = peer.Close()
		release()
	case <-s.done:
	}
}

type DialConfig struct {
	SignalingURL      string
	TLSConfig         *tls.Config
	Header            http.Header
	ServerAddresses   []netip.Addr
	Engine            EngineConfig
	PeerConfiguration pion.Configuration
	GatherTimeout     time.Duration
	ConnectTimeout    time.Duration
}

func Dial(ctx context.Context, config DialConfig) (*Conn, error) {
	if ctx == nil || config.GatherTimeout < 0 || config.ConnectTimeout < 0 {
		return nil, ErrInvalidConfig
	}
	httpClient, err := signalingHTTPClient(config.SignalingURL, config.TLSConfig, config.ServerAddresses)
	if err != nil {
		return nil, err
	}
	defer httpClient.CloseIdleConnections()
	api, err := newAPI(config.Engine)
	if err != nil {
		return nil, err
	}
	peer, err := api.NewPeerConnection(config.PeerConfiguration)
	if err != nil {
		return nil, fmt.Errorf("%w: create peer: %v", ErrSignaling, err)
	}
	ordered := true
	dataChannel, err := peer.CreateDataChannel(dataChannelLabel, &pion.DataChannelInit{Ordered: &ordered})
	if err != nil {
		_ = peer.Close()
		return nil, fmt.Errorf("%w: create data channel: %v", ErrSignaling, err)
	}
	unordered := false
	zeroRetransmits := uint16(0)
	datagramChannel, err := peer.CreateDataChannel(datagramChannelLabel, &pion.DataChannelInit{
		Ordered: &unordered, MaxRetransmits: &zeroRetransmits,
	})
	if err != nil {
		_ = dataChannel.Close()
		_ = peer.Close()
		return nil, fmt.Errorf("%w: create datagram channel: %v", ErrSignaling, err)
	}
	connection := newConn(peer, dataChannel, datagramChannel, nil, nil)
	fail := func(stage string, err error) (*Conn, error) {
		connection.finish(err)
		return nil, errors.Join(ErrSignaling, fmt.Errorf("%s: %w", stage, err))
	}
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		return fail("create offer", err)
	}
	gathered := pion.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(offer); err != nil {
		return fail("set local offer", err)
	}
	gatherTimeout := config.GatherTimeout
	if gatherTimeout == 0 {
		gatherTimeout = defaultGatherTimeout
	}
	gatherContext, cancelGather := context.WithTimeout(ctx, gatherTimeout)
	defer cancelGather()
	select {
	case <-gathered:
	case <-gatherContext.Done():
		return fail("gather offer", gatherContext.Err())
	}
	localDescription := peer.LocalDescription()
	if localDescription == nil || len(localDescription.SDP) > MaxSDPSize {
		return fail("local offer", ErrSignaling)
	}
	body, err := json.Marshal(localDescription)
	if err != nil {
		return fail("marshal offer", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.SignalingURL, bytes.NewReader(body))
	if err != nil {
		return fail("create request", err)
	}
	if config.Header != nil {
		request.Header = config.Header.Clone()
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := httpClient.Do(request)
	if err != nil {
		return fail("post offer", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fail("offer rejected", ErrSignaling)
	}
	rawAnswer, err := io.ReadAll(io.LimitReader(response.Body, MaxSignalingBody+1))
	if err != nil || len(rawAnswer) > MaxSignalingBody {
		if err == nil {
			err = ErrSignaling
		}
		return fail("read answer", err)
	}
	answer, err := decodeDescription(bytes.NewReader(rawAnswer))
	if err != nil || answer.Type != pion.SDPTypeAnswer {
		if err == nil {
			err = ErrSignaling
		}
		return fail("decode answer", err)
	}
	if err := peer.SetRemoteDescription(answer); err != nil {
		return fail("set remote answer", err)
	}
	connectTimeout := config.ConnectTimeout
	if connectTimeout == 0 {
		connectTimeout = defaultConnectTimeout
	}
	connectContext, cancelConnect := context.WithTimeout(ctx, connectTimeout)
	defer cancelConnect()
	if err := connection.waitOpen(connectContext); err != nil {
		return fail("open data channel", err)
	}
	return connection, nil
}

func signalingHTTPClient(rawURL string, config *tls.Config, serverAddresses []netip.Addr) (*http.Client, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, ErrTLSRequired
	}
	tlsConfig := &tls.Config{}
	if config != nil {
		tlsConfig = config.Clone()
	}
	if tlsConfig.InsecureSkipVerify {
		return nil, ErrTLSVerificationRequired
	}
	if (tlsConfig.MinVersion != 0 && tlsConfig.MinVersion < tls.VersionTLS13) ||
		(tlsConfig.MaxVersion != 0 && tlsConfig.MaxVersion < tls.VersionTLS13) {
		return nil, ErrTLS13Required
	}
	tlsConfig.MinVersion = tls.VersionTLS13
	tlsConfig.NextProtos = []string{"http/1.1"}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsConfig,
		DisableCompression: true, ForceAttemptHTTP2: false,
	}
	if len(serverAddresses) > 0 {
		port := parsed.Port()
		if port == "" {
			port = "443"
		}
		addresses := append([]netip.Addr(nil), serverAddresses...)
		for _, address := range addresses {
			if !address.IsValid() || address.IsUnspecified() {
				return nil, ErrInvalidConfig
			}
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var failures []error
			for _, address := range addresses {
				connection, dialErr := (&net.Dialer{}).DialContext(
					ctx, network, net.JoinHostPort(address.String(), port),
				)
				if dialErr == nil {
					return connection, nil
				}
				failures = append(failures, dialErr)
			}
			return nil, errors.Join(failures...)
		}
	}
	return &http.Client{Transport: transport}, nil
}

func decodeDescription(reader io.Reader) (pion.SessionDescription, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var description pion.SessionDescription
	if err := decoder.Decode(&description); err != nil || len(description.SDP) == 0 || len(description.SDP) > MaxSDPSize {
		return pion.SessionDescription{}, ErrSignaling
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return pion.SessionDescription{}, ErrSignaling
	}
	return description, nil
}

func validRoute(route string) bool {
	return strings.HasPrefix(route, "/") && route != "/" && !strings.ContainsAny(route, "?#") &&
		!strings.Contains(route, "//") && path.Clean(route) == route
}
