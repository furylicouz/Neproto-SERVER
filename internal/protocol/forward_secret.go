package protocol

import (
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
)

var ErrForwardSecrecy = errors.New("invalid NP/2 forward secrecy exchange")

type X25519KeyPair struct {
	private *ecdh.PrivateKey
	public  [32]byte
}

func GenerateX25519KeyPair(random io.Reader) (*X25519KeyPair, error) {
	if random == nil {
		return nil, ErrForwardSecrecy
	}
	private, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return nil, errors.Join(ErrForwardSecrecy, err)
	}
	return x25519KeyPair(private)
}

func (p *X25519KeyPair) Public() [32]byte {
	if p == nil {
		return [32]byte{}
	}
	return p.public
}

func DeriveForwardSecretSessionKeys(
	base SessionKeys,
	local *X25519KeyPair,
	peerPublic [32]byte,
	serverPublic [32]byte,
	clientPublic [32]byte,
) (SessionKeys, error) {
	if base.Control == ([32]byte{}) || local == nil || local.private == nil ||
		peerPublic == ([32]byte{}) || serverPublic == ([32]byte{}) ||
		clientPublic == ([32]byte{}) || local.public != serverPublic && local.public != clientPublic {
		return SessionKeys{}, ErrForwardSecrecy
	}
	peer, err := ecdh.X25519().NewPublicKey(peerPublic[:])
	if err != nil {
		return SessionKeys{}, errors.Join(ErrForwardSecrecy, err)
	}
	shared, err := local.private.ECDH(peer)
	if err != nil {
		return SessionKeys{}, errors.Join(ErrForwardSecrecy, err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("Neproto NP/2 X25519 v1\x00"))
	_, _ = hash.Write(base.Control[:])
	_, _ = hash.Write(serverPublic[:])
	_, _ = hash.Write(clientPublic[:])
	salt := hash.Sum(nil)
	derive := func(label string, size int) ([]byte, error) {
		value, deriveErr := hkdf.Key(sha256.New, shared, salt, label, size)
		if deriveErr != nil {
			return nil, errors.Join(ErrForwardSecrecy, deriveErr)
		}
		return value, nil
	}
	derive32 := func(label string) ([32]byte, error) {
		value, deriveErr := derive(label, 32)
		if deriveErr != nil {
			return [32]byte{}, deriveErr
		}
		return [32]byte(value), nil
	}
	control, err := derive32("NP2 FS control")
	if err != nil {
		return SessionKeys{}, err
	}
	clientToServer, err := derive32("NP2 FS cell c2s")
	if err != nil {
		return SessionKeys{}, err
	}
	serverToClient, err := derive32("NP2 FS cell s2c")
	if err != nil {
		return SessionKeys{}, err
	}
	clientNonce, err := derive("NP2 FS nonce c2s", 4)
	if err != nil {
		return SessionKeys{}, err
	}
	serverNonce, err := derive("NP2 FS nonce s2c", 4)
	if err != nil {
		return SessionKeys{}, err
	}
	result := base
	result.Control = control
	result.ClientToServer = clientToServer
	result.ServerToClient = serverToClient
	result.ClientToServerNonce = [4]byte(clientNonce)
	result.ServerToClientNonce = [4]byte(serverNonce)
	return result, nil
}

func ForwardSecretConfirmation(
	keys SessionKeys,
	serverPublic [32]byte,
	clientPublic [32]byte,
) ([32]byte, error) {
	return forwardSecretProof(keys, serverPublic, clientPublic, "NP2 forward secrecy ready\x00")
}

func ForwardSecretAcknowledgement(
	keys SessionKeys,
	serverPublic [32]byte,
	clientPublic [32]byte,
) ([32]byte, error) {
	return forwardSecretProof(keys, serverPublic, clientPublic, "NP2 forward secrecy acknowledged\x00")
}

func forwardSecretProof(
	keys SessionKeys,
	serverPublic [32]byte,
	clientPublic [32]byte,
	label string,
) ([32]byte, error) {
	if keys.Control == ([32]byte{}) || serverPublic == ([32]byte{}) || clientPublic == ([32]byte{}) {
		return [32]byte{}, ErrForwardSecrecy
	}
	mac := hmac.New(sha256.New, keys.Control[:])
	_, _ = mac.Write([]byte(label))
	_, _ = mac.Write(serverPublic[:])
	_, _ = mac.Write(clientPublic[:])
	return [32]byte(mac.Sum(nil)), nil
}

func x25519KeyPair(private *ecdh.PrivateKey) (*X25519KeyPair, error) {
	if private == nil || len(private.PublicKey().Bytes()) != 32 {
		return nil, ErrForwardSecrecy
	}
	pair := &X25519KeyPair{private: private}
	copy(pair.public[:], private.PublicKey().Bytes())
	return pair, nil
}
