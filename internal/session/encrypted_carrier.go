package session

import (
	"context"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

var (
	ErrRecordAuthentication    = errors.New("NP/2 record authentication failed")
	ErrRecordSequenceExhausted = errors.New("NP/2 record sequence exhausted")
	ErrRecordTooLarge          = errors.New("NP/2 record too large")
)

const (
	recordDirectionClientToServer byte = 1
	recordDirectionServerToClient byte = 2
	recordKeyEpochInterval             = 16 * 1024
)

type encryptedCarrier struct {
	inner carrier.Carrier

	sendAEAD      cipher.AEAD
	receiveAEAD   cipher.AEAD
	sendKey       [32]byte
	receiveKey    [32]byte
	sendPrefix    [4]byte
	receivePrefix [4]byte
	sendDirection byte
	recvDirection byte

	sendMu        sync.Mutex
	receiveMu     sync.Mutex
	sendCounter   uint64
	recvCounter   uint64
	sendEpoch     uint64
	receiveEpoch  uint64
	epochInterval uint64
	pendingRekey  *recordRekey
}

type recordRekey struct {
	sendAEAD      cipher.AEAD
	receiveAEAD   cipher.AEAD
	sendKey       [32]byte
	receiveKey    [32]byte
	sendPrefix    [4]byte
	receivePrefix [4]byte
}

var _ carrier.Carrier = (*encryptedCarrier)(nil)

func newEncryptedCarrier(inner carrier.Carrier, role Role, keys protocol.SessionKeys) (*encryptedCarrier, error) {
	if inner == nil || (role != RoleClient && role != RoleServer) ||
		keys.ClientToServer == ([32]byte{}) || keys.ServerToClient == ([32]byte{}) {
		return nil, ErrInvalidConfig
	}

	sendKey, receiveKey := keys.ClientToServer, keys.ServerToClient
	sendPrefix, receivePrefix := keys.ClientToServerNonce, keys.ServerToClientNonce
	sendDirection, receiveDirection := recordDirectionClientToServer, recordDirectionServerToClient
	if role == RoleServer {
		sendKey, receiveKey = receiveKey, sendKey
		sendPrefix, receivePrefix = receivePrefix, sendPrefix
		sendDirection, receiveDirection = receiveDirection, sendDirection
	}
	sendAEAD, err := chacha20poly1305.New(sendKey[:])
	if err != nil {
		return nil, fmt.Errorf("create NP/2 send AEAD: %w", err)
	}
	receiveAEAD, err := chacha20poly1305.New(receiveKey[:])
	if err != nil {
		return nil, fmt.Errorf("create NP/2 receive AEAD: %w", err)
	}
	return &encryptedCarrier{
		inner: inner, sendAEAD: sendAEAD, receiveAEAD: receiveAEAD,
		sendKey: sendKey, receiveKey: receiveKey,
		sendPrefix: sendPrefix, receivePrefix: receivePrefix,
		sendDirection: sendDirection, recvDirection: receiveDirection,
	}, nil
}

func (c *encryptedCarrier) Send(ctx context.Context, plaintext []byte) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if len(plaintext) > protocol.MaxCellSize {
		_ = c.Close()
		return ErrRecordTooLarge
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	transition := c.pendingRekey
	if transition != nil {
		// Keep Receive from authenticating the peer's first new-generation
		// record until the old-generation confirmation has been sent and the
		// local record state has switched atomically.
		c.receiveMu.Lock()
		defer c.receiveMu.Unlock()
	}
	if c.sendCounter == math.MaxUint64 {
		_ = c.Close()
		return ErrRecordSequenceExhausted
	}
	if err := c.rotateSendEpochIfNeeded(); err != nil {
		_ = c.Close()
		return err
	}
	nonce := recordNonce(c.sendPrefix, c.sendCounter)
	aad := recordAssociatedData(c.inner.Kind(), c.sendDirection, c.sendCounter, c.sendEpoch)
	record := c.sendAEAD.Seal(nil, nonce[:], plaintext, aad[:])
	if err := c.inner.Send(ctx, record); err != nil {
		_ = c.Close()
		return err
	}
	c.sendCounter++
	if transition != nil {
		c.applyRekeyLocked(transition)
		c.pendingRekey = nil
	}
	return nil
}

func (c *encryptedCarrier) Receive(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	record, err := c.inner.Receive(ctx)
	if err != nil {
		return nil, err
	}
	c.receiveMu.Lock()
	defer c.receiveMu.Unlock()
	if c.recvCounter == math.MaxUint64 {
		_ = c.Close()
		return nil, ErrRecordSequenceExhausted
	}
	if err := c.rotateReceiveEpochIfNeeded(); err != nil {
		_ = c.Close()
		return nil, err
	}
	if len(record) < c.receiveAEAD.Overhead() || len(record) > protocol.MaxCellSize+c.receiveAEAD.Overhead() {
		_ = c.Close()
		return nil, ErrRecordAuthentication
	}
	nonce := recordNonce(c.receivePrefix, c.recvCounter)
	aad := recordAssociatedData(c.inner.Kind(), c.recvDirection, c.recvCounter, c.receiveEpoch)
	plaintext, err := c.receiveAEAD.Open(nil, nonce[:], record, aad[:])
	if err != nil {
		_ = c.Close()
		return nil, ErrRecordAuthentication
	}
	c.recvCounter++
	return plaintext, nil
}

func (c *encryptedCarrier) rekey(keys protocol.SessionKeys) error {
	transition, err := c.prepareRekey(keys)
	if err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.receiveMu.Lock()
	defer c.receiveMu.Unlock()
	c.applyRekeyLocked(transition)
	c.pendingRekey = nil
	return nil
}

// rekeyAfterNextSend arms an atomic record boundary: the next outbound record
// uses the current generation, while every later outbound record and the
// peer's response use the prepared generation. The caller must ensure the next
// send is the authenticated rekey confirmation.
func (c *encryptedCarrier) rekeyAfterNextSend(keys protocol.SessionKeys) error {
	transition, err := c.prepareRekey(keys)
	if err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.pendingRekey != nil {
		return ErrInvalidConfig
	}
	c.pendingRekey = transition
	return nil
}

func (c *encryptedCarrier) prepareRekey(keys protocol.SessionKeys) (*recordRekey, error) {
	if c == nil || keys.ClientToServer == ([32]byte{}) || keys.ServerToClient == ([32]byte{}) {
		return nil, ErrInvalidConfig
	}
	sendKey, receiveKey := keys.ClientToServer, keys.ServerToClient
	sendPrefix, receivePrefix := keys.ClientToServerNonce, keys.ServerToClientNonce
	if c.sendDirection == recordDirectionServerToClient {
		sendKey, receiveKey = receiveKey, sendKey
		sendPrefix, receivePrefix = receivePrefix, sendPrefix
	}
	sendAEAD, err := chacha20poly1305.New(sendKey[:])
	if err != nil {
		return nil, err
	}
	receiveAEAD, err := chacha20poly1305.New(receiveKey[:])
	if err != nil {
		return nil, err
	}
	return &recordRekey{
		sendAEAD: sendAEAD, receiveAEAD: receiveAEAD,
		sendKey: sendKey, receiveKey: receiveKey,
		sendPrefix: sendPrefix, receivePrefix: receivePrefix,
	}, nil
}

// applyRekeyLocked requires both sendMu and receiveMu.
func (c *encryptedCarrier) applyRekeyLocked(transition *recordRekey) {
	c.sendAEAD, c.receiveAEAD = transition.sendAEAD, transition.receiveAEAD
	c.sendKey, c.receiveKey = transition.sendKey, transition.receiveKey
	c.sendPrefix, c.receivePrefix = transition.sendPrefix, transition.receivePrefix
	c.sendCounter, c.recvCounter = 0, 0
	c.sendEpoch, c.receiveEpoch = 0, 0
	// rekey is entered only after the authenticated forward-secret extension
	// barrier. Keeping ratcheting disabled before this point preserves the
	// byte-compatible v2.2 behavior of peers that did not negotiate it.
	c.epochInterval = recordKeyEpochInterval
}

func (c *encryptedCarrier) rotateSendEpochIfNeeded() error {
	if c.epochInterval == 0 || c.sendCounter == 0 || c.sendCounter%c.epochInterval != 0 {
		return nil
	}
	nextEpoch := c.sendEpoch + 1
	nextKey := deriveRecordEpochKey(c.sendKey, c.sendDirection, nextEpoch)
	nextAEAD, err := chacha20poly1305.New(nextKey[:])
	if err != nil {
		return err
	}
	c.sendKey, c.sendAEAD, c.sendEpoch = nextKey, nextAEAD, nextEpoch
	return nil
}

func (c *encryptedCarrier) rotateReceiveEpochIfNeeded() error {
	if c.epochInterval == 0 || c.recvCounter == 0 || c.recvCounter%c.epochInterval != 0 {
		return nil
	}
	nextEpoch := c.receiveEpoch + 1
	nextKey := deriveRecordEpochKey(c.receiveKey, c.recvDirection, nextEpoch)
	nextAEAD, err := chacha20poly1305.New(nextKey[:])
	if err != nil {
		return err
	}
	c.receiveKey, c.receiveAEAD, c.receiveEpoch = nextKey, nextAEAD, nextEpoch
	return nil
}

func deriveRecordEpochKey(current [32]byte, direction byte, epoch uint64) [32]byte {
	mac := hmac.New(sha256.New, current[:])
	_, _ = mac.Write([]byte("NeProto NP/2 record epoch v1"))
	_, _ = mac.Write([]byte{direction})
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], epoch)
	_, _ = mac.Write(encoded[:])
	var next [32]byte
	copy(next[:], mac.Sum(nil))
	return next
}

func (c *encryptedCarrier) Close() error {
	return c.inner.Close()
}

func (c *encryptedCarrier) Kind() protocol.CarrierKind {
	return c.inner.Kind()
}

func recordNonce(prefix [4]byte, counter uint64) [chacha20poly1305.NonceSize]byte {
	var nonce [chacha20poly1305.NonceSize]byte
	copy(nonce[:4], prefix[:])
	binary.BigEndian.PutUint64(nonce[4:], counter)
	return nonce
}

func recordAssociatedData(kind protocol.CarrierKind, direction byte, counter, epoch uint64) [37]byte {
	var aad [37]byte
	copy(aad[:], "Neproto NP/2 cell")
	aad[18] = protocol.Version
	aad[19] = byte(kind)
	aad[20] = direction
	binary.BigEndian.PutUint64(aad[21:], counter)
	binary.BigEndian.PutUint64(aad[29:], epoch)
	return aad
}
