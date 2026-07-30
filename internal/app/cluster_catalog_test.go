package app

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
)

func TestClusterCatalogHandlerBuildsFreshSignedPerUserCatalog(t *testing.T) {
	directory := t.TempDir()
	store, err := cluster.OpenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	state := cluster.State{
		Version: cluster.StateVersion, ClusterID: "cluster-01", Revision: 3, UpdatedAt: now,
		Nodes:  []cluster.Node{{ID: "master", Name: "Master", Region: "Moscow", Roles: []cluster.NodeRole{cluster.RoleMaster, cluster.RoleIngress}, PublicIdentity: "vpn.example.com", PublicAddresses: []string{"8.8.8.8"}, NP2Endpoint: "vpn.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "node-master", HostKeySHA256: "SHA256:master", ProvisionedAt: now, UpdatedAt: now}},
		Access: []cluster.UserAccess{{UserID: "alice", AllowedNodeIDs: []string{"master"}, AllowAutoSelection: true, Revision: 1}},
	}
	if err := store.Initialize(state); err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := store.LoadOrCreateSigningKey(bytes.NewReader(bytes.Repeat([]byte{0x44}, ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newClusterCatalogHandler(directory, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatalf("newClusterCatalogHandler() error = %v", err)
	}
	raw, err := handler(t.Context(), "alice")
	if err != nil {
		t.Fatalf("catalog handler error = %v", err)
	}
	catalog, err := cluster.DecodeAndVerifyCatalogEnvelope(raw, publicKey, "cluster-01", 3, now.Add(time.Minute))
	if err != nil || catalog.Revision != 3 {
		t.Fatalf("DecodeAndVerifyCatalogEnvelope() = %+v, %v", catalog, err)
	}
}

func TestClusterCatalogHandlerIsDisabledWithoutDirectory(t *testing.T) {
	handler, err := newClusterCatalogHandler("", 0, time.Now)
	if err != nil || handler != nil {
		t.Fatalf("disabled handler = %v, error = %v", handler, err)
	}
}
