package protocol

import (
	"bytes"
	"crypto/ecdh"
	"testing"
)

func TestX25519ForwardSecretKeysMatchAndBindTranscript(t *testing.T) {
	serverPrivate, err := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	clientPrivate, err := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		t.Fatal(err)
	}
	server, _ := x25519KeyPair(serverPrivate)
	client, _ := x25519KeyPair(clientPrivate)
	base := SessionKeys{Control: [32]byte{1}, ClientToServer: [32]byte{2}, ServerToClient: [32]byte{3}}
	serverKeys, err := DeriveForwardSecretSessionKeys(base, server, client.Public(), server.Public(), client.Public())
	if err != nil {
		t.Fatal(err)
	}
	clientKeys, err := DeriveForwardSecretSessionKeys(base, client, server.Public(), server.Public(), client.Public())
	if err != nil {
		t.Fatal(err)
	}
	if serverKeys != clientKeys || serverKeys == base || serverKeys.Control == base.Control {
		t.Fatal("peers did not derive matching fresh keys")
	}
	changedBase := base
	changedBase.Control[1] = 9
	changed, err := DeriveForwardSecretSessionKeys(changedBase, client, server.Public(), server.Public(), client.Public())
	if err != nil {
		t.Fatal(err)
	}
	if changed == clientKeys {
		t.Fatal("forward secret keys were not bound to authenticated transcript")
	}
	serverConfirm, _ := ForwardSecretConfirmation(serverKeys, server.Public(), client.Public())
	clientConfirm, _ := ForwardSecretConfirmation(clientKeys, server.Public(), client.Public())
	if serverConfirm != clientConfirm || serverConfirm == ([32]byte{}) {
		t.Fatal("key confirmation mismatch")
	}
	serverAck, _ := ForwardSecretAcknowledgement(serverKeys, server.Public(), client.Public())
	clientAck, _ := ForwardSecretAcknowledgement(clientKeys, server.Public(), client.Public())
	if serverAck != clientAck || serverAck == ([32]byte{}) || serverAck == serverConfirm {
		t.Fatal("key acknowledgement mismatch or reflection label collision")
	}
}

func TestX25519RejectsInvalidPeerAndRoleBinding(t *testing.T) {
	pair, err := GenerateX25519KeyPair(bytes.NewReader(bytes.Repeat([]byte{0x61}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	base := SessionKeys{Control: [32]byte{1}}
	if _, err := DeriveForwardSecretSessionKeys(base, pair, [32]byte{}, pair.Public(), [32]byte{2}); err == nil {
		t.Fatal("zero peer key was accepted")
	}
	if _, err := DeriveForwardSecretSessionKeys(base, pair, [32]byte{3}, [32]byte{4}, [32]byte{5}); err == nil {
		t.Fatal("unbound local key was accepted")
	}
}
