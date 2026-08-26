package windowsclient

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"neproto.local/chameleon/internal/cluster"
)

const (
	storeFileName = "client-state.json"
	storeVersion  = 1
)

var (
	ErrProfileExists   = errors.New("NP/2 profile already exists")
	ErrProfileNotFound = errors.New("NP/2 profile not found")
	ErrNoProfile       = errors.New("no NP/2 profile selected")
)

type Protector interface {
	Protect([]byte) ([]byte, error)
	Unprotect([]byte) ([]byte, error)
}

type storedProfile struct {
	Profile         Profile `json:"profile"`
	ProtectedSecret string  `json:"protected_secret"`
}

type storeState struct {
	Version           int                           `json:"version"`
	DeviceID          string                        `json:"device_id"`
	SelectedProfileID string                        `json:"selected_profile_id,omitempty"`
	Profiles          []storedProfile               `json:"profiles"`
	Catalogs          map[string]ClientCatalogState `json:"catalogs,omitempty"`
	SuppressedNodes   map[string][]string           `json:"suppressed_nodes,omitempty"`
}

type ClientCatalogState struct {
	ClusterID      string                     `json:"cluster_id"`
	Revision       uint64                     `json:"revision"`
	SynchronizedAt time.Time                  `json:"synchronized_at"`
	AdminRoutes    []cluster.Route            `json:"admin_routes"`
	LocalRoutes    []cluster.Route            `json:"local_routes"`
	Permissions    cluster.CatalogPermissions `json:"permissions"`
}

type Store struct {
	mu        sync.RWMutex
	directory string
	protector Protector
	state     storeState
}

func OpenStore(directory string, protector Protector) (*Store, error) {
	if directory == "" || !filepath.IsAbs(directory) || protector == nil {
		return nil, fmt.Errorf("open Windows client store: invalid configuration")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create Windows client store: %w", err)
	}
	store := &Store{directory: directory, protector: protector}
	path := filepath.Join(directory, storeFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		deviceID, err := newDeviceID()
		if err != nil {
			return nil, err
		}
		store.state = storeState{Version: storeVersion, DeviceID: deviceID, Profiles: []storedProfile{}, Catalogs: make(map[string]ClientCatalogState), SuppressedNodes: make(map[string][]string)}
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Windows client store: %w", err)
	}
	if len(raw) == 0 || len(raw) > MaxIPCMessageBytes {
		return nil, fmt.Errorf("read Windows client store: invalid size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store.state); err != nil || store.state.Version != storeVersion || store.state.DeviceID == "" {
		return nil, fmt.Errorf("read Windows client store: invalid state")
	}
	if store.state.Profiles == nil {
		store.state.Profiles = []storedProfile{}
	}
	if store.state.Catalogs == nil {
		store.state.Catalogs = make(map[string]ClientCatalogState)
	}
	if store.state.SuppressedNodes == nil {
		store.state.SuppressedNodes = make(map[string][]string)
	}
	if !store.hasProfileLocked(store.state.SelectedProfileID) {
		store.state.SelectedProfileID = ""
	}
	return store, nil
}

func (s *Store) DeviceID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.DeviceID
}

func (s *Store) SelectedProfileID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.SelectedProfileID
}

func (s *Store) HasCredential(profileID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.profileLocked(profileID)
	if !ok || profile.ProtectedSecret == "" {
		return false
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(profile.ProtectedSecret)
	return err == nil && len(ciphertext) > 0 && len(ciphertext) <= 8192
}

func (s *Store) CatalogRevision(profileID string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.profileLocked(profileID)
	if !ok || profile.Profile.ClusterID == "" {
		return 0
	}
	return s.state.Catalogs[profile.Profile.ClusterID].Revision
}

func (s *Store) Catalog(profileID string) (ClientCatalogState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.profileLocked(profileID)
	if !ok || profile.Profile.ClusterID == "" {
		return ClientCatalogState{}, false
	}
	state, ok := s.state.Catalogs[profile.Profile.ClusterID]
	if !ok {
		return ClientCatalogState{}, false
	}
	return cloneCatalogState(state), true
}

func (s *Store) EffectiveRoutes(profileID string) []cluster.Route {
	state, ok := s.Catalog(profileID)
	if !ok {
		return []cluster.Route{}
	}
	return cluster.EffectiveRoutes(state.AdminRoutes, state.LocalRoutes, state.Permissions.AllowClientRoutes)
}

func (s *Store) Profiles() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profiles := make([]Profile, 0, len(s.state.Profiles))
	for _, stored := range s.state.Profiles {
		profiles = append(profiles, cloneProfile(stored.Profile))
	}
	sort.SliceStable(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles
}

func (s *Store) Import(uri string) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, _, secret, err := ImportURI(uri, s.state.DeviceID)
	if err != nil {
		return Profile{}, err
	}
	if s.hasProfileLocked(profile.ID) {
		return Profile{}, ErrProfileExists
	}
	protected, err := s.protector.Protect([]byte(secret))
	if err != nil {
		return Profile{}, fmt.Errorf("protect NP/2 credential: %w", err)
	}
	s.state.Profiles = append(s.state.Profiles, storedProfile{Profile: profile, ProtectedSecret: base64.RawURLEncoding.EncodeToString(protected)})
	previous := s.state.SelectedProfileID
	if previous == "" {
		s.state.SelectedProfileID = profile.ID
	}
	if err := s.saveLocked(); err != nil {
		s.state.Profiles = s.state.Profiles[:len(s.state.Profiles)-1]
		s.state.SelectedProfileID = previous
		return Profile{}, err
	}
	return cloneProfile(profile), nil
}

