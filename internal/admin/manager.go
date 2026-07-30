package admin

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/credentials"
	"neproto.local/chameleon/internal/onboarding"
)

const (
	ModeBareMetal = "bare-metal"
	ModeDocker    = "docker"

	StatusActive  = "active"
	StatusRevoked = "revoked"

	stateVersion   = 1
	maxStateBytes  = 1 << 20
	MaxUserDevices = 16
)

var (
	ErrInvalidState      = errors.New("invalid NP/2 installation state")
	ErrInvalidUser       = errors.New("invalid NP/2 user")
	ErrUserNotFound      = errors.New("NP/2 user not found")
	ErrUserMustBeRevoked = errors.New("NP/2 user must be revoked before deletion")
)

// Installation is the non-secret state written by the deployment installer.
// Secret credentials live only in users/active and users/revoked.
type Installation struct {
	Version              int      `json:"version"`
	Mode                 string   `json:"mode"`
	Domain               string   `json:"domain"`
	ServerAddresses      []string `json:"server_addresses"`
	HTTPSPath            string   `json:"https_path"`
	WebRTCPath           string   `json:"webrtc_path"`
	HTTP3Path            string   `json:"http3_path,omitempty"`
	RequireDatagrams     bool     `json:"require_datagrams,omitempty"`
	EnableConstellation  bool     `json:"enable_constellation,omitempty"`
	EnableForwardSecrecy bool     `json:"enable_forward_secrecy,omitempty"`
	WebEnabled           bool     `json:"web_enabled,omitempty"`
	WebDomain            string   `json:"web_domain,omitempty"`
	WebPort              int      `json:"web_port,omitempty"`
	ServiceUID           *int     `json:"service_uid,omitempty"`
	ServiceGID           *int     `json:"service_gid,omitempty"`
}

type User struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name"`
	Profile                string     `json:"profile"`
	Status                 string     `json:"status"`
	CreatedAt              time.Time  `json:"created_at"`
	RotatedAt              *time.Time `json:"rotated_at,omitempty"`
	RevokedAt              *time.Time `json:"revoked_at,omitempty"`
	MaxDevices             int        `json:"max_devices"`
	TrafficResetGeneration uint64     `json:"traffic_reset_generation,omitempty"`
	TrafficResetAt         *time.Time `json:"traffic_reset_at,omitempty"`
}

type userIndex struct {
	Version int    `json:"version"`
	Users   []User `json:"users"`
}

type Manager struct {
	root         string
	installation Installation
	random       io.Reader
	now          func() time.Time
}

