package clusterrelay

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"neproto.local/chameleon/internal/cluster"
)

func NewCredentialSyncHandler(nodeID, masterNodeID string, principals map[string]string, directory string) (func(context.Context, string, cluster.CredentialSyncRequest) error, error) {
	if !identifier(nodeID) || !identifier(masterNodeID) || nodeID == masterNodeID || principals == nil || directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, ErrInvalidConfig
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidConfig
	}
	return func(_ context.Context, peerCredentialID string, request cluster.CredentialSyncRequest) error {
		if principals[peerCredentialID] != masterNodeID {
			return fmt.Errorf("%w: peer principal mismatch", ErrRelayUnauthorized)
		}
		if err := cluster.ValidateCredentialSync(request); err != nil {
			return fmt.Errorf("%w: invalid credential request", ErrInvalidConfig)
		}
		path := filepath.Join(directory, request.CredentialID+".secret")
		switch request.Operation {
		case cluster.CredentialSyncUpsert:
			return replaceCredentialFile(directory, path, []byte(request.Secret+"\n"))
		case cluster.CredentialSyncRevoke:
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return ErrInvalidConfig
			}
			return os.Remove(path)
		default:
			return ErrInvalidConfig
		}
	}, nil
}

func replaceCredentialFile(directory, path string, raw []byte) error {
	decoded, err := base64.RawURLEncoding.DecodeString(string(raw[:len(raw)-1]))
	if err != nil || len(decoded) != 32 {
		return ErrInvalidConfig
	}
	for index := range decoded {
		decoded[index] = 0
	}
	file, err := os.CreateTemp(directory, ".credential-sync-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("commit credential sync: %w", err)
	}
	committed = true
	return nil
}
