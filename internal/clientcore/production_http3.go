package clientcore

import (
	"context"
	"time"

	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/carrier/http3wt"
	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

// NewProductionStrictHTTP3Core constructs the first-candidate production core.
// Its dependency graph contains one carrier dialer: HTTP/3 WebTransport.
func NewProductionStrictHTTP3Core() (*Core, error) {
	connector, err := NewStrictHTTP3Connector(StrictHTTP3Dependencies{
		DialHTTP3:    dialProductionHTTP3,
		Authenticate: authenticateProductionHTTP3,
	})
	if err != nil {
		return nil, err
	}
	return New(Options{Connect: connector})
}

func dialProductionHTTP3(ctx context.Context, clientConfig config.Client) (carrier.Carrier, error) {
	if ctx == nil || clientConfig.HTTP3Timeout.Duration <= 0 {
		return nil, ErrStrictHTTP3Required
	}
	attempt, cancel := context.WithTimeout(ctx, clientConfig.HTTP3Timeout.Duration)
	defer cancel()
	return http3wt.Dial(attempt, http3wt.DialConfig{
		URL:                  clientConfig.HTTP3URL,
		ServerAddresses:      clientConfig.ServerAddresses,
		HandshakeIdleTimeout: clientConfig.HTTP3Timeout.Duration,
		IdleTimeout:          boundedHTTP3IdleTimeout(clientConfig.HTTP3Timeout.Duration),
	})
}

func authenticateProductionHTTP3(
	ctx context.Context,
	clientConfig config.Client,
	connection carrier.Carrier,
) (Runtime, error) {
	if connection == nil || connection.Kind() != protocol.CarrierHTTP3 {
		return nil, ErrUnexpectedCarrier
	}
	authenticated, err := app.AuthenticateClientCarrier(ctx, clientConfig, connection)
	if err != nil {
		return nil, err
	}
	if authenticated == nil || authenticated.Mux == nil || authenticated.Carrier != protocol.CarrierHTTP3 {
		if authenticated != nil && authenticated.Mux != nil {
			_ = authenticated.Mux.Close()
		}
		return nil, ErrNoRuntime
	}
	return &authenticatedRuntime{authenticated: authenticated}, nil
}

func boundedHTTP3IdleTimeout(handshakeTimeout time.Duration) time.Duration {
	idleTimeout := 6 * handshakeTimeout
	if idleTimeout < 30*time.Second {
		return 30 * time.Second
	}
	if idleTimeout > 3*time.Minute {
		return 3 * time.Minute
	}
	return idleTimeout
}

type authenticatedRuntime struct {
	authenticated *session.Authenticated
}

func (r *authenticatedRuntime) Carrier() clienthost.Carrier {
	if r == nil || r.authenticated == nil || r.authenticated.Carrier != protocol.CarrierHTTP3 {
		return clienthost.CarrierUnknown
	}
	return clienthost.CarrierHTTP3WebTransport
}

func (r *authenticatedRuntime) Wait(ctx context.Context) error {
	if r == nil || r.authenticated == nil || r.authenticated.Mux == nil {
		return ErrNoRuntime
	}
	return r.authenticated.Mux.Wait(ctx)
}

func (r *authenticatedRuntime) Probe(ctx context.Context) error {
	if r == nil || r.authenticated == nil || r.authenticated.Mux == nil {
		return ErrNoRuntime
	}
	return r.authenticated.Mux.Ping(ctx)
}

func (r *authenticatedRuntime) Close() error {
	if r == nil || r.authenticated == nil || r.authenticated.Mux == nil {
		return nil
	}
	return r.authenticated.Mux.Close()
}
