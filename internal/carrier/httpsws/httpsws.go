package httpsws

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"neproto.local/chameleon/internal/carrier"
)

const (
	defaultBacklog = 32
	maxBacklog     = 1024
)

var (
	ErrInvalidConfig           = errors.New("invalid HTTPS carrier configuration")
	ErrTLSRequired             = errors.New("wss is required")
	ErrTLSVerificationRequired = errors.New("TLS certificate verification is required")
	ErrTLS13Required           = errors.New("TLS 1.3 is required")
	ErrAcceptorClosed          = errors.New("HTTPS carrier acceptor closed")
)

type DialConfig struct {
	URL             string
	TLSConfig       *tls.Config
	Header          http.Header
	ServerAddresses []netip.Addr
}

func Dial(ctx context.Context, config DialConfig) (*Conn, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Scheme != "wss" || parsed.Host == "" || parsed.User != nil {
		return nil, ErrTLSRequired
	}

	tlsConfig := &tls.Config{}
	if config.TLSConfig != nil {
		tlsConfig = config.TLSConfig.Clone()
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
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     tlsConfig,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
		MaxIdleConnsPerHost: 1,
	}
	if len(config.ServerAddresses) > 0 {
		port := parsed.Port()
		if port == "" {
			port = "443"
		}
		addresses := append([]netip.Addr(nil), config.ServerAddresses...)
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
	httpClient := &http.Client{Transport: transport}
	connection, _, err := websocket.Dial(ctx, config.URL, &websocket.DialOptions{
		HTTPClient:      httpClient,
		HTTPHeader:      config.Header.Clone(),
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, fmt.Errorf("dial HTTPS carrier: %w", err)
	}
	return newConn(connection), nil
}

type AcceptorConfig struct {
	Path               string
	Backlog            int
	AllowLoopbackProxy bool
}

type Acceptor struct {
	path               string
	allowLoopbackProxy bool
	connections        chan carrier.Carrier
	done               chan struct{}
	mu                 sync.Mutex
	closed             bool
	closeOnce          sync.Once
}

func NewAcceptor(config AcceptorConfig) (*Acceptor, error) {
	if !validRoute(config.Path) || config.Backlog < 0 || config.Backlog > maxBacklog {
		return nil, ErrInvalidConfig
	}
	backlog := config.Backlog
	if backlog == 0 {
		backlog = defaultBacklog
	}
	return &Acceptor{
		path:               config.Path,
		allowLoopbackProxy: config.AllowLoopbackProxy,
		connections:        make(chan carrier.Carrier, backlog),
		done:               make(chan struct{}),
	}, nil
}

func (a *Acceptor) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		a.mu.Lock()
		closed := a.closed
		a.mu.Unlock()
		secureRequest := request.TLS != nil && request.TLS.Version >= tls.VersionTLS13
		if !secureRequest && a.allowLoopbackProxy {
			secureRequest = remoteIsLoopback(request.RemoteAddr)
		}
		if closed || request.URL.Path != a.path || !secureRequest {
			http.NotFound(writer, request)
			return
		}
		connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
			CompressionMode: websocket.CompressionDisabled,
		})
		if err != nil {
			return
		}
		wrapped := newConn(connection)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.closed {
			_ = wrapped.Close()
			return
		}
		select {
		case a.connections <- wrapped:
			return
		default:
			_ = wrapped.Close()
		}
	})
}

func (a *Acceptor) Accept(ctx context.Context) (carrier.Carrier, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	select {
	case <-a.done:
		return nil, ErrAcceptorClosed
	default:
	}
	select {
	case connection := <-a.connections:
		return connection, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.done:
		return nil, ErrAcceptorClosed
	}
}

func (a *Acceptor) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.closed = true
		close(a.done)
		for {
			select {
			case connection := <-a.connections:
				_ = connection.Close()
			default:
				return
			}
		}
	})
	return nil
}

func validRoute(route string) bool {
	return strings.HasPrefix(route, "/") && route != "/" &&
		!strings.ContainsAny(route, "?#") && !strings.Contains(route, "//") && path.Clean(route) == route
}

func remoteIsLoopback(remoteAddress string) bool {
	address, err := netip.ParseAddrPort(remoteAddress)
	return err == nil && address.Addr().IsLoopback()
}
