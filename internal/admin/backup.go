package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"neproto.local/chameleon/internal/credentials"
)

const backupVersion = 1

type backupManifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Domain    string    `json:"domain"`
}

func (m *Manager) CreateBackup() (string, error) {
	root := m.backupRoot()
	if err := ensureDirectory(root); err != nil {
		return "", err
	}
	base := m.now().UTC().Format("20060102T150405.000000000Z")
	path := filepath.Join(root, base)
	for suffix := 1; ; suffix++ {
		err := os.Mkdir(path, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		path = filepath.Join(root, fmt.Sprintf("%s-%02d", base, suffix))
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(path)
		}
	}()

	files := []struct{ source, relative string }{
		{m.installationPath(), filepath.Join("etc", "neproto", "installation.json")},
		{filepath.Join(m.root, "etc", "neproto", "server.json"), filepath.Join("etc", "neproto", "server.json")},
		{filepath.Join(m.root, "etc", "caddy", "Caddyfile"), filepath.Join("etc", "caddy", "Caddyfile")},
	}
	for _, file := range files {
		if err := copyBackupFile(file.source, filepath.Join(path, file.relative)); err != nil {
			return "", err
		}
	}
	if _, err := os.Lstat(m.indexPath()); err == nil {
		if err := copyBackupFile(m.indexPath(), filepath.Join(path, "etc", "neproto", "users", "index.json")); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, name := range []string{"active", "revoked"} {
		source := filepath.Join(m.root, "etc", "neproto", "users", name)
		destination := filepath.Join(path, "etc", "neproto", "users", name)
		if err := copyCredentialFiles(source, destination); err != nil {
			return "", err
		}
	}
	manifestRaw, err := json.MarshalIndent(backupManifest{
		Version: backupVersion, CreatedAt: m.now().UTC(), Domain: m.installation.Domain,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeNewFile(filepath.Join(path, "manifest.json"), append(manifestRaw, '\n'), 0o600); err != nil {
		return "", err
	}
	if err := syncDirectory(path); err != nil {
		return "", err
	}
	success = true
	return path, nil
}

func (m *Manager) ListBackups() ([]string, error) {
	root := m.backupRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(root, entry.Name())
		var manifest backupManifest
		if err := readStrictJSON(filepath.Join(path, "manifest.json"), &manifest); err != nil ||
			manifest.Version != backupVersion || manifest.CreatedAt.IsZero() {
			continue
		}
		result = append(result, path)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(result)))
	return result, nil
}

// RestoreBackup creates a recovery backup of the current state before
// restoring the selected root-owned snapshot. The returned path can be used
// to undo the restore.
func (m *Manager) RestoreBackup(path string) (string, error) {
	validatedPath, candidate, err := m.validateBackupPath(path)
	if err != nil {
		return "", err
	}
	recovery, err := m.CreateBackup()
	if err != nil {
		return "", fmt.Errorf("create pre-restore backup: %w", err)
	}
	if err := m.restoreBackupFiles(validatedPath, candidate); err != nil {
		_, recoveryCandidate, recoveryErr := m.validateBackupPath(recovery)
		if recoveryErr == nil {
			recoveryErr = m.restoreBackupFiles(recovery, recoveryCandidate)
		}
		return recovery, fmt.Errorf("restore backup: %w", errors.Join(err, recoveryErr))
	}
	return recovery, nil
}

func (m *Manager) validateBackupPath(path string) (string, Installation, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", Installation{}, ErrInvalidState
	}
	root := filepath.Clean(m.backupRoot())
	absolute = filepath.Clean(absolute)
	if filepath.Dir(absolute) != root {
		return "", Installation{}, fmt.Errorf("%w: backup must be a direct child of %s", ErrInvalidState, root)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", Installation{}, ErrInvalidState
	}
	var manifest backupManifest
	if err := readStrictJSON(filepath.Join(absolute, "manifest.json"), &manifest); err != nil ||
		manifest.Version != backupVersion || manifest.CreatedAt.IsZero() {
		return "", Installation{}, ErrInvalidState
	}
	var installation Installation
	if err := readStrictJSON(filepath.Join(absolute, "etc", "neproto", "installation.json"), &installation); err != nil {
		return "", Installation{}, err
	}
	if err := validateInstallation(installation); err != nil || installation.Domain != manifest.Domain {
		return "", Installation{}, ErrInvalidState
	}
	serverRaw, err := readRegularFile(filepath.Join(absolute, "etc", "neproto", "server.json"))
	if err != nil {
		return "", Installation{}, err
	}
	if _, err := replaceServerIdentity(serverRaw, installation.Domain, installation.Domain); err != nil {
		return "", Installation{}, err
	}
	caddyRaw, err := readRegularFile(filepath.Join(absolute, "etc", "caddy", "Caddyfile"))
	if err != nil {
		return "", Installation{}, err
	}
	if _, err := replaceCaddyDomain(caddyRaw, installation.Domain, installation.Domain); err != nil {
		return "", Installation{}, err
	}
	return absolute, installation, nil
}

