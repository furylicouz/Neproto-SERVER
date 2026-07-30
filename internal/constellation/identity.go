package constellation

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"

	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/protocol"
)

var ErrContinuityIdentity = errors.New("invalid constellation identity")

func PrincipalFromCredentialID(credentialID string) (continuity.PrincipalID, error) {
	if credentialID == "" || len(credentialID) > 256 {
		return continuity.PrincipalID{}, ErrContinuityIdentity
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("NP2 constellation principal\x00"))
	_, _ = hash.Write([]byte(credentialID))
	return continuity.PrincipalID(hash.Sum(nil)), nil
}

func TranscriptFromSessionKeys(keys protocol.SessionKeys) (continuity.TranscriptID, error) {
	if keys.Control == ([32]byte{}) {
		return continuity.TranscriptID{}, ErrContinuityIdentity
	}
	mac := hmac.New(sha256.New, keys.Control[:])
	_, _ = mac.Write([]byte("NP2 constellation authenticated transcript"))
	return continuity.TranscriptID(mac.Sum(nil)), nil
}

// LeaseKeyFromSessionKeys creates a public identifier for one independently
// authenticated carrier. It is deterministic at both peers but cannot be
// selected by the client or reused across fresh authentication transcripts.
func LeaseKeyFromSessionKeys(keys protocol.SessionKeys) (protocol.ContinuityID, error) {
	if keys.Control == ([32]byte{}) {
		return protocol.ContinuityID{}, ErrContinuityIdentity
	}
	mac := hmac.New(sha256.New, keys.Control[:])
	_, _ = mac.Write([]byte("NP2 constellation lease key v1"))
	digest := mac.Sum(nil)
	var key protocol.ContinuityID
	copy(key[:], digest[:len(key)])
	if key == (protocol.ContinuityID{}) {
		return protocol.ContinuityID{}, ErrContinuityIdentity
	}
	return key, nil
}
