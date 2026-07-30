package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/netip"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/cover"
	"neproto.local/chameleon/internal/protocol"
)

type AuthenticatedConfig struct {
	RootSecret              [protocol.RootSecretSize]byte
	Credentials             []ServerCredential
	ServerIdentity          string
	Features                protocol.FeatureSet
	InitialWindow           uint64
	MaxStreams              int
	MaxCoverOverheadPercent uint8
	MaxCoverBudgetBytes     int
	ExtensionOffer          *protocol.ExtensionParameters
	ExtensionRequest        *protocol.ExtensionParameters
	RequiredExtensions      protocol.ExtensionCapability
	ExtensionTimeout        time.Duration
	EnableForwardSecrecy    bool
	DeviceID                protocol.DeviceID
	forwardSecret           *protocol.X25519KeyPair
}

type ServerCredential struct {
	ID         string
	RootSecret [protocol.RootSecretSize]byte
}

type Authenticated struct {
	Mux                    *Mux
	Keys                   protocol.SessionKeys
	Features               protocol.FeatureSet
	Carrier                protocol.CarrierKind
	Cover                  *cover.Transport
	Datagrams              *DatagramMux
	CarrierRemoteAddresses []netip.Addr
	CredentialID           string
	DeviceID               protocol.DeviceID
	extensions             *extensionNegotiationState
	encrypted              *encryptedCarrier
}

func ConnectClient(ctx context.Context, connection carrier.Carrier, config AuthenticatedConfig) (*Authenticated, error) {
	if err := prepareForwardSecrecy(&config, RoleClient); err != nil {
		return nil, closeAuthentication(connection, "prepare forward secrecy", err)
	}
	if err := validateAuthenticatedConfig(ctx, connection, config); err != nil {
		return nil, err
	}
	handshakeConfig := protocol.HandshakeConfig{
		RootSecret: config.RootSecret, ServerIdentity: config.ServerIdentity, Carrier: connection.Kind(),
		DeviceID: config.DeviceID,
	}

	rawChallenge, err := connection.Receive(ctx)
	if err != nil {
		return nil, closeAuthentication(connection, "receive challenge", err)
	}
	challenge, err := protocol.ParseChallenge(rawChallenge)
	if err != nil {
		return nil, closeAuthentication(connection, "parse challenge", err)
	}
	requestedFeatures := config.Features
	negotiatedDeviceID := config.DeviceID
	if requestedFeatures&protocol.FeatureDeviceIdentity != 0 &&
		challenge.SupportedFeatures&protocol.FeatureDeviceIdentity == 0 {
		requestedFeatures &^= protocol.FeatureDeviceIdentity
		negotiatedDeviceID = protocol.DeviceID{}
	}
	handshakeConfig.DeviceID = negotiatedDeviceID
	response, clientHandshake, err := protocol.RespondToChallenge(
		handshakeConfig, challenge, requestedFeatures, rand.Reader,
	)
	if err != nil {
		return nil, closeAuthentication(connection, "build response", err)
	}
	if err := connection.Send(ctx, response.MarshalBinary()); err != nil {
		return nil, closeAuthentication(connection, "send response", err)
	}
	rawConfirm, err := connection.Receive(ctx)
	if err != nil {
		return nil, closeAuthentication(connection, "receive confirm", err)
	}
	confirm, err := protocol.ParseConfirm(rawConfirm)
	if err != nil {
		return nil, closeAuthentication(connection, "parse confirm", err)
	}
	keys, err := clientHandshake.VerifyConfirm(confirm)
	if err != nil {
		return nil, closeAuthentication(connection, "verify confirm", err)
	}
	authenticated, err := startAuthenticated(connection, config, RoleClient, keys, requestedFeatures, "", negotiatedDeviceID)
	if err != nil {
		return nil, err
	}
	if err := startClientExtensionNegotiation(ctx, authenticated, config); err != nil {
		_ = authenticated.Mux.Close()
		return nil, err
	}
	return authenticated, nil
}

func AcceptServer(ctx context.Context, connection carrier.Carrier, config AuthenticatedConfig) (*Authenticated, error) {
	if err := prepareForwardSecrecy(&config, RoleServer); err != nil {
		return nil, closeAuthentication(connection, "prepare forward secrecy", err)
	}
	if err := validateAuthenticatedConfig(ctx, connection, config); err != nil {
		return nil, err
	}
	credentials := make([]protocol.Credential, 0, len(config.Credentials)+1)
	if config.RootSecret != ([protocol.RootSecretSize]byte{}) {
		credentials = append(credentials, protocol.Credential{ID: "legacy", RootSecret: config.RootSecret})
	}
	for _, credential := range config.Credentials {
		credentials = append(credentials, protocol.Credential{ID: credential.ID, RootSecret: credential.RootSecret})
	}
	if len(credentials) == 0 {
		return nil, ErrInvalidConfig
	}
	handshakeConfig := protocol.HandshakeConfig{
		RootSecret: credentials[0].RootSecret, ServerIdentity: config.ServerIdentity, Carrier: connection.Kind(),
	}
	serverHandshake, challenge, err := protocol.NewServerHandshake(
		handshakeConfig, config.Features, time.Now(), rand.Reader,
	)
	if err != nil {
		return nil, closeAuthentication(connection, "create challenge", err)
	}
	if err := connection.Send(ctx, challenge.MarshalBinary()); err != nil {
		return nil, closeAuthentication(connection, "send challenge", err)
	}
	rawResponse, err := connection.Receive(ctx)
	if err != nil {
		return nil, closeAuthentication(connection, "receive response", err)
	}
	response, err := protocol.ParseResponse(rawResponse)
	if err != nil {
		return nil, closeAuthentication(connection, "parse response", err)
	}
	confirm, keys, credentialID, err := serverHandshake.VerifyResponseWithCredentials(response, time.Now(), credentials)
	if err != nil {
		return nil, closeAuthentication(connection, "verify response", err)
	}
	if err := connection.Send(ctx, confirm.MarshalBinary()); err != nil {
		return nil, closeAuthentication(connection, "send confirm", err)
	}
	authenticated, err := startAuthenticated(
		connection, config, RoleServer, keys, response.RequestedFeatures, credentialID, response.DeviceID,
	)
	if err != nil {
		return nil, err
	}
	startServerExtensionNegotiation(authenticated, config)
	return authenticated, nil
}

