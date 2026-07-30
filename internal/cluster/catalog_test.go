package cluster

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func TestSignAndVerifyCatalogBindsContentClusterAndRevision(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	catalog := Catalog{
		Version: CatalogVersion, ClusterID: "cluster-01", Revision: 7,
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), UserID: "alice",
		Servers:     []CatalogServer{{NodeID: "edge", Name: "Edge", Region: "Helsinki", ServerIdentity: "edge.example.com", ServerAddresses: []string{"203.0.113.10"}, Enabled: true}},
		Permissions: CatalogPermissions{AllowAutoSelection: true, AllowClientRoutes: true},
	}
	signed, err := SignCatalog(catalog, privateKey)
	if err != nil {
		t.Fatalf("SignCatalog() error = %v", err)
	}
	if err := VerifyCatalog(signed, publicKey, "cluster-01", 6, now.Add(time.Minute)); err != nil {
		t.Fatalf("VerifyCatalog() error = %v", err)
	}

	tampered := signed
	tampered.Servers = append([]CatalogServer(nil), signed.Servers...)
	tampered.Servers[0].Region = "Tampered"
	if err := VerifyCatalog(tampered, publicKey, "cluster-01", 6, now.Add(time.Minute)); !errors.Is(err, ErrInvalidCatalogSignature) {
		t.Fatalf("VerifyCatalog(tampered) error = %v", err)
	}
	if err := VerifyCatalog(signed, publicKey, "another", 6, now.Add(time.Minute)); !errors.Is(err, ErrCatalogClusterMismatch) {
		t.Fatalf("VerifyCatalog(cluster mismatch) error = %v", err)
	}
	if err := VerifyCatalog(signed, publicKey, "cluster-01", 8, now.Add(time.Minute)); !errors.Is(err, ErrCatalogRollback) {
		t.Fatalf("VerifyCatalog(rollback) error = %v", err)
	}
	if err := VerifyCatalog(signed, publicKey, "cluster-01", 6, now.Add(2*time.Hour)); !errors.Is(err, ErrCatalogExpired) {
		t.Fatalf("VerifyCatalog(expired) error = %v", err)
	}
}

func TestCatalogEnvelopePreservesExactSignedPayload(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	catalog := Catalog{Version: CatalogVersion, ClusterID: "cluster-01", Revision: 9, IssuedAt: now, ExpiresAt: now.Add(time.Hour), UserID: "alice", Servers: []CatalogServer{{NodeID: "master", Name: "Master", Region: "Moscow", ServerIdentity: "vpn.example.com", ServerAddresses: []string{"8.8.8.8"}, Enabled: true}}}
	signed, err := SignCatalog(catalog, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeCatalogEnvelope(signed)
	if err != nil {
		t.Fatalf("EncodeCatalogEnvelope() error = %v", err)
	}
	decoded, err := DecodeAndVerifyCatalogEnvelope(raw, publicKey, "cluster-01", 9, now.Add(time.Minute))
	if err != nil || decoded.Revision != 9 || decoded.UserID != "alice" {
		t.Fatalf("DecodeAndVerifyCatalogEnvelope() = %+v, %v", decoded, err)
	}
	raw[len(raw)/2] ^= 1
	if _, err := DecodeAndVerifyCatalogEnvelope(raw, publicKey, "cluster-01", 9, now.Add(time.Minute)); err == nil {
		t.Fatal("tampered envelope verified")
	}
}

func TestBuildCatalogFiltersNodesRoutesAndNeverPublishesCredentials(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	state := testState(now)
	state.Nodes = append(state.Nodes, Node{ID: "hidden", Name: "Hidden", Region: "Private", Roles: []NodeRole{RoleEgress}, PublicIdentity: "hidden.example.com", PublicAddresses: []string{"192.0.2.40"}, NP2Endpoint: "hidden.example.com:443", Enabled: true, ClientVisible: false, CredentialID: "must-not-leak", HostKeySHA256: "SHA256:hidden", ProvisionedAt: now, UpdatedAt: now})

	catalog, err := BuildCatalog(state, "alice", now, time.Hour)
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	if len(catalog.Servers) != 2 || len(catalog.AdminRoutes) != 1 {
		t.Fatalf("unexpected filtered catalog: %+v", catalog)
	}
	encoded, err := canonicalCatalogBytes(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if containsCredentialMaterial(encoded) {
		t.Fatalf("catalog leaked credential material: %s", encoded)
	}
}

func TestBuildCatalogPublishesGeoSelectorsAndKeepsLegacyFailSafe(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	state := testState(now)
	state.Routes[0].Match = RouteMatch{
		GeoSiteCategories: []string{"youtube"},
		Protocols:         []NetworkProtocol{ProtocolTCP, ProtocolUDP},
	}
	catalog, err := BuildCatalog(state, "alice", now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.AdminRoutes) != 1 {
		t.Fatalf("routes=%+v", catalog.AdminRoutes)
	}
	match := catalog.AdminRoutes[0].Match
	if len(match.GeoSiteCategories) != 1 || match.GeoSiteCategories[0] != "youtube" || len(match.GeoIPCountries) != 0 ||
		len(match.DomainSuffixes) != 1 || match.DomainSuffixes[0] != geoClientFallbackDomain {
		t.Fatalf("missing geodata selector or legacy fail-safe: %+v", match)
	}
	if len(state.Routes[0].Match.GeoSiteCategories) != 1 {
		t.Fatalf("BuildCatalog mutated authoritative state: %+v", state.Routes[0].Match)
	}
}
