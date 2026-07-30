package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"neproto.local/chameleon/internal/protocol"
)

const (
	stateVersion             = 1
	maximumStateBytes        = 4 << 20
	maximumPolicyBytes       = 1 << 20
	maximumUsers             = protocol.MaxServerCredentials
	maximumDevicesPerUser    = 64
	maximumConfiguredDevices = 16
	lockAttempts             = 100
)

var (
	ErrInvalidConfig          = errors.New("invalid usage configuration")
	ErrInvalidState           = errors.New("invalid usage state")
	ErrUserInactive           = errors.New("usage user is unavailable")
	ErrDeviceIdentityRequired = errors.New("device identity is required")
	ErrDeviceLimit            = errors.New("device limit reached")
	ErrDeviceNotFound         = errors.New("device not found")
	ErrDeviceOnline           = errors.New("device is online")
	ErrStateBusy              = errors.New("usage state is busy")
)

type Config struct {
	PolicyPath string
	StatePath  string
	Now        func() time.Time
}

type Counters struct {
	UploadBytes   uint64
	DownloadBytes uint64
}

type DeviceSnapshot struct {
	DeviceID       string     `json:"device_id"`
	Online         bool       `json:"online"`
	ActiveSessions uint64     `json:"active_sessions"`
	FirstSeen      time.Time  `json:"first_seen"`
	LastSeen       *time.Time `json:"last_seen,omitempty"`
}

type UserSnapshot struct {
	UserID          string           `json:"user_id"`
	Online          bool             `json:"online"`
	LastSeen        *time.Time       `json:"last_seen,omitempty"`
	ActiveSessions  uint64           `json:"active_sessions"`
	OnlineDevices   int              `json:"online_devices"`
	EnrolledDevices int              `json:"enrolled_devices"`
	UploadBytes     uint64           `json:"upload_bytes"`
	DownloadBytes   uint64           `json:"download_bytes"`
	TotalBytes      uint64           `json:"total_bytes"`
	Devices         []DeviceSnapshot `json:"devices"`
}

type Snapshot struct {
	Version   int            `json:"version"`
	Revision  uint64         `json:"revision"`
	UpdatedAt time.Time      `json:"updated_at"`
	Users     []UserSnapshot `json:"users"`
}

type persistedState struct {
	Version   int             `json:"version"`
	Revision  uint64          `json:"revision"`
	UpdatedAt time.Time       `json:"updated_at"`
	Users     []persistedUser `json:"users"`
}

type persistedUser struct {
	UserID          string            `json:"user_id"`
	ResetGeneration uint64            `json:"reset_generation,omitempty"`
	UploadBytes     uint64            `json:"upload_bytes"`
	DownloadBytes   uint64            `json:"download_bytes"`
	LastSeen        *time.Time        `json:"last_seen,omitempty"`
	ActiveSessions  uint64            `json:"active_sessions"`
	Devices         []persistedDevice `json:"devices"`
}

type persistedDevice struct {
	DeviceID       string     `json:"device_id"`
	FirstSeen      time.Time  `json:"first_seen"`
	LastSeen       *time.Time `json:"last_seen,omitempty"`
	ActiveSessions uint64     `json:"active_sessions"`
}

type policyIndex struct {
	Version int          `json:"version"`
	Users   []policyUser `json:"users"`
}

type policyUser struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name,omitempty"`
	Profile                string     `json:"profile,omitempty"`
	Status                 string     `json:"status"`
	CreatedAt              *time.Time `json:"created_at,omitempty"`
	RotatedAt              *time.Time `json:"rotated_at,omitempty"`
	RevokedAt              *time.Time `json:"revoked_at,omitempty"`
	MaxDevices             int        `json:"max_devices,omitempty"`
	TrafficResetGeneration uint64     `json:"traffic_reset_generation,omitempty"`
	TrafficResetAt         *time.Time `json:"traffic_reset_at,omitempty"`
}

type Tracker struct {
	mu         sync.Mutex
	policyPath string
	statePath  string
	now        func() time.Time
	state      persistedState
	sessions   map[uint64]*trackedSession
	nextID     uint64
}

