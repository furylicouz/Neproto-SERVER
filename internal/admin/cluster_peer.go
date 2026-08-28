package admin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/credentials"
)

var (
	ErrClusterPeerExists   = errors.New("NP/2 cluster peer already exists")
	ErrClusterPeerNotFound = errors.New("NP/2 cluster peer not found")
)

// ClusterPeerMaterial is a pair-scoped NP/2 credential. The same material is
// installed on both ends of the master-edge link; each endpoint maps the
// credential to the opposite node identity.
type ClusterPeerMaterial struct {
	CredentialID string
	Secret       [32]byte
}

// ClusterPeerEndpoint contains only public connection data for a peer node.
type ClusterPeerEndpoint struct {
	NodeID          string
	ServerIdentity  string
	ServerAddresses []string
	HTTPSPath       string
	WebRTCPath      string
	HTTP3Path       string
}

type acceptedPeerDocument struct {
	Version int                 `json:"version"`
	Peers   []acceptedPeerEntry `json:"peers"`
}

type acceptedPeerEntry struct {
	CredentialID string `json:"credential_id"`
	NodeID       string `json:"node_id"`
}

// NewClusterPeerMaterial creates a unique pair credential without adding it
// to the user index. Peer identities therefore cannot be exported as clients.
func (m *Manager) NewClusterPeerMaterial() (ClusterPeerMaterial, error) {
	if m == nil {
		return ClusterPeerMaterial{}, ErrInvalidState
	}
	identifierRaw := make([]byte, credentials.IDSize)
	if _, err := readFullNonZero(m.random, identifierRaw); err != nil {
		return ClusterPeerMaterial{}, fmt.Errorf("create cluster peer identifier: %w", err)
	}
	secretRaw := make([]byte, 32)
	if _, err := readFullNonZero(m.random, secretRaw); err != nil {
		zeroBytes(secretRaw)
		return ClusterPeerMaterial{}, fmt.Errorf("create cluster peer secret: %w", err)
	}
	var secret [32]byte
	copy(secret[:], secretRaw)
	zeroBytes(secretRaw)
	return ClusterPeerMaterial{CredentialID: base64.RawURLEncoding.EncodeToString(identifierRaw), Secret: secret}, nil
}