func Open(root string, random io.Reader, now func() time.Time) (*Manager, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: empty root", ErrInvalidState)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve root", ErrInvalidState)
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	manager := &Manager{root: filepath.Clean(absoluteRoot), random: random, now: now}
	if err := readStrictJSON(manager.installationPath(), &manager.installation); err != nil {
		return nil, err
	}
	if err := validateInstallation(manager.installation); err != nil {
		return nil, err
	}
	if err := ensureDirectory(manager.activeDirectory()); err != nil {
		return nil, err
	}
	if err := manager.secureActiveDirectory(); err != nil {
		return nil, err
	}
	if err := ensureDirectory(manager.revokedDirectory()); err != nil {
		return nil, err
	}
	if err := manager.secureServiceParents(); err != nil {
		return nil, err
	}
	if _, err := manager.loadIndex(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Installation() Installation {
	copy := m.installation
	copy.ServerAddresses = append([]string(nil), m.installation.ServerAddresses...)
	return copy
}

// ServerBundlePath returns the root-only bundle retained by install.sh for
// cluster node enrolment.
func (m *Manager) ServerBundlePath() string {
	return filepath.Join(m.root, "opt", "neproto", "neproto-server-bundle.tar.gz")
}

func (m *Manager) ServerConfigPath() string {
	return filepath.Join(m.root, "etc", "neproto", "server.json")
}

func (m *Manager) GeodataDirectory() string {
	return filepath.Join(m.root, "etc", "neproto", "geodata")
}

func (m *Manager) UsageStatePath() string {
	return filepath.Join(m.root, "var", "lib", "neproto", "usage", "state.json")
}

func (m *Manager) RootDirectory() string {
	if m == nil {
		return ""
	}
	return m.root
}

func (m *Manager) ActiveCredentialSecret(identifier string) (string, error) {
	if !validIdentifier(identifier) {
		return "", ErrInvalidUser
	}
	loaded, err := credentials.LoadActiveDirectory(m.activeDirectory())
	if err != nil {
		return "", err
	}
	for _, credential := range loaded {
		if credential.ID == identifier {
			return base64.RawURLEncoding.EncodeToString(credential.Secret[:]), nil
		}
	}
	return "", ErrUserNotFound
}

// SetDomain atomically updates the public identity used by future client
// exports, the NP/2 server configuration, and the Caddy site label. Callers
// must create an external backup and validate/restart services before treating
// the change as committed operationally.
func (m *Manager) SetDomain(domain string, addresses []string) error {
	candidate := m.Installation()
	candidate.Domain = domain
	candidate.ServerAddresses = append([]string(nil), addresses...)
	if err := validateInstallation(candidate); err != nil {
		return fmt.Errorf("%w: invalid domain or server addresses", ErrInvalidState)
	}

	installationPath := m.installationPath()
	serverPath := filepath.Join(m.root, "etc", "neproto", "server.json")
	caddyPath := filepath.Join(m.root, "etc", "caddy", "Caddyfile")
	paths := []string{installationPath, serverPath, caddyPath}
	previous := make([][]byte, len(paths))
	for position, path := range paths {
		raw, err := readRegularFile(path)
		if err != nil {
			return fmt.Errorf("read domain configuration: %w", err)
		}
		previous[position] = raw
	}

	installationRaw, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return err
	}
	installationRaw = append(installationRaw, '\n')
	serverRaw, err := replaceServerIdentity(previous[1], m.installation.Domain, domain)
	if err != nil {
		return err
	}
	caddyRaw, err := replaceCaddyDomain(previous[2], m.installation.Domain, domain)
	if err != nil {
		return err
	}
	next := [][]byte{installationRaw, serverRaw, caddyRaw}

	applied := 0
	for position, path := range paths {
		if err := replaceRegularFile(path, next[position]); err != nil {
			rollbackErrors := []error{err}
			for rollback := applied - 1; rollback >= 0; rollback-- {
				rollbackErrors = append(rollbackErrors, replaceRegularFile(paths[rollback], previous[rollback]))
			}
			return fmt.Errorf("update domain configuration: %w", errors.Join(rollbackErrors...))
		}
		applied++
	}
	m.installation = candidate
	return nil
}

// SetFeatures atomically updates the non-secret installation state and the
// server runtime configuration. Service validation and restart are deliberately
// owned by neprotoctl so a failed rollout can restore its pre-change backup.
func (m *Manager) SetFeatures(enableConstellation, enableForwardSecrecy bool) error {
	if m == nil {
		return ErrInvalidState
	}
	candidate := m.Installation()
	candidate.EnableConstellation = enableConstellation
	candidate.EnableForwardSecrecy = enableForwardSecrecy
	if err := validateInstallation(candidate); err != nil {
		return fmt.Errorf("%w: invalid feature policy", ErrInvalidState)
	}
	paths := []string{
		m.installationPath(),
		filepath.Join(m.root, "etc", "neproto", "server.json"),
	}
	previous := make([][]byte, len(paths))
	for position, path := range paths {
		raw, err := readRegularFile(path)
		if err != nil {
			return fmt.Errorf("read feature configuration: %w", err)
		}
		previous[position] = raw
	}
	installationRaw, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return err
	}
	installationRaw = append(installationRaw, '\n')
	serverRaw, err := replaceServerFeatures(
		previous[1], m.installation.EnableConstellation, m.installation.EnableForwardSecrecy,
		enableConstellation, enableForwardSecrecy,
	)
	if err != nil {
		return err
	}
	next := [][]byte{installationRaw, serverRaw}
	applied := 0
	for position, path := range paths {
		if err := replaceRegularFile(path, next[position]); err != nil {
			rollbackErrors := []error{err}
			for rollback := applied - 1; rollback >= 0; rollback-- {
				rollbackErrors = append(rollbackErrors, replaceRegularFile(paths[rollback], previous[rollback]))
			}
			return fmt.Errorf("update feature configuration: %w", errors.Join(rollbackErrors...))
		}
		applied++
	}
	m.installation = candidate
	return nil
}

