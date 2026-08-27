package clientcore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
)

import (
	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
)

var ErrStrictHTTPSRequired = errors.New("strict HTTPS WebSocket configuration is required")

type HTTPSCarrierDialer func(context.Context, config.Client) (carrier.Carrier, error)

// StrictHTTPSDependencies deliberately has no HTTP/3 or WebRTC fields. The
// A/B candidate cannot retain or invoke an alternate carrier.
type StrictHTTPSDependencies struct {
	DialHTTPS    HTTPSCarrierDialer
	Authenticate NP2Authenticator
}

func NewStrictHTTPSConnector(dependencies StrictHTTPSDependencies) (Connector, error) {
	if dependencies.DialHTTPS == nil || dependencies.Authenticate == nil {
		return nil, ErrInvalidOptions
	}
	return func(ctx context.Context, clientConfig config.Client) (Runtime, error) {
		if ctx == nil || clientConfig.CarrierPolicy != config.CarrierPolicyHTTPSOnly ||
			clientConfig.HTTPSURL == "" || clientConfig.HTTPSTimeout.Duration <= 0 ||
			clientConfig.MaxParallelCarriers != 1 || clientConfig.RequireDatagrams ||
			clientConfig.HTTP3URL != "" || clientConfig.WebRTCSignalingURL != "" ||
			clientConfig.HTTP3Timeout.Duration != 0 || clientConfig.WebRTCTimeout.Duration != 0 {
			return nil, ErrStrictHTTPSRequired
		}
		runtimeConfig := clientConfig
		runtimeConfig.EnableConstellation = false
		connection, err := dependencies.DialHTTPS(ctx, runtimeConfig)
		if err != nil {
			return nil, classifyHTTPSDialError(err)
		}
		if connection == nil || connection.Kind() != protocol.CarrierHTTPS {
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

func classifyHTTPSDialError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return clienthost.WrapError(
			clienthost.CodeCancelled,
			clienthost.StageTLSHandshake,
			"Operation cancelled.",
			false,
			err,
		)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return clienthost.WrapError(
			clienthost.CodeHostUnavailable,
			clienthost.StageTLSHandshake,
			"HTTPS carrier deadline expired.",
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
	return clienthost.WrapError(
		clienthost.CodeHostUnavailable,
		clienthost.StageTLSHandshake,
		"HTTPS carrier could not be established.",
		true,
		err,
	)
}
