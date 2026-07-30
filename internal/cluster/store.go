package cluster

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	stateFileName      = "state.json"
	lastGoodFileName   = "state.last-good.json"
	signingKeyFileName = "catalog-signing.key"
	maxStateBytes      = 4 << 20
)

var (
	ErrStateExists      = errors.New("cluster state already exists")
	ErrStateNotFound    = errors.New("cluster state not found")
	ErrRevisionConflict = errors.New("cluster state revision conflict")
)

type Store struct {
	root string
	mu   sync.Mutex
}

func (store *Store) LoadOrCreateSigningKey(random io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	path := filepath.Join(store.root, signingKeyFileName)
	publicKey, privateKey, err := loadSigningKey(path)
	if err == nil {
		return publicKey, privateKey, nil
	}
	if !errors.Is(err, ErrStateNotFound) {
		return nil, nil, err
	}
	if random == nil {
		random = rand.Reader
	}
	publicKey, privateKey, err = ed25519.GenerateKey(random)
	if err != nil {
		return nil, nil, fmt.Errorf("generate catalog signing key: %w", err)
	}
	encoded := []byte(base64.RawURLEncoding.EncodeToString(privateKey))
	if err := writePrivateFile(path, encoded); err != nil {
		return nil, nil, fmt.Errorf("write catalog signing key: %w", err)
	}
	return publicKey, privateKey, nil
}

func (store *Store) LoadSigningKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return loadSigningKey(filepath.Join(store.root, signingKeyFileName))
}

func loadSigningKey(path string) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrStateNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	privateKey, err := base64.RawURLEncoding.DecodeString(string(raw))
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, nil, ErrInvalidState
	}
	key := ed25519.PrivateKey(append([]byte(nil), privateKey...))
	publicKey := append(ed25519.PublicKey(nil), key.Public().(ed25519.PublicKey)...)
	return publicKey, key, nil
}

func OpenStore(root string) (*Store, error) {
	if root == "" {
		return nil, ErrInvalidState
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, ErrInvalidState
	}
	info, statErr := os.Lstat(absolute)
	if errors.Is(statErr, os.ErrNotExist) {
		if err := os.MkdirAll(absolute, 0o700); err != nil {
			return nil, fmt.Errorf("create cluster state directory: %w", err)
		}
		if err := os.Chmod(absolute, 0o700); err != nil {
			return nil, fmt.Errorf("secure cluster state directory: %w", err)
		}
	} else if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: cluster state path is not a directory", ErrInvalidState)
	}
	return &Store{root: filepath.Clean(absolute)}, nil
}

func (store *Store) Initialize(state State) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ValidateState(state); err != nil {
		return err
	}
	if _, err := os.Stat(store.statePath()); err == nil {
		return ErrStateExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeStateFile(store.statePath(), state)
}

func (store *Store) Load() (State, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return loadStateFile(store.statePath())
}

func (store *Store) LoadLastGood() (State, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return loadStateFile(store.lastGoodPath())
}

func (store *Store) Save(expectedRevision uint64, next State) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, err := loadStateFile(store.statePath())
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision || next.Revision != expectedRevision+1 {
		return ErrRevisionConflict
	}
	if next.ClusterID != current.ClusterID {
		return ErrInvalidState
	}
	if err := ValidateState(next); err != nil {
		return ErrInvalidState
	}
	if err := writeStateFile(store.lastGoodPath(), current); err != nil {
		return fmt.Errorf("write last-good cluster state: %w", err)
	}
	if err := writeStateFile(store.statePath(), next); err != nil {
		return fmt.Errorf("write cluster state: %w", err)
	}
	return nil
}

func (store *Store) statePath() string    { return filepath.Join(store.root, stateFileName) }
func (store *Store) lastGoodPath() string { return filepath.Join(store.root, lastGoodFileName) }

func loadStateFile(path string) (State, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, ErrStateNotFound
	}
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateBytes+1))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("decode cluster state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return State{}, ErrInvalidState
	}
	if err := ValidateState(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func writeStateFile(path string, state State) error {
	if err := ValidateState(state); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxStateBytes {
		return ErrInvalidState
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cluster-state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func writePrivateFile(path string, payload []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cluster-secret-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFile(temporaryPath, path)
}

func replaceFile(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	backup := destination + ".replace-backup"
	_ = os.Remove(backup)
	if err := os.Rename(destination, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		_ = os.Rename(backup, destination)
		return err
	}
	_ = os.Remove(backup)
	return nil
}