func replaceServerFeatures(
	raw []byte,
	oldConstellation, oldForwardSecrecy bool,
	newConstellation, newForwardSecrecy bool,
) ([]byte, error) {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("%w: decode server configuration", ErrInvalidState)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing server configuration", ErrInvalidState)
	}
	readBool := func(name string) (bool, error) {
		value, exists := object[name]
		if !exists {
			return false, nil
		}
		var decoded bool
		if err := json.Unmarshal(value, &decoded); err != nil {
			return false, err
		}
		return decoded, nil
	}
	currentConstellation, err := readBool("enable_constellation")
	if err != nil {
		return nil, ErrInvalidState
	}
	currentForwardSecrecy, err := readBool("enable_forward_secrecy")
	if err != nil || currentConstellation != oldConstellation ||
		currentForwardSecrecy != oldForwardSecrecy {
		return nil, fmt.Errorf("%w: server feature policy does not match installation", ErrInvalidState)
	}
	object["enable_constellation"] = json.RawMessage(strconv.FormatBool(newConstellation))
	object["enable_forward_secrecy"] = json.RawMessage(strconv.FormatBool(newForwardSecrecy))
	updated, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(updated, '\n'), nil
}

func replaceServerIdentity(raw []byte, oldDomain, newDomain string) ([]byte, error) {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("%w: decode server configuration", ErrInvalidState)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing server configuration", ErrInvalidState)
	}
	var current string
	if err := json.Unmarshal(object["server_identity"], &current); err != nil || current != oldDomain {
		return nil, fmt.Errorf("%w: server identity does not match installation", ErrInvalidState)
	}
	encoded, _ := json.Marshal(newDomain)
	object["server_identity"] = encoded
	updated, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(updated, '\n'), nil
}

func replaceCaddyDomain(raw []byte, oldDomain, newDomain string) ([]byte, error) {
	oldLabel := oldDomain + " {"
	if bytes.Count(raw, []byte(oldLabel)) != 1 {
		return nil, fmt.Errorf("%w: Caddy site label does not match installation", ErrInvalidState)
	}
	return bytes.Replace(raw, []byte(oldLabel), []byte(newDomain+" {"), 1), nil
}

func (m *Manager) AddUser(name, profile string) (User, error) {
	if !validUserName(name) || !validProfile(profile) {
		return User{}, ErrInvalidUser
	}
	index, err := m.loadIndex()
	if err != nil {
		return User{}, err
	}
	for _, existing := range index.Users {
		if strings.EqualFold(existing.Name, name) {
			return User{}, fmt.Errorf("%w: name already exists", ErrInvalidUser)
		}
	}

	identifier, err := m.newIdentifier(index.Users)
	if err != nil {
		return User{}, err
	}
	secret, err := m.newSecret()
	if err != nil {
		return User{}, err
	}
	secretPath := m.activeSecretPath(identifier)
	if err := writeNewFile(secretPath, []byte(base64.RawURLEncoding.EncodeToString(secret)+"\n"), 0o600); err != nil {
		return User{}, fmt.Errorf("create user credential: %w", err)
	}
	if err := m.secureCredential(secretPath); err != nil {
		_ = os.Remove(secretPath)
		return User{}, err
	}

	created := m.now().UTC()
	user := User{
		ID: identifier, Name: name, Profile: profile, Status: StatusActive, CreatedAt: created,
	}
	index.Users = append(index.Users, user)
	sortUsers(index.Users)
	if err := m.saveIndex(index); err != nil {
		_ = os.Remove(secretPath)
		return User{}, err
	}
	return user, nil
}

