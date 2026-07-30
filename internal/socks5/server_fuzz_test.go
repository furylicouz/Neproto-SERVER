package socks5

import (
	"bytes"
	"testing"
)

func FuzzReadRequest(f *testing.F) {
	f.Add([]byte{5, 1, 0, 1, 1, 1, 1, 1, 0, 80})
	f.Add([]byte{5, 1, 0, 3, 11, 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm', 1, 187})
	f.Add([]byte{5, 1, 0, 4, 0x20, 0x01, 0x48, 0x60, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x88, 0x88, 0, 53})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _, _ = readRequest(bytes.NewReader(raw))
	})
}

func FuzzDecodeSOCKSUDPDatagram(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 1, 1, 1, 1, 0, 53, 'd', 'n', 's'})
	f.Add([]byte{0, 0, 0, 3, 11, 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm', 1, 187})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _, _ = decodeSOCKSUDPDatagram(raw)
	})
}
