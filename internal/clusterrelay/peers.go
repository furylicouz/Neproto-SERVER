package clusterrelay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/proxy"
	"neproto.local/chameleon/internal/session"
)

const maxPeerConfigBytes = 256 << 10

type ConnectFunc func(context.Context, config.Client) (*session.Authenticated, error)

type PeerPool struct {
	configs map[string]config.Client
	connect ConnectFunc

	mu       sync.Mutex
	sessions map[string]*session.Authenticated
	closed   bool
}

func LoadPeerConfigs(directory string) (map[string]config.Client, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidConfig
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) > cluster.MaxNodes {
		return nil, ErrInvalidConfig
	}
	loaded := make(map[string]config.Client, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !identifier(entry.Name()) {
			return nil, ErrInvalidConfig
		}
		peerDirectory := filepath.Join(directory, entry.Name())
		clientPath := filepath.Join(peerDirectory, "client.json")
		client, err := config.LoadClient(clientPath)
		if err != nil || filepath.Clean(client.SecretFile) != filepath.Join(peerDirectory, "secret") {
			return nil, ErrInvalidConfig
		}
		client.SOCKSListen = "127.0.0.1:0"
		loaded[entry.Name()] = client
	}
	return loaded, nil
}

type acceptedPeerFile struct {
	Version int            `json:"version"`
	Peers   []acceptedPeer `json:"peers"`
}

type acceptedPeer struct {
	CredentialID string `json:"credential_id"`
	NodeID       string `json:"node_id"`
}

