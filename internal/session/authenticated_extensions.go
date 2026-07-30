package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

const defaultExtensionTimeout = 2 * time.Second

type extensionNegotiationState struct {
	once sync.Once
	mu   sync.RWMutex
	done chan struct{}

	parameters protocol.ExtensionParameters
	negotiated bool
	err        error
}

func newExtensionNegotiationState() *extensionNegotiationState {
	return &extensionNegotiationState{done: make(chan struct{})}
}

func (s *extensionNegotiationState) complete(
	parameters protocol.ExtensionParameters,
	negotiated bool,
	err error,
) {
	s.once.Do(func() {
		s.mu.Lock()
		s.parameters = parameters
		s.negotiated = negotiated
		s.err = err
		s.mu.Unlock()
		close(s.done)
	})
}

func (a *Authenticated) Extensions() (protocol.ExtensionParameters, bool) {
	if a == nil || a.extensions == nil {
		return protocol.ExtensionParameters{}, false
	}
	select {
	case <-a.extensions.done:
	default:
		return protocol.ExtensionParameters{}, false
	}
	a.extensions.mu.RLock()
	defer a.extensions.mu.RUnlock()
	return a.extensions.parameters, a.extensions.negotiated
}

func (a *Authenticated) WaitExtensions(
	ctx context.Context,
) (protocol.ExtensionParameters, bool, error) {
	if a == nil || a.extensions == nil || ctx == nil {
		return protocol.ExtensionParameters{}, false, ErrInvalidConfig
	}
	select {
	case <-a.extensions.done:
		a.extensions.mu.RLock()
		defer a.extensions.mu.RUnlock()
		return a.extensions.parameters, a.extensions.negotiated, a.extensions.err
	case <-ctx.Done():
		return protocol.ExtensionParameters{}, false, ctx.Err()
	}
}

func startClientExtensionNegotiation(
	ctx context.Context,
	authenticated *Authenticated,
	config AuthenticatedConfig,
) error {
	if config.ExtensionRequest == nil {
		authenticated.extensions.complete(protocol.ExtensionParameters{}, false, nil)
		return nil
	}
	negotiationContext, cancel := context.WithTimeout(ctx, extensionTimeout(config.ExtensionTimeout))
	defer cancel()
	selected, offer, err := negotiateClientExtensions(
		negotiationContext,
		authenticated.Mux,
		*config.ExtensionRequest,
		config.RequiredExtensions,
	)
	if err != nil {
		authenticated.extensions.complete(protocol.ExtensionParameters{}, false, err)
		if config.RequiredExtensions != 0 {
			return err
		}
		return nil
	}
	if selected.Capabilities&protocol.CapabilityForwardSecrecy != 0 {
		if err := applyClientForwardSecrecy(negotiationContext, authenticated, config, offer, selected); err != nil {
			authenticated.extensions.complete(protocol.ExtensionParameters{}, false, err)
			return err
		}
	}
	applyNegotiatedCover(authenticated, selected)
	authenticated.extensions.complete(selected, true, nil)
	return nil
}

func startServerExtensionNegotiation(authenticated *Authenticated, config AuthenticatedConfig) {
	if config.ExtensionOffer == nil {
		authenticated.extensions.complete(protocol.ExtensionParameters{}, false, nil)
		return
	}
	offer := *config.ExtensionOffer
	go func() {
		ctx, cancel := context.WithTimeout(authenticated.Mux.ctx, extensionTimeout(config.ExtensionTimeout))
		defer cancel()
		selected, err := NegotiateServerExtensions(ctx, authenticated.Mux, offer, 1)
		if err == nil {
			if selected.Capabilities&protocol.CapabilityForwardSecrecy != 0 {
				err = applyServerForwardSecrecy(ctx, authenticated, config, offer, selected)
			}
		}
		if err == nil {
			applyNegotiatedCover(authenticated, selected)
		}
		authenticated.extensions.complete(selected, err == nil, err)
	}()
}

func prepareForwardSecrecy(config *AuthenticatedConfig, role Role) error {
	if config == nil || (role != RoleClient && role != RoleServer) {
		return ErrInvalidConfig
	}
	var parameters *protocol.ExtensionParameters
	if role == RoleClient {
		parameters = config.ExtensionRequest
	} else {
		parameters = config.ExtensionOffer
	}
	if !config.EnableForwardSecrecy {
		if parameters != nil && parameters.Capabilities&protocol.CapabilityForwardSecrecy != 0 {
			return ErrInvalidConfig
		}
		return nil
	}
	if parameters == nil {
		return ErrInvalidConfig
	}
	parametersCopy := *parameters
	parameters = &parametersCopy
	if role == RoleClient {
		config.ExtensionRequest = parameters
	} else {
		config.ExtensionOffer = parameters
	}
	pair, err := protocol.GenerateX25519KeyPair(rand.Reader)
	if err != nil {
		return errors.Join(ErrInvalidConfig, err)
	}
	parameters.Capabilities |= protocol.CapabilityForwardSecrecy
	parameters.ForwardSecretKeyShare = pair.Public()
	config.forwardSecret = pair
	if role == RoleClient {
		config.RequiredExtensions |= protocol.CapabilityForwardSecrecy
	}
	return nil
}

