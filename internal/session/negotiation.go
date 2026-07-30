package session

import (
	"context"
	"errors"
	"fmt"

	"neproto.local/chameleon/internal/protocol"
)

var (
	ErrExtensionNegotiation = errors.New("extension negotiation failed")
	ErrRequiredExtension    = errors.New("required extension unavailable")
)

func NegotiateServerExtensions(
	ctx context.Context,
	mux *Mux,
	offer protocol.ExtensionParameters,
	messageID uint64,
) (protocol.ExtensionParameters, error) {
	if ctx == nil || mux == nil || messageID == 0 || messageID >= protocol.MaxSequence {
		return protocol.ExtensionParameters{}, ErrExtensionNegotiation
	}
	envelope, err := offer.Envelope(protocol.ExtensionOffer, messageID)
	if err != nil {
		return protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	if err := mux.SendExtension(ctx, envelope); err != nil {
		return protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	response, err := mux.ReceiveExtension(ctx)
	if err != nil {
		return protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	if response.Type != protocol.ExtensionAccept || response.MessageID != messageID+1 {
		return protocol.ExtensionParameters{}, fmt.Errorf(
			"%w: unexpected accept type=%d id=%d", ErrExtensionNegotiation, response.Type, response.MessageID,
		)
	}
	accept, err := protocol.ParseExtensionParameters(response)
	if err != nil {
		return protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	if err := protocol.ValidateExtensionAccept(offer, accept); err != nil {
		return protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	return accept, nil
}

func NegotiateClientExtensions(
	ctx context.Context,
	mux *Mux,
	request protocol.ExtensionParameters,
	required protocol.ExtensionCapability,
) (protocol.ExtensionParameters, error) {
	selected, _, err := negotiateClientExtensions(ctx, mux, request, required)
	return selected, err
}

func negotiateClientExtensions(
	ctx context.Context,
	mux *Mux,
	request protocol.ExtensionParameters,
	required protocol.ExtensionCapability,
) (protocol.ExtensionParameters, protocol.ExtensionParameters, error) {
	if ctx == nil || mux == nil {
		return protocol.ExtensionParameters{}, protocol.ExtensionParameters{}, ErrExtensionNegotiation
	}
	if _, err := request.Envelope(protocol.ExtensionAccept, 1); err != nil {
		return protocol.ExtensionParameters{}, protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	offerEnvelope, err := mux.ReceiveExtension(ctx)
	if err != nil {
		return protocol.ExtensionParameters{}, protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	if offerEnvelope.Type != protocol.ExtensionOffer || offerEnvelope.MessageID >= protocol.MaxSequence {
		return protocol.ExtensionParameters{}, protocol.ExtensionParameters{}, fmt.Errorf(
			"%w: unexpected offer type=%d id=%d",
			ErrExtensionNegotiation, offerEnvelope.Type, offerEnvelope.MessageID,
		)
	}
	offer, err := protocol.ParseExtensionParameters(offerEnvelope)
	if err != nil {
		return protocol.ExtensionParameters{}, protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	selected := selectExtensionParameters(offer, request)
	if required&^selected.Capabilities != 0 {
		return protocol.ExtensionParameters{}, protocol.ExtensionParameters{}, ErrRequiredExtension
	}
	if err := protocol.ValidateExtensionAccept(offer, selected); err != nil {
		return protocol.ExtensionParameters{}, protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	acceptEnvelope, err := selected.Envelope(protocol.ExtensionAccept, offerEnvelope.MessageID+1)
	if err != nil {
		return protocol.ExtensionParameters{}, protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	if err := mux.SendExtension(ctx, acceptEnvelope); err != nil {
		return protocol.ExtensionParameters{}, protocol.ExtensionParameters{}, errors.Join(ErrExtensionNegotiation, err)
	}
	return selected, offer, nil
}

func selectExtensionParameters(
	offer protocol.ExtensionParameters,
	request protocol.ExtensionParameters,
) protocol.ExtensionParameters {
	selected := protocol.ExtensionParameters{
		Capabilities:           offer.Capabilities & request.Capabilities,
		MaxSessionReceiveBytes: min(offer.MaxSessionReceiveBytes, request.MaxSessionReceiveBytes),
		MaxStreamWindowBytes:   min(offer.MaxStreamWindowBytes, request.MaxStreamWindowBytes),
	}
	if selected.Capabilities&protocol.CapabilityForwardSecrecy != 0 {
		selected.ForwardSecretKeyShare = request.ForwardSecretKeyShare
	}
	if selected.Capabilities&protocol.CapabilityReliableUDP == 0 {
		selected.Capabilities &^= protocol.CapabilityUnreliableDatagrams
		return selected
	}
	selected.MaxUDPAssociations = min(offer.MaxUDPAssociations, request.MaxUDPAssociations)
	selected.MaxUDPPayload = min(offer.MaxUDPPayload, request.MaxUDPPayload)
	selected.UDPIdleTimeoutMS = min(offer.UDPIdleTimeoutMS, request.UDPIdleTimeoutMS)
	if selected.Capabilities&protocol.CapabilityUnreliableDatagrams != 0 {
		selected.UnreliableDatagramSize = min(
			offer.UnreliableDatagramSize,
			request.UnreliableDatagramSize,
		)
	}
	return selected
}