func LoadAcceptedPeers(path string) (map[string]string, error) {
	raw, err := readStrictRegular(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file acceptedPeerFile
	if err := decoder.Decode(&file); err != nil {
		return nil, ErrInvalidConfig
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || file.Version != 1 || len(file.Peers) == 0 || len(file.Peers) > cluster.MaxNodes {
		return nil, ErrInvalidConfig
	}
	result := make(map[string]string, len(file.Peers))
	seenNodes := make(map[string]struct{}, len(file.Peers))
	for _, peer := range file.Peers {
		rawID, err := base64.RawURLEncoding.DecodeString(peer.CredentialID)
		if err != nil || len(rawID) != 16 || base64.RawURLEncoding.EncodeToString(rawID) != peer.CredentialID || !identifier(peer.NodeID) {
			return nil, ErrInvalidConfig
		}
		if _, duplicate := result[peer.CredentialID]; duplicate {
			return nil, ErrInvalidConfig
		}
		if _, duplicate := seenNodes[peer.NodeID]; duplicate {
			return nil, ErrInvalidConfig
		}
		result[peer.CredentialID] = peer.NodeID
		seenNodes[peer.NodeID] = struct{}{}
	}
	return result, nil
}

func NewPeerPool(configs map[string]config.Client, connect ConnectFunc) (*PeerPool, error) {
	if len(configs) == 0 || len(configs) > cluster.MaxNodes || connect == nil {
		return nil, ErrInvalidConfig
	}
	copyConfigs := make(map[string]config.Client, len(configs))
	for nodeID, client := range configs {
		if !identifier(nodeID) {
			return nil, ErrInvalidConfig
		}
		copyConfigs[nodeID] = client
	}
	return &PeerPool{configs: copyConfigs, connect: connect, sessions: make(map[string]*session.Authenticated)}, nil
}

func (pool *PeerPool) Open(ctx context.Context, nodeID string, request cluster.RelayRequest) (proxy.DuplexStream, error) {
	if pool == nil || ctx == nil || !identifier(nodeID) {
		return nil, ErrInvalidConfig
	}
	metadata, err := proxy.EncodeOpenRequest(proxy.OpenRequest{Command: proxy.CommandClusterRelay, Relay: &request})
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		authenticated, err := pool.session(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		stream, err := authenticated.Mux.Open(ctx, metadata)
		if err == nil {
			return stream, nil
		}
		pool.evict(nodeID, authenticated)
	}
	return nil, ErrPeerUnavailable
}

func (pool *PeerPool) FetchCatalog(ctx context.Context, nodeID, userID string) ([]byte, error) {
	if pool == nil || ctx == nil || !identifier(nodeID) {
		return nil, ErrInvalidConfig
	}
	for attempt := 0; attempt < 2; attempt++ {
		authenticated, err := pool.session(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		payload, err := proxy.FetchRelayedCatalog(ctx, authenticated.Mux, userID)
		if err == nil {
			return payload, nil
		}
		pool.evict(nodeID, authenticated)
	}
	return nil, ErrPeerUnavailable
}

func (pool *PeerPool) FetchState(ctx context.Context, nodeID string) ([]byte, error) {
	if pool == nil || ctx == nil || !identifier(nodeID) {
		return nil, ErrInvalidConfig
	}
	for attempt := 0; attempt < 2; attempt++ {
		authenticated, err := pool.session(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		payload, err := proxy.FetchClusterState(ctx, authenticated.Mux)
		if err == nil {
			return payload, nil
		}
		pool.evict(nodeID, authenticated)
	}
	return nil, ErrPeerUnavailable
}

func (pool *PeerPool) GeoData(ctx context.Context, nodeID string, request cluster.GeoDataRequest) ([]byte, error) {
	if pool == nil || ctx == nil || !identifier(nodeID) || cluster.ValidateGeoDataRequest(request) != nil {
		return nil, ErrInvalidConfig
	}
	for attempt := 0; attempt < 2; attempt++ {
		authenticated, err := pool.session(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		payload, err := proxy.FetchGeoDataControl(ctx, authenticated.Mux, request)
		if err == nil {
			return payload, nil
		}
		pool.evict(nodeID, authenticated)
	}
	return nil, ErrPeerUnavailable
}

func (pool *PeerPool) SyncCredential(ctx context.Context, nodeID string, request cluster.CredentialSyncRequest) error {
	if pool == nil || ctx == nil || !identifier(nodeID) || cluster.ValidateCredentialSync(request) != nil {
		return ErrInvalidConfig
	}
	metadata, err := proxy.EncodeOpenRequest(proxy.OpenRequest{Command: proxy.CommandClusterCredentialSync, CredentialSync: &request})
	if err != nil {
		return err
	}
	failures := make([]error, 0, 2)
	for attempt := 0; attempt < 2; attempt++ {
		authenticated, err := pool.session(ctx, nodeID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		stream, err := authenticated.Mux.Open(ctx, metadata)
		if err == nil {
			var acknowledgement [1]byte
			_, readErr := io.ReadFull(stream, acknowledgement[:])
			_ = stream.Close()
			if readErr != nil {
				return fmt.Errorf("%w: read credential acknowledgement: %v", ErrPeerUnavailable, readErr)
			}
			if acknowledgement[0] != 1 {
				return fmt.Errorf("%w: invalid credential acknowledgement", ErrPeerUnavailable)
			}
			return nil
		}
		failures = append(failures, fmt.Errorf("open credential sync stream: %w", err))
		pool.evict(nodeID, authenticated)
	}
	return errors.Join(append([]error{ErrPeerUnavailable}, failures...)...)
}

func (pool *PeerPool) session(ctx context.Context, nodeID string) (*session.Authenticated, error) {
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return nil, ErrPeerUnavailable
	}
	if existing := pool.sessions[nodeID]; existing != nil {
		pool.mu.Unlock()
		return existing, nil
	}
	client, exists := pool.configs[nodeID]
	pool.mu.Unlock()
	if !exists {
		return nil, ErrPeerUnavailable
	}
	authenticated, err := pool.connect(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("connect relay peer %s: %w", nodeID, err)
	}
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		_ = authenticated.Mux.Close()
		return nil, ErrPeerUnavailable
	}
	if existing := pool.sessions[nodeID]; existing != nil {
		pool.mu.Unlock()
		_ = authenticated.Mux.Close()
		return existing, nil
	}
	pool.sessions[nodeID] = authenticated
	pool.mu.Unlock()
	return authenticated, nil
}

func (pool *PeerPool) evict(nodeID string, expected *session.Authenticated) {
	pool.mu.Lock()
	if pool.sessions[nodeID] == expected {
		delete(pool.sessions, nodeID)
	}
	pool.mu.Unlock()
	_ = expected.Mux.Close()
}

func (pool *PeerPool) Close() error {
	if pool == nil {
		return nil
	}
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return nil
	}
	pool.closed = true
	sessions := make([]*session.Authenticated, 0, len(pool.sessions))
	for _, authenticated := range pool.sessions {
		sessions = append(sessions, authenticated)
	}
	pool.sessions = nil
	pool.mu.Unlock()
	var failures []error
	for _, authenticated := range sessions {
		failures = append(failures, authenticated.Mux.Close())
	}
	return errors.Join(failures...)
}

func DialTarget(ctx context.Context, target proxy.Target) (proxy.DuplexStream, error) {
	addresses, err := (proxy.DestinationPolicy{}).Resolve(ctx, target, net.DefaultResolver)
	if err != nil {
		return nil, err
	}
	var last error
	for _, address := range addresses {
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", netip.AddrPortFrom(address, target.Port).String())
		if err == nil {
			if tcp, ok := connection.(*net.TCPConn); ok {
				return tcp, nil
			}
			_ = connection.Close()
			return nil, ErrPeerUnavailable
		}
		last = err
	}
	if last == nil {
		last = ErrPeerUnavailable
	}
	return nil, last
}

func readStrictRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxPeerConfigBytes {
		return nil, ErrInvalidConfig
	}
	return os.ReadFile(path)
}