// InstallClusterPeer atomically makes a remote node usable by the local NP/2
// service. It installs the inbound credential, outbound client profile,
// accepted-principal map, and cluster runtime fields in server.json.
func (m *Manager) InstallClusterPeer(masterNodeID string, endpoint ClusterPeerEndpoint, material ClusterPeerMaterial) error {
	if m == nil || !clusterIdentifier(masterNodeID) || !validPeerEndpoint(endpoint) ||
		!validIdentifier(material.CredentialID) || material.Secret == ([32]byte{}) {
		return ErrInvalidState
	}
	state, err := m.ClusterState()
	if err != nil {
		return err
	}
	if !clusterHasNode(state, masterNodeID, cluster.RoleMaster) || endpoint.NodeID == masterNodeID {
		return ErrInvalidState
	}
	for _, node := range state.Nodes {
		if node.ID == endpoint.NodeID && node.CredentialID != material.CredentialID {
			return ErrClusterPeerExists
		}
	}

	paths := m.clusterRuntimePaths(endpoint.NodeID, material.CredentialID)
	for _, path := range []string{paths.activeSecret, paths.peerSecret, paths.peerClient} {
		if _, statErr := os.Lstat(path); statErr == nil {
			return ErrClusterPeerExists
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	}
	if err := m.prepareClusterRuntimeDirectories(paths); err != nil {
		return err
	}
	accepted, err := m.loadAcceptedPeerDocument(paths.acceptedPeers)
	if err != nil {
		return err
	}
	for _, peer := range accepted.Peers {
		if peer.NodeID == endpoint.NodeID || peer.CredentialID == material.CredentialID {
			return ErrClusterPeerExists
		}
	}
	accepted.Peers = append(accepted.Peers, acceptedPeerEntry{CredentialID: material.CredentialID, NodeID: endpoint.NodeID})
	sort.Slice(accepted.Peers, func(i, j int) bool { return accepted.Peers[i].NodeID < accepted.Peers[j].NodeID })

	secretRaw := []byte(base64.RawURLEncoding.EncodeToString(material.Secret[:]) + "\n")
	defer zeroBytes(secretRaw)
	clientRaw, err := m.clusterPeerClientJSON(endpoint, paths.peerSecret)
	if err != nil {
		return err
	}
	acceptedRaw, err := json.MarshalIndent(accepted, "", "  ")
	if err != nil {
		return err
	}
	acceptedRaw = append(acceptedRaw, '\n')
	serverPath := filepath.Join(m.root, "etc", "neproto", "server.json")
	serverRaw, err := m.clusterServerJSON(serverPath, masterNodeID, masterNodeID, paths)
	if err != nil {
		return err
	}

	created := make([]string, 0, 3)
	rollbackCreated := func() {
		for index := len(created) - 1; index >= 0; index-- {
			_ = os.Remove(created[index])
		}
	}
	for _, file := range []struct {
		path string
		raw  []byte
	}{
		{paths.activeSecret, secretRaw},
		{paths.peerSecret, secretRaw},
		{paths.peerClient, clientRaw},
	} {
		if err := writeNewFile(file.path, file.raw, 0o600); err != nil {
			rollbackCreated()
			return fmt.Errorf("install cluster peer: %w", err)
		}
		created = append(created, file.path)
		if err := m.secureCredential(file.path); err != nil {
			rollbackCreated()
			return err
		}
	}
	previousAccepted, acceptedExisted, err := readOptionalRegular(paths.acceptedPeers)
	if err != nil {
		rollbackCreated()
		return err
	}
	previousServer, err := readRegularFile(serverPath)
	if err != nil {
		rollbackCreated()
		return err
	}
	if err := replaceFile(paths.acceptedPeers, acceptedRaw, 0o600); err != nil {
		rollbackCreated()
		return err
	}
	if err := m.secureCredential(paths.acceptedPeers); err != nil {
		rollbackOptional(paths.acceptedPeers, previousAccepted, acceptedExisted)
		rollbackCreated()
		return err
	}
	if err := replaceRegularFile(serverPath, serverRaw); err != nil {
		rollbackOptional(paths.acceptedPeers, previousAccepted, acceptedExisted)
		rollbackCreated()
		return err
	}
	if _, err := config.LoadServer(serverPath); err != nil {
		_ = replaceRegularFile(serverPath, previousServer)
		rollbackOptional(paths.acceptedPeers, previousAccepted, acceptedExisted)
		rollbackCreated()
		return fmt.Errorf("validate cluster runtime: %w", err)
	}
	return nil
}

// RemoveClusterPeer revokes both directions of a pair credential and removes
// its outbound profile. Callers must ensure no route still references nodeID.
func (m *Manager) RemoveClusterPeer(nodeID, credentialID string) error {
	if m == nil || !clusterIdentifier(nodeID) || !validIdentifier(credentialID) {
		return ErrInvalidState
	}
	paths := m.clusterRuntimePaths(nodeID, credentialID)
	accepted, err := m.loadAcceptedPeerDocument(paths.acceptedPeers)
	if err != nil {
		return err
	}
	index := -1
	for position, peer := range accepted.Peers {
		if peer.NodeID == nodeID && peer.CredentialID == credentialID {
			index = position
			break
		}
	}
	if index < 0 {
		return ErrClusterPeerNotFound
	}
	accepted.Peers = append(accepted.Peers[:index], accepted.Peers[index+1:]...)
	serverPath := filepath.Join(m.root, "etc", "neproto", "server.json")
	previousServer, err := readRegularFile(serverPath)
	if err != nil {
		return err
	}
	nextServer, err := clusterServerWithoutRelayPeer(previousServer, len(accepted.Peers) == 0)
	if err != nil {
		return err
	}
	previousAccepted, _, err := readOptionalRegular(paths.acceptedPeers)
	if err != nil {
		return err
	}
	if len(accepted.Peers) == 0 {
		if err := os.Remove(paths.acceptedPeers); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		raw, err := json.MarshalIndent(accepted, "", "  ")
		if err != nil {
			return err
		}
		if err := replaceFile(paths.acceptedPeers, append(raw, '\n'), 0o600); err != nil {
			return err
		}
		if err := m.secureCredential(paths.acceptedPeers); err != nil {
			_ = replaceFile(paths.acceptedPeers, previousAccepted, 0o600)
			return err
		}
	}
	if err := replaceRegularFile(serverPath, nextServer); err != nil {
		_ = replaceFile(paths.acceptedPeers, previousAccepted, 0o600)
		return err
	}
	if err := os.Remove(paths.activeSecret); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = replaceRegularFile(serverPath, previousServer)
		_ = replaceFile(paths.acceptedPeers, previousAccepted, 0o600)
		return err
	}
	if err := os.RemoveAll(paths.peerDirectory); err != nil {
		return fmt.Errorf("remove cluster peer profile: %w", err)
	}
	return nil
}