func (m *Manager) restoreBackupFiles(path string, installation Installation) error {
	pairs := []struct{ backup, current string }{
		{filepath.Join(path, "etc", "neproto", "installation.json"), m.installationPath()},
		{filepath.Join(path, "etc", "neproto", "server.json"), filepath.Join(m.root, "etc", "neproto", "server.json")},
		{filepath.Join(path, "etc", "caddy", "Caddyfile"), filepath.Join(m.root, "etc", "caddy", "Caddyfile")},
	}
	for _, pair := range pairs {
		raw, err := readRegularFile(pair.backup)
		if err != nil {
			return err
		}
		if err := replaceRegularFile(pair.current, raw); err != nil {
			return err
		}
	}

	backupIndex := filepath.Join(path, "etc", "neproto", "users", "index.json")
	if raw, err := readRegularFile(backupIndex); err == nil {
		if _, statErr := os.Lstat(m.indexPath()); statErr == nil {
			if err := replaceRegularFile(m.indexPath(), raw); err != nil {
				return err
			}
		} else if errors.Is(statErr, os.ErrNotExist) {
			if err := writeNewFile(m.indexPath(), raw, 0o600); err != nil {
				return err
			}
		} else {
			return statErr
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Remove(m.indexPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		return err
	}

	for _, name := range []string{"active", "revoked"} {
		current := filepath.Join(m.root, "etc", "neproto", "users", name)
		backup := filepath.Join(path, "etc", "neproto", "users", name)
		if err := replaceCredentialDirectory(backup, current); err != nil {
			return err
		}
	}
	m.installation = installation
	if _, err := m.loadIndex(); err != nil {
		return err
	}
	loaded, err := credentials.LoadActiveDirectory(m.activeDirectory())
	if err != nil {
		return err
	}
	users, err := m.ListUsers()
	if err != nil {
		return err
	}
	active := 0
	for _, user := range users {
		if user.Status == StatusActive {
			active++
		}
	}
	if len(loaded) != active {
		return fmt.Errorf("%w: active credential count mismatch", ErrInvalidState)
	}
	for _, credential := range loaded {
		if err := m.secureCredential(m.activeSecretPath(credential.ID)); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) backupRoot() string {
	return filepath.Join(m.root, "var", "backups", "neproto")
}

func copyBackupFile(source, destination string) error {
	raw, err := readRegularFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	return writeNewFile(destination, raw, 0o600)
}

func copyCredentialFiles(source, destination string) error {
	entries, err := safeCredentialEntries(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyBackupFile(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func replaceCredentialDirectory(backup, current string) error {
	backupEntries, err := safeCredentialEntries(backup)
	if err != nil {
		return err
	}
	currentEntries, err := safeCredentialEntries(current)
	if err != nil {
		return err
	}
	for _, entry := range currentEntries {
		if err := os.Remove(filepath.Join(current, entry.Name())); err != nil {
			return err
		}
	}
	for _, entry := range backupEntries {
		raw, err := readRegularFile(filepath.Join(backup, entry.Name()))
		if err != nil {
			return err
		}
		if err := writeNewFile(filepath.Join(current, entry.Name()), raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func safeCredentialEntries(directory string) ([]os.DirEntry, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidState
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			!strings.HasSuffix(entry.Name(), ".secret") {
			return nil, ErrInvalidState
		}
	}
	return entries, nil
}