func (m *Manager) ListUsers() ([]User, error) {
	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}
	users := append([]User(nil), index.Users...)
	sortUsers(users)
	return users, nil
}

func (m *Manager) SetUserDeviceLimit(identifier string, maximum int) (User, error) {
	if !validIdentifier(identifier) || maximum < 0 || maximum > MaxUserDevices {
		return User{}, ErrInvalidUser
	}
	index, err := m.loadIndex()
	if err != nil {
		return User{}, err
	}
	position := findUserPosition(index.Users, identifier)
	if position < 0 {
		return User{}, ErrUserNotFound
	}
	index.Users[position].MaxDevices = maximum
	if err := m.saveIndex(index); err != nil {
		return User{}, err
	}
	return index.Users[position], nil
}

func (m *Manager) ResetUserTraffic(identifier string) (User, error) {
	if !validIdentifier(identifier) {
		return User{}, ErrInvalidUser
	}
	index, err := m.loadIndex()
	if err != nil {
		return User{}, err
	}
	position := findUserPosition(index.Users, identifier)
	if position < 0 {
		return User{}, ErrUserNotFound
	}
	if index.Users[position].TrafficResetGeneration == ^uint64(0) {
		return User{}, ErrInvalidState
	}
	resetAt := m.now().UTC()
	index.Users[position].TrafficResetGeneration++
	index.Users[position].TrafficResetAt = &resetAt
	if err := m.saveIndex(index); err != nil {
		return User{}, err
	}
	return index.Users[position], nil
}

func (m *Manager) ExportUserProfile(identifier string) (onboarding.Profile, error) {
	if !validIdentifier(identifier) {
		return onboarding.Profile{}, ErrInvalidUser
	}
	index, err := m.loadIndex()
	if err != nil {
		return onboarding.Profile{}, err
	}
	user, ok := findUser(index.Users, identifier)
	if !ok || user.Status != StatusActive {
		return onboarding.Profile{}, ErrUserNotFound
	}
	loaded, err := credentials.LoadActiveDirectory(m.activeDirectory())
	if err != nil {
		return onboarding.Profile{}, fmt.Errorf("load active credential: %w", err)
	}
	var encodedSecret string
	for _, credential := range loaded {
		if credential.ID == identifier {
			encodedSecret = base64.RawURLEncoding.EncodeToString(credential.Secret[:])
			break
		}
	}
	if encodedSecret == "" {
		return onboarding.Profile{}, ErrUserNotFound
	}
	clusterID, catalogPublicKey, err := m.ClusterBootstrap()
	if err != nil {
		return onboarding.Profile{}, fmt.Errorf("load cluster bootstrap: %w", err)
	}
	onboardingVersion := 1
	maxParallelCarriers := 0
	if m.installation.HTTP3Path != "" {
		onboardingVersion = 2
		maxParallelCarriers = 3
	}
	return onboarding.Profile{
		Version:              onboardingVersion,
		CredentialID:         user.ID,
		Name:                 user.Name,
		ServerIdentity:       m.installation.Domain,
		ServerAddresses:      append([]string(nil), m.installation.ServerAddresses...),
		HTTPSPath:            m.installation.HTTPSPath,
		WebRTCPath:           m.installation.WebRTCPath,
		HTTP3Path:            m.installation.HTTP3Path,
		RequireDatagrams:     m.installation.RequireDatagrams,
		MaxParallelCarriers:  maxParallelCarriers,
		EnableConstellation:  m.installation.EnableConstellation,
		EnableForwardSecrecy: m.installation.EnableForwardSecrecy,
		ClusterID:            clusterID,
		CatalogPublicKey:     catalogPublicKey,
		Profile:              user.Profile,
		Secret:               encodedSecret,
	}, nil
}

func (m *Manager) ExportUserURI(identifier string) (string, error) {
	profile, err := m.ExportUserProfile(identifier)
	if err != nil {
		return "", err
	}
	return onboarding.EncodeURI(profile)
}

