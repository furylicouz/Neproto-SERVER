package admin

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/onboarding"
)

func TestManagerInitializesAndMutatesClusterAndBuildsRestrictedCatalog(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	manager, err := Open(root, bytes.NewReader(bytes.Repeat([]byte{0x42}, 1024)), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.AddUser("Alice", "web")
	if err != nil {
		t.Fatal(err)
	}
	master := cluster.Node{
		ID: "master", Name: "Master", Region: "Moscow", Roles: []cluster.NodeRole{cluster.RoleMaster, cluster.RoleIngress},
		PublicIdentity: "vpn.example.com", PublicAddresses: []string{"8.8.8.8"}, NP2Endpoint: "vpn.example.com:443",
		HTTPSPath: "/private_https_route_0123456789", WebRTCPath: "/private_webrtc_route_0123456789",
		HTTP3Path: "/private_http3_route_01234567890", RequireDatagrams: true, Enabled: true, ClientVisible: true,
		CredentialID: "node-master", HostKeySHA256: "SHA256:master", ProvisionedAt: now, UpdatedAt: now,
	}
	if _, err := manager.InitializeCluster("cluster-01", master); err != nil {
		t.Fatalf("InitializeCluster() error = %v", err)
	}
	edge := cluster.Node{
		ID: "edge", Name: "Edge", Region: "Helsinki", Roles: []cluster.NodeRole{cluster.RoleRelay, cluster.RoleEgress},
		PublicIdentity: "edge.example.com", PublicAddresses: []string{"1.1.1.1"}, NP2Endpoint: "edge.example.com:443",
		Enabled: true, ClientVisible: true, CredentialID: "node-edge", HostKeySHA256: "SHA256:edge",
		ProvisionedAt: now, UpdatedAt: now,
	}
	if _, err := manager.UpsertClusterNode(edge); err != nil {
		t.Fatalf("UpsertClusterNode() error = %v", err)
	}
	route := cluster.Route{ID: "media", Name: "Media", Priority: 10, Enabled: true, Source: cluster.RouteSourceAdmin, Match: cluster.RouteMatch{DomainSuffixes: []string{"youtube.com"}, Protocols: []cluster.NetworkProtocol{cluster.ProtocolTCP}}, Action: cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{"edge"}}}
	if _, err := manager.UpsertClusterRoute(route); err != nil {
		t.Fatalf("UpsertClusterRoute() error = %v", err)
	}
	access := cluster.UserAccess{UserID: user.ID, AllowedNodeIDs: []string{"master", "edge"}, AllowedRouteIDs: []string{"media"}, AllowAutoSelection: true, AllowClientRoutes: true, Revision: 1}
	if _, err := manager.SetClusterUserAccess(access); err != nil {
		t.Fatalf("SetClusterUserAccess() error = %v", err)
	}

	catalog, publicKey, err := manager.SignedClusterCatalog(user.ID, time.Hour)
	if err != nil {
		t.Fatalf("SignedClusterCatalog() error = %v", err)
	}
	if len(publicKey) != ed25519.PublicKeySize || len(catalog.Servers) != 2 || len(catalog.AdminRoutes) != 1 {
		t.Fatalf("unexpected catalog/public key: %+v key=%d", catalog, len(publicKey))
	}
	if err := cluster.VerifyCatalog(catalog, publicKey, "cluster-01", 1, now.Add(time.Minute)); err != nil {
		t.Fatalf("VerifyCatalog() error = %v", err)
	}
	uri, err := manager.ExportUserURI(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	onboardingProfile, err := onboarding.DecodeURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	if onboardingProfile.ClusterID != "cluster-01" || onboardingProfile.CatalogPublicKey == "" {
		t.Fatalf("onboarding cluster pin missing: %+v", onboardingProfile)
	}
}

func TestManagerRejectsAccessForUnknownOrRevokedUser(t *testing.T) {
	root := t.TempDir()
	writeInstallation(t, root)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	manager, err := Open(root, bytes.NewReader(bytes.Repeat([]byte{0x33}, 1024)), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	master := cluster.Node{ID: "master", Name: "Master", Region: "Moscow", Roles: []cluster.NodeRole{cluster.RoleMaster}, PublicIdentity: "vpn.example.com", PublicAddresses: []string{"8.8.8.8"}, NP2Endpoint: "vpn.example.com:443", Enabled: true, ClientVisible: true, CredentialID: "node-master", HostKeySHA256: "SHA256:master", ProvisionedAt: now, UpdatedAt: now}
	if _, err := manager.InitializeCluster("cluster-01", master); err != nil {
		t.Fatal(err)
	}
	access := cluster.UserAccess{UserID: "unknown", AllowedNodeIDs: []string{"master"}, Revision: 1}
	if _, err := manager.SetClusterUserAccess(access); err == nil {
		t.Fatal("accepted cluster access for unknown user")
	}
}
