package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"neproto.local/chameleon/internal/carrier"
	"neproto.local/chameleon/internal/protocol"
)

func TestEncryptedCarrierRoundTripHidesPlaintext(t *testing.T) {
	left, right := newMemoryCarrierPair()
	recorder := &recordingCarrier{Carrier: left}
	client := mustEncryptedCarrier(t, recorder, RoleClient)
	server := mustEncryptedCarrier(t, right, RoleServer)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	plaintext := []byte("private target metadata and stream payload")
	if err := client.Send(ctx, plaintext); err != nil {
		t.Fatalf("send encrypted record: %v", err)
	}
	wire := recorder.LastSent()
	if len(wire) <= len(plaintext) {
		t.Fatalf("encrypted record does not contain an authentication tag: %d", len(wire))
	}
	if bytes.Contains(wire, plaintext) {
		t.Fatal("carrier record exposes plaintext")
	}
	received, err := server.Receive(ctx)
	if err != nil {
		t.Fatalf("receive encrypted record: %v", err)
	}
	if !bytes.Equal(received, plaintext) {
		t.Fatalf("plaintext mismatch: %x != %x", received, plaintext)
	}

	response := []byte("server response")
	if err := server.Send(ctx, response); err != nil {
		t.Fatalf("send response: %v", err)
	}
	received, err = client.Receive(ctx)
	if err != nil {
		t.Fatalf("receive response: %v", err)
	}
	if !bytes.Equal(received, response) {
		t.Fatalf("response mismatch: %x != %x", received, response)
	}
}

func TestEncryptedCarrierRejectsTampering(t *testing.T) {
	left, right := newMemoryCarrierPair()
	recorder := &recordingCarrier{Carrier: left}
	client := mustEncryptedCarrier(t, recorder, RoleClient)
	server := mustEncryptedCarrier(t, right, RoleServer)
	ctx := context.Background()

	if err := client.Send(ctx, []byte("authenticated")); err != nil {
		t.Fatalf("send record: %v", err)
	}
	wire, err := right.Receive(ctx)
	if err != nil {
		t.Fatalf("capture original record: %v", err)
	}
	wire[len(wire)/2] ^= 0x80
	if err := left.Send(ctx, wire); err != nil {
		t.Fatalf("inject tampered record: %v", err)
	}
	if _, err := server.Receive(ctx); !errors.Is(err, ErrRecordAuthentication) {
		t.Fatalf("expected record authentication failure, got %v", err)
	}
}

func TestEncryptedCarrierRejectsReplay(t *testing.T) {
	left, right := newMemoryCarrierPair()
	recorder := &recordingCarrier{Carrier: left}
	client := mustEncryptedCarrier(t, recorder, RoleClient)
	server := mustEncryptedCarrier(t, right, RoleServer)
	ctx := context.Background()

	if err := client.Send(ctx, []byte("one")); err != nil {
		t.Fatalf("send record: %v", err)
	}
	wire := recorder.LastSent()
	if _, err := server.Receive(ctx); err != nil {
		t.Fatalf("receive original: %v", err)
	}
	if err := left.Send(ctx, wire); err != nil {
		t.Fatalf("inject replay: %v", err)
	}
	if _, err := server.Receive(ctx); !errors.Is(err, ErrRecordAuthentication) {
		t.Fatalf("expected replay authentication failure, got %v", err)
	}
}

func TestEncryptedCarrierRejectsReflection(t *testing.T) {
	left, right := newMemoryCarrierPair()
	recorder := &recordingCarrier{Carrier: left}
	client := mustEncryptedCarrier(t, recorder, RoleClient)
	ctx := context.Background()

	if err := client.Send(ctx, []byte("client direction")); err != nil {
		t.Fatalf("send record: %v", err)
	}
	if err := right.Send(ctx, recorder.LastSent()); err != nil {
		t.Fatalf("reflect record: %v", err)
	}
	if _, err := client.Receive(ctx); !errors.Is(err, ErrRecordAuthentication) {
		t.Fatalf("expected reflection authentication failure, got %v", err)
	}
}