func (s *Store) Select(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasProfileLocked(id) {
		return ErrProfileNotFound
	}
	previous := s.state.SelectedProfileID
	s.state.SelectedProfileID = id
	if err := s.saveLocked(); err != nil {
		s.state.SelectedProfileID = previous
		return err
	}
	return nil
}

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for candidate := range s.state.Profiles {
		if s.state.Profiles[candidate].Profile.ID == id {
			index = candidate
			break
		}
	}
	if index < 0 {
		return ErrProfileNotFound
	}
	previous := cloneStoreState(s.state)
	removed := s.state.Profiles[index].Profile
	s.state.Profiles = append(s.state.Profiles[:index:index], s.state.Profiles[index+1:]...)
	if removed.ManagedByCluster && removed.ClusterID != "" && removed.ClusterNodeID != "" {
		s.state.SuppressedNodes[removed.ClusterID] = appendUnique(s.state.SuppressedNodes[removed.ClusterID], removed.ClusterNodeID)
	}
	if s.state.SelectedProfileID == id {
		s.state.SelectedProfileID = ""
		if len(s.state.Profiles) > 0 {
			s.state.SelectedProfileID = s.state.Profiles[0].Profile.ID
		}
	}
	if err := s.saveLocked(); err != nil {
		s.state = previous
		return err
	}
	return nil
}

func (s *Store) Selected() (Profile, []byte, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state.SelectedProfileID == "" {
		return Profile{}, nil, "", ErrNoProfile
	}
	for _, stored := range s.state.Profiles {
		if stored.Profile.ID != s.state.SelectedProfileID {
			continue
		}
		ciphertext, err := base64.RawURLEncoding.DecodeString(stored.ProtectedSecret)
		if err != nil {
			return Profile{}, nil, "", fmt.Errorf("decode NP/2 credential: %w", err)
		}
		plain, err := s.protector.Unprotect(ciphertext)
		if err != nil {
			return Profile{}, nil, "", fmt.Errorf("unprotect NP/2 credential: %w", err)
		}
		raw, err := stored.Profile.clientConfiguration(s.state.DeviceID)
		if err != nil {
			return Profile{}, nil, "", err
		}
		return cloneProfile(stored.Profile), raw, string(plain), nil
	}
	return Profile{}, nil, "", ErrNoProfile
}

func (s *Store) ApplyCatalog(bootstrapID string, catalog cluster.Catalog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bootstrap, ok := s.profileLocked(bootstrapID)
	if !ok || bootstrap.Profile.ClusterID == "" || bootstrap.Profile.ClusterID != catalog.ClusterID {
		return cluster.ErrInvalidCatalog
	}
	previous := cloneStoreState(s.state)
	retained := make([]storedProfile, 0, len(s.state.Profiles)+len(catalog.Servers))
	for _, stored := range s.state.Profiles {
		if stored.Profile.ManagedByCluster && stored.Profile.ClusterID == catalog.ClusterID {
			continue
		}
		retained = append(retained, stored)
	}
	for _, server := range catalog.Servers {
		if containsString(s.state.SuppressedNodes[catalog.ClusterID], server.NodeID) {
			continue
		}
		profile := clusterProfile(bootstrap.Profile, server)
		found := false
		for index := range retained {
			if retained[index].Profile.ID != profile.ID {
				continue
			}
			profile.ManagedByCluster = retained[index].Profile.ManagedByCluster
			retained[index].Profile = profile
			found = true
			break
		}
		if !found {
			profile.ManagedByCluster = true
			retained = append(retained, storedProfile{Profile: profile, ProtectedSecret: bootstrap.ProtectedSecret})
		}
	}
	s.state.Profiles = retained
	if !s.hasProfileLocked(s.state.SelectedProfileID) {
		s.state.SelectedProfileID = ""
		if len(retained) > 0 {
			s.state.SelectedProfileID = retained[0].Profile.ID
		}
	}
	local := s.state.Catalogs[catalog.ClusterID].LocalRoutes
	s.state.Catalogs[catalog.ClusterID] = ClientCatalogState{
		ClusterID: catalog.ClusterID, Revision: catalog.Revision, SynchronizedAt: time.Now().UTC(),
		AdminRoutes: append([]cluster.Route(nil), catalog.AdminRoutes...),
		LocalRoutes: local, Permissions: catalog.Permissions,
	}
	if err := s.saveLocked(); err != nil {
		s.state = previous
		return err
	}
	return nil
}

