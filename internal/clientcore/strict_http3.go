package clientcore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
)

var ErrStrictHTTP3Required = errors.New("strict HTTP/3 WebTransport configuration is required")

type HTTP3CarrierDialer func(context.Context, config.Client) (carrier.Carrier, error)

type NP2Authenticator func(context.Context, config.Client, carrier.Carrier) (Runtime, error)

// StrictHTTP3Dependencies deliberately has no HTTPS or WebRTC fields. An
// alternate connector cannot be supplied to, retained by, or invoked from the
// first candidate's construction path.
type StrictHTTP3Dependencies struct {
	DialHTTP3    HTTP3CarrierDialer
	Authenticate NP2Authenticator
}

func NewStrictHTTP3Connector(dependencies StrictHTTP3Dependencies) (Connector, error) {
	if dependencies.DialHTTP3 == nil || dependencies.Authenticate == nil {
		return nil, ErrInvalidOptions
	}
	return func(ctx context.Context, clientConfig config.Client) (Runtime, error) {
		if ctx == nil || clientConfig.CarrierPolicy != config.CarrierPolicyHTTP3Only ||
			!clientConfig.HTTP3Configured() || clientConfig.MaxParallelCarriers != 1 ||
			clientConfig.HTTPSURL != "" || clientConfig.WebRTCSignalingURL != "" ||
			clientConfig.HTTPSTimeout.Duration != 0 || clientConfig.WebRTCTimeout.Duration != 0 {
			return nil, ErrStrictHTTP3Required
		}
		// The first candidate owns one HTTP/3 session and does not yet implement
		// the mandatory Constellation control exchange. Imported profiles may
		// retain the additive flag for legacy clients, but advertising it here
		// would make the server wait for a ConstellationCreate frame that this
		// runtime cannot send. Work on a value copy so persistence is unchanged.
		runtimeConfig := clientConfig
		runtimeConfig.EnableConstellation = false
		connection, err := dependencies.DialHTTP3(ctx, runtimeConfig)
		if err != nil {
			return nil, classifyHTTP3DialError(err)
		}
		if connection == nil || connection.Kind() != protocol.CarrierHTTP3 {
			if connection != nil {
				_ = connection.Close()
			}
			return nil, ErrUnexpectedCarrier
		}
		runtime, err := dependencies.Authenticate(ctx, runtimeConfig, connection)
		if err != nil {
			_ = connection.Close()
			return nil, clienthost.WrapError(
				clienthost.CodeNP2AuthFailed,
				clienthost.StageNP2Authentication,
				"NP/2 authentication failed.",
				false,
				err,
			)
		}
		if runtime == nil {
			_ = connection.Close()
			return nil, clienthost.WrapError(
				clienthost.CodeInternal,
				clienthost.StageNP2Authentication,
				"NP/2 authentication returned no session.",
				false,
				ErrNoRuntime,
			)
		}
		return runtime, nil
	}, nil
}

func classifyHTTP3DialError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return clienthost.WrapError(
			clienthost.CodeCancelled,
			clienthost.StageWebTransportConnect,
			"Operation cancelled.",
			false,
			err,
		)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return clienthost.WrapError(
			clienthost.CodeHTTP3Timeout,
			clienthost.StageWebTransportConnect,
			"HTTP/3 WebTransport deadline expired.",
			true,
			err,
		)
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return clienthost.WrapError(
			clienthost.CodeDNSFailed,
			clienthost.StageDNSResolution,
			"Carrier host resolution failed.",
			true,
			err,
		)
	}
	var certificateVerificationError *tls.CertificateVerificationError
	var unknownAuthorityError x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var systemRootsError x509.SystemRootsError
	if errors.As(err, &certificateVerificationError) ||
		errors.As(err, &unknownAuthorityError) ||
		errors.As(err, &hostnameError) ||
		errors.As(err, &systemRootsError) {
		return clienthost.WrapError(
			clienthost.CodeTLSFailed,
			clienthost.StageTLSHandshake,
			"TLS negotiation failed.",
			false,
			err,
		)
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) && strings.HasPrefix(operationError.Net, "udp") {
		return clienthost.WrapError(
			clienthost.CodeUDPUnreachable,
			clienthost.StageQUICHandshake,
			"UDP path could not establish QUIC.",
			true,
			err,
		)
	}
	return clienthost.WrapError(
		clienthost.CodeInternal,
		clienthost.StageWebTransportConnect,
		"WebTransport session could not be established.",
		true,
		err,
	)
}
