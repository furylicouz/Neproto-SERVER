package http3wt

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	webtransport "github.com/quic-go/webtransport-go"
	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

const (
	defaultBacklog             = 32
	defaultMaxSessions         = 64
	defaultFirstStreamTimeout  = 5 * time.Second
	defaultHandshakeTimeout    = 5 * time.Second
	defaultIdleTimeout         = 30 * time.Second
	defaultMaxDatagramPayload  = 1150
	maxBacklog                 = 1024
	maxSessions                = 4096
	maxConfiguredDatagramBytes = 65507
	reliableStreamPreface      = byte(0)
)

var (
	ErrInvalidConfig           = errors.New("invalid HTTP/3 carrier configuration")
	ErrTLSRequired             = errors.New("HTTPS URL is required")
	ErrTLSVerificationRequired = errors.New("TLS certificate verification is required")
	ErrTLS13Required           = errors.New("TLS 1.3 is required")
	ErrServerClosed            = errors.New("HTTP/3 carrier server closed")
	ErrServerBusy              = errors.New("HTTP/3 carrier session limit reached")
	ErrMessageTooLarge         = errors.New("HTTP/3 carrier message too large")
	ErrDatagramTooLarge        = errors.New("HTTP/3 carrier datagram too large")
	ErrEmptyMessage            = errors.New("HTTP/3 carrier message is empty")
	ErrClosed                  = errors.New("HTTP/3 carrier closed")
)

type DialConfig struct {
	URL                    string
	TLSConfig              *tls.Config
	Header                 http.Header
	ServerAddresses        []netip.Addr
	HandshakeIdleTimeout   time.Duration
	IdleTimeout            time.Duration
	MaxDatagramPayload     int
	InitialReceiveWindow   uint64
	MaximumReceiveWindow   uint64
	ConnectionReceiveLimit uint64
}

type ServerConfig struct {
	Path                   string
	TLSConfig              *tls.Config
	Backlog                int
	MaxSessions            int
	FirstStreamTimeout     time.Duration
	HandshakeIdleTimeout   time.Duration
	IdleTimeout            time.Duration
	MaxDatagramPayload     int
	InitialReceiveWindow   uint64
	MaximumReceiveWindow   uint64
	ConnectionReceiveLimit uint64
}

type Conn struct {
	session            *webtransport.Session
	stream             *webtransport.Stream
	dialer             *webtransport.Dialer
	maxDatagramPayload int
	release            func()

	readMu    sync.Mutex
	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
	closeErr  error
}

var (
	_ carrier.Carrier         = (*Conn)(nil)
	_ carrier.DatagramCarrier = (*Conn)(nil)
)

func Dial(ctx context.Context, config DialConfig) (*Conn, error) {
	if ctx == nil || config.HandshakeIdleTimeout < 0 || config.IdleTimeout < 0 ||
		config.InitialReceiveWindow > config.MaximumReceiveWindow ||
		config.MaxDatagramPayload < 0 || config.MaxDatagramPayload > maxConfiguredDatagramBytes {
		return nil, ErrInvalidConfig
	}
	parsed, err := validateURL(config.URL)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := clientTLSConfig(config.TLSConfig)
	if err != nil {
		return nil, err
	}
	// ServerAddresses controls only the UDP route. Certificate verification and
	// SNI must remain bound to the authenticated hostname from the WebTransport
	// URL, otherwise quic.DialAddrEarly derives an IP ServerName from the direct
	// address and rejects a valid DNS certificate.
	tlsConfig.ServerName = parsed.Hostname()
	quicConfig := newQUICConfig(
		config.HandshakeIdleTimeout, config.IdleTimeout,
		config.InitialReceiveWindow, config.MaximumReceiveWindow, config.ConnectionReceiveLimit,
	)
	dialer := &webtransport.Dialer{TLSClientConfig: tlsConfig, QUICConfig: quicConfig}
	if len(config.ServerAddresses) > 0 {
		addresses := append([]netip.Addr(nil), config.ServerAddresses...)
		for _, address := range addresses {
			if !address.IsValid() || address.IsUnspecified() {
				return nil, ErrInvalidConfig
			}
		}
		port := parsed.Port()
		if port == "" {
			port = "443"
		}
		dialer.DialAddr = func(attempt context.Context, _ string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			failures := make([]error, 0, len(addresses))
			for _, address := range addresses {
				connection, dialErr := quic.DialAddrEarly(attempt, net.JoinHostPort(address.String(), port), tlsCfg, cfg)
				if dialErr == nil {
					return connection, nil
				}
				failures = append(failures, dialErr)
				if attempt.Err() != nil {
					break
				}
			}
			return nil, errors.Join(failures...)
		}
	}

	_, session, err := dialer.Dial(ctx, config.URL, config.Header.Clone())
	if err != nil {
		_ = dialer.Close()
		return nil, fmt.Errorf("dial WebTransport: %w", err)
	}
	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		_ = session.CloseWithError(1, "reliable stream unavailable")
		_ = dialer.Close()
		return nil, fmt.Errorf("open reliable WebTransport stream: %w", err)
	}
	resetWriteDeadline := installWriteContext(stream, ctx)
	err = writeAll(stream, []byte{reliableStreamPreface})
	resetWriteDeadline()
	if err != nil {
		stream.CancelRead(1)
		stream.CancelWrite(1)
		_ = session.CloseWithError(1, "reliable stream preface failed")
		_ = dialer.Close()
		return nil, fmt.Errorf("write reliable WebTransport stream preface: %w", err)
	}
	connection := newConn(session, stream, dialer, normalizedDatagramLimit(config.MaxDatagramPayload), nil)
	go connection.rejectExtraStreams()
	return connection, nil
}

