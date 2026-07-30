package protocol

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	Version                      = 2
	RootSecretSize               = 32
	NonceSize                    = 32
	AuthTagSize                  = 32
	MaxAuthenticationMessageSize = 512
	MaxChallengeAge              = 15 * time.Second
	MaxServerCredentials         = 256

	challengeMessageSize = 1 + NonceSize + 4
	responseMessageSize  = NonceSize + 4 + AuthTagSize
	confirmMessageSize   = AuthTagSize
)

var (
	ErrAuthentication      = errors.New("authentication failed")
	ErrChallengeUsed       = errors.New("challenge already used")
	ErrExpiredChallenge    = errors.New("challenge expired")
	ErrMalformedMessage    = errors.New("malformed authentication message")
	ErrUnsupportedFeatures = errors.New("unsupported features")
	ErrInvalidConfig       = errors.New("invalid handshake configuration")
)

type CarrierKind uint8

const (
	CarrierHTTPS CarrierKind = iota + 1
	CarrierWebRTC
	CarrierHTTP3
)

func (c CarrierKind) valid() bool {
	return c == CarrierHTTPS || c == CarrierWebRTC || c == CarrierHTTP3
}

type FeatureSet uint32

const (
	FeatureMultiplex FeatureSet = 1 << iota
	FeatureProfileQuiet
	FeatureProfileWeb
	FeatureProfileInteractive
	FeatureCellAEAD
	FeatureDeviceIdentity
)

const knownFeatures = FeatureMultiplex |
	FeatureProfileQuiet |
	FeatureProfileWeb |
	FeatureProfileInteractive |
	FeatureCellAEAD |
	FeatureDeviceIdentity

func (f FeatureSet) valid() bool {
	return f&^knownFeatures == 0
}

type HandshakeConfig struct {
	RootSecret     [RootSecretSize]byte
	ServerIdentity string
	Carrier        CarrierKind
	DeviceID       DeviceID
}

// Credential is server-local authentication metadata. Its ID is never placed
// on the NP/2 wire; the server evaluates the bounded credential set and keeps
// the matched ID only for local administration and observability.
type Credential struct {
	ID         string
	RootSecret [RootSecretSize]byte
}

func (c HandshakeConfig) validate() error {
	if len(c.ServerIdentity) == 0 || len(c.ServerIdentity) > 253 {
		return fmt.Errorf("%w: server identity length", ErrInvalidConfig)
	}
	if !c.Carrier.valid() {
		return fmt.Errorf("%w: carrier", ErrInvalidConfig)
	}
	var zero [RootSecretSize]byte
	if subtle.ConstantTimeCompare(c.RootSecret[:], zero[:]) == 1 {
		return fmt.Errorf("%w: zero root secret", ErrInvalidConfig)
	}
	return nil
}

type Challenge struct {
	Version           uint8
	ServerNonce       [NonceSize]byte
	SupportedFeatures FeatureSet
}

func (c Challenge) MarshalBinary() []byte {
	raw := make([]byte, challengeMessageSize)
	raw[0] = c.Version
	copy(raw[1:1+NonceSize], c.ServerNonce[:])
	binary.BigEndian.PutUint32(raw[1+NonceSize:], uint32(c.SupportedFeatures))
	return raw
}

func ParseChallenge(raw []byte) (Challenge, error) {
	if len(raw) > MaxAuthenticationMessageSize || len(raw) != challengeMessageSize {
		return Challenge{}, ErrMalformedMessage
	}
	if raw[0] != Version {
		return Challenge{}, ErrMalformedMessage
	}
	var challenge Challenge
	challenge.Version = raw[0]
	copy(challenge.ServerNonce[:], raw[1:1+NonceSize])
	challenge.SupportedFeatures = FeatureSet(binary.BigEndian.Uint32(raw[1+NonceSize:]))
	if !challenge.SupportedFeatures.valid() {
		return Challenge{}, ErrMalformedMessage
	}
	return challenge, nil
}

type Response struct {
	ClientNonce       [NonceSize]byte
	RequestedFeatures FeatureSet
	DeviceID          DeviceID
	Tag               [AuthTagSize]byte
}