func (m *Manager) RotateUser(identifier string) error {
	if !validIdentifier(identifier) {
		return ErrInvalidUser
	}
	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	userPosition := findUserPosition(index.Users, identifier)
	if userPosition < 0 || index.Users[userPosition].Status != StatusActive {
		return ErrUserNotFound
	}
	secret, err := m.newSecret()
	if err != nil {
		return err
	}
	activePath := m.activeSecretPath(identifier)
	if info, err := os.Lstat(activePath); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("active credential unavailable: %w", ErrUserNotFound)
	}
	archivePath, err := m.uniqueRevokedPath(identifier + "-rotated")
	if err != nil {
		return err
	}
	temporaryPath, err := writeTemporary(m.activeDirectory(), ".rotate-*", []byte(base64.RawURLEncoding.EncodeToString(secret)+"\n"), 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(temporaryPath)
	if err := m.secureCredential(temporaryPath); err != nil {
		return err
	}
	if err := os.Rename(activePath, archivePath); err != nil {
		return fmt.Errorf("archive old credential: %w", err)
	}
	if err := os.Rename(temporaryPath, activePath); err != nil {
		_ = os.Rename(archivePath, activePath)
		return fmt.Errorf("activate rotated credential: %w", err)
	}
	rotated := m.now().UTC()
	index.Users[userPosition].RotatedAt = &rotated
	if err := m.saveIndex(index); err != nil {
		_ = os.Remove(activePath)
		_ = os.Rename(archivePath, activePath)
		return err
	}
	return nil
}

func (m *Manager) RevokeUser(identifier string) error {
	if !validIdentifier(identifier) {
		return ErrInvalidUser
	}
	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	userPosition := findUserPosition(index.Users, identifier)
	if userPosition < 0 || index.Users[userPosition].Status != StatusActive {
		return ErrUserNotFound
	}
	activePath := m.activeSecretPath(identifier)
	revokedPath, err := m.uniqueRevokedPath(identifier)
	if err != nil {
		return err
	}
	if err := os.Rename(activePath, revokedPath); err != nil {
		return fmt.Errorf("revoke credential: %w", err)
	}
	revoked := m.now().UTC()
	index.Users[userPosition].Status = StatusRevoked
	index.Users[userPosition].RevokedAt = &revoked
	if err := m.saveIndex(index); err != nil {
		_ = os.Rename(revokedPath, activePath)
		return err
	}
	return nil
}

// DeleteUser permanently removes a revoked user, all archived credentials and
// its cluster access. Active users must be revoked explicitly first so a
// mistaken key press cannot invalidate a live client without an audit step.
func (m *Manager) DeleteUser(identifier string) error {
	if !validIdentifier(identifier) {
		return ErrInvalidUser
	}
	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	position := findUserPosition(index.Users, identifier)
	if position < 0 {
		return ErrUserNotFound
	}
	if index.Users[position].Status != StatusRevoked {
		return ErrUserMustBeRevoked
	}

	entries, err := os.ReadDir(m.revokedDirectory())
	if err != nil {
		return fmt.Errorf("list revoked credentials: %w", err)
	}
	quarantine, err := os.MkdirTemp(m.revokedDirectory(), ".delete-"+identifier+"-")
	if err != nil {
		return fmt.Errorf("prepare credential deletion: %w", err)
	}
	if err := os.Chmod(quarantine, 0o700); err != nil {
		_ = os.RemoveAll(quarantine)
		return fmt.Errorf("secure credential deletion: %w", err)
	}
	moved := make([]string, 0)
	rollbackFiles := func() {
		for movedIndex := len(moved) - 1; movedIndex >= 0; movedIndex-- {
			name := moved[movedIndex]
			_ = os.Rename(filepath.Join(quarantine, name), filepath.Join(m.revokedDirectory(), name))
		}
		_ = os.RemoveAll(quarantine)
	}
	for _, entry := range entries {
		name := entry.Name()
		belongsToUser := name == identifier+".secret" ||
			(strings.HasPrefix(name, identifier+"-") && strings.HasSuffix(name, ".secret"))
		if !belongsToUser {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			rollbackFiles()
			return fmt.Errorf("archive credential is not a regular file: %w", ErrInvalidState)
		}
		if err := os.Rename(filepath.Join(m.revokedDirectory(), name), filepath.Join(quarantine, name)); err != nil {
			rollbackFiles()
			return fmt.Errorf("stage credential deletion: %w", err)
		}
		moved = append(moved, name)
	}

	previousIndex := index
	previousIndex.Users = append([]User(nil), index.Users...)
	index.Users = append(index.Users[:position], index.Users[position+1:]...)
	if err := m.saveIndex(index); err != nil {
		rollbackFiles()
		return err
	}
	if _, err := m.RemoveClusterUserAccess(identifier); err != nil &&
		!errors.Is(err, ErrUserNotFound) && !errors.Is(err, cluster.ErrStateNotFound) {
		rollbackErr := m.saveIndex(previousIndex)
		rollbackFiles()
		return fmt.Errorf("remove cluster access: %w", errors.Join(err, rollbackErr))
	}
	if err := os.RemoveAll(quarantine); err != nil {
		return fmt.Errorf("erase revoked credentials: %w", err)
	}
	return nil
}

