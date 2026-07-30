package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const maxSecretFileBytes = 128

var (
	ErrInvalidSecret = errors.New("invalid root secret file")
	ErrRandomSource  = errors.New("failed to generate a nonzero root secret")
)

func LoadSecret(path string) (RootSecret, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return RootSecret{}, fmt.Errorf("%w: stat: %v", ErrInvalidSecret, err)
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return RootSecret{}, fmt.Errorf("%w: must be a regular non-symlink file", ErrInvalidSecret)
	}
	if runtime.GOOS != "windows" && before.Mode().Perm()&0o077 != 0 {
		return RootSecret{}, fmt.Errorf("%w: group/other permission bits must be zero", ErrInvalidSecret)
	}
	file, err := os.Open(path)
	if err != nil {
		return RootSecret{}, fmt.Errorf("%w: open: %v", ErrInvalidSecret, err)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return RootSecret{}, fmt.Errorf("%w: file changed while opening", ErrInvalidSecret)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxSecretFileBytes+1))
	if err != nil || len(raw) > maxSecretFileBytes {
		return RootSecret{}, fmt.Errorf("%w: read/size failure", ErrInvalidSecret)
	}
	encoded := string(raw)
	if strings.HasSuffix(encoded, "\r\n") {
		encoded = strings.TrimSuffix(encoded, "\r\n")
	} else if strings.HasSuffix(encoded, "\n") {
		encoded = strings.TrimSuffix(encoded, "\n")
	}
	return ParseSecret(encoded)
}

// ParseSecret validates a canonical unpadded base64url-encoded 256-bit root
// secret supplied by a platform key store.
func ParseSecret(encoded string) (RootSecret, error) {
	if len(encoded) != 43 || strings.ContainsAny(encoded, " \t\r\n=") {
		return RootSecret{}, ErrInvalidSecret
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return RootSecret{}, ErrInvalidSecret
	}
	var value [32]byte
	copy(value[:], decoded)
	if value == ([32]byte{}) {
		return RootSecret{}, ErrInvalidSecret
	}
	return RootSecret{value: value}, nil
}

func GenerateSecret(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	for attempt := 0; attempt < 8; attempt++ {
		var value [32]byte
		if _, err := io.ReadFull(random, value[:]); err != nil {
			return "", fmt.Errorf("%w: %v", ErrRandomSource, err)
		}
		if value != ([32]byte{}) {
			return base64.RawURLEncoding.EncodeToString(value[:]), nil
		}
	}
	return "", ErrRandomSource
}