func (r Response) MarshalBinary() []byte {
	size := responseMessageSize
	if r.RequestedFeatures&FeatureDeviceIdentity != 0 {
		size += DeviceIDSize
	}
	raw := make([]byte, size)
	copy(raw[:NonceSize], r.ClientNonce[:])
	binary.BigEndian.PutUint32(raw[NonceSize:], uint32(r.RequestedFeatures))
	offset := NonceSize + 4
	if r.RequestedFeatures&FeatureDeviceIdentity != 0 {
		copy(raw[offset:offset+DeviceIDSize], r.DeviceID[:])
		offset += DeviceIDSize
	}
	copy(raw[offset:], r.Tag[:])
	return raw
}

func ParseResponse(raw []byte) (Response, error) {
	if len(raw) > MaxAuthenticationMessageSize || len(raw) < NonceSize+4 {
		return Response{}, ErrMalformedMessage
	}
	var response Response
	copy(response.ClientNonce[:], raw[:NonceSize])
	response.RequestedFeatures = FeatureSet(binary.BigEndian.Uint32(raw[NonceSize:]))
	if !response.RequestedFeatures.valid() {
		return Response{}, ErrMalformedMessage
	}
	expectedSize := responseMessageSize
	if response.RequestedFeatures&FeatureDeviceIdentity != 0 {
		expectedSize += DeviceIDSize
	}
	if len(raw) != expectedSize {
		return Response{}, ErrMalformedMessage
	}
	offset := NonceSize + 4
	if response.RequestedFeatures&FeatureDeviceIdentity != 0 {
		copy(response.DeviceID[:], raw[offset:offset+DeviceIDSize])
		if response.DeviceID.IsZero() {
			return Response{}, ErrMalformedMessage
		}
		offset += DeviceIDSize
	}
	copy(response.Tag[:], raw[offset:])
	return response, nil
}

type Confirm struct {
	Tag [AuthTagSize]byte
}

func (c Confirm) MarshalBinary() []byte {
	raw := make([]byte, confirmMessageSize)
	copy(raw, c.Tag[:])
	return raw
}

func ParseConfirm(raw []byte) (Confirm, error) {
	if len(raw) > MaxAuthenticationMessageSize || len(raw) != confirmMessageSize {
		return Confirm{}, ErrMalformedMessage
	}
	var confirm Confirm
	copy(confirm.Tag[:], raw)
	return confirm, nil
}

type SessionKeys struct {
	HeaderMap           [32]byte
	Padding             [32]byte
	Control             [32]byte
	ClientToServer      [32]byte
	ServerToClient      [32]byte
	ClientToServerNonce [4]byte
	ServerToClientNonce [4]byte
}

type ServerHandshake struct {
	mu                sync.Mutex
	config            HandshakeConfig
	challenge         Challenge
	createdAt         time.Time
	used              bool
	supportedFeatures FeatureSet
}

func NewServerHandshake(
	config HandshakeConfig,
	supportedFeatures FeatureSet,
	now time.Time,
	random io.Reader,
) (*ServerHandshake, Challenge, error) {
	if err := config.validate(); err != nil {
		return nil, Challenge{}, err
	}
	if !supportedFeatures.valid() {
		return nil, Challenge{}, ErrUnsupportedFeatures
	}
	challenge := Challenge{
		Version:           Version,
		SupportedFeatures: supportedFeatures,
	}
	if _, err := io.ReadFull(random, challenge.ServerNonce[:]); err != nil {
		return nil, Challenge{}, fmt.Errorf("read server nonce: %w", err)
	}
	server := &ServerHandshake{
		config:            config,
		challenge:         challenge,
		createdAt:         now,
		supportedFeatures: supportedFeatures,
	}
	return server, challenge, nil
}

type ClientHandshake struct {
	expectedConfirm [AuthTagSize]byte
	keys            SessionKeys
}

