package protocol

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestHandshakeDerivesMatchingSessionKeys(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	serverConfig := testHandshakeConfig(CarrierHTTPS)
	clientConfig := testHandshakeConfig(CarrierHTTPS)

	server, challenge, err := NewServerHandshake(
		serverConfig,
		FeatureMultiplex|FeatureProfileWeb|FeatureProfileInteractive,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x41}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}

	response, client, err := RespondToChallenge(
		clientConfig,
		challenge,
		FeatureMultiplex|FeatureProfileInteractive,
		bytes.NewReader(bytes.Repeat([]byte{0x72}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create response: %v", err)
	}

	confirm, serverKeys, err := server.VerifyResponse(response, now.Add(time.Second))
	if err != nil {
		t.Fatalf("verify response: %v", err)
	}
	clientKeys, err := client.VerifyConfirm(confirm)
	if err != nil {
		t.Fatalf("verify confirm: %v", err)
	}

	if serverKeys != clientKeys {
		t.Fatalf("session keys differ\nserver: %#v\nclient: %#v", serverKeys, clientKeys)
	}
	if serverKeys == (SessionKeys{}) {
		t.Fatal("session keys must not be all zero")
	}
	if serverKeys.ClientToServer == serverKeys.ServerToClient {
		t.Fatal("cell encryption keys must be directionally separated")
	}
	if serverKeys.ClientToServer == ([32]byte{}) || serverKeys.ServerToClient == ([32]byte{}) {
		t.Fatal("cell encryption keys must not be zero")
	}
	if serverKeys.ClientToServerNonce == serverKeys.ServerToClientNonce {
		t.Fatal("cell nonce prefixes must be directionally separated")
	}
}

func TestHandshakeRejectsWrongSecret(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	serverConfig := testHandshakeConfig(CarrierHTTPS)
	clientConfig := testHandshakeConfig(CarrierHTTPS)
	clientConfig.RootSecret[0] ^= 0xff

	server, challenge, err := NewServerHandshake(
		serverConfig,
		FeatureMultiplex,
		now,
		bytes.NewReader(bytes.Repeat([]byte{1}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}
	response, _, err := RespondToChallenge(
		clientConfig,
		challenge,
		FeatureMultiplex,
		bytes.NewReader(bytes.Repeat([]byte{2}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create response: %v", err)
	}

	_, _, err = server.VerifyResponse(response, now)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected authentication error, got %v", err)
	}
}

func TestHandshakeSelectsMatchingCredentialWithoutWireIdentifier(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	serverConfig := testHandshakeConfig(CarrierHTTPS)
	clientConfig := serverConfig
	clientConfig.RootSecret = [RootSecretSize]byte{0x7a, 0x31}
	server, challenge, err := NewServerHandshake(
		serverConfig,
		FeatureMultiplex,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x61}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}
	response, client, err := RespondToChallenge(
		clientConfig,
		challenge,
		FeatureMultiplex,
		bytes.NewReader(bytes.Repeat([]byte{0x62}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create response: %v", err)
	}
	confirm, serverKeys, credentialID, err := server.VerifyResponseWithCredentials(response, now, []Credential{
		{ID: "first", RootSecret: [RootSecretSize]byte{0x11}},
		{ID: "alice", RootSecret: clientConfig.RootSecret},
		{ID: "last", RootSecret: [RootSecretSize]byte{0x33}},
	})
	if err != nil {
		t.Fatalf("verify credential set: %v", err)
	}
	if credentialID != "alice" {
		t.Fatalf("matched credential=%q", credentialID)
	}
	clientKeys, err := client.VerifyConfirm(confirm)
	if err != nil || clientKeys != serverKeys {
		t.Fatalf("credential session keys mismatch: err=%v", err)
	}
}

func TestHandshakeCredentialSetRejectsMissingAndDuplicateMatch(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	config := testHandshakeConfig(CarrierHTTPS)
	makeResponse := func(t *testing.T) (*ServerHandshake, Response) {
		t.Helper()
		server, challenge, err := NewServerHandshake(
			config, FeatureMultiplex, now,
			bytes.NewReader(bytes.Repeat([]byte{0x71}, NonceSize)),
		)
		if err != nil {
			t.Fatal(err)
		}
		response, _, err := RespondToChallenge(
			config, challenge, FeatureMultiplex,
			bytes.NewReader(bytes.Repeat([]byte{0x72}, NonceSize)),
		)
		if err != nil {
			t.Fatal(err)
		}
		return server, response
	}

	server, response := makeResponse(t)
	if _, _, _, err := server.VerifyResponseWithCredentials(response, now, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty credential set error=%v", err)
	}
	server, response = makeResponse(t)
	if _, _, _, err := server.VerifyResponseWithCredentials(response, now, []Credential{
		{ID: "one", RootSecret: config.RootSecret},
		{ID: "two", RootSecret: config.RootSecret},
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("duplicate matching credential error=%v", err)
	}
}

func TestHandshakeBindsFeatureBits(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	config := testHandshakeConfig(CarrierWebRTC)
	server, challenge, err := NewServerHandshake(
		config,
		FeatureMultiplex|FeatureProfileInteractive,
		now,
		bytes.NewReader(bytes.Repeat([]byte{3}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}
	response, _, err := RespondToChallenge(
		config,
		challenge,
		FeatureMultiplex,
		bytes.NewReader(bytes.Repeat([]byte{4}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create response: %v", err)
	}

	response.RequestedFeatures |= FeatureProfileInteractive
	_, _, err = server.VerifyResponse(response, now)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected authentication error after feature tampering, got %v", err)
	}
}

func TestHandshakeRejectsResponseReplay(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	config := testHandshakeConfig(CarrierHTTPS)
	server, challenge, err := NewServerHandshake(
		config,
		FeatureMultiplex,
		now,
		bytes.NewReader(bytes.Repeat([]byte{5}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}
	response, _, err := RespondToChallenge(
		config,
		challenge,
		FeatureMultiplex,
		bytes.NewReader(bytes.Repeat([]byte{6}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create response: %v", err)
	}
	if _, _, err := server.VerifyResponse(response, now); err != nil {
		t.Fatalf("first response failed: %v", err)
	}

	_, _, err = server.VerifyResponse(response, now)
	if !errors.Is(err, ErrChallengeUsed) {
		t.Fatalf("expected used challenge error, got %v", err)
	}
}

func TestHandshakeRejectsResponseFromAnotherChallenge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	config := testHandshakeConfig(CarrierHTTPS)
	_, firstChallenge, err := NewServerHandshake(
		config,
		FeatureMultiplex,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x31}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create first server handshake: %v", err)
	}
	secondServer, _, err := NewServerHandshake(
		config,
		FeatureMultiplex,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x32}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create second server handshake: %v", err)
	}
	response, _, err := RespondToChallenge(
		config,
		firstChallenge,
		FeatureMultiplex,
		bytes.NewReader(bytes.Repeat([]byte{0x33}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create response: %v", err)
	}

	_, _, err = secondServer.VerifyResponse(response, now)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected cross-challenge authentication failure, got %v", err)
	}
}

func TestClientRejectsTamperedConfirm(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	config := testHandshakeConfig(CarrierWebRTC)
	server, challenge, err := NewServerHandshake(
		config,
		FeatureMultiplex,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x51}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}
	response, client, err := RespondToChallenge(
		config,
		challenge,
		FeatureMultiplex,
		bytes.NewReader(bytes.Repeat([]byte{0x52}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create response: %v", err)
	}
	confirm, _, err := server.VerifyResponse(response, now)
	if err != nil {
		t.Fatalf("verify response: %v", err)
	}
	confirm.Tag[0] ^= 0xff

	_, err = client.VerifyConfirm(confirm)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected confirm authentication failure, got %v", err)
	}
}

func TestHandshakeRejectsExpiredChallenge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	config := testHandshakeConfig(CarrierHTTPS)
	server, challenge, err := NewServerHandshake(
		config,
		FeatureMultiplex,
		now,
		bytes.NewReader(bytes.Repeat([]byte{7}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}
	response, _, err := RespondToChallenge(
		config,
		challenge,
		FeatureMultiplex,
		bytes.NewReader(bytes.Repeat([]byte{8}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create response: %v", err)
	}

	_, _, err = server.VerifyResponse(response, now.Add(MaxChallengeAge+time.Nanosecond))
	if !errors.Is(err, ErrExpiredChallenge) {
		t.Fatalf("expected expired challenge error, got %v", err)
	}
}

func TestHandshakeBindsCarrierAndServerIdentity(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	serverConfig := testHandshakeConfig(CarrierHTTPS)
	clientConfig := testHandshakeConfig(CarrierWebRTC)
	clientConfig.ServerIdentity = "other.example"

	server, challenge, err := NewServerHandshake(
		serverConfig,
		FeatureMultiplex,
		now,
		bytes.NewReader(bytes.Repeat([]byte{9}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}
	response, _, err := RespondToChallenge(
		clientConfig,
		challenge,
		FeatureMultiplex,
		bytes.NewReader(bytes.Repeat([]byte{10}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create response: %v", err)
	}

	_, _, err = server.VerifyResponse(response, now)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected transcript binding failure, got %v", err)
	}
}

func TestRespondRejectsUnsupportedFeatures(t *testing.T) {
	config := testHandshakeConfig(CarrierHTTPS)
	_, challenge, err := NewServerHandshake(
		config,
		FeatureMultiplex,
		time.Unix(1_800_000_000, 0),
		bytes.NewReader(bytes.Repeat([]byte{11}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}

	_, _, err = RespondToChallenge(
		config,
		challenge,
		FeatureMultiplex|FeatureProfileWeb,
		bytes.NewReader(bytes.Repeat([]byte{12}, NonceSize)),
	)
	if !errors.Is(err, ErrUnsupportedFeatures) {
		t.Fatalf("expected unsupported features error, got %v", err)
	}
}

func TestAuthenticationMessagesRoundTripCanonically(t *testing.T) {
	challenge := Challenge{
		Version:           Version,
		SupportedFeatures: FeatureMultiplex | FeatureProfileWeb,
	}
	copy(challenge.ServerNonce[:], bytes.Repeat([]byte{0x23}, NonceSize))
	response := Response{RequestedFeatures: FeatureMultiplex}
	copy(response.ClientNonce[:], bytes.Repeat([]byte{0x34}, NonceSize))
	copy(response.Tag[:], bytes.Repeat([]byte{0x45}, AuthTagSize))
	confirm := Confirm{}
	copy(confirm.Tag[:], bytes.Repeat([]byte{0x56}, AuthTagSize))

	challengeRaw := challenge.MarshalBinary()
	parsedChallenge, err := ParseChallenge(challengeRaw)
	if err != nil {
		t.Fatalf("parse challenge: %v", err)
	}
	if parsedChallenge != challenge {
		t.Fatalf("challenge mismatch: %#v != %#v", parsedChallenge, challenge)
	}

	responseRaw := response.MarshalBinary()
	parsedResponse, err := ParseResponse(responseRaw)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if parsedResponse != response {
		t.Fatalf("response mismatch: %#v != %#v", parsedResponse, response)
	}

	confirmRaw := confirm.MarshalBinary()
	parsedConfirm, err := ParseConfirm(confirmRaw)
	if err != nil {
		t.Fatalf("parse confirm: %v", err)
	}
	if parsedConfirm != confirm {
		t.Fatalf("confirm mismatch: %#v != %#v", parsedConfirm, confirm)
	}
}

func TestAuthenticationParsersRejectWrongLengthAndVersion(t *testing.T) {
	challenge := Challenge{Version: Version, SupportedFeatures: FeatureMultiplex}
	raw := challenge.MarshalBinary()

	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "truncated", raw: raw[:len(raw)-1]},
		{name: "trailing", raw: append(append([]byte(nil), raw...), 0)},
		{name: "wrong version", raw: append([]byte{0xff}, raw[1:]...)},
		{name: "oversized", raw: make([]byte, MaxAuthenticationMessageSize+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseChallenge(tt.raw); !errors.Is(err, ErrMalformedMessage) {
				t.Fatalf("expected malformed message error, got %v", err)
			}
		})
	}
}

func testHandshakeConfig(carrier CarrierKind) HandshakeConfig {
	var root [RootSecretSize]byte
	copy(root[:], bytes.Repeat([]byte{0xa5}, RootSecretSize))
	return HandshakeConfig{
		RootSecret:     root,
		ServerIdentity: "edge.example",
		Carrier:        carrier,
	}
}
