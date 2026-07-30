package protocol

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

// TestV21CompatibilityVectors freezes the deployed NP/2 v2.1 handshake,
// key schedule, type map, and base-cell encoding. Any intentional change to
// these vectors requires a separate protocol version and migration plan.
func TestV21CompatibilityVectors(t *testing.T) {
	const expected = `challenge=02414141414141414141414141414141414141414141414141414141414141414100000015
response=727272727272727272727272727272727272727272727272727272727272727200000015ad7667464c9af65318d4816c5f8fbe43c8dcdee6a7e58c70e3c9be40c79c7b4d
confirm=f23833aad97511b25f90334ad5c052d1d7a2cbc19a8c14f2457a9ab7f121b286
header_map=526b46c3077f0fd0c67d21d23d7e6c76c4359a58ff9b1c7d5f202275a0bb5769
padding=08d569e604e3430f633eed1a7e6a51ed16e2b86846c55768196b5502072ea42c
control=24cb5c90609dd60575bc60c976161dc2fbf350ba2de972fbdf0328543488cd4c
c2s=075d48627f2e49718d2645c569efb532e0b6f5b445a56f64f56f51fd6e8f0ade
s2c=c7da91624a3b5d44b5297026d486022cbc80d73e792e5088bce0e3bb248c707c
c2s_nonce=44edf122
s2c_nonce=c98424a6
type_map=06020c0b0a09010405070803
cell=0b11090f0376322e312d636f6d70617469626c65a1b2c3`

	now := time.Date(2026, time.July, 18, 0, 0, 0, 0, time.UTC)
	config := testHandshakeConfig(CarrierHTTPS)
	features := FeatureMultiplex | FeatureProfileWeb | FeatureCellAEAD
	server, challenge, err := NewServerHandshake(
		config,
		features,
		now,
		bytes.NewReader(bytes.Repeat([]byte{0x41}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create server handshake: %v", err)
	}
	response, client, err := RespondToChallenge(
		config,
		challenge,
		features,
		bytes.NewReader(bytes.Repeat([]byte{0x72}, NonceSize)),
	)
	if err != nil {
		t.Fatalf("create client response: %v", err)
	}
	confirm, serverKeys, err := server.VerifyResponse(response, now.Add(time.Second))
	if err != nil {
		t.Fatalf("verify response: %v", err)
	}
	clientKeys, err := client.VerifyConfirm(confirm)
	if err != nil || clientKeys != serverKeys {
		t.Fatalf("verify confirm: err=%v keys_equal=%v", err, clientKeys == serverKeys)
	}

	typeMap, err := NewTypeMap(serverKeys.HeaderMap)
	if err != nil {
		t.Fatalf("derive type map: %v", err)
	}
	mapBytes := make([]byte, 0, CellKindCount-1)
	for kind := CellOpen; kind < CellKindCount; kind++ {
		wire, encodeErr := typeMap.EncodeKind(kind)
		if encodeErr != nil {
			t.Fatalf("encode kind %d: %v", kind, encodeErr)
		}
		mapBytes = append(mapBytes, wire)
	}
	cellRaw, err := EncodeCell(typeMap, Cell{
		Kind: CellData, StreamID: 17, Sequence: 9,
		Payload: []byte("v2.1-compatible"), Padding: []byte{0xa1, 0xb2, 0xc3},
	})
	if err != nil {
		t.Fatalf("encode base cell: %v", err)
	}

	actual := fmt.Sprintf(
		"challenge=%s\nresponse=%s\nconfirm=%s\nheader_map=%s\npadding=%s\ncontrol=%s\nc2s=%s\ns2c=%s\nc2s_nonce=%s\ns2c_nonce=%s\ntype_map=%s\ncell=%s",
		hex.EncodeToString(challenge.MarshalBinary()),
		hex.EncodeToString(response.MarshalBinary()),
		hex.EncodeToString(confirm.MarshalBinary()),
		hex.EncodeToString(serverKeys.HeaderMap[:]),
		hex.EncodeToString(serverKeys.Padding[:]),
		hex.EncodeToString(serverKeys.Control[:]),
		hex.EncodeToString(serverKeys.ClientToServer[:]),
		hex.EncodeToString(serverKeys.ServerToClient[:]),
		hex.EncodeToString(serverKeys.ClientToServerNonce[:]),
		hex.EncodeToString(serverKeys.ServerToClientNonce[:]),
		hex.EncodeToString(mapBytes),
		hex.EncodeToString(cellRaw),
	)
	if actual != expected {
		t.Fatalf("NP/2 v2.1 compatibility vector changed:\n%s", actual)
	}
}

func BenchmarkV21BaseCellCodec(b *testing.B) {
	typeMap, err := NewTypeMap([32]byte{0x42, 0x18, 0x99})
	if err != nil {
		b.Fatal(err)
	}
	cell := Cell{
		Kind: CellData, StreamID: 17, Sequence: 9,
		Payload: bytes.Repeat([]byte{0x5a}, MaxCellPayloadSize),
		Padding: bytes.Repeat([]byte{0xa5}, 1024),
	}
	raw, err := EncodeCell(typeMap, cell)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(cell.Payload)))
		for range b.N {
			if _, encodeErr := EncodeCell(typeMap, cell); encodeErr != nil {
				b.Fatal(encodeErr)
			}
		}
	})
	b.Run("decode", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(cell.Payload)))
		for range b.N {
			if _, decodeErr := DecodeCell(typeMap, raw); decodeErr != nil {
				b.Fatal(decodeErr)
			}
		}
	})
}