func (m *Manager) loadIndex() (userIndex, error) {
	var index userIndex
	err := readStrictJSON(m.indexPath(), &index)
	if errors.Is(err, os.ErrNotExist) {
		return userIndex{Version: stateVersion, Users: []User{}}, nil
	}
	if err != nil {
		return userIndex{}, err
	}
	if index.Version != stateVersion || index.Users == nil {
		return userIndex{}, ErrInvalidState
	}
	identifiers := make(map[string]struct{}, len(index.Users))
	names := make(map[string]struct{}, len(index.Users))
	for _, user := range index.Users {
		if !validIdentifier(user.ID) || !validUserName(user.Name) || !validProfile(user.Profile) ||
			user.MaxDevices < 0 || user.MaxDevices > MaxUserDevices ||
			(user.Status != StatusActive && user.Status != StatusRevoked) || user.CreatedAt.IsZero() ||
			(user.Status == StatusActive && user.RevokedAt != nil) ||
			(user.Status == StatusRevoked && user.RevokedAt == nil) {
			return userIndex{}, ErrInvalidState
		}
		if _, duplicate := identifiers[user.ID]; duplicate {
			return userIndex{}, ErrInvalidState
		}
		identifiers[user.ID] = struct{}{}
		foldedName := strings.ToLower(user.Name)
		if _, duplicate := names[foldedName]; duplicate {
			return userIndex{}, ErrInvalidState
		}
		names[foldedName] = struct{}{}
	}
	return index, nil
}

func (m *Manager) saveIndex(index userIndex) error {
	index.Version = stateVersion
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode user index: %w", err)
	}
	raw = append(raw, '\n')
	if err := replaceFile(m.indexPath(), raw, 0o600); err != nil {
		return fmt.Errorf("save user index: %w", err)
	}
	if err := m.secureServiceParents(); err != nil {
		return err
	}
	if err := m.secureIndexFile(); err != nil {
		return err
	}
	return nil
}

func (m *Manager) newIdentifier(users []User) (string, error) {
	existing := make(map[string]struct{}, len(users))
	for _, user := range users {
		existing[user.ID] = struct{}{}
	}
	for attempt := 0; attempt < 8; attempt++ {
		raw := make([]byte, credentials.IDSize)
		if _, err := io.ReadFull(m.random, raw); err != nil {
			return "", fmt.Errorf("generate credential ID: %w", err)
		}
		if allZero(raw) {
			continue
		}
		identifier := base64.RawURLEncoding.EncodeToString(raw)
		if _, duplicate := existing[identifier]; !duplicate {
			return identifier, nil
		}
	}
	return "", errors.New("generate unique credential ID")
}

func (m *Manager) newSecret() ([]byte, error) {
	for attempt := 0; attempt < 8; attempt++ {
		secret := make([]byte, 32)
		if _, err := io.ReadFull(m.random, secret); err != nil {
			return nil, fmt.Errorf("generate credential: %w", err)
		}
		if !allZero(secret) {
			return secret, nil
		}
	}
	return nil, errors.New("generate non-zero credential")
}

