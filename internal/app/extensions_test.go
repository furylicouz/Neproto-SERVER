package app

import (
	"context"
	"io"
	"testing"

	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
)

func TestProductionExtensionParametersEnableBoundedReliableUDP(t *testing.T) {
	parameters := productionExtensionParameters(128)
	if parameters.Capabilities != protocol.CapabilityReliableUDP ||
		parameters.MaxUDPAssociations != 128 || parameters.MaxUDPPayload != 65507 ||
		parameters.UnreliableDatagramSize != 0 {
		t.Fatalf("unexpected parameters: %+v", parameters)
	}
	if _, err := parameters.Envelope(protocol.ExtensionOffer, 1); err != nil {
		t.Fatalf("parameters are not wire-valid: %v", err)
	}
	limited := productionExtensionParameters(4096)
	if limited.MaxUDPAssociations != 256 {
		t.Fatalf("association cap=%d", limited.MaxUDPAssociations)
	}
}

func TestProductionExtensionsAdvertiseMosaicOnlyWhenExplicitlyEnabled(t *testing.T) {
	https := &extensionDatagramCarrier{kind: protocol.CarrierHTTPS}
	if got := productionServerExtensionOffer(config.Server{MaxStreams: 8}, https); got.Capabilities&protocol.CapabilityMosaicCover != 0 {
		t.Fatal("server advertised Mosaic while cover mode is off")
	}
	if got := productionClientExtensionRequest(config.Client{MaxStreams: 8}, https); got.Capabilities&protocol.CapabilityMosaicCover != 0 {
		t.Fatal("client advertised Mosaic while cover mode is off")
	}
	if got := productionServerExtensionOffer(config.Server{MaxStreams: 8, CoverMode: config.CoverModePulse}, https); got.Capabilities&protocol.CapabilityMosaicCover != 0 {
		t.Fatal("server advertised Mosaic while Pulse is sender-local")
	}
	if got := productionClientExtensionRequest(config.Client{MaxStreams: 8, CoverMode: config.CoverModePulse}, https); got.Capabilities&protocol.CapabilityMosaicCover != 0 {
		t.Fatal("client advertised Mosaic while Pulse is sender-local")
	}
	serverOffer := productionServerExtensionOffer(config.Server{
		MaxStreams: 8, CoverMode: config.CoverModeMosaic,
	}, https)
	if serverOffer.Capabilities&protocol.CapabilityMosaicCover == 0 {
		t.Fatal("server did not advertise explicit Mosaic cover mode")
	}
	clientRequest := productionClientExtensionRequest(config.Client{
		MaxStreams: 8, CoverMode: config.CoverModeMosaic,
	}, https)
	if clientRequest.Capabilities&protocol.CapabilityMosaicCover == 0 {
		t.Fatal("client did not advertise explicit Mosaic cover mode")
	}
}

func TestProductionServerDatagramKillSwitchesAreCarrierSpecific(t *testing.T) {
	webRTC := &extensionDatagramCarrier{kind: protocol.CarrierWebRTC, maximum: 1150}
	http3 := &extensionDatagramCarrier{kind: protocol.CarrierHTTP3, maximum: 1150}
	disabled := config.Server{}
	if got := productionServerExtensionOffer(disabled, webRTC); got.Capabilities&protocol.CapabilityUnreliableDatagrams != 0 {
		t.Fatal("WebRTC datagrams enabled while kill switch is off")
	}
	if got := productionServerExtensionOffer(disabled, http3); got.Capabilities&protocol.CapabilityUnreliableDatagrams != 0 {
		t.Fatal("HTTP/3 datagrams enabled while kill switch is off")
	}

	enabled := config.Server{EnableWebRTCDatagrams: true, EnableHTTP3Datagrams: true}
	if got := productionServerExtensionOffer(enabled, webRTC); got.Capabilities&protocol.CapabilityUnreliableDatagrams == 0 {
		t.Fatal("WebRTC datagrams not enabled by its kill switch")
	}
	if got := productionServerExtensionOffer(enabled, http3); got.Capabilities&protocol.CapabilityUnreliableDatagrams == 0 {
		t.Fatal("HTTP/3 datagrams not enabled by its kill switch")
	}
}

func TestConstellationCapabilityIsExplicitAndIndependentOfCarrier(t *testing.T) {
	https := &extensionDatagramCarrier{kind: protocol.CarrierHTTPS}
	if got := productionServerExtensionOffer(config.Server{MaxStreams: 8}, https); got.Capabilities&protocol.CapabilityConstellationContinuity != 0 {
		t.Fatal("server enabled constellation without kill switch")
	}
	serverOffer := productionServerExtensionOffer(config.Server{MaxStreams: 8, EnableConstellation: true}, https)
	if serverOffer.Capabilities&protocol.CapabilityConstellationContinuity == 0 {
		t.Fatal("server constellation capability missing")
	}
	if got := productionClientExtensionRequest(config.Client{MaxStreams: 8}, https); got.Capabilities&protocol.CapabilityConstellationContinuity != 0 {
		t.Fatal("client enabled constellation without kill switch")
	}
	clientRequest := productionClientExtensionRequest(config.Client{MaxStreams: 8, EnableConstellation: true}, https)
	if clientRequest.Capabilities&protocol.CapabilityConstellationContinuity == 0 {
		t.Fatal("client constellation capability missing")
	}
}

func TestProductionResourceLimiterUsesServerConfiguration(t *testing.T) {
	serverConfig := config.Server{ResourceLimits: config.ServerResourceLimits{
		MaxSessionsPerUser:      1,
		MaxTCPConnectionsGlobal: 2, MaxTCPConnectionsPerUser: 1,
		MaxUDPAssociationsGlobal: 2, MaxUDPAssociationsPerUser: 1,
		UDPPacketsPerSecondGlobal: 10, UDPPacketsPerSecondPerUser: 5,
		UDPBytesPerSecondGlobal: 100, UDPBytesPerSecondPerUser: 50,
		DNSQueriesPerSecondGlobal: 10, DNSQueriesPerSecondPerUser: 5,
		TargetCreatesPerSecondGlobal: 10, TargetCreatesPerSecondPerUser: 5,
	}}
	limiter, err := newProductionResourceLimiter(serverConfig)
	if err != nil {
		t.Fatalf("new production resource limiter: %v", err)
	}
	if !limiter.AcquireSession("alice") || limiter.AcquireSession("alice") {
		t.Fatal("configured per-user session limit was not enforced")
	}
	if !limiter.AcquireTCP("alice") || limiter.AcquireTCP("alice") {
		t.Fatal("configured per-user TCP limit was not enforced")
	}
	if !limiter.AcquireUDP("alice") || limiter.AcquireUDP("alice") {
		t.Fatal("configured per-user UDP limit was not enforced")
	}
}

type extensionDatagramCarrier struct {
	kind    protocol.CarrierKind
	maximum int
}

func (*extensionDatagramCarrier) Send(context.Context, []byte) error      { return nil }
func (*extensionDatagramCarrier) Receive(context.Context) ([]byte, error) { return nil, io.EOF }
func (*extensionDatagramCarrier) Close() error                            { return nil }
func (c *extensionDatagramCarrier) Kind() protocol.CarrierKind            { return c.kind }
func (*extensionDatagramCarrier) SendDatagram(context.Context, []byte) error {
	return nil
}
func (*extensionDatagramCarrier) ReceiveDatagram(context.Context) ([]byte, error) {
	return nil, io.EOF
}
func (c *extensionDatagramCarrier) MaxDatagramPayload() int { return c.maximum }