func (c *Conn) Send(ctx context.Context, raw []byte) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if len(raw) == 0 {
		return ErrEmptyMessage
	}
	if len(raw) > protocol.MaxCellSize {
		return ErrMessageTooLarge
	}
	if err := c.ready(ctx); err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ready(ctx); err != nil {
		return err
	}
	reset := installWriteContext(c.stream, ctx)
	defer reset()
	header := [4]byte{}
	binary.BigEndian.PutUint32(header[:], uint32(len(raw)))
	if err := writeAll(c.stream, header[:]); err != nil {
		return c.operationError(ctx, err)
	}
	if err := writeAll(c.stream, raw); err != nil {
		return c.operationError(ctx, err)
	}
	return nil
}

func (c *Conn) Receive(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	if err := c.ready(ctx); err != nil {
		return nil, err
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if err := c.ready(ctx); err != nil {
		return nil, err
	}
	reset := installReadContext(c.stream, ctx)
	defer reset()
	header := [4]byte{}
	if _, err := io.ReadFull(c.stream, header[:]); err != nil {
		return nil, c.operationError(ctx, err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 {
		_ = c.Close()
		return nil, ErrEmptyMessage
	}
	if size > uint32(protocol.MaxCellSize) {
		_ = c.Close()
		return nil, ErrMessageTooLarge
	}
	raw := make([]byte, int(size))
	if _, err := io.ReadFull(c.stream, raw); err != nil {
		return nil, c.operationError(ctx, err)
	}
	return raw, nil
}

func (c *Conn) SendDatagram(ctx context.Context, raw []byte) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if len(raw) > c.maxDatagramPayload {
		return ErrDatagramTooLarge
	}
	if err := c.ready(ctx); err != nil {
		return err
	}
	if err := c.session.SendDatagram(raw); err != nil {
		var tooLarge *quic.DatagramTooLargeError
		if errors.As(err, &tooLarge) {
			return errors.Join(ErrDatagramTooLarge, err)
		}
		return c.operationError(ctx, err)
	}
	return nil
}

func (c *Conn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	if err := c.ready(ctx); err != nil {
		return nil, err
	}
	raw, err := c.session.ReceiveDatagram(ctx)
	if err != nil {
		return nil, c.operationError(ctx, err)
	}
	if len(raw) > c.maxDatagramPayload {
		_ = c.Close()
		return nil, ErrDatagramTooLarge
	}
	return raw, nil
}

func (c *Conn) MaxDatagramPayload() int { return c.maxDatagramPayload }

func (c *Conn) Kind() protocol.CarrierKind { return protocol.CarrierHTTP3 }

func (c *Conn) RemoteAddresses() []netip.Addr {
	address, err := netip.ParseAddrPort(c.session.RemoteAddr().String())
	if err != nil || !address.Addr().IsValid() || address.Addr().IsUnspecified() {
		return nil
	}
	return []netip.Addr{address.Addr().Unmap()}
}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.stream.CancelRead(0)
		c.stream.CancelWrite(0)
		_ = c.session.CloseWithError(0, "")
		if c.dialer != nil {
			_ = c.dialer.Close()
		}
		if c.release != nil {
			c.release()
		}
	})
	return c.closeErr
}