func (m *Manager) uniqueRevokedPath(prefix string) (string, error) {
	for suffix := 0; suffix < 1000; suffix++ {
		name := prefix + ".secret"
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d.secret", prefix, suffix)
		}
		candidate := filepath.Join(m.revokedDirectory(), name)
		_, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("revoked credential archive is full")
}

func (m *Manager) installationPath() string {
	return filepath.Join(m.root, "etc", "neproto", "installation.json")
}

func (m *Manager) indexPath() string {
	return filepath.Join(m.root, "etc", "neproto", "users", "index.json")
}

func (m *Manager) activeDirectory() string {
	return filepath.Join(m.root, "etc", "neproto", "users", "active")
}

func (m *Manager) revokedDirectory() string {
	return filepath.Join(m.root, "etc", "neproto", "users", "revoked")
}

func (m *Manager) activeSecretPath(identifier string) string {
	return filepath.Join(m.activeDirectory(), identifier+".secret")
}

func validateInstallation(installation Installation) error {
	if installation.Version != stateVersion ||
		(installation.Mode != ModeBareMetal && installation.Mode != ModeDocker) ||
		(installation.ServiceUID == nil) != (installation.ServiceGID == nil) ||
		(installation.ServiceUID != nil && (*installation.ServiceUID < 1 || *installation.ServiceGID < 1)) {
		return ErrInvalidState
	}
	if installation.WebEnabled {
		if !validWebPort(installation.WebPort) ||
			(installation.WebDomain != "" &&
				(!validDNSDomain(installation.WebDomain) || installation.WebDomain == installation.Domain)) {
			return ErrInvalidState
		}
	} else if installation.WebDomain != "" || installation.WebPort != 0 {
		return ErrInvalidState
	}
	dummyID := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, onboarding.IDSize))
	dummySecret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	onboardingVersion := 1
	maxParallelCarriers := 0
	if installation.HTTP3Path != "" {
		onboardingVersion = 2
		maxParallelCarriers = 3
	}
	_, err := onboarding.EncodeURI(onboarding.Profile{
		Version: onboardingVersion, CredentialID: dummyID, Name: "validation",
		ServerIdentity: installation.Domain, ServerAddresses: installation.ServerAddresses,
		HTTPSPath: installation.HTTPSPath, WebRTCPath: installation.WebRTCPath,
		HTTP3Path: installation.HTTP3Path, RequireDatagrams: installation.RequireDatagrams,
		MaxParallelCarriers:  maxParallelCarriers,
		EnableConstellation:  installation.EnableConstellation,
		EnableForwardSecrecy: installation.EnableForwardSecrecy,
		Profile:              "web", Secret: dummySecret,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidState, err)
	}
	return nil
}

func validWebPort(port int) bool {
	return port >= 1024 && port <= 65535 && port != 9080 && port != 9464 && (port < 40000 || port > 40100)
}

