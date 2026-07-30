package cluster

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	CatalogVersion          = 1
	MaxCatalogBytes         = 256 << 10
	MaxCatalogTTL           = 24 * time.Hour
	geoClientFallbackDomain = "np2-geodata-never-match.invalid"
)

var (
	ErrInvalidCatalog          = errors.New("invalid cluster catalog")
	ErrInvalidCatalogSignature = errors.New("invalid cluster catalog signature")
	ErrCatalogClusterMismatch  = errors.New("cluster catalog identity mismatch")
	ErrCatalogRollback         = errors.New("cluster catalog revision rollback")
	ErrCatalogExpired          = errors.New("cluster catalog expired")
)

type CatalogServer struct {
	NodeID           string   `json:"node_id"`
	Name             string   `json:"name"`
	Region           string   `json:"region"`
	ServerIdentity   string   `json:"server_identity"`
	ServerAddresses  []string `json:"server_addresses"`
	HTTPSPath        string   `json:"https_path,omitempty"`
	WebRTCPath       string   `json:"webrtc_path,omitempty"`
	HTTP3Path        string   `json:"http3_path,omitempty"`
	RequireDatagrams bool     `json:"require_datagrams,omitempty"`
	Enabled          bool     `json:"enabled"`
}

type CatalogPermissions struct {
	AllowAutoSelection bool `json:"allow_auto_selection"`
	AllowClientRoutes  bool `json:"allow_client_routes"`
}

type Catalog struct {
	Version     int                `json:"version"`
	ClusterID   string             `json:"cluster_id"`
	Revision    uint64             `json:"revision"`
	IssuedAt    time.Time          `json:"issued_at"`
	ExpiresAt   time.Time          `json:"expires_at"`
	UserID      string             `json:"user_id"`
	Servers     []CatalogServer    `json:"servers"`
	AdminRoutes []Route            `json:"admin_routes"`
	Permissions CatalogPermissions `json:"permissions"`
	Signature   string             `json:"signature"`
}

type unsignedCatalog struct {
	Version     int                `json:"version"`
	ClusterID   string             `json:"cluster_id"`
	Revision    uint64             `json:"revision"`
	IssuedAt    time.Time          `json:"issued_at"`
	ExpiresAt   time.Time          `json:"expires_at"`
	UserID      string             `json:"user_id"`
	Servers     []CatalogServer    `json:"servers"`
	AdminRoutes []Route            `json:"admin_routes"`
	Permissions CatalogPermissions `json:"permissions"`
}

