package clientcore

import (
	"context"

	"neproto.local/chameleon/internal/app"
	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/carrier/httpsws"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
)

// NewProductionStrictHTTPSCore constructs the TCP/TLS A/B core. Its
// dependency graph contains exactly one carrier dialer: HTTPS WebSocket.
func NewProductionStrictHTTPSCore() (*Core, error) {
	connector, err := NewStrictHTTPSConnector(StrictHTTPSDependencies{
		DialHTTPS:    dialProductionHTTPS,
		Authenticate: authenticateProductionHTTPS,
	})
	if err != nil {
		return nil, err
	}
	return New(Options{Connect: connector})
}

func dialProductionHTTPS(ctx context.Context, clientConfig config.Client) (carrier.Carrier, error) {
	if ctx == nil || clientConfig.HTTPSTimeout.Duration <= 0 {
		return nil, ErrStrictHTTPSRequired
	}
	attempt, cancel := context.WithTimeout(ctx, clientConfig.HTTPSTimeout.Duration)
	defer cancel()
	return httpsws.Dial(attempt, httpsws.DialConfig{
		URL:             clientConfig.HTTPSURL,
		ServerAddresses: clientConfig.ServerAddresses,
	})
}

func authenticateProductionHTTPS(
	ctx context.Context,
	clientConfig config.Client,
	connection carrier.Carrier,
) (Runtime, error) {
	if connection == nil || connection.Kind() != protocol.CarrierHTTPS {
		return nil, ErrUnexpectedCarrier
	}
	authenticated, err := app.AuthenticateClientCarrier(ctx, clientConfig, connection)
	if err != nil {
		return nil, err
	}
	if authenticated == nil || authenticated.Mux == nil || authenticated.Carrier != protocol.CarrierHTTPS {
		if authenticated != nil && authenticated.Mux != nil {
			_ = authenticated.Mux.Close()
		}
		return nil, ErrNoRuntime
	}
	return newAuthenticatedRuntime(clientConfig, authenticated, nil)
}
