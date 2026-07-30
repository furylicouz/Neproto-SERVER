package proxy

import (
	"bytes"
	"testing"
)

func FuzzDecodeTarget(f *testing.F) {
	for _, target := range []Target{
		{Host: "example.com", Port: 443},
		{Host: "1.1.1.1", Port: 53},
		{Host: "2606:4700:4700::1111", Port: 853},
	} {
		raw, err := EncodeTarget(target)
		if err != nil {
			f.Fatalf("encode seed: %v", err)
		}
		f.Add(raw)
	}
	f.Add([]byte{})
	f.Add([]byte{1, 0x11, 0x81, 0})
	f.Fuzz(func(t *testing.T, raw []byte) {
		target, err := DecodeTarget(raw)
		if err != nil {
			return
		}
		canonical, err := EncodeTarget(target)
		if err != nil {
			t.Fatalf("accepted target does not encode: %v", err)
		}
		if !bytes.Equal(canonical, raw) {
			t.Fatalf("accepted non-canonical target: %x != %x", raw, canonical)
		}
	})
}

func FuzzDecodeOpenRequest(f *testing.F) {
	for _, request := range []OpenRequest{
		{Command: CommandTCPConnect, Target: Target{Host: "example.com", Port: 443}},
		{Command: CommandUDPFixed, Target: Target{Host: "1.1.1.1", Port: 53}},
		{Command: CommandUDPAssociate},
	} {
		raw, err := EncodeOpenRequest(request)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		request, err := DecodeOpenRequest(raw)
		if err != nil {
			return
		}
		canonical, err := EncodeOpenRequest(request)
		if err != nil {
			t.Fatalf("accepted request cannot encode: %v", err)
		}
		if !bytes.Equal(canonical, raw) {
			t.Fatalf("accepted noncanonical request: %x != %x", raw, canonical)
		}
	})
}
