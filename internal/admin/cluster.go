package admin

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/geodata"
)

const historicalLocalRegion = "Primary"

// EnsureLocalCluster initializes this installation as the authoritative
// master on first use and returns the existing state on subsequent calls.
func (m *Manager) EnsureLocalCluster() (cluster.State, error) {
	state, err := m.ClusterState()
	if err == nil {
		return m.ensureDetectedLocalRegion(state)
	}
	if !errors.Is(err, cluster.ErrStateNotFound) {
		return cluster.State{}, err
	}
	randomID := make([]byte, 6)
	if _, err := rand.Read(randomID); err != nil {
		return cluster.State{}, err
	}
	installation := m.Installation()
	now := m.now().UTC()
	region := m.detectInstallationRegion()
	if region == "" {
		region = historicalLocalRegion
	}
	master := cluster.Node{
		ID: "master", Name: "Primary", Region: region, Roles: []cluster.NodeRole{cluster.RoleMaster, cluster.RoleIngress, cluster.RoleRelay, cluster.RoleEgress},
		PublicIdentity: installation.Domain, PublicAddresses: append([]string(nil), installation.ServerAddresses...), NP2Endpoint: installation.Domain + ":443",
		HTTPSPath: installation.HTTPSPath, WebRTCPath: installation.WebRTCPath, HTTP3Path: installation.HTTP3Path,
		RequireDatagrams: installation.RequireDatagrams, Enabled: true, ClientVisible: true,
		CredentialID: "local-master", HostKeySHA256: "SHA256:local-controller", ProvisionedAt: now, UpdatedAt: now,
	}
	return m.InitializeCluster("np2-"+hex.EncodeToString(randomID), master)
}

func (m *Manager) ensureDetectedLocalRegion(state cluster.State) (cluster.State, error) {
	for _, node := range state.Nodes {
		if node.ClientVisible && node.PublicIdentity == m.installation.Domain && node.Region == historicalLocalRegion {
			region := m.detectInstallationRegion()
			if region == "" {
				return state, nil
			}
			node.Region = region
			return m.UpsertClusterNode(node)
		}
	}
	return state, nil
}

func (m *Manager) detectInstallationRegion() string {
	engine, err := geodata.Load(m.GeodataDirectory())
	if err != nil {
		return ""
	}
	for _, rawAddress := range m.installation.ServerAddresses {
		address, err := netip.ParseAddr(rawAddress)
		if err != nil {
			continue
		}
		if country, ok := engine.CountryCode(address); ok {
			return country
		}
	}
	return ""
}

func (m *Manager) ClusterBootstrap() (string, string, error) {
	store, err := m.openClusterStore()
	if err != nil {
		return "", "", err
	}
	state, err := store.Load()
	if errors.Is(err, cluster.ErrStateNotFound) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	publicKey, _, err := store.LoadSigningKey()
	if err != nil {
		return "", "", err
	}
	return state.ClusterID, base64.RawURLEncoding.EncodeToString(publicKey), nil
}

var (
	ErrClusterNodeNotFound  = errors.New("NP/2 cluster node not found")
	ErrClusterRouteNotFound = errors.New("NP/2 cluster route not found")
	ErrClusterNodeInUse     = errors.New("NP/2 cluster node is used by a route")
)

func (m *Manager) InitializeCluster(clusterID string, master cluster.Node) (cluster.State, error) {
	store, err := m.openClusterStore()
	if err != nil {
		return cluster.State{}, err
	}
	now := m.now().UTC()
	if master.ProvisionedAt.IsZero() {
		master.ProvisionedAt = now
	}
	master.UpdatedAt = now
	state := cluster.State{
		Version: cluster.StateVersion, ClusterID: clusterID, Revision: 1,
		Nodes: []cluster.Node{master}, UpdatedAt: now,
	}
	if err := cluster.ValidateState(state); err != nil {
		return cluster.State{}, err
	}
	if _, _, err := store.LoadOrCreateSigningKey(m.random); err != nil {
		return cluster.State{}, err
	}
	if err := store.Initialize(state); err != nil {
		return cluster.State{}, err
	}
	if err := m.secureClusterState(); err != nil {
		return cluster.State{}, err
	}
	return state, nil
}

func (m *Manager) ClusterState() (cluster.State, error) {
	store, err := m.openClusterStore()
	if err != nil {
		return cluster.State{}, err
	}
	return store.Load()
}

func (m *Manager) UpsertClusterNode(node cluster.Node) (cluster.State, error) {
	return m.mutateCluster(func(state *cluster.State) error {
		now := m.now().UTC()
		for index := range state.Nodes {
			if state.Nodes[index].ID != node.ID {
				continue
			}
			if node.ProvisionedAt.IsZero() {
				node.ProvisionedAt = state.Nodes[index].ProvisionedAt
			}
			node.UpdatedAt = now
			state.Nodes[index] = node
			return nil
		}
		if node.ProvisionedAt.IsZero() {
			node.ProvisionedAt = now
		}
		node.UpdatedAt = now
		state.Nodes = append(state.Nodes, node)
		return nil
	})
}

func (m *Manager) RemoveClusterNode(nodeID string) (cluster.State, error) {
	return m.mutateCluster(func(state *cluster.State) error {
		index := -1
		for position, node := range state.Nodes {
			if node.ID == nodeID {
				if hasClusterRole(node.Roles, cluster.RoleMaster) {
					return cluster.ErrInvalidState
				}
				index = position
				break
			}
		}
		if index < 0 {
			return ErrClusterNodeNotFound
		}
		for _, route := range state.Routes {
			for _, referenced := range route.Action.NodeIDs {
				if referenced == nodeID {
					return ErrClusterNodeInUse
				}
			}
		}
		state.Nodes = append(state.Nodes[:index], state.Nodes[index+1:]...)
		for position := range state.Access {
			state.Access[position].AllowedNodeIDs = removeClusterID(state.Access[position].AllowedNodeIDs, nodeID)
			state.Access[position].Revision++
		}
		return nil
	})
}

