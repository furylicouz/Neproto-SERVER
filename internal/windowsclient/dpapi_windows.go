//go:build windows

package windowsclient

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

type MachineProtector struct{}

var dpapiEntropy = []byte("NeProto/NP2/windows-client/v1")

func (MachineProtector) Protect(value []byte) ([]byte, error) {
	if len(value) == 0 || len(value) > 4096 {
		return nil, errors.New("invalid DPAPI plaintext")
	}
	input := windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
	entropy := windows.DataBlob{Size: uint32(len(dpapiEntropy)), Data: &dpapiEntropy[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &entropy, 0, nil,
		windows.CRYPTPROTECT_LOCAL_MACHINE|windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}

func (MachineProtector) Unprotect(value []byte) ([]byte, error) {
	if len(value) == 0 || len(value) > 8192 {
		return nil, errors.New("invalid DPAPI ciphertext")
	}
	input := windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
	entropy := windows.DataBlob{Size: uint32(len(dpapiEntropy)), Data: &dpapiEntropy[0]}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, &entropy, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	if output.Size == 0 || output.Size > 4096 {
		return nil, errors.New("invalid DPAPI result")
	}
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
