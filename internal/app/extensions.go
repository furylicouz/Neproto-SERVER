package app

import (
	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

const (
	productionMaxUDPAssociations   = 256
	productionMaxUDPPayload        = 65507
	productionUDPIdleTimeoutMS     = 60_000
	productionSessionReceiveBytes  = 64 * 1024 * 1024
	productionMaxStreamWindowBytes = 8 * 1024 * 1024
)

func productionExtensionParameters(maxStreams int) protocol.ExtensionParameters {
	maxAssociations := maxStreams
	if maxAssociations > productionMaxUDPAssociations {
		maxAssociations = productionMaxUDPAssociations
	}
	return protocol.ExtensionParameters{
		Capabilities:           protocol.CapabilityReliableUDP | protocol.CapabilityMosaicCover,
		MaxUDPAssociations:     uint64(maxAssociations),
		MaxUDPPayload:          productionMaxUDPPayload,
		UDPIdleTimeoutMS:       productionUDPIdleTimeoutMS,
		MaxSessionReceiveBytes: productionSessionReceiveBytes,
		MaxStreamWindowBytes:   productionMaxStreamWindowBytes,
	}
}

func productionServerExtensionOffer(
	serverConfig config.Server, connection carrier.Carrier,
) protocol.ExtensionParameters {
	parameters := productionExtensionParameters(serverConfig.MaxStreams)
	if serverConfig.EnableConstellation {
		parameters.Capabilities |= protocol.CapabilityConstellationContinuity
	}
	enabled := connection != nil &&
		(connection.Kind() == protocol.CarrierWebRTC && serverConfig.EnableWebRTCDatagrams ||
			connection.Kind() == protocol.CarrierHTTP3 && serverConfig.EnableHTTP3Datagrams)
	if !enabled {
		return parameters
	}
	parameters = productionExtensionParametersForCarrier(serverConfig.MaxStreams, connection)
	if serverConfig.EnableConstellation {
		parameters.Capabilities |= protocol.CapabilityConstellationContinuity
	}
	return parameters
}

func productionClientExtensionRequest(
	clientConfig config.Client,
	connection carrier.Carrier,
) protocol.ExtensionParameters {
	parameters := productionExtensionParametersForCarrier(clientConfig.MaxStreams, connection)
	if clientConfig.EnableConstellation {
		parameters.Capabilities |= protocol.CapabilityConstellationContinuity
	}
	return parameters
}

func productionExtensionParametersForCarrier(maxStreams int, connection carrier.Carrier) protocol.ExtensionParameters {
	parameters := productionExtensionParameters(maxStreams)
	datagramCarrier, ok := connection.(carrier.DatagramCarrier)
	if !ok {
		return parameters
	}
	maximum := session.CarrierDatagramPayloadLimit(datagramCarrier)
	if maximum == 0 {
		return parameters
	}
	parameters.Capabilities |= protocol.CapabilityUnreliableDatagrams
	parameters.UnreliableDatagramSize = uint64(maximum)
	return parameters
}