func validDNSDomain(domain string) bool {
	if domain == "" || len(domain) > 253 || domain != strings.ToLower(domain) ||
		!strings.Contains(domain, ".") || strings.Contains(domain, "..") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func (m *Manager) secureActiveDirectory() error {
	if m.installation.ServiceUID == nil {
		return nil
	}
	if err := os.Chown(m.activeDirectory(), *m.installation.ServiceUID, *m.installation.ServiceGID); err != nil {
		return fmt.Errorf("set credential directory ownership: %w", err)
	}
	if err := os.Chmod(m.activeDirectory(), 0o700); err != nil {
		return fmt.Errorf("set credential directory permissions: %w", err)
	}
	return nil
}

func (m *Manager) secureServiceParents() error {
	if m.installation.ServiceUID == nil {
		return nil
	}
	directories := []struct {
		path string
		mode os.FileMode
	}{
		{path: filepath.Join(m.root, "etc", "neproto"), mode: 0o750},
		{path: filepath.Join(m.root, "etc", "neproto", "users"), mode: 0o710},
	}
	for _, directory := range directories {
		info, err := os.Lstat(directory.path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: service state parent is not a directory", ErrInvalidState)
		}
		if err := os.Chown(directory.path, 0, *m.installation.ServiceGID); err != nil {
			return fmt.Errorf("set service state ownership: %w", err)
		}
		if err := os.Chmod(directory.path, directory.mode); err != nil {
			return fmt.Errorf("set service state permissions: %w", err)
		}
	}
	return nil
}

func (m *Manager) secureCredential(path string) error {
	if m.installation.ServiceUID == nil {
		return nil
	}
	if err := os.Chown(path, *m.installation.ServiceUID, *m.installation.ServiceGID); err != nil {
		return fmt.Errorf("set credential ownership: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set credential permissions: %w", err)
	}
	return nil
}

func (m *Manager) secureIndexFile() error {
	if m.installation.ServiceUID == nil {
		return nil
	}
	path := m.indexPath()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: user index is not a regular file", ErrInvalidState)
	}
	if err := os.Chown(path, 0, *m.installation.ServiceGID); err != nil {
		return fmt.Errorf("set user index ownership: %w", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		return fmt.Errorf("set user index permissions: %w", err)
	}
	return nil
}

func validUserName(value string) bool {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		strings.ContainsAny(value, "/\\") || value == "." || value == ".." {
		return false
	}
	count := utf8.RuneCountInString(value)
	if count < 1 || count > 64 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validProfile(value string) bool {
	return value == "quiet" || value == "web" || value == "interactive"
}

func validIdentifier(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == credentials.IDSize &&
		base64.RawURLEncoding.EncodeToString(raw) == value && !allZero(raw)
}

func findUser(users []User, identifier string) (User, bool) {
	position := findUserPosition(users, identifier)
	if position < 0 {
		return User{}, false
	}
	return users[position], true
}

func findUserPosition(users []User, identifier string) int {
	for position := range users {
		if users[position].ID == identifier {
			return position
		}
	}
	return -1
}

func sortUsers(users []User) {
	sort.Slice(users, func(left, right int) bool {
		if users[left].Name == users[right].Name {
			return users[left].ID < users[right].ID
		}
		return strings.ToLower(users[left].Name) < strings.ToLower(users[right].Name)
	})
}

func ensureDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: state path is not a directory", ErrInvalidState)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure state directory: %w", err)
	}
	return nil
}

func writeNewFile(path string, raw []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	name := file.Name()
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(name)
		}
	}()
	if err := file.Chmod(mode); err != nil {
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
	success = true
	return nil
}

func writeTemporary(directory, pattern string, raw []byte, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(name)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return "", err
	}
	if _, err := file.Write(raw); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	success = true
	return name, nil
}

func replaceFile(path string, raw []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := ensureDirectory(directory); err != nil {
		return err
	}
	temporary, err := writeTemporary(directory, ".state-*", raw, mode)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, path); err == nil {
		return syncDirectory(directory)
	}
	// Windows does not replace an existing destination with os.Rename. The
	// production target is Linux, but keep tests and offline tooling reliable.
	backup := path + ".previous"
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Rename(backup, path)
		return err
	}
	_ = os.Remove(backup)
	return syncDirectory(directory)
}

func syncDirectory(directory string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return err
	}
	return nil
}

func readStrictJSON(path string, destination any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidState
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil || len(raw) > maxStateBytes {
		return ErrInvalidState
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode JSON", ErrInvalidState)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON", ErrInvalidState)
	}
	return nil
}

func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidState
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil || len(raw) > maxStateBytes {
		return nil, ErrInvalidState
	}
	return raw, nil
}

func replaceRegularFile(path string, raw []byte) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidState
	}
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidState
	}
	temporary, err := writeTemporary(directory, ".replace-*", raw, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := preserveFileOwnership(info, temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err == nil {
		return syncDirectory(directory)
	}
	backup := path + ".replace-previous"
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Rename(backup, path)
		return err
	}
	_ = os.Remove(backup)
	return syncDirectory(directory)
}

func allZero(value []byte) bool {
	var combined byte
	for _, element := range value {
		combined |= element
	}
	return combined == 0
}
