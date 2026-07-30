package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/clusterrelay"
	"neproto.local/chameleon/internal/geodata"
)

func TestGeoDataControlAcceptsOnlyMasterAndReloadsAfterSuccessfulUpdate(t *testing.T) {
	updated := false
	reloaded := false
	handler := newGeoDataControlHandler(
		"edge", "master", map[string]string{"peer-master": "master"}, "/etc/neproto/geodata",
		func(_ context.Context, directory string) (geodata.UpdateStatus, error) {
			updated = directory == "/etc/neproto/geodata"
			return geodata.UpdateStatus{State: geodata.UpdateStateReady, UpdatedAt: time.Unix(123, 0), GeoIPSHA256: "ip", GeoSiteSHA256: "site"}, nil
		},
		func(string) (geodata.UpdateStatus, error) { return geodata.UpdateStatus{}, errors.New("not used") },
		func(directory string) error { reloaded = directory == "/etc/neproto/geodata"; return nil },
	)
	payload, err := handler(context.Background(), "peer-master", cluster.GeoDataRequest{Version: 1, Operation: cluster.GeoDataUpdate})
	if err != nil {
		t.Fatal(err)
	}
	var status cluster.GeoDataNodeStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatal(err)
	}
	if !updated || !reloaded || status.NodeID != "edge" || status.State != geodata.UpdateStateReady {
		t.Fatalf("updated=%v reloaded=%v status=%+v", updated, reloaded, status)
	}
	if _, err := handler(context.Background(), "unknown", cluster.GeoDataRequest{Version: 1, Operation: cluster.GeoDataUpdate}); !errors.Is(err, clusterrelay.ErrRelayUnauthorized) {
		t.Fatalf("unauthorized peer error=%v", err)
	}
}

func TestGeoDataControlReturnsSanitizedNodeErrorWithoutReload(t *testing.T) {
	reloaded := false
	handler := newGeoDataControlHandler(
		"edge", "master", map[string]string{"peer-master": "master"}, "/etc/neproto/geodata",
		func(context.Context, string) (geodata.UpdateStatus, error) {
			return geodata.UpdateStatus{}, errors.New("checksum mismatch")
		},
		func(string) (geodata.UpdateStatus, error) { return geodata.UpdateStatus{}, nil },
		func(string) error { reloaded = true; return nil },
	)
	payload, err := handler(context.Background(), "peer-master", cluster.GeoDataRequest{Version: 1, Operation: cluster.GeoDataUpdate})
	if err != nil {
		t.Fatal(err)
	}
	var status cluster.GeoDataNodeStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatal(err)
	}
	if reloaded || status.State != geodata.UpdateStateError || status.Error != "checksum mismatch" {
		t.Fatalf("reloaded=%v status=%+v", reloaded, status)
	}
}
