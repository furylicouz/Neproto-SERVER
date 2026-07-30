package clusterrelay

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
)

func TestStateCacheValidatesPinsAndCachesMasterState(t *testing.T) {
	state := relayState()
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	cache, err := NewStateCache("edge-01", "master", func(context.Context) ([]byte, error) {
		fetches++
		return append([]byte(nil), payload...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := cache.Load()
	if err != nil || first.ClusterID != state.ClusterID || first.Revision != state.Revision {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := cache.Load()
	if err != nil || second.Revision != state.Revision || fetches != 1 {
		t.Fatalf("second=%+v fetches=%d err=%v", second, fetches, err)
	}
}

func TestStateCacheRejectsWrongMasterAndRevisionRollback(t *testing.T) {
	state := relayState()
	wrongMaster := state
	wrongMaster.Nodes = append([]cluster.Node(nil), state.Nodes...)
	wrongMaster.Nodes[0].ID = "other-master"
	wrongMaster.Nodes[0].CredentialID = "other-master-control"
	wrongMaster.Access = nil
	wrongMaster.Routes = nil
	if err := cluster.ValidateState(wrongMaster); err != nil {
		t.Fatalf("test state invalid: %v", err)
	}
	payload, _ := json.Marshal(wrongMaster)
	cache, err := NewStateCache("edge-01", "master", func(context.Context) ([]byte, error) { return payload, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("wrong master error=%v", err)
	}

	now := time.Now().UTC()
	state.Revision = 2
	validPayload, _ := json.Marshal(state)
	rollback := state
	rollback.Revision = 1
	rollback.UpdatedAt = now
	rollbackPayload, _ := json.Marshal(rollback)
	responses := [][]byte{validPayload, rollbackPayload}
	index := 0
	cache, err = NewStateCache("edge-01", "master", func(context.Context) ([]byte, error) {
		payload := responses[index]
		index++
		return payload, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cache.ttl = 0
	if _, err := cache.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("rollback error=%v", err)
	}
}