func clusterServerWithoutRelayPeer(raw []byte, removeRuntime bool) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if removeRuntime {
		delete(document, "cluster_node_id")
		delete(document, "cluster_master_node_id")
		delete(document, "cluster_peer_directory")
		delete(document, "cluster_peer_map_file")
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(updated, '\n'), nil
}

type clusterRuntimeFilePaths struct {
	clusterDirectory string
	peerDirectory    string
	peerSecret       string
	peerClient       string
	acceptedPeers    string
	activeSecret     string
}

func (m *Manager) clusterRuntimePaths(nodeID, credentialID string) clusterRuntimeFilePaths {
	clusterDirectory := filepath.Join(m.root, "etc", "neproto", "cluster")
	peerDirectory := filepath.Join(clusterDirectory, "peers", nodeID)
	return clusterRuntimeFilePaths{
		clusterDirectory: clusterDirectory,
		peerDirectory:    peerDirectory,
		peerSecret:       filepath.Join(peerDirectory, "secret"),
		peerClient:       filepath.Join(peerDirectory, "client.json"),
		acceptedPeers:    filepath.Join(clusterDirectory, "accepted-peers.json"),
		activeSecret:     m.activeSecretPath(credentialID),
	}
}

func (m *Manager) prepareClusterRuntimeDirectories(paths clusterRuntimeFilePaths) error {
	for _, directory := range []string{paths.clusterDirectory, filepath.Dir(paths.peerDirectory), paths.peerDirectory} {
		if err := ensureDirectory(directory); err != nil {
			return err
		}
		if m.installation.ServiceGID != nil {
			if err := os.Chown(directory, 0, *m.installation.ServiceGID); err != nil {
				return err
			}
			if err := os.Chmod(directory, 0o750); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) clusterPeerClientJSON(endpoint ClusterPeerEndpoint, secretPath string) ([]byte, error) {
	addresses := make([]netip.Addr, 0, len(endpoint.ServerAddresses))
	for _, raw := range endpoint.ServerAddresses {
		address, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, ErrInvalidState
		}
		addresses = append(addresses, address)
	}
	document := map[string]any{
		"server_identity":            endpoint.ServerIdentity,
		"server_addresses":           addresses,
		"secret_file":                secretPath,
		"socks_listen":               "127.0.0.1:0",
		"https_url":                  "wss://" + endpoint.ServerIdentity + endpoint.HTTPSPath,
		"webrtc_signaling_url":       "https://" + endpoint.ServerIdentity + endpoint.WebRTCPath,
		"profile":                    "web",
		"carrier_policy":             "performance",
		"cover_mode":                 "pulse",
		"max_cover_overhead_percent": 5,
		"initial_window_bytes":       1048576,
		"max_streams":                256,
		"max_parallel_carriers":      3,
		"max_socks_connections":      256,
		"webrtc_timeout":             "5s",
		"https_timeout":              "10s",
		"carrier_cache_ttl":          "10m",
		"enable_constellation":       false,
		"enable_forward_secrecy":     true,
	}
	if endpoint.HTTP3Path != "" {
		document["http3_url"] = "https://" + endpoint.ServerIdentity + endpoint.HTTP3Path
		document["http3_timeout"] = "5s"
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func (m *Manager) clusterServerJSON(path, nodeID, masterNodeID string, paths clusterRuntimeFilePaths) ([]byte, error) {
	if _, err := config.LoadServer(path); err != nil {
		return nil, fmt.Errorf("load server runtime before cluster update: %w", err)
	}
	raw, err := readRegularFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	document["cluster_directory"] = paths.clusterDirectory
	document["cluster_catalog_ttl"] = "1h"
	document["cluster_node_id"] = nodeID
	document["cluster_master_node_id"] = masterNodeID
	document["cluster_peer_directory"] = filepath.Dir(paths.peerDirectory)
	document["cluster_peer_map_file"] = paths.acceptedPeers
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(updated, '\n'), nil
}

func (m *Manager) loadAcceptedPeerDocument(path string) (acceptedPeerDocument, error) {
	raw, exists, err := readOptionalRegular(path)
	if err != nil {
		return acceptedPeerDocument{}, err
	}
	if !exists {
		return acceptedPeerDocument{Version: 1}, nil
	}
	var document acceptedPeerDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || document.Version != 1 {
		return acceptedPeerDocument{}, ErrInvalidState
	}
	return document, nil
}

func validPeerEndpoint(endpoint ClusterPeerEndpoint) bool {
	if !clusterIdentifier(endpoint.NodeID) || endpoint.ServerIdentity == "" || len(endpoint.ServerAddresses) == 0 || len(endpoint.ServerAddresses) > 8 ||
		!privateClusterPath(endpoint.HTTPSPath) || !privateClusterPath(endpoint.WebRTCPath) || endpoint.HTTPSPath == endpoint.WebRTCPath ||
		(endpoint.HTTP3Path != "" && (!privateClusterPath(endpoint.HTTP3Path) || endpoint.HTTP3Path == endpoint.HTTPSPath || endpoint.HTTP3Path == endpoint.WebRTCPath)) {
		return false
	}
	for _, raw := range endpoint.ServerAddresses {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.IsGlobalUnicast() || address.IsPrivate() {
			return false
		}
	}
	return strings.ToLower(endpoint.ServerIdentity) == endpoint.ServerIdentity && strings.Contains(endpoint.ServerIdentity, ".")
}

func privateClusterPath(value string) bool {
	if len(value) != 49 || value[0] != '/' {
		return false
	}
	for _, character := range value[1:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func clusterIdentifier(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || (index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func clusterHasNode(state cluster.State, nodeID string, role cluster.NodeRole) bool {
	for _, node := range state.Nodes {
		if node.ID != nodeID {
			continue
		}
		for _, candidate := range node.Roles {
			if candidate == role {
				return true
			}
		}
	}
	return false
}

func readOptionalRegular(path string) ([]byte, bool, error) {
	raw, err := readRegularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

func rollbackOptional(path string, raw []byte, existed bool) {
	if existed {
		_ = replaceFile(path, raw, 0o600)
	} else {
		_ = os.Remove(path)
	}
}

func readFullNonZero(reader interface{ Read([]byte) (int, error) }, destination []byte) (int, error) {
	read := 0
	for read < len(destination) {
		count, err := reader.Read(destination[read:])
		read += count
		if err != nil {
			return read, err
		}
		if count == 0 {
			return read, errors.New("random source returned no data")
		}
	}
	allZero := true
	for _, value := range destination {
		allZero = allZero && value == 0
	}
	if allZero {
		return read, errors.New("random source returned zero material")
	}
	return read, nil
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
