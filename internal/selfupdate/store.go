package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	statusSchema    = 1
	maxStatusBytes  = 16 << 10
	maxMessageRunes = 240
)

type Status struct {
	Schema           int    `json:"schema"`
	State            string `json:"state"`
	CurrentVersion   string `json:"current_version"`
	AvailableVersion string `json:"available_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available"`
	Progress         int    `json:"progress"`
	Message          string `json:"message"`
	ErrorCode        string `json:"error_code,omitempty"`
	UpdatedAt        string `json:"updated_at"`
}

type StatusStore struct {
	Directory string
	Now       func() time.Time
}

func (store StatusStore) Path() string {
	return filepath.Join(store.Directory, "status.json")
}

func (store StatusStore) normalize(status Status) (Status, error) {
	if _, ok := ProgressForStage(status.State); !ok || status.Progress < 0 || status.Progress > 100 {
		return Status{}, errors.New("invalid update status")
	}
	if _, err := ParseVersion(status.CurrentVersion); err != nil {
		return Status{}, err
	}
	if status.AvailableVersion != "" {
		if _, err := ParseVersion(status.AvailableVersion); err != nil {
			return Status{}, err
		}
	}
	status.Schema = statusSchema
	now := time.Now
	if store.Now != nil {
		now = store.Now
	}
	status.UpdatedAt = now().UTC().Format(time.RFC3339)
	status.Message = boundSingleLine(status.Message, maxMessageRunes)
	status.ErrorCode = boundSingleLine(status.ErrorCode, 64)
	return status, nil
}

func (store StatusStore) Write(status Status) error {
	normalized, err := store.normalize(status)
	if err != nil {
		return err
	}
	return store.writeNormalized(normalized)
}

func (store StatusStore) writeNormalized(status Status) error {
	if err := os.MkdirAll(store.Directory, 0o750); err != nil {
		return fmt.Errorf("create update state directory: %w", err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if len(encoded) > maxStatusBytes {
		return errors.New("update status exceeds size limit")
	}
	temporary, err := os.CreateTemp(store.Directory, ".status-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o640); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, store.Path()); err != nil {
		// Windows cannot atomically replace an existing file. Production Linux
		// always uses the atomic rename above; this fallback keeps local tests
		// and developer builds portable.
		if removeErr := os.Remove(store.Path()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if secondErr := os.Rename(temporaryPath, store.Path()); secondErr != nil {
			return secondErr
		}
	}
	return nil
}

func (store StatusStore) Read() (Status, error) {
	file, err := os.Open(store.Path())
	if err != nil {
		return Status{}, err
	}
	defer file.Close()
	limited := &io.LimitedReader{R: file, N: maxStatusBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var status Status
	if err := decoder.Decode(&status); err != nil {
		return Status{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Status{}, errors.New("update status contains trailing data")
	}
	if limited.N <= 0 || status.Schema != statusSchema {
		return Status{}, errors.New("invalid update status")
	}
	if _, ok := ProgressForStage(status.State); !ok || status.Progress < 0 || status.Progress > 100 {
		return Status{}, errors.New("invalid update status")
	}
	return status, nil
}

func boundSingleLine(value string, limit int) string {
	value = strings.Map(func(character rune) rune {
		if character == '\r' || character == '\n' || character == 0 {
			return ' '
		}
		return character
	}, strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