func (c *Conn) ready(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	default:
		return nil
	}
}

func (c *Conn) operationError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	select {
	case <-c.done:
		return ErrClosed
	default:
		return err
	}
}

func (c *Conn) rejectExtraStreams() {
	for {
		stream, err := c.session.AcceptStream(c.session.Context())
		if err != nil {
			return
		}
		stream.CancelRead(1)
		stream.CancelWrite(1)
	}
}

func newConn(session *webtransport.Session, stream *webtransport.Stream, dialer *webtransport.Dialer, maxDatagramPayload int, release func()) *Conn {
	connection := &Conn{
		session: session, stream: stream, dialer: dialer,
		maxDatagramPayload: maxDatagramPayload, release: release, done: make(chan struct{}),
	}
	context.AfterFunc(session.Context(), func() { _ = connection.Close() })
	return connection
}

type Server struct {
	transport   *webtransport.Server
	path        string
	connections chan carrier.Carrier
	done        chan struct{}
	maxSessions int64
	active      atomic.Int64
	streamWait  time.Duration
	datagramMax int
	closeOnce   sync.Once
}

func NewServer(config ServerConfig) (*Server, error) {
	if !validRoute(config.Path) || config.Backlog < 0 || config.Backlog > maxBacklog ||
		config.MaxSessions < 0 || config.MaxSessions > maxSessions ||
		config.FirstStreamTimeout < 0 || config.HandshakeIdleTimeout < 0 || config.IdleTimeout < 0 ||
		config.InitialReceiveWindow > config.MaximumReceiveWindow ||
		config.MaxDatagramPayload < 0 || config.MaxDatagramPayload > maxConfiguredDatagramBytes {
		return nil, ErrInvalidConfig
	}
	tlsConfig, err := serverTLSConfig(config.TLSConfig)
	if err != nil {
		return nil, err
	}
	backlog := config.Backlog
	if backlog == 0 {
		backlog = defaultBacklog
	}
	sessionLimit := config.MaxSessions
	if sessionLimit == 0 {
		sessionLimit = defaultMaxSessions
	}
	streamWait := config.FirstStreamTimeout
	if streamWait == 0 {
		streamWait = defaultFirstStreamTimeout
	}
	h3Server := &http3.Server{
		TLSConfig: tlsConfig,
		QUICConfig: newQUICConfig(
			config.HandshakeIdleTimeout, config.IdleTimeout,
			config.InitialReceiveWindow, config.MaximumReceiveWindow, config.ConnectionReceiveLimit,
		),
	}
	webtransport.ConfigureHTTP3Server(h3Server)
	server := &Server{
		path: config.Path, connections: make(chan carrier.Carrier, backlog), done: make(chan struct{}),
		maxSessions: int64(sessionLimit), streamWait: streamWait,
		datagramMax: normalizedDatagramLimit(config.MaxDatagramPayload),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(config.Path, server.handle)
	h3Server.Handler = mux
	server.transport = &webtransport.Server{H3: h3Server}
	return server, nil
}

func (s *Server) Serve(packet net.PacketConn) error {
	if packet == nil {
		return ErrInvalidConfig
	}
	select {
	case <-s.done:
		return ErrServerClosed
	default:
	}
	err := s.transport.Serve(packet)
	if errors.Is(err, context.Canceled) || errors.Is(err, http.ErrServerClosed) {
		return ErrServerClosed
	}
	return err
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

func (s *Server) ActiveSessions() int { return int(s.active.Load()) }

func (s *Server) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		close(s.done)
		closeErr = s.transport.Close()
		for {
			select {
			case connection := <-s.connections:
				_ = connection.Close()
			default:
				return
			}
		}
	})
	return closeErr
}

func (s *Server) handle(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != s.path {
		http.NotFound(writer, request)
		return
	}
	if !s.acquire() {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
		return
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			s.active.Add(-1)
		})
	}
	ownedByConnection := false
	defer func() {
		if !ownedByConnection {
			release()
		}
	}()

	session, err := s.transport.Upgrade(writer, request)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	ctx, cancel := context.WithTimeout(session.Context(), s.streamWait)
	stream, err := session.AcceptStream(ctx)
	if err != nil {
		cancel()
		_ = session.CloseWithError(1, "reliable stream required")
		return
	}
	resetReadDeadline := installReadContext(stream, ctx)
	preface := [1]byte{}
	_, err = io.ReadFull(stream, preface[:])
	resetReadDeadline()
	cancel()
	if err != nil || preface[0] != reliableStreamPreface {
		stream.CancelRead(1)
		stream.CancelWrite(1)
		_ = session.CloseWithError(1, "invalid reliable stream preface")
		return
	}
	connection := newConn(session, stream, nil, s.datagramMax, release)
	ownedByConnection = true
	go connection.rejectExtraStreams()
	select {
	case s.connections <- connection:
	case <-s.done:
		_ = connection.Close()
	default:
		_ = connection.Close()
	}
}