func (s *Store) UpsertLocalRoute(profileID string, route cluster.Route) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profileLocked(profileID)
	if !ok {
		return ErrProfileNotFound
	}
	state, stateOK := s.state.Catalogs[profile.Profile.ClusterID]
	if !stateOK || !state.Permissions.AllowClientRoutes {
		return cluster.ErrInvalidState
	}
	route.Source, route.Mandatory = cluster.RouteSourceClient, false
	if err := cluster.ValidateRoute(route); err != nil {
		return err
	}
	previous := cloneStoreState(s.state)
	replaced := false
	for index := range state.LocalRoutes {
		if state.LocalRoutes[index].ID == route.ID {
			state.LocalRoutes[index] = route
			replaced = true
			break
		}
	}
	if !replaced {
		state.LocalRoutes = append(state.LocalRoutes, route)
	}
	s.state.Catalogs[profile.Profile.ClusterID] = state
	if err := s.saveLocked(); err != nil {
		s.state = previous
		return err
	}
	return nil
}

func (s *Store) RemoveLocalRoute(profileID, routeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profileLocked(profileID)
	if !ok {
		return ErrProfileNotFound
	}
	state, stateOK := s.state.Catalogs[profile.Profile.ClusterID]
	if !stateOK || !state.Permissions.AllowClientRoutes {
		return cluster.ErrInvalidState
	}
	for index := range state.LocalRoutes {
		if state.LocalRoutes[index].ID != routeID {
			continue
		}
		previous := cloneStoreState(s.state)
		state.LocalRoutes = append(state.LocalRoutes[:index:index], state.LocalRoutes[index+1:]...)
		s.state.Catalogs[profile.Profile.ClusterID] = state
		if err := s.saveLocked(); err != nil {
			s.state = previous
			return err
		}
		return nil
	}
	return ErrProfileNotFound
}

func (s *Store) hasProfileLocked(id string) bool {
	if id == "" {
		return false
	}
	for _, profile := range s.state.Profiles {
		if profile.Profile.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) profileLocked(id string) (storedProfile, bool) {
	for _, profile := range s.state.Profiles {
		if profile.Profile.ID == id {
			return profile, true
		}
	}
	return storedProfile{}, false
}

func (s *Store) saveLocked() error {
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil || len(raw) > MaxIPCMessageBytes {
		return fmt.Errorf("encode Windows client store")
	}
	temporary, err := os.CreateTemp(s.directory, ".client-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create Windows client state: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
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
	if err := replaceFile(temporaryName, filepath.Join(s.directory, storeFileName)); err != nil {
		return fmt.Errorf("replace Windows client state: %w", err)
	}
	return nil
}

func newDeviceID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate NP/2 device ID: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func cloneProfile(profile Profile) Profile {
	profile.ServerAddresses = append([]string(nil), profile.ServerAddresses...)
	profile.Secret = ""
	return profile
}

func clusterProfile(bootstrap Profile, server cluster.CatalogServer) Profile {
	profile := cloneProfile(bootstrap)
	profile.ID = profileIDFor(bootstrap.CredentialID, server.ServerIdentity)
	profile.Name, profile.Region = server.Name, server.Region
	profile.ServerIdentity = server.ServerIdentity
	profile.ServerAddresses = append([]string(nil), server.ServerAddresses...)
	if server.HTTPSPath != "" {
		profile.HTTPSPath = server.HTTPSPath
	}
	if server.WebRTCPath != "" {
		profile.WebRTCPath = server.WebRTCPath
	}
	if server.HTTP3Path != "" {
		profile.HTTP3Path = server.HTTP3Path
	}
	profile.RequireDatagrams = server.RequireDatagrams
	profile.ClusterNodeID, profile.ClusterAvailable = server.NodeID, server.Enabled
	return profile
}

func cloneCatalogState(state ClientCatalogState) ClientCatalogState {
	state.AdminRoutes = append([]cluster.Route(nil), state.AdminRoutes...)
	state.LocalRoutes = append([]cluster.Route(nil), state.LocalRoutes...)
	return state
}

func cloneStoreState(state storeState) storeState {
	state.Profiles = append([]storedProfile(nil), state.Profiles...)
	state.Catalogs = make(map[string]ClientCatalogState, len(state.Catalogs))
	for id, catalog := range state.Catalogs {
		state.Catalogs[id] = cloneCatalogState(catalog)
	}
	state.SuppressedNodes = make(map[string][]string, len(state.SuppressedNodes))
	for id, nodes := range state.SuppressedNodes {
		state.SuppressedNodes[id] = append([]string(nil), nodes...)
	}
	return state
}

func appendUnique(values []string, value string) []string {
	if containsString(values, value) {
		return values
	}
	return append(values, value)
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