func applyClientForwardSecrecy(
	ctx context.Context,
	authenticated *Authenticated,
	config AuthenticatedConfig,
	offer protocol.ExtensionParameters,
	selected protocol.ExtensionParameters,
) error {
	if config.forwardSecret == nil || authenticated == nil || authenticated.encrypted == nil ||
		offer.Capabilities&protocol.CapabilityForwardSecrecy == 0 {
		return ErrExtensionNegotiation
	}
	serverPublic := offer.ForwardSecretKeyShare
	clientPublic := selected.ForwardSecretKeyShare
	keys, err := protocol.DeriveForwardSecretSessionKeys(
		authenticated.Keys, config.forwardSecret, serverPublic, serverPublic, clientPublic,
	)
	if err != nil {
		return errors.Join(ErrExtensionNegotiation, err)
	}
	if err := authenticated.encrypted.rekey(keys); err != nil {
		return errors.Join(ErrExtensionNegotiation, err)
	}
	if authenticated.Datagrams != nil {
		if err := authenticated.Datagrams.rekey(keys); err != nil {
			return errors.Join(ErrExtensionNegotiation, err)
		}
	}
	authenticated.Keys = keys
	confirmation, err := authenticated.Mux.ReceiveExtension(ctx)
	if err != nil || confirmation.Type != protocol.ExtensionUpdate || confirmation.MessageID != 3 ||
		len(confirmation.TLVs) != 1 || confirmation.TLVs[0].Type != protocol.ExtensionTLVForwardSecretConfirm ||
		len(confirmation.TLVs[0].Value) != 32 {
		return errors.Join(ErrExtensionNegotiation, err)
	}
	expected, err := protocol.ForwardSecretConfirmation(keys, serverPublic, clientPublic)
	if err != nil || !hmac.Equal(confirmation.TLVs[0].Value, expected[:]) {
		return errors.Join(ErrExtensionNegotiation, protocol.ErrForwardSecrecy)
	}
	return nil
}

func applyServerForwardSecrecy(
	ctx context.Context,
	authenticated *Authenticated,
	config AuthenticatedConfig,
	offer protocol.ExtensionParameters,
	selected protocol.ExtensionParameters,
) error {
	if config.forwardSecret == nil || authenticated == nil || authenticated.encrypted == nil {
		return ErrExtensionNegotiation
	}
	serverPublic := offer.ForwardSecretKeyShare
	clientPublic := selected.ForwardSecretKeyShare
	keys, err := protocol.DeriveForwardSecretSessionKeys(
		authenticated.Keys, config.forwardSecret, clientPublic, serverPublic, clientPublic,
	)
	if err != nil {
		return errors.Join(ErrExtensionNegotiation, err)
	}
	if err := authenticated.encrypted.rekey(keys); err != nil {
		return errors.Join(ErrExtensionNegotiation, err)
	}
	if authenticated.Datagrams != nil {
		if err := authenticated.Datagrams.rekey(keys); err != nil {
			return errors.Join(ErrExtensionNegotiation, err)
		}
	}
	authenticated.Keys = keys
	confirmation, err := protocol.ForwardSecretConfirmation(keys, serverPublic, clientPublic)
	if err != nil {
		return errors.Join(ErrExtensionNegotiation, err)
	}
	if err := authenticated.Mux.SendExtension(ctx, protocol.ExtensionEnvelope{
		Type: protocol.ExtensionUpdate, MessageID: 3,
		TLVs: []protocol.ExtensionTLV{{
			Type:  protocol.ExtensionTLVForwardSecretConfirm,
			Value: append([]byte(nil), confirmation[:]...),
		}},
	}); err != nil {
		return errors.Join(ErrExtensionNegotiation, err)
	}
	return nil
}

func applyNegotiatedCover(authenticated *Authenticated, selected protocol.ExtensionParameters) {
	if authenticated != nil && authenticated.Cover != nil &&
		selected.Capabilities&protocol.CapabilityMosaicCover != 0 {
		authenticated.Cover.EnableMosaic()
	}
	if authenticated != nil && authenticated.Datagrams != nil &&
		selected.Capabilities&protocol.CapabilityUnreliableDatagrams != 0 {
		authenticated.Datagrams.Enable(int(selected.UnreliableDatagramSize))
	}
}

func validateExtensionConfig(config AuthenticatedConfig) error {
	if config.ExtensionOffer != nil && config.ExtensionRequest != nil {
		return ErrInvalidConfig
	}
	if config.RequiredExtensions != 0 && config.ExtensionRequest == nil {
		return ErrInvalidConfig
	}
	if config.ExtensionTimeout < 0 || config.ExtensionTimeout > 30*time.Second {
		return ErrInvalidConfig
	}
	if config.ExtensionOffer != nil {
		if _, err := config.ExtensionOffer.Envelope(protocol.ExtensionOffer, 1); err != nil {
			return ErrInvalidConfig
		}
	}
	if config.ExtensionRequest != nil {
		if config.RequiredExtensions&^config.ExtensionRequest.Capabilities != 0 {
			return ErrInvalidConfig
		}
		if _, err := config.ExtensionRequest.Envelope(protocol.ExtensionAccept, 1); err != nil {
			return ErrInvalidConfig
		}
	}
	return nil
}

func extensionTimeout(configured time.Duration) time.Duration {
	if configured == 0 {
		return defaultExtensionTimeout
	}
	return configured
}