func RespondToChallenge(
	config HandshakeConfig,
	challenge Challenge,
	requestedFeatures FeatureSet,
	random io.Reader,
) (Response, *ClientHandshake, error) {
	if err := config.validate(); err != nil {
		return Response{}, nil, err
	}
	if challenge.Version != Version || !challenge.SupportedFeatures.valid() {
		return Response{}, nil, ErrMalformedMessage
	}
	if !requestedFeatures.valid() || requestedFeatures&^challenge.SupportedFeatures != 0 {
		return Response{}, nil, ErrUnsupportedFeatures
	}
	var response Response
	response.RequestedFeatures = requestedFeatures
	if requestedFeatures&FeatureDeviceIdentity != 0 {
		if config.DeviceID.IsZero() {
			return Response{}, nil, fmt.Errorf("%w: zero device identity", ErrInvalidConfig)
		}
		response.DeviceID = config.DeviceID
	}
	if _, err := io.ReadFull(random, response.ClientNonce[:]); err != nil {
		return Response{}, nil, fmt.Errorf("read client nonce: %w", err)
	}
	authKey, err := deriveAuthKey(config)
	if err != nil {
		return Response{}, nil, err
	}
	transcript := buildTranscript(config, challenge, response.ClientNonce, requestedFeatures, response.DeviceID)
	response.Tag = calculateTag(authKey[:], "NP2 response", transcript)
	keys, err := deriveSessionKeys(authKey, transcript)
	if err != nil {
		return Response{}, nil, err
	}
	client := &ClientHandshake{
		expectedConfirm: calculateTag(authKey[:], "NP2 confirm", transcript),
		keys:            keys,
	}
	return response, client, nil
}

func (s *ServerHandshake) VerifyResponse(response Response, now time.Time) (Confirm, SessionKeys, error) {
	confirm, keys, _, err := s.VerifyResponseWithCredentials(response, now, []Credential{{
		ID: "legacy", RootSecret: s.config.RootSecret,
	}})
	return confirm, keys, err
}

func (s *ServerHandshake) VerifyResponseWithCredentials(
	response Response,
	now time.Time,
	credentials []Credential,
) (Confirm, SessionKeys, string, error) {
	if len(credentials) == 0 || len(credentials) > MaxServerCredentials {
		return Confirm{}, SessionKeys{}, "", ErrInvalidConfig
	}
	for _, credential := range credentials {
		candidate := s.config
		candidate.RootSecret = credential.RootSecret
		if len(credential.ID) == 0 || len(credential.ID) > 128 || candidate.validate() != nil {
			return Confirm{}, SessionKeys{}, "", ErrInvalidConfig
		}
	}
	s.mu.Lock()
	if s.used {
		s.mu.Unlock()
		return Confirm{}, SessionKeys{}, "", ErrChallengeUsed
	}
	s.used = true
	s.mu.Unlock()

	age := now.Sub(s.createdAt)
	if age < 0 || age > MaxChallengeAge {
		return Confirm{}, SessionKeys{}, "", ErrExpiredChallenge
	}
	if !response.RequestedFeatures.valid() || response.RequestedFeatures&^s.supportedFeatures != 0 {
		return Confirm{}, SessionKeys{}, "", ErrUnsupportedFeatures
	}
	if response.RequestedFeatures&FeatureDeviceIdentity != 0 && response.DeviceID.IsZero() {
		return Confirm{}, SessionKeys{}, "", ErrMalformedMessage
	}
	transcript := buildTranscript(s.config, s.challenge, response.ClientNonce, response.RequestedFeatures, response.DeviceID)
	var matchedAuthKey [32]byte
	matchedID := ""
	matches := 0
	for _, credential := range credentials {
		candidate := s.config
		candidate.RootSecret = credential.RootSecret
		authKey, err := deriveAuthKey(candidate)
		if err != nil {
			return Confirm{}, SessionKeys{}, "", err
		}
		expectedResponse := calculateTag(authKey[:], "NP2 response", transcript)
		match := subtle.ConstantTimeCompare(response.Tag[:], expectedResponse[:])
		if match == 1 {
			matchedAuthKey = authKey
			matchedID = credential.ID
			matches++
		}
	}
	if matches == 0 {
		return Confirm{}, SessionKeys{}, "", ErrAuthentication
	}
	if matches != 1 {
		return Confirm{}, SessionKeys{}, "", ErrInvalidConfig
	}
	keys, err := deriveSessionKeys(matchedAuthKey, transcript)
	if err != nil {
		return Confirm{}, SessionKeys{}, "", err
	}
	confirm := Confirm{Tag: calculateTag(matchedAuthKey[:], "NP2 confirm", transcript)}
	return confirm, keys, matchedID, nil
}