func (m *Manager) UpsertClusterRoute(route cluster.Route) (cluster.State, error) {
	return m.mutateCluster(func(state *cluster.State) error {
		for index := range state.Routes {
			if state.Routes[index].ID == route.ID {
				state.Routes[index] = route
				return nil
			}
		}
		state.Routes = append(state.Routes, route)
		return nil
	})
}

func (m *Manager) RemoveClusterRoute(routeID string) (cluster.State, error) {
	return m.mutateCluster(func(state *cluster.State) error {
		index := -1
		for position, route := range state.Routes {
			if route.ID == routeID {
				index = position
				break
			}
		}
		if index < 0 {
			return ErrClusterRouteNotFound
		}
		state.Routes = append(state.Routes[:index], state.Routes[index+1:]...)
		for position := range state.Access {
			state.Access[position].AllowedRouteIDs = removeClusterID(state.Access[position].AllowedRouteIDs, routeID)
			state.Access[position].Revision++
		}
		return nil
	})
}

func (m *Manager) SetClusterUserAccess(access cluster.UserAccess) (cluster.State, error) {
	users, err := m.ListUsers()
	if err != nil {
		return cluster.State{}, err
	}
	active := false
	for _, user := range users {
		if user.ID == access.UserID && user.Status == StatusActive {
			active = true
			break
		}
	}
	if !active {
		return cluster.State{}, ErrUserNotFound
	}
	return m.mutateCluster(func(state *cluster.State) error {
		for index := range state.Access {
			if state.Access[index].UserID == access.UserID {
				if access.Revision <= state.Access[index].Revision {
					access.Revision = state.Access[index].Revision + 1
				}
				state.Access[index] = access
				return nil
			}
		}
		if access.Revision == 0 {
			access.Revision = 1
		}
		state.Access = append(state.Access, access)
		return nil
	})
}

func (m *Manager) RemoveClusterUserAccess(userID string) (cluster.State, error) {
	return m.mutateCluster(func(state *cluster.State) error {
		for index := range state.Access {
			if state.Access[index].UserID == userID {
				state.Access = append(state.Access[:index], state.Access[index+1:]...)
				return nil
			}
		}
		return ErrUserNotFound
	})
}

func (m *Manager) SignedClusterCatalog(userID string, ttl time.Duration) (cluster.Catalog, ed25519.PublicKey, error) {
	store, err := m.openClusterStore()
	if err != nil {
		return cluster.Catalog{}, nil, err
	}
	state, err := store.Load()
	if err != nil {
		return cluster.Catalog{}, nil, err
	}
	catalog, err := cluster.BuildCatalog(state, userID, m.now().UTC(), ttl)
	if err != nil {
		return cluster.Catalog{}, nil, err
	}
	publicKey, privateKey, err := store.LoadOrCreateSigningKey(m.random)
	if err != nil {
		return cluster.Catalog{}, nil, err
	}
	signed, err := cluster.SignCatalog(catalog, privateKey)
	if err != nil {
		return cluster.Catalog{}, nil, err
	}
	return signed, publicKey, nil
}

func (m *Manager) mutateCluster(mutation func(*cluster.State) error) (cluster.State, error) {
	store, err := m.openClusterStore()
	if err != nil {
		return cluster.State{}, err
	}
	state, err := store.Load()
	if err != nil {
		return cluster.State{}, err
	}
	previousRevision := state.Revision
	if err := mutation(&state); err != nil {
		return cluster.State{}, err
	}
	state.Revision++
	state.UpdatedAt = m.now().UTC()
	if err := store.Save(previousRevision, state); err != nil {
		return cluster.State{}, fmt.Errorf("save cluster state: %w", err)
	}
	if err := m.secureClusterState(); err != nil {
		return cluster.State{}, err
	}
	return state, nil
}

func (m *Manager) secureClusterState() error {
	if m.installation.ServiceUID == nil {
		return nil
	}
	directory := filepath.Join(m.root, "etc", "neproto", "cluster")
	if err := os.Chown(directory, 0, *m.installation.ServiceGID); err != nil {
		return fmt.Errorf("set cluster directory ownership: %w", err)
	}
	if err := os.Chmod(directory, 0o750); err != nil {
		return fmt.Errorf("set cluster directory permissions: %w", err)
	}
	for _, name := range []string{"state.json", "state.last-good.json", "catalog-signing.key"} {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: invalid cluster state file", ErrInvalidState)
		}
		if err := os.Chown(path, *m.installation.ServiceUID, *m.installation.ServiceGID); err != nil {
			return fmt.Errorf("set cluster state ownership: %w", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("set cluster state permissions: %w", err)
		}
	}
	return nil
}

func (m *Manager) openClusterStore() (*cluster.Store, error) {
	if m == nil {
		return nil, ErrInvalidState
	}
	return cluster.OpenStore(filepath.Join(m.root, "etc", "neproto", "cluster"))
}

func hasClusterRole(roles []cluster.NodeRole, wanted cluster.NodeRole) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}

func removeClusterID(values []string, removed string) []string {
	result := values[:0]
	for _, value := range values {
		if value != removed {
			result = append(result, value)
		}
	}
	return result
}
