package clusterrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/proxy"
)

const (
	defaultStateCacheTTL         = 30 * time.Second
	defaultStateFetchTimeout     = 5 * time.Second
	defaultStateMaximumStaleness = 5 * time.Minute
)

type StateFetcher func(context.Context) ([]byte, error)

// StateCache keeps an edge node's routing decisions aligned with the
// authoritative master without putting a control-plane round trip on every
// user flow.
type StateCache struct {
	nodeID       string
	masterNodeID string
	fetch        StateFetcher
	ttl          time.Duration
	maximumStale time.Duration
	timeout      time.Duration
	now          func() time.Time

	mu          sync.Mutex
	state       cluster.State
	refreshedAt time.Time
	clusterID   string
}

func NewStateCache(nodeID, masterNodeID string, fetch StateFetcher) (*StateCache, error) {
	if !identifier(nodeID) || !identifier(masterNodeID) || nodeID == masterNodeID || fetch == nil {
		return nil, ErrInvalidConfig
	}
	return &StateCache{
		nodeID: nodeID, masterNodeID: masterNodeID, fetch: fetch,
		ttl: defaultStateCacheTTL, maximumStale: defaultStateMaximumStaleness,
		timeout: defaultStateFetchTimeout, now: time.Now,
	}, nil
}

func (cache *StateCache) Load() (cluster.State, error) {
	if cache == nil || cache.fetch == nil || cache.now == nil {
		return cluster.State{}, ErrInvalidConfig
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now().UTC()
	if !cache.refreshedAt.IsZero() && now.Sub(cache.refreshedAt) < cache.ttl {
		return cache.state, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), cache.timeout)
	payload, err := cache.fetch(ctx)
	cancel()
	if err != nil {
		if !cache.refreshedAt.IsZero() && now.Sub(cache.refreshedAt) <= cache.maximumStale {
			return cache.state, nil
		}
		return cluster.State{}, fmt.Errorf("%w: fetch authoritative cluster state: %v", ErrPeerUnavailable, err)
	}
	state, err := decodeClusterState(payload)
	if err != nil || !cache.accepts(state) {
		return cluster.State{}, fmt.Errorf("%w: invalid authoritative cluster state", ErrInvalidConfig)
	}
	cache.state = state
	cache.refreshedAt = now
	cache.clusterID = state.ClusterID
	return cache.state, nil
}

func (cache *StateCache) accepts(state cluster.State) bool {
	if cache.clusterID != "" && state.ClusterID != cache.clusterID {
		return false
	}
	if cache.state.Revision != 0 && state.Revision < cache.state.Revision {
		return false
	}
	masterFound, currentFound := false, false
	for _, node := range state.Nodes {
		if node.ID == cache.masterNodeID {
			masterFound = node.Enabled && hasNodeRole(node.Roles, cluster.RoleMaster)
		}
		if node.ID == cache.nodeID {
			currentFound = node.Enabled
		}
	}
	return masterFound && currentFound
}

func decodeClusterState(payload []byte) (cluster.State, error) {
	if len(payload) == 0 || len(payload) > proxy.MaxClusterStatePayload {
		return cluster.State{}, ErrInvalidConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var state cluster.State
	if err := decoder.Decode(&state); err != nil {
		return cluster.State{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return cluster.State{}, ErrInvalidConfig
	}
	if err := cluster.ValidateState(state); err != nil {
		return cluster.State{}, err
	}
	return state, nil
}

func hasNodeRole(roles []cluster.NodeRole, expected cluster.NodeRole) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}