type CatalogEnvelope struct {
	Version   int    `json:"version"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

func EncodeCatalogEnvelope(catalog Catalog) ([]byte, error) {
	payload, err := canonicalCatalogBytes(catalog)
	if err != nil || len(payload) == 0 || len(payload) > MaxCatalogBytes {
		return nil, ErrInvalidCatalog
	}
	signature, err := base64.RawURLEncoding.DecodeString(catalog.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, ErrInvalidCatalogSignature
	}
	envelope := CatalogEnvelope{
		Version: CatalogVersion, Payload: base64.RawURLEncoding.EncodeToString(payload),
		Signature: catalog.Signature,
	}
	raw, err := json.Marshal(envelope)
	if err != nil || len(raw) > MaxCatalogBytes {
		return nil, ErrInvalidCatalog
	}
	return raw, nil
}

func DecodeAndVerifyCatalogEnvelope(raw []byte, publicKey ed25519.PublicKey, clusterID string, minimumRevision uint64, now time.Time) (Catalog, error) {
	if len(raw) == 0 || len(raw) > MaxCatalogBytes {
		return Catalog{}, ErrInvalidCatalog
	}
	if err := rejectDuplicateEnvelopeFields(raw); err != nil {
		return Catalog{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope CatalogEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return Catalog{}, ErrInvalidCatalog
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Catalog{}, ErrInvalidCatalog
	}
	if envelope.Version != CatalogVersion {
		return Catalog{}, ErrInvalidCatalog
	}
	payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if err != nil || len(payload) == 0 || len(payload) > MaxCatalogBytes {
		return Catalog{}, ErrInvalidCatalog
	}
	unsignedDecoder := json.NewDecoder(bytes.NewReader(payload))
	unsignedDecoder.DisallowUnknownFields()
	var unsigned unsignedCatalog
	if err := unsignedDecoder.Decode(&unsigned); err != nil {
		return Catalog{}, ErrInvalidCatalog
	}
	if err := unsignedDecoder.Decode(&trailing); err != io.EOF {
		return Catalog{}, ErrInvalidCatalog
	}
	catalog := Catalog{
		Version: unsigned.Version, ClusterID: unsigned.ClusterID, Revision: unsigned.Revision,
		IssuedAt: unsigned.IssuedAt, ExpiresAt: unsigned.ExpiresAt, UserID: unsigned.UserID,
		Servers: unsigned.Servers, AdminRoutes: unsigned.AdminRoutes,
		Permissions: unsigned.Permissions, Signature: envelope.Signature,
	}
	if err := VerifyCatalog(catalog, publicKey, clusterID, minimumRevision, now); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func rejectDuplicateEnvelopeFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalidCatalog
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return ErrInvalidCatalog
		}
		if _, exists := seen[key]; exists {
			return ErrInvalidCatalog
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return ErrInvalidCatalog
		}
	}
	if _, err := decoder.Token(); err != nil {
		return ErrInvalidCatalog
	}
	return nil
}

func BuildCatalog(state State, userID string, now time.Time, ttl time.Duration) (Catalog, error) {
	if err := ValidateState(state); err != nil || !validOpaqueID(userID, 128) || now.IsZero() || ttl <= 0 || ttl > MaxCatalogTTL {
		return Catalog{}, ErrInvalidCatalog
	}
	var access *UserAccess
	for index := range state.Access {
		if state.Access[index].UserID == userID {
			access = &state.Access[index]
			break
		}
	}
	if access == nil {
		return Catalog{}, ErrInvalidCatalog
	}
	allowedNodes := make(map[string]struct{}, len(access.AllowedNodeIDs))
	for _, id := range access.AllowedNodeIDs {
		allowedNodes[id] = struct{}{}
	}
	allowedRoutes := make(map[string]struct{}, len(access.AllowedRouteIDs))
	for _, id := range access.AllowedRouteIDs {
		allowedRoutes[id] = struct{}{}
	}
	catalog := Catalog{
		Version: CatalogVersion, ClusterID: state.ClusterID, Revision: state.Revision,
		IssuedAt: now.UTC(), ExpiresAt: now.Add(ttl).UTC(), UserID: userID,
		Permissions: CatalogPermissions{AllowAutoSelection: access.AllowAutoSelection, AllowClientRoutes: access.AllowClientRoutes},
	}
	for _, node := range state.Nodes {
		if _, permitted := allowedNodes[node.ID]; !permitted || !node.ClientVisible {
			continue
		}
		catalog.Servers = append(catalog.Servers, CatalogServer{
			NodeID: node.ID, Name: node.Name, Region: node.Region, ServerIdentity: node.PublicIdentity,
			ServerAddresses: append([]string(nil), node.PublicAddresses...), HTTPSPath: node.HTTPSPath,
			WebRTCPath: node.WebRTCPath, HTTP3Path: node.HTTP3Path,
			RequireDatagrams: node.RequireDatagrams, Enabled: node.Enabled,
		})
	}
	for _, route := range state.Routes {
		if _, permitted := allowedRoutes[route.ID]; permitted && route.Source == RouteSourceAdmin {
			clientRoute := cloneRoute(route)
			// GeoIP and GeoSite are resolved authoritatively by the server. Publish
			// their selectors so current clients can describe the rule, but retain
			// an impossible reserved suffix for catalog-v1 clients that ignore the
			// new fields. This keeps old clients fail-closed without hiding the real
			// administrator policy from current clients.
			if len(clientRoute.Match.GeoIPCountries) > 0 || len(clientRoute.Match.GeoSiteCategories) > 0 {
				clientRoute.Match.DomainSuffixes = append(clientRoute.Match.DomainSuffixes, geoClientFallbackDomain)
			}
			catalog.AdminRoutes = append(catalog.AdminRoutes, clientRoute)
		}
	}
	catalog.AdminRoutes = EffectiveRoutes(catalog.AdminRoutes, nil, false)
	if len(catalog.Servers) == 0 {
		return Catalog{}, ErrInvalidCatalog
	}
	return catalog, nil
}

func SignCatalog(catalog Catalog, privateKey ed25519.PrivateKey) (Catalog, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Catalog{}, ErrInvalidCatalog
	}
	catalog.Signature = ""
	payload, err := canonicalCatalogBytes(catalog)
	if err != nil || len(payload) > MaxCatalogBytes {
		return Catalog{}, ErrInvalidCatalog
	}
	catalog.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return catalog, nil
}

func VerifyCatalog(catalog Catalog, publicKey ed25519.PublicKey, clusterID string, minimumRevision uint64, now time.Time) error {
	if len(publicKey) != ed25519.PublicKeySize || catalog.Version != CatalogVersion || !validOpaqueID(catalog.UserID, 128) ||
		catalog.Revision == 0 || catalog.IssuedAt.IsZero() || catalog.ExpiresAt.IsZero() || !catalog.ExpiresAt.After(catalog.IssuedAt) ||
		catalog.ExpiresAt.Sub(catalog.IssuedAt) > MaxCatalogTTL || len(catalog.Servers) == 0 || len(catalog.Servers) > MaxNodes || len(catalog.AdminRoutes) > MaxRoutes {
		return ErrInvalidCatalog
	}
	if catalog.ClusterID != clusterID {
		return ErrCatalogClusterMismatch
	}
	if catalog.Revision < minimumRevision {
		return ErrCatalogRollback
	}
	if now.Before(catalog.IssuedAt.Add(-5*time.Minute)) || !now.Before(catalog.ExpiresAt) {
		return ErrCatalogExpired
	}
	signature, err := base64.RawURLEncoding.DecodeString(catalog.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrInvalidCatalogSignature
	}
	payload, err := canonicalCatalogBytes(catalog)
	if err != nil || len(payload) > MaxCatalogBytes || !ed25519.Verify(publicKey, payload, signature) {
		return ErrInvalidCatalogSignature
	}
	return nil
}

func canonicalCatalogBytes(catalog Catalog) ([]byte, error) {
	unsigned := unsignedCatalog{
		Version: catalog.Version, ClusterID: catalog.ClusterID, Revision: catalog.Revision,
		IssuedAt: catalog.IssuedAt.UTC(), ExpiresAt: catalog.ExpiresAt.UTC(), UserID: catalog.UserID,
		Servers: catalog.Servers, AdminRoutes: catalog.AdminRoutes, Permissions: catalog.Permissions,
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("encode catalog: %w", err)
	}
	return payload, nil
}

func containsCredentialMaterial(payload []byte) bool {
	lower := bytes.ToLower(payload)
	return bytes.Contains(lower, []byte("credential")) || bytes.Contains(lower, []byte("host_key")) || bytes.Contains(lower, []byte("password")) || bytes.Contains(lower, []byte("private_key"))
}