func startAuthenticated(
	connection carrier.Carrier,
	config AuthenticatedConfig,
	role Role,
	keys protocol.SessionKeys,
	features protocol.FeatureSet,
	credentialID string,
	deviceID protocol.DeviceID,
) (*Authenticated, error) {
	var remoteAddresses []netip.Addr
	if endpoint, ok := connection.(interface{ RemoteAddresses() []netip.Addr }); ok {
		remoteAddresses = append(remoteAddresses, endpoint.RemoteAddresses()...)
	}
	if features&protocol.FeatureCellAEAD == 0 {
		return nil, closeAuthentication(connection, "negotiate cell encryption", protocol.ErrUnsupportedFeatures)
	}
	typeMap, err := protocol.NewTypeMap(keys.HeaderMap)
	if err != nil {
		return nil, closeAuthentication(connection, "derive type map", err)
	}
	profile, err := coverProfile(features)
	if err != nil {
		return nil, closeAuthentication(connection, "select cover profile", err)
	}
	overhead := config.MaxCoverOverheadPercent
	engine, err := cover.NewEngine(cover.Config{
		Profile: profile, MaxOverheadPercent: overhead, MaxBudgetBytes: config.MaxCoverBudgetBytes,
		Seed: deriveCoverSeed(keys.Padding, role, "schedule"),
	})
	if err != nil {
		return nil, closeAuthentication(connection, "create cover engine", err)
	}
	encrypted, err := newEncryptedCarrier(connection, role, keys)
	if err != nil {
		return nil, closeAuthentication(connection, "create cell encryption", err)
	}
	covered, err := cover.NewTransport(cover.TransportConfig{
		Carrier: encrypted, TypeMap: typeMap, Engine: engine,
		PaddingSeed: deriveCoverSeed(keys.Padding, role, "padding"),
	})
	if err != nil {
		return nil, closeAuthentication(encrypted, "create cover transport", err)
	}
	if config.EnableForwardSecrecy {
		covered.PauseDummies()
	}
	mux, err := New(Config{
		Role: role, Carrier: covered, TypeMap: typeMap,
		InitialWindow: config.InitialWindow, MaxStreams: config.MaxStreams,
	})
	if err != nil {
		_ = covered.Close()
		return nil, fmt.Errorf("authenticate carrier (start multiplexer): %w", err)
	}
	var datagrams *DatagramMux
	if datagramCarrier, ok := connection.(carrier.DatagramCarrier); ok {
		datagrams, err = newDatagramMux(mux.ctx, datagramCarrier, role, keys)
		if err != nil {
			_ = mux.Close()
			return nil, fmt.Errorf("authenticate carrier (start datagram multiplexer): %w", err)
		}
	}
	return &Authenticated{
		Mux: mux, Keys: keys, Features: features, Carrier: connection.Kind(), Cover: covered,
		Datagrams:              datagrams,
		CarrierRemoteAddresses: remoteAddresses, CredentialID: credentialID, DeviceID: deviceID,
		extensions: newExtensionNegotiationState(), encrypted: encrypted,
	}, nil
}

func validateAuthenticatedConfig(ctx context.Context, connection carrier.Carrier, config AuthenticatedConfig) error {
	if ctx == nil || connection == nil ||
		(config.RootSecret == ([protocol.RootSecretSize]byte{}) && len(config.Credentials) == 0) ||
		len(config.Credentials) > protocol.MaxServerCredentials || config.InitialWindow == 0 ||
		config.InitialWindow > MaxInitialWindow || config.MaxStreams <= 0 ||
		config.MaxCoverOverheadPercent > 100 || config.MaxCoverBudgetBytes < 0 ||
		config.MaxCoverBudgetBytes > cover.MaxWireCellBytes {
		return ErrInvalidConfig
	}
	if err := validateExtensionConfig(config); err != nil {
		return err
	}
	return nil
}

func coverProfile(features protocol.FeatureSet) (cover.ProfileID, error) {
	profile := cover.ProfileQuiet
	count := 0
	if features&protocol.FeatureProfileQuiet != 0 {
		profile = cover.ProfileQuiet
		count++
	}
	if features&protocol.FeatureProfileWeb != 0 {
		profile = cover.ProfileWeb
		count++
	}
	if features&protocol.FeatureProfileInteractive != 0 {
		profile = cover.ProfileInteractive
		count++
	}
	if count > 1 {
		return 0, protocol.ErrUnsupportedFeatures
	}
	return profile, nil
}

func deriveCoverSeed(key [32]byte, role Role, purpose string) [32]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("NP2 cover direction"))
	_, _ = mac.Write([]byte{byte(role), 0})
	_, _ = mac.Write([]byte(purpose))
	return [32]byte(mac.Sum(nil))
}

func closeAuthentication(connection carrier.Carrier, stage string, err error) error {
	_ = connection.Close()
	return fmt.Errorf("authenticate carrier (%s): %w", stage, err)
}