type trackedSession struct {
	userID     string
	deviceID   string
	read       func() Counters
	last       Counters
	closed     bool
	tracker    *Tracker
	identifier uint64
}

type Session struct{ session *trackedSession }

func New(config Config) (*Tracker, error) {
	if config.PolicyPath == "" || config.StatePath == "" {
		return nil, ErrInvalidConfig
	}
	policyPath, err := filepath.Abs(config.PolicyPath)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	statePath, err := filepath.Abs(config.StatePath)
	if err != nil || filepath.Clean(policyPath) == filepath.Clean(statePath) {
		return nil, ErrInvalidConfig
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if _, err := readPolicies(policyPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return nil, fmt.Errorf("create usage directory: %w", err)
	}
	unlock, err := acquireLock(statePath)
	if err != nil {
		return nil, err
	}
	defer unlock()
	tracker := &Tracker{
		policyPath: filepath.Clean(policyPath), statePath: filepath.Clean(statePath), now: config.Now,
		sessions: make(map[uint64]*trackedSession),
	}
	state, err := readState(statePath)
	if errors.Is(err, os.ErrNotExist) {
		state = persistedState{Version: stateVersion, Users: []persistedUser{}}
	} else if err != nil {
		return nil, err
	}
	tracker.state = state
	for userPosition := range tracker.state.Users {
		tracker.state.Users[userPosition].ActiveSessions = 0
		for devicePosition := range tracker.state.Users[userPosition].Devices {
			tracker.state.Users[userPosition].Devices[devicePosition].ActiveSessions = 0
		}
	}
	if err := tracker.persistLocked(); err != nil {
		return nil, err
	}
	return tracker, nil
}

func (t *Tracker) Admit(userID string, deviceID protocol.DeviceID, read func() Counters) (*Session, error) {
	if t == nil || !validUserID(userID) || read == nil {
		return nil, ErrInvalidConfig
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	unlock, err := acquireLock(t.statePath)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := t.reloadExternalLocked(); err != nil {
		return nil, err
	}
	policies, err := readPolicies(t.policyPath)
	if err != nil {
		return nil, err
	}
	t.pruneDeletedUsersLocked(policies)
	policy, ok := policies[userID]
	if !ok || policy.Status != "active" {
		return nil, ErrUserInactive
	}
	if policy.MaxDevices > 0 && deviceID.IsZero() {
		return nil, ErrDeviceIdentityRequired
	}
	user := t.ensureUserLocked(userID)
	t.applyResetLocked(user, policy.TrafficResetGeneration)
	deviceText := ""
	if !deviceID.IsZero() {
		encoded, _ := deviceID.MarshalText()
		deviceText = string(encoded)
		device := findDevice(user, deviceText)
		if device == nil {
			if policy.MaxDevices > 0 && len(user.Devices) >= policy.MaxDevices {
				return nil, ErrDeviceLimit
			}
			if len(user.Devices) >= maximumDevicesPerUser {
				// Unlimited policy must remain unlimited. Keep the persisted
				// history bounded and count this carrier as an unlabelled user
				// session once the diagnostic device history is full.
				deviceText = ""
			} else {
				now := t.now().UTC()
				user.Devices = append(user.Devices, persistedDevice{DeviceID: deviceText, FirstSeen: now})
				device = &user.Devices[len(user.Devices)-1]
			}
		}
		if device != nil {
			device.ActiveSessions = saturatingAdd(device.ActiveSessions, 1)
		}
	}
	user.ActiveSessions = saturatingAdd(user.ActiveSessions, 1)
	t.nextID++
	if t.nextID == 0 {
		t.nextID++
	}
	tracked := &trackedSession{
		userID: userID, deviceID: deviceText, read: read, last: read(), tracker: t, identifier: t.nextID,
	}
	t.sessions[tracked.identifier] = tracked
	if err := t.persistLocked(); err != nil {
		delete(t.sessions, tracked.identifier)
		decrementUserSession(user, deviceText, t.now().UTC())
		return nil, err
	}
	return &Session{session: tracked}, nil
}

func (t *Tracker) Sample() error {
	if t == nil {
		return ErrInvalidConfig
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	unlock, err := acquireLock(t.statePath)
	if err != nil {
		return err
	}
	defer unlock()
	if err := t.reloadExternalLocked(); err != nil {
		return err
	}
	policies, err := readPolicies(t.policyPath)
	if err != nil {
		return err
	}
	dirty := t.pruneDeletedUsersLocked(policies)
	for userID, policy := range policies {
		if user := findUser(&t.state, userID); user != nil {
			dirty = t.applyResetLocked(user, policy.TrafficResetGeneration) || dirty
		}
	}
	for _, tracked := range t.sessions {
		dirty = t.sampleSessionLocked(tracked) || dirty
	}
	if !dirty {
		return nil
	}
	return t.persistLocked()
}

func (t *Tracker) Run(ctxDone <-chan struct{}, interval time.Duration) {
	if t == nil || ctxDone == nil || interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctxDone:
			_ = t.Sample()
			return
		case <-ticker.C:
			_ = t.Sample()
		}
	}
}

func (s *Session) Close() error {
	if s == nil || s.session == nil || s.session.tracker == nil {
		return nil
	}
	t := s.session.tracker
	t.mu.Lock()
	defer t.mu.Unlock()
	tracked, ok := t.sessions[s.session.identifier]
	if !ok || tracked.closed {
		return nil
	}
	unlock, err := acquireLock(t.statePath)
	if err != nil {
		return err
	}
	defer unlock()
	if err := t.reloadExternalLocked(); err != nil {
		return err
	}
	t.sampleSessionLocked(tracked)
	tracked.closed = true
	delete(t.sessions, tracked.identifier)
	if user := findUser(&t.state, tracked.userID); user != nil {
		decrementUserSession(user, tracked.deviceID, t.now().UTC())
	}
	return t.persistLocked()
}

func (t *Tracker) Snapshot() (Snapshot, error) {
	if t == nil {
		return Snapshot{}, ErrInvalidConfig
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	unlock, err := acquireLock(t.statePath)
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	if err := t.reloadExternalLocked(); err != nil {
		return Snapshot{}, err
	}
	return snapshotFromState(t.state), nil
}

func ReadSnapshot(path string) (Snapshot, error) {
	state, err := readState(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{Version: stateVersion, Users: []UserSnapshot{}}, nil
		}
		return Snapshot{}, err
	}
	return snapshotFromState(state), nil
}

// ResetTraffic makes an administrator-requested reset visible immediately.
// The reset generation remains owned by the policy file; the running tracker
// observes that generation and re-baselines active session counters before it
// can add any pre-reset bytes again.
func ResetTraffic(path, userID string) error {
	if path == "" || !validUserID(userID) {
		return ErrInvalidConfig
	}
	unlock, err := acquireLock(path)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := readState(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	user := findUser(&state, userID)
	if user == nil {
		return nil
	}
	user.UploadBytes = 0
	user.DownloadBytes = 0
	state.Revision = saturatingAdd(state.Revision, 1)
	state.UpdatedAt = time.Now().UTC()
	return writeState(path, state)
}

func RemoveOfflineDevice(path, userID string, deviceID protocol.DeviceID) error {
	if path == "" || !validUserID(userID) || deviceID.IsZero() {
		return ErrInvalidConfig
	}
	encoded, _ := deviceID.MarshalText()
	deviceText := string(encoded)
	unlock, err := acquireLock(path)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := readState(path)
	if err != nil {
		return err
	}
	user := findUser(&state, userID)
	if user == nil {
		return ErrDeviceNotFound
	}
	for position := range user.Devices {
		if user.Devices[position].DeviceID != deviceText {
			continue
		}
		if user.Devices[position].ActiveSessions > 0 {
			return ErrDeviceOnline
		}
		user.Devices = append(user.Devices[:position], user.Devices[position+1:]...)
		state.Revision = saturatingAdd(state.Revision, 1)
		state.UpdatedAt = time.Now().UTC()
		return writeState(path, state)
	}
	return ErrDeviceNotFound
}

func (t *Tracker) applyResetLocked(user *persistedUser, generation uint64) bool {
	if generation <= user.ResetGeneration {
		return false
	}
	user.ResetGeneration = generation
	user.UploadBytes = 0
	user.DownloadBytes = 0
	for _, tracked := range t.sessions {
		if tracked.userID == user.UserID {
			tracked.last = tracked.read()
		}
	}
	return true
}

func (t *Tracker) sampleSessionLocked(tracked *trackedSession) bool {
	if tracked == nil || tracked.closed {
		return false
	}
	current := tracked.read()
	user := findUser(&t.state, tracked.userID)
	if user == nil {
		tracked.last = current
		return false
	}
	changed := false
	if current.UploadBytes >= tracked.last.UploadBytes {
		delta := current.UploadBytes - tracked.last.UploadBytes
		user.UploadBytes = saturatingAdd(user.UploadBytes, delta)
		changed = changed || delta > 0
	}
	if current.DownloadBytes >= tracked.last.DownloadBytes {
		delta := current.DownloadBytes - tracked.last.DownloadBytes
		user.DownloadBytes = saturatingAdd(user.DownloadBytes, delta)
		changed = changed || delta > 0
	}
	tracked.last = current
	return changed
}

func (t *Tracker) ensureUserLocked(userID string) *persistedUser {
	if user := findUser(&t.state, userID); user != nil {
		return user
	}
	t.state.Users = append(t.state.Users, persistedUser{UserID: userID, Devices: []persistedDevice{}})
	return &t.state.Users[len(t.state.Users)-1]
}

func (t *Tracker) pruneDeletedUsersLocked(policies map[string]policyUser) bool {
	before := len(t.state.Users)
	retained := t.state.Users[:0]
	for _, user := range t.state.Users {
		if _, exists := policies[user.UserID]; exists || user.ActiveSessions > 0 {
			retained = append(retained, user)
		}
	}
	t.state.Users = retained
	if t.state.Users == nil {
		t.state.Users = []persistedUser{}
	}
	return len(t.state.Users) != before
}

func (t *Tracker) reloadExternalLocked() error {
	state, err := readState(t.statePath)
	if err != nil {
		return err
	}
	if state.Revision > t.state.Revision {
		t.state = state
	}
	return nil
}

func (t *Tracker) persistLocked() error {
	t.state.Version = stateVersion
	t.state.Revision = saturatingAdd(t.state.Revision, 1)
	t.state.UpdatedAt = t.now().UTC()
	return writeState(t.statePath, t.state)
}

func readPolicies(path string) (map[string]policyUser, error) {
	var index policyIndex
	if err := readStrict(path, maximumPolicyBytes, &index); err != nil {
		return nil, fmt.Errorf("read usage policy: %w", err)
	}
	if index.Version != 1 || index.Users == nil || len(index.Users) > maximumUsers {
		return nil, ErrInvalidState
	}
	result := make(map[string]policyUser, len(index.Users))
	for _, user := range index.Users {
		if !validUserID(user.ID) || (user.Status != "active" && user.Status != "revoked") ||
			user.MaxDevices < 0 || user.MaxDevices > maximumConfiguredDevices {
			return nil, ErrInvalidState
		}
		if _, duplicate := result[user.ID]; duplicate {
			return nil, ErrInvalidState
		}
		result[user.ID] = user
	}
	return result, nil
}

func readState(path string) (persistedState, error) {
	var state persistedState
	if err := readStrict(path, maximumStateBytes, &state); err != nil {
		return persistedState{}, err
	}
	if err := validateState(state); err != nil {
		return persistedState{}, err
	}
	return state, nil
}

func readStrict(path string, maximum int64, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maximum {
		return ErrInvalidState
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	limited := &io.LimitedReader{R: file, N: maximum + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidState
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || limited.N <= 0 {
		return ErrInvalidState
	}
	return nil
}

func validateState(state persistedState) error {
	if state.Version != stateVersion || state.Users == nil || len(state.Users) > maximumUsers {
		return ErrInvalidState
	}
	users := make(map[string]struct{}, len(state.Users))
	for _, user := range state.Users {
		if !validUserID(user.UserID) || user.Devices == nil || len(user.Devices) > maximumDevicesPerUser {
			return ErrInvalidState
		}
		if _, duplicate := users[user.UserID]; duplicate {
			return ErrInvalidState
		}
		users[user.UserID] = struct{}{}
		devices := make(map[string]struct{}, len(user.Devices))
		var active uint64
		for _, device := range user.Devices {
			var parsed protocol.DeviceID
			if err := parsed.UnmarshalText([]byte(device.DeviceID)); err != nil || device.FirstSeen.IsZero() {
				return ErrInvalidState
			}
			if _, duplicate := devices[device.DeviceID]; duplicate {
				return ErrInvalidState
			}
			devices[device.DeviceID] = struct{}{}
			active = saturatingAdd(active, device.ActiveSessions)
		}
		if active > user.ActiveSessions {
			return ErrInvalidState
		}
	}
	return nil
}

func writeState(path string, state persistedState) error {
	if err := validateState(state); err != nil {
		return err
	}
	sort.Slice(state.Users, func(i, j int) bool { return state.Users[i].UserID < state.Users[j].UserID })
	for position := range state.Users {
		sort.Slice(state.Users[position].Devices, func(i, j int) bool {
			return state.Users[position].Devices[i].DeviceID < state.Users[position].Devices[j].DeviceID
		})
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil || len(raw) > maximumStateBytes {
		return ErrInvalidState
	}
	raw = append(raw, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".usage-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
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
	if err := os.Rename(temporaryPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			return err
		}
	}
	if runtime.GOOS != "windows" {
		if directoryHandle, err := os.Open(directory); err == nil {
			_ = directoryHandle.Sync()
			_ = directoryHandle.Close()
		}
	}
	return nil
}

func acquireLock(statePath string) (func(), error) {
	lockPath := statePath + ".lock"
	for attempt := 0; attempt < lockAttempts; attempt++ {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Lstat(lockPath); statErr == nil && info.IsDir() && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lockPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, ErrStateBusy
}

func snapshotFromState(state persistedState) Snapshot {
	snapshot := Snapshot{Version: state.Version, Revision: state.Revision, UpdatedAt: state.UpdatedAt, Users: make([]UserSnapshot, 0, len(state.Users))}
	for _, user := range state.Users {
		entry := UserSnapshot{
			UserID: user.UserID, Online: user.ActiveSessions > 0, LastSeen: user.LastSeen,
			ActiveSessions: user.ActiveSessions, EnrolledDevices: len(user.Devices),
			UploadBytes: user.UploadBytes, DownloadBytes: user.DownloadBytes,
			TotalBytes: saturatingAdd(user.UploadBytes, user.DownloadBytes),
			Devices:    make([]DeviceSnapshot, 0, len(user.Devices)),
		}
		for _, device := range user.Devices {
			entry.Devices = append(entry.Devices, DeviceSnapshot{
				DeviceID: device.DeviceID, Online: device.ActiveSessions > 0,
				ActiveSessions: device.ActiveSessions, FirstSeen: device.FirstSeen, LastSeen: device.LastSeen,
			})
			if device.ActiveSessions > 0 {
				entry.OnlineDevices++
			}
		}
		snapshot.Users = append(snapshot.Users, entry)
	}
	sort.Slice(snapshot.Users, func(i, j int) bool { return snapshot.Users[i].UserID < snapshot.Users[j].UserID })
	return snapshot
}

func findUser(state *persistedState, userID string) *persistedUser {
	for position := range state.Users {
		if state.Users[position].UserID == userID {
			return &state.Users[position]
		}
	}
	return nil
}

func findDevice(user *persistedUser, deviceID string) *persistedDevice {
	for position := range user.Devices {
		if user.Devices[position].DeviceID == deviceID {
			return &user.Devices[position]
		}
	}
	return nil
}

func decrementUserSession(user *persistedUser, deviceID string, now time.Time) {
	if user.ActiveSessions > 0 {
		user.ActiveSessions--
	}
	user.LastSeen = timePointer(now)
	if deviceID == "" {
		return
	}
	if device := findDevice(user, deviceID); device != nil {
		if device.ActiveSessions > 0 {
			device.ActiveSessions--
		}
		device.LastSeen = timePointer(now)
	}
}

func validUserID(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e || character == '/' || character == '\\' {
			return false
		}
	}
	return true
}

func saturatingAdd(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}