func TestEncryptedCarrierRejectsTruncation(t *testing.T) {
	left, right := newMemoryCarrierPair()
	server := mustEncryptedCarrier(t, right, RoleServer)
	if err := left.Send(context.Background(), []byte{1, 2, 3}); err != nil {
		t.Fatalf("inject short record: %v", err)
	}
	if _, err := server.Receive(context.Background()); !errors.Is(err, ErrRecordAuthentication) {
		t.Fatalf("expected truncated record failure, got %v", err)
	}
}

func TestEncryptedCarrierRejectsCounterExhaustion(t *testing.T) {
	left, _ := newMemoryCarrierPair()
	client := mustEncryptedCarrier(t, left, RoleClient)
	client.sendCounter = math.MaxUint64
	if err := client.Send(context.Background(), []byte("never sent")); !errors.Is(err, ErrRecordSequenceExhausted) {
		t.Fatalf("expected counter exhaustion, got %v", err)
	}
}

func TestEncryptedCarrierRatchetingKeepsDirectionsSynchronized(t *testing.T) {
	left, right := newMemoryCarrierPair()
	client := mustEncryptedCarrier(t, left, RoleClient)
	server := mustEncryptedCarrier(t, right, RoleServer)
	client.epochInterval = 2
	server.epochInterval = 2
	initialClientSend := client.sendKey
	initialServerSend := server.sendKey

	for index := range 6 {
		request := []byte{byte(index), 0xa1}
		if err := client.Send(context.Background(), request); err != nil {
			t.Fatalf("send request %d: %v", index, err)
		}
		got, err := server.Receive(context.Background())
		if err != nil || !bytes.Equal(got, request) {
			t.Fatalf("receive request %d=%x error=%v", index, got, err)
		}
		response := []byte{byte(index), 0xb2}
		if err := server.Send(context.Background(), response); err != nil {
			t.Fatalf("send response %d: %v", index, err)
		}
		got, err = client.Receive(context.Background())
		if err != nil || !bytes.Equal(got, response) {
			t.Fatalf("receive response %d=%x error=%v", index, got, err)
		}
	}
	if client.sendEpoch != 2 || client.receiveEpoch != 2 ||
		server.sendEpoch != 2 || server.receiveEpoch != 2 {
		t.Fatalf("ratchet epochs client=(%d,%d) server=(%d,%d)",
			client.sendEpoch, client.receiveEpoch, server.sendEpoch, server.receiveEpoch)
	}
	if client.sendKey == initialClientSend || server.sendKey == initialServerSend {
		t.Fatal("record keys did not change across epochs")
	}
	if client.sendKey != server.receiveKey || server.sendKey != client.receiveKey {
		t.Fatal("directional epoch keys diverged")
	}
}

func mustEncryptedCarrier(t *testing.T, inner carrier.Carrier, role Role) *encryptedCarrier {
	t.Helper()
	keys := protocol.SessionKeys{
		ClientToServer:      [32]byte{0x11, 0x22, 0x33},
		ServerToClient:      [32]byte{0x91, 0x82, 0x73},
		ClientToServerNonce: [4]byte{0x44, 0x55, 0x66, 0x77},
		ServerToClientNonce: [4]byte{0xa4, 0xb5, 0xc6, 0xd7},
	}
	result, err := newEncryptedCarrier(inner, role, keys)
	if err != nil {
		t.Fatalf("create encrypted carrier: %v", err)
	}
	return result
}

type recordingCarrier struct {
	carrier.Carrier
	mu   sync.Mutex
	last []byte
}

func (c *recordingCarrier) Send(ctx context.Context, raw []byte) error {
	c.mu.Lock()
	c.last = append(c.last[:0], raw...)
	c.mu.Unlock()
	return c.Carrier.Send(ctx, raw)
}

func (c *recordingCarrier) LastSent() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.last...)
}

var _ carrier.Carrier = (*recordingCarrier)(nil)
var _ io.Closer = (*encryptedCarrier)(nil)