func (s *Server) acquire() bool {
	for {
		current := s.active.Load()
		if current >= s.maxSessions {
			return false
		}
		if s.active.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func newQUICConfig(handshakeTimeout, idleTimeout time.Duration, initialWindow, maximumWindow, connectionLimit uint64) *quic.Config {
	if handshakeTimeout == 0 {
		handshakeTimeout = defaultHandshakeTimeout
	}
	if idleTimeout == 0 {
		idleTimeout = defaultIdleTimeout
	}
	if initialWindow == 0 {
		initialWindow = 512 << 10
	}
	if maximumWindow == 0 {
		maximumWindow = 4 << 20
	}
	if connectionLimit == 0 {
		connectionLimit = 16 << 20
	}
	return &quic.Config{
		HandshakeIdleTimeout:             handshakeTimeout,
		MaxIdleTimeout:                   idleTimeout,
		InitialStreamReceiveWindow:       initialWindow,
		MaxStreamReceiveWindow:           maximumWindow,
		InitialConnectionReceiveWindow:   min(initialWindow*2, connectionLimit),
		MaxConnectionReceiveWindow:       connectionLimit,
		MaxIncomingStreams:               8,
		MaxIncomingUniStreams:            16,
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
		Allow0RTT:                        false,
		KeepAlivePeriod:                  0,
	}
}

func validateURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, ErrTLSRequired
	}
	if !validRoute(parsed.EscapedPath()) || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrInvalidConfig
	}
	return parsed, nil
}

func clientTLSConfig(input *tls.Config) (*tls.Config, error) {
	config := &tls.Config{}
	if input != nil {
		config = input.Clone()
	}
	if config.InsecureSkipVerify {
		return nil, ErrTLSVerificationRequired
	}
	if (config.MinVersion != 0 && config.MinVersion < tls.VersionTLS13) ||
		(config.MaxVersion != 0 && config.MaxVersion < tls.VersionTLS13) {
		return nil, ErrTLS13Required
	}
	config.MinVersion = tls.VersionTLS13
	return config, nil
}

func serverTLSConfig(input *tls.Config) (*tls.Config, error) {
	if input == nil || (len(input.Certificates) == 0 && input.GetCertificate == nil) {
		return nil, ErrInvalidConfig
	}
	config := input.Clone()
	if (config.MinVersion != 0 && config.MinVersion < tls.VersionTLS13) ||
		(config.MaxVersion != 0 && config.MaxVersion < tls.VersionTLS13) {
		return nil, ErrTLS13Required
	}
	config.MinVersion = tls.VersionTLS13
	return http3.ConfigureTLSConfig(config), nil
}

func normalizedDatagramLimit(configured int) int {
	if configured == 0 {
		return defaultMaxDatagramPayload
	}
	return configured
}

func validRoute(route string) bool {
	return strings.HasPrefix(route, "/") && route != "/" &&
		!strings.ContainsAny(route, "?#") && !strings.Contains(route, "//") && path.Clean(route) == route
}

func writeAll(writer io.Writer, raw []byte) error {
	for len(raw) > 0 {
		written, err := writer.Write(raw)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrNoProgress
		}
		raw = raw[written:]
	}
	return nil
}

func installReadContext(stream *webtransport.Stream, ctx context.Context) func() {
	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetReadDeadline(deadline)
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = stream.SetReadDeadline(time.Now())
		close(finished)
	})
	return func() {
		if !stop() {
			<-finished
		}
		_ = stream.SetReadDeadline(time.Time{})
	}
}

func installWriteContext(stream *webtransport.Stream, ctx context.Context) func() {
	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetWriteDeadline(deadline)
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = stream.SetWriteDeadline(time.Now())
		close(finished)
	})
	return func() {
		if !stop() {
			<-finished
		}
		_ = stream.SetWriteDeadline(time.Time{})
	}
}
