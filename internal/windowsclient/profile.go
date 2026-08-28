package windowsclient

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/onboarding"
)

// Profile is the public, persistable portion of one imported NP/2 profile.
// Secret is deliberately excluded from JSON and is always empty on return.
type Profile struct {
	ID                   string   `json:"id"`
	CredentialID         string   `json:"credential_id"`
	Name                 string   `json:"name"`
	ServerIdentity       string   `json:"server_identity"`
	ServerAddresses      []string `json:"server_addresses"`
	HTTPSPath            string   `json:"https_path"`
	WebRTCPath           string   `json:"webrtc_path"`
	HTTP3Path            string   `json:"http3_path,omitempty"`
	RequireDatagrams     bool     `json:"require_datagrams"`
	MaxParallelCarriers  int      `json:"max_parallel_carriers"`
	EnableConstellation  bool     `json:"enable_constellation"`
	EnableForwardSecrecy bool     `json:"enable_forward_secrecy"`
	ClusterID            string   `json:"cluster_id,omitempty"`
	CatalogPublicKey     string   `json:"catalog_public_key,omitempty"`
	ClusterNodeID        string   `json:"cluster_node_id,omitempty"`
	Region               string   `json:"region,omitempty"`
	ManagedByCluster     bool     `json:"managed_by_cluster,omitempty"`
	ClusterAvailable     bool     `json:"cluster_available"`
	Profile              string   `json:"profile"`
	Secret               string   `json:"-"`
}

type directClientConfig struct {
	ServerIdentity          string   `json:"server_identity"`
	DeviceID                string   `json:"device_id"`
	ServerAddresses         []string `json:"server_addresses"`
	SecretFile              string   `json:"secret_file"`
	HTTP3URL                string   `json:"http3_url,omitempty"`
	Profile                 string   `json:"profile"`
	CarrierPolicy           string   `json:"carrier_policy"`
	CoverMode               string   `json:"cover_mode"`
	MaxCoverOverheadPercent int      `json:"max_cover_overhead_percent"`
	InitialWindowBytes      int      `json:"initial_window_bytes"`
	MaxStreams              int      `json:"max_streams"`
	MaxParallelCarriers     int      `json:"max_parallel_carriers"`
	RequireDatagrams        bool     `json:"require_datagrams,omitempty"`
	EnableConstellation     bool     `json:"enable_constellation,omitempty"`
	EnableForwardSecrecy    bool     `json:"enable_forward_secrecy,omitempty"`
	HTTP3Timeout            string   `json:"http3_timeout,omitempty"`
	CarrierCacheTTL         string   `json:"carrier_cache_ttl"`
}

func ImportURI(uri, deviceID string) (Profile, []byte, string, error) {
	onboarded, err := onboarding.DecodeURI(strings.TrimSpace(uri))
	if err != nil {
		return Profile{}, nil, "", err
	}
	return profileFromOnboarding(onboarded, deviceID)
}

func profileFromOnboarding(source onboarding.Profile, deviceID string) (Profile, []byte, string, error) {
	// Windows temporarily runs one HTTP/3 carrier end-to-end. The other route
	// fields stay persisted for forward compatibility, but are not eligible for
	// initial selection, fallback, pool warming, or migration in this policy.
	parallel := 1
	id := profileIDFor(source.CredentialID, source.ServerIdentity)
	profile := Profile{
		ID: id, CredentialID: source.CredentialID,
		Name: source.Name, ServerIdentity: source.ServerIdentity,
		ServerAddresses: append([]string(nil), source.ServerAddresses...),
		HTTPSPath:       source.HTTPSPath, WebRTCPath: source.WebRTCPath, HTTP3Path: source.HTTP3Path,
		RequireDatagrams: source.RequireDatagrams, MaxParallelCarriers: parallel,
		EnableConstellation: source.EnableConstellation, EnableForwardSecrecy: source.EnableForwardSecrecy,
		ClusterID: source.ClusterID, CatalogPublicKey: source.CatalogPublicKey,
		ClusterAvailable: true, Profile: "web",
	}
	client := directClientConfig{
		ServerIdentity: source.ServerIdentity, DeviceID: deviceID,
		ServerAddresses: append([]string(nil), source.ServerAddresses...), SecretFile: "dpapi",
		Profile: "web", CarrierPolicy: string(config.CarrierPolicyHTTP3Only), CoverMode: string(config.CoverModeOff),
		MaxCoverOverheadPercent: 30,
		InitialWindowBytes:      2_097_152, MaxStreams: 128, MaxParallelCarriers: parallel,
		RequireDatagrams: source.RequireDatagrams, EnableConstellation: source.EnableConstellation,
		EnableForwardSecrecy: source.EnableForwardSecrecy,
		CarrierCacheTTL:      "10m",
	}
	if source.HTTP3Path != "" {
		client.HTTP3URL = "https://" + source.ServerIdentity + source.HTTP3Path
		client.HTTP3Timeout = "5s"
	}
	raw, err := json.Marshal(client)
	if err != nil {
		return Profile{}, nil, "", fmt.Errorf("encode direct NP/2 profile: %w", err)
	}
	return profile, raw, source.Secret, nil
}

func profileIDFor(credentialID, identity string) string {
	digest := sha256.Sum256([]byte(credentialID + "\x00" + identity))
	return base64.RawURLEncoding.EncodeToString(digest[:16])
}

func (p Profile) clientConfiguration(deviceID string) ([]byte, error) {
	source := onboarding.Profile{
		Version: 2, CredentialID: p.CredentialID, Name: p.Name,
		ServerIdentity: p.ServerIdentity, ServerAddresses: append([]string(nil), p.ServerAddresses...),
		HTTPSPath: p.HTTPSPath, WebRTCPath: p.WebRTCPath, HTTP3Path: p.HTTP3Path,
		RequireDatagrams: p.RequireDatagrams, MaxParallelCarriers: p.MaxParallelCarriers,
		EnableConstellation: p.EnableConstellation, EnableForwardSecrecy: p.EnableForwardSecrecy,
		ClusterID: p.ClusterID, CatalogPublicKey: p.CatalogPublicKey, Profile: p.Profile,
		Secret: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
	}
	_, raw, _, err := profileFromOnboarding(source, deviceID)
	return raw, err
}
