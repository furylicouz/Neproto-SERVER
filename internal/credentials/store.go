package credentials

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	IDSize               = 16
	MaxActiveCredentials = 256
)

var ErrInvalidStore = errors.New("invalid credential store")

type Credential struct {
	ID     string
	Secret [32]byte
}

func LoadActiveDirectory(directory string) ([]Credential, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: active path must be a directory", ErrInvalidStore)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("%w: read active directory", ErrInvalidStore)
	}
	secretEntries := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".secret") {
			secretEntries = append(secretEntries, entry)
		}
	}
	if len(secretEntries) == 0 || len(secretEntries) > MaxActiveCredentials {
		return nil, fmt.Errorf("%w: active credential count", ErrInvalidStore)
	}
	sort.Slice(secretEntries, func(left, right int) bool {
		return secretEntries[left].Name() < secretEntries[right].Name()
	})
	loaded := make([]Credential, 0, len(secretEntries))
	seenSecrets := make(map[[32]byte]string, len(secretEntries))
	for _, entry := range secretEntries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: credential must be a regular file", ErrInvalidStore)
		}
		id := strings.TrimSuffix(entry.Name(), ".secret")
		if !validID(id) {
			return nil, fmt.Errorf("%w: malformed credential ID", ErrInvalidStore)
		}
		secret, err := loadSecret(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, errors.Join(ErrInvalidStore, err)
		}
		if previous, duplicate := seenSecrets[secret]; duplicate {
			return nil, fmt.Errorf("%w: duplicate secret for %s and %s", ErrInvalidStore, previous, id)
		}
		seenSecrets[secret] = id
		loaded = append(loaded, Credential{ID: id, Secret: secret})
	}
	return loaded, nil
}

func loadSecret(path string) ([32]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return [32]byte{}, fmt.Errorf("%w: credential must be a regular non-symlink file", ErrInvalidStore)
	}
	if runtime.GOOS != "windows" && before.Mode().Perm()&0o077 != 0 {
		return [32]byte{}, fmt.Errorf("%w: group/other permission bits must be zero", ErrInvalidStore)
	}
	file, err := os.Open(path)
	if err != nil {
		return [32]byte{}, fmt.Errorf("%w: open credential", ErrInvalidStore)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return [32]byte{}, fmt.Errorf("%w: credential changed while opening", ErrInvalidStore)
	}
	raw, err := io.ReadAll(io.LimitReader(file, 129))
	if err != nil || len(raw) > 128 {
		return [32]byte{}, fmt.Errorf("%w: credential read/size failure", ErrInvalidStore)
	}
	encoded := string(raw)
	if strings.HasSuffix(encoded, "\r\n") {
		encoded = strings.TrimSuffix(encoded, "\r\n")
	} else if strings.HasSuffix(encoded, "\n") {
		encoded = strings.TrimSuffix(encoded, "\n")
	}
	if len(encoded) != 43 || strings.ContainsAny(encoded, " \t\r\n=") {
		return [32]byte{}, fmt.Errorf("%w: malformed credential", ErrInvalidStore)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return [32]byte{}, fmt.Errorf("%w: malformed credential", ErrInvalidStore)
	}
	var secret [32]byte
	copy(secret[:], decoded)
	if secret == ([32]byte{}) {
		return [32]byte{}, fmt.Errorf("%w: zero credential", ErrInvalidStore)
	}
	return secret, nil
}

func validID(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == IDSize && base64.RawURLEncoding.EncodeToString(raw) == value
}
