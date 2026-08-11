//go:build !windows

package windowsclient

import "errors"

type MachineProtector struct{}

func (MachineProtector) Protect([]byte) ([]byte, error) {
	return nil, errors.New("DPAPI is only available on Windows")
}
func (MachineProtector) Unprotect([]byte) ([]byte, error) {
	return nil, errors.New("DPAPI is only available on Windows")
}