func (c *ClientHandshake) VerifyConfirm(confirm Confirm) (SessionKeys, error) {
	if c == nil {
		return SessionKeys{}, ErrAuthentication
	}
	if !hmac.Equal(confirm.Tag[:], c.expectedConfirm[:]) {
		return SessionKeys{}, ErrAuthentication
	}
	return c.keys, nil
}

func deriveAuthKey(config HandshakeConfig) ([32]byte, error) {
	info := "NP2 auth\x00" + config.ServerIdentity
	key, err := hkdf.Key(sha256.New, config.RootSecret[:], []byte("Neproto NP/2"), info, 32)
	if err != nil {
		return [32]byte{}, fmt.Errorf("derive authentication key: %w", err)
	}
	return [32]byte(key), nil
}

func deriveSessionKeys(authKey [32]byte, transcript []byte) (SessionKeys, error) {
	salt := sha256.Sum256(transcript)
	derive := func(info string, size int) ([]byte, error) {
		key, err := hkdf.Key(sha256.New, authKey[:], salt[:], info, size)
		if err != nil {
			return nil, err
		}
		return key, nil
	}
	derive32 := func(info string) ([32]byte, error) {
		raw, err := derive(info, 32)
		if err != nil {
			return [32]byte{}, err
		}
		return [32]byte(raw), nil
	}

	headerMap, err := derive32("NP2 header map")
	if err != nil {
		return SessionKeys{}, fmt.Errorf("derive header map key: %w", err)
	}
	padding, err := derive32("NP2 padding")
	if err != nil {
		return SessionKeys{}, fmt.Errorf("derive padding key: %w", err)
	}
	control, err := derive32("NP2 control")
	if err != nil {
		return SessionKeys{}, fmt.Errorf("derive control key: %w", err)
	}
	clientToServer, err := derive32("NP2 cell c2s key")
	if err != nil {
		return SessionKeys{}, fmt.Errorf("derive client-to-server cell key: %w", err)
	}
	serverToClient, err := derive32("NP2 cell s2c key")
	if err != nil {
		return SessionKeys{}, fmt.Errorf("derive server-to-client cell key: %w", err)
	}
	clientNonceRaw, err := derive("NP2 cell c2s nonce", 4)
	if err != nil {
		return SessionKeys{}, fmt.Errorf("derive client-to-server nonce prefix: %w", err)
	}
	serverNonceRaw, err := derive("NP2 cell s2c nonce", 4)
	if err != nil {
		return SessionKeys{}, fmt.Errorf("derive server-to-client nonce prefix: %w", err)
	}
	return SessionKeys{
		HeaderMap: headerMap, Padding: padding, Control: control,
		ClientToServer: clientToServer, ServerToClient: serverToClient,
		ClientToServerNonce: [4]byte(clientNonceRaw),
		ServerToClientNonce: [4]byte(serverNonceRaw),
	}, nil
}

func buildTranscript(
	config HandshakeConfig,
	challenge Challenge,
	clientNonce [NonceSize]byte,
	requestedFeatures FeatureSet,
	deviceID DeviceID,
) []byte {
	identity := []byte(config.ServerIdentity)
	capacity := 2 + 2 + len(identity) + NonceSize*2 + 8
	if requestedFeatures&FeatureDeviceIdentity != 0 {
		capacity += DeviceIDSize
	}
	transcript := make([]byte, 0, capacity)
	transcript = append(transcript, Version, byte(config.Carrier))
	transcript = binary.BigEndian.AppendUint16(transcript, uint16(len(identity)))
	transcript = append(transcript, identity...)
	transcript = append(transcript, challenge.ServerNonce[:]...)
	transcript = append(transcript, clientNonce[:]...)
	transcript = binary.BigEndian.AppendUint32(transcript, uint32(challenge.SupportedFeatures))
	transcript = binary.BigEndian.AppendUint32(transcript, uint32(requestedFeatures))
	if requestedFeatures&FeatureDeviceIdentity != 0 {
		transcript = append(transcript, deviceID[:]...)
	}
	return transcript
}

func calculateTag(key []byte, label string, transcript []byte) [AuthTagSize]byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(label))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(transcript)
	return [AuthTagSize]byte(mac.Sum(nil))
}
