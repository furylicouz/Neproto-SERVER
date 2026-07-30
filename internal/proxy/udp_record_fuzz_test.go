package proxy

import (
	"bytes"
	"testing"
)

func FuzzDecodeUDPRecord(f *testing.F) {
	for _, record := range []UDPRecord{
		{Type: UDPRecordDatagram, PacketID: 1, Payload: []byte("payload")},
		{
			Type: UDPRecordDatagram, PacketID: 2, HasTarget: true,
			Target: Target{Host: "1.1.1.1", Port: 53}, Payload: []byte("dns"),
		},
		{Type: UDPRecordError, PacketID: 3, ErrorCode: UDPErrorGeneral, ErrorMessage: "failed"},
		{Type: UDPRecordClose, PacketID: 4},
	} {
		raw, err := EncodeUDPRecord(record)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		record, err := DecodeUDPRecord(raw)
		if err != nil {
			return
		}
		canonical, err := EncodeUDPRecord(record)
		if err != nil {
			t.Fatalf("accepted record cannot encode: %v", err)
		}
		if !bytes.Equal(canonical, raw) {
			t.Fatalf("accepted noncanonical UDP record: %x != %x", raw, canonical)
		}
	})
}
