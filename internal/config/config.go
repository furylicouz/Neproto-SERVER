package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"neproto.local/chameleon/internal/cover"
	"neproto.local/chameleon/internal/credentials"
	"neproto.local/chameleon/internal/protocol"
	"neproto.local/chameleon/internal/session"
)

const (
	maxConfigBytes     = 64 * 1024
	minPrivatePath     = 16
	maxStreams         = 4096
	maxSessions        = 256
	maxGlobalResources = 1_000_000
	maxResourceRate    = 1 << 40
)

var ErrInvalidConfig = errors.New("invalid configuration")

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: duration must be a string", ErrInvalidConfig)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%w: malformed duration", ErrInvalidConfig)
	}
	d.Duration = parsed
	return nil
}

type RootSecret struct {
	value [32]byte
}

func (s RootSecret) Bytes() [32]byte { return s.value }
func (RootSecret) String() string    { return "<redacted>" }
func (RootSecret) GoString() string  { return "<redacted>" }

type CarrierPolicy string

const (
	CarrierPolicyPerformance CarrierPolicy = "performance"
	CarrierPolicyUDPFirst    CarrierPolicy = "udp-first"
	// CarrierPolicyHTTP3Only is the first cross-platform candidate policy. Its
	// client configuration contains no alternate carrier endpoints or timers.
	CarrierPolicyHTTP3Only CarrierPolicy = "http3-only"
	// CarrierPolicyHTTPSOnly is the single-carrier TCP/TLS A/B policy. It keeps
	// HTTP/3 and WebRTC endpoints out of the runtime configuration entirely.
	CarrierPolicyHTTPSOnly CarrierPolicy = "https-only"
)

type Client struct {
	ServerIdentity          string            `json:"server_identity"`
	DeviceID                protocol.DeviceID `json:"device_id,omitempty"`
	ServerAddresses         []netip.Addr      `json:"server_addresses,omitempty"`
	SecretFile              string            `json:"secret_file"`
	SOCKSListen             string            `json:"socks_listen"`
	HTTPSURL                string            `json:"https_url"`
	WebRTCSignalingURL      string            `json:"webrtc_signaling_url"`
	HTTP3URL                string            `json:"http3_url,omitempty"`
	Profile                 string            `json:"profile"`
	CarrierPolicy           CarrierPolicy     `json:"carrier_policy,omitempty"`
	MaxCoverOverheadPercent uint8             `json:"max_cover_overhead_percent"`
	InitialWindowBytes      uint64            `json:"initial_window_bytes"`
	MaxStreams              int               `json:"max_streams"`
	MaxParallelCarriers     int               `json:"max_parallel_carriers,omitempty"`
	MaxSOCKSConnections     int               `json:"max_socks_connections"`
	WebRTCTimeout           Duration          `json:"webrtc_timeout"`
	HTTPSTimeout            Duration          `json:"https_timeout"`
	HTTP3Timeout            Duration          `json:"http3_timeout,omitempty"`
	CarrierCacheTTL         Duration          `json:"carrier_cache_ttl"`
	RequireDatagrams        bool              `json:"require_datagrams,omitempty"`
	EnableConstellation     bool              `json:"enable_constellation,omitempty"`
	EnableForwardSecrecy    bool              `json:"enable_forward_secrecy,omitempty"`
	Secret                  RootSecret        `json:"-"`
}

func (c Client) ProfileID() cover.ProfileID {
	switch c.Profile {
	case "quiet":
		return cover.ProfileQuiet
	case "web":
		return cover.ProfileWeb
	case "interactive":
		return cover.ProfileInteractive
	default:
		return 0
	}
}

func (c Client) HTTP3Configured() bool { return c.HTTP3URL != "" }

func applyClientDefaults(client *Client, directMobile bool) {
	if client == nil {
		return
	}
	if client.CarrierPolicy == "" {
		client.CarrierPolicy = CarrierPolicyPerformance
	}
	if client.MaxParallelCarriers == 0 {
		client.MaxParallelCarriers = 1
		if directMobile && client.CarrierPolicy == CarrierPolicyPerformance && client.Profile != "quiet" {
			client.MaxParallelCarriers = 3
		}
	}
}

type Server struct {
	ServerIdentity          string                   `json:"server_identity"`
	SecretFile              string                   `json:"secret_file,omitempty"`
	CredentialDirectory     string                   `json:"credential_directory,omitempty"`
	UserPolicyFile          string                   `json:"user_policy_file,omitempty"`
	UsageStateFile          string                   `json:"usage_state_file,omitempty"`
	Listen                  string                   `json:"listen"`
	MetricsListen           string                   `json:"metrics_listen,omitempty"`
	ClusterDirectory        string                   `json:"cluster_directory,omitempty"`
	ClusterCatalogTTL       Duration                 `json:"cluster_catalog_ttl,omitempty"`
	ClusterNodeID           string                   `json:"cluster_node_id,omitempty"`
	ClusterMasterNodeID     string                   `json:"cluster_master_node_id,omitempty"`
	ClusterPeerDirectory    string                   `json:"cluster_peer_directory,omitempty"`
	ClusterPeerMapFile      string                   `json:"cluster_peer_map_file,omitempty"`
	GeodataDirectory        string                   `json:"geodata_directory,omitempty"`
	HTTPSPath               string                   `json:"https_path"`
	WebRTCPath              string                   `json:"webrtc_path"`
	EnableHTTP3             bool                     `json:"enable_http3,omitempty"`
	EnableWebRTCDatagrams   bool                     `json:"enable_webrtc_datagrams,omitempty"`
	EnableHTTP3Datagrams    bool                     `json:"enable_http3_datagrams,omitempty"`
	HTTP3Listen             string                   `json:"http3_listen,omitempty"`
	HTTP3Path               string                   `json:"http3_path,omitempty"`
	HTTP3CertFile           string                   `json:"http3_cert_file,omitempty"`
	HTTP3KeyFile            string                   `json:"http3_key_file,omitempty"`
	UDPPortMin              uint16                   `json:"udp_port_min"`
	UDPPortMax              uint16                   `json:"udp_port_max"`
	MaxCoverOverheadPercent uint8                    `json:"max_cover_overhead_percent"`
	InitialWindowBytes      uint64                   `json:"initial_window_bytes"`
	MaxStreams              int                      `json:"max_streams"`
	MaxSessions             int                      `json:"max_sessions"`
	MaxWebRTCPeers          int                      `json:"max_webrtc_peers"`
	MaxHTTP3Sessions        int                      `json:"max_http3_sessions,omitempty"`
	MaxTargetConnections    int                      `json:"max_target_connections"`
	ResourceLimits          ServerResourceLimits     `json:"resource_limits,omitempty"`
	DialTimeout             Duration                 `json:"dial_timeout"`
	GatherTimeout           Duration                 `json:"gather_timeout"`
	ConnectTimeout          Duration                 `json:"connect_timeout"`
	HTTP3HandshakeTimeout   Duration                 `json:"http3_handshake_timeout,omitempty"`
	HTTP3IdleTimeout        Duration                 `json:"http3_idle_timeout,omitempty"`
	ShutdownTimeout         Duration                 `json:"shutdown_timeout"`
	EnableConstellation     bool                     `json:"enable_constellation,omitempty"`
	EnableForwardSecrecy    bool                     `json:"enable_forward_secrecy,omitempty"`
	Secret                  RootSecret               `json:"-"`
	Credentials             []credentials.Credential `json:"-"`
}

// ServerResourceLimits are authenticated-user and process-wide abuse
// ceilings. Zero fields are populated with conservative production defaults
// when a legacy configuration is loaded.
type ServerResourceLimits struct {
	MaxSessionsPerUser            int   `json:"max_sessions_per_user,omitempty"`
	MaxTCPConnectionsGlobal       int   `json:"max_tcp_connections_global,omitempty"`
	MaxTCPConnectionsPerUser      int   `json:"max_tcp_connections_per_user,omitempty"`
	MaxUDPAssociationsGlobal      int   `json:"max_udp_associations_global,omitempty"`
	MaxUDPAssociationsPerUser     int   `json:"max_udp_associations_per_user,omitempty"`
	UDPPacketsPerSecondGlobal     int64 `json:"udp_packets_per_second_global,omitempty"`
	UDPPacketsPerSecondPerUser    int64 `json:"udp_packets_per_second_per_user,omitempty"`
	UDPBytesPerSecondGlobal       int64 `json:"udp_bytes_per_second_global,omitempty"`
	UDPBytesPerSecondPerUser      int64 `json:"udp_bytes_per_second_per_user,omitempty"`
	DNSQueriesPerSecondGlobal     int64 `json:"dns_queries_per_second_global,omitempty"`
	DNSQueriesPerSecondPerUser    int64 `json:"dns_queries_per_second_per_user,omitempty"`
	TargetCreatesPerSecondGlobal  int64 `json:"target_creates_per_second_global,omitempty"`
	TargetCreatesPerSecondPerUser int64 `json:"target_creates_per_second_per_user,omitempty"`
}

func (s Server) HTTP3Configured() bool {
	return s.HTTP3Listen != "" || s.HTTP3Path != "" || s.HTTP3CertFile != "" || s.HTTP3KeyFile != "" ||
		s.MaxHTTP3Sessions != 0 || s.HTTP3HandshakeTimeout.Duration != 0 || s.HTTP3IdleTimeout.Duration != 0
}

func LoadClient(configPath string) (Client, error) {
	var config Client
	if err := loadStrictJSON(configPath, &config); err != nil {
		return Client{}, err
	}
	applyClientDefaults(&config, false)
	config.SecretFile = resolveRelative(configPath, config.SecretFile)
	secret, err := LoadSecret(config.SecretFile)
	if err != nil {
		return Client{}, err
	}
	config.Secret = secret
	if err := validateClient(config); err != nil {
		return Client{}, err
	}
	return config, nil
}

// ParseClientBytes validates an in-memory client profile and its canonical
// root secret. Mobile clients use this path so key material can stay in the
// platform key store instead of being written to a temporary file.
func ParseClientBytes(raw []byte, encodedSecret string) (Client, error) {
	return parseClientBytes(raw, encodedSecret, false)
}

// ParseMobileClientBytes validates the direct mobile data-plane profile. It
// rejects desktop SOCKS adapter fields, then supplies private internal values
// only to reuse the common connection configuration validation.
func ParseMobileClientBytes(raw []byte, encodedSecret string) (Client, error) {
	return parseClientBytes(raw, encodedSecret, true)
}

func parseClientBytes(raw []byte, encodedSecret string, directMobile bool) (Client, error) {
	var config Client
	if len(raw) == 0 || len(raw) > maxConfigBytes {
		return Client{}, fmt.Errorf("%w: config exceeds size limit", ErrInvalidConfig)
	}
	if err := decodeStrictJSON(raw, &config); err != nil {
		return Client{}, err
	}
	applyClientDefaults(&config, directMobile)
	if directMobile {
		if config.SOCKSListen != "" || config.MaxSOCKSConnections != 0 {
			return Client{}, fmt.Errorf("%w: mobile profile contains a SOCKS adapter", ErrInvalidConfig)
		}
		if len(config.ServerAddresses) == 0 {
			return Client{}, fmt.Errorf("%w: mobile profile requires pinned server addresses", ErrInvalidConfig)
		}
		config.SOCKSListen = "127.0.0.1:0"
		config.MaxSOCKSConnections = config.MaxStreams
	}
	secret, err := ParseSecret(encodedSecret)
	if err != nil {
		return Client{}, err
	}
	config.Secret = secret
	if err := validateClient(config); err != nil {
		return Client{}, err
	}
	return config, nil
}

func LoadServer(configPath string) (Server, error) {
	var config Server
	if err := loadStrictJSON(configPath, &config); err != nil {
		return Server{}, err
	}
	if config.HTTP3CertFile != "" {
		config.HTTP3CertFile = resolveRelative(configPath, config.HTTP3CertFile)
	}
	if config.HTTP3KeyFile != "" {
		config.HTTP3KeyFile = resolveRelative(configPath, config.HTTP3KeyFile)
	}
	if config.SecretFile != "" {
		config.SecretFile = resolveRelative(configPath, config.SecretFile)
		secret, err := LoadSecret(config.SecretFile)
		if err != nil {
			return Server{}, err
		}
		config.Secret = secret
	}
	if config.CredentialDirectory != "" {
		config.CredentialDirectory = resolveRelative(configPath, config.CredentialDirectory)
		loaded, err := credentials.LoadActiveDirectory(config.CredentialDirectory)
		if err != nil {
			return Server{}, err
		}
		config.Credentials = loaded
	}
	if config.UserPolicyFile != "" {
		config.UserPolicyFile = resolveRelative(configPath, config.UserPolicyFile)
	}
	if config.UsageStateFile != "" {
		config.UsageStateFile = resolveRelative(configPath, config.UsageStateFile)
	}
	if config.ClusterDirectory != "" {
		config.ClusterDirectory = resolveRelative(configPath, config.ClusterDirectory)
	}
	if config.GeodataDirectory != "" {
		config.GeodataDirectory = resolveRelative(configPath, config.GeodataDirectory)
	}
	if config.Secret != (RootSecret{}) {
		legacy := config.Secret.Bytes()
		for _, credential := range config.Credentials {
			if credential.Secret == legacy {
				return Server{}, fmt.Errorf("%w: legacy and active credential secrets must be distinct", ErrInvalidConfig)
			}
		}
	}
	applyServerResourceDefaults(&config)
	if err := validateServer(config); err != nil {
		return Server{}, err
	}
	return config, nil
}

func applyServerResourceDefaults(config *Server) {
	if config == nil {
		return
	}
	limits := &config.ResourceLimits
	setDefaultInt(&limits.MaxSessionsPerUser, min(8, config.MaxSessions))
	setDefaultInt(&limits.MaxTCPConnectionsGlobal, max(6000, config.MaxTargetConnections))
	setDefaultInt(&limits.MaxTCPConnectionsPerUser, max(512, config.MaxTargetConnections))
	setDefaultInt(&limits.MaxUDPAssociationsGlobal, 10000)
	setDefaultInt(&limits.MaxUDPAssociationsPerUser, 1024)
	setDefaultInt64(&limits.UDPPacketsPerSecondGlobal, 100000)
	setDefaultInt64(&limits.UDPPacketsPerSecondPerUser, 20000)
	setDefaultInt64(&limits.UDPBytesPerSecondGlobal, 256*1024*1024)
	setDefaultInt64(&limits.UDPBytesPerSecondPerUser, 64*1024*1024)
	setDefaultInt64(&limits.DNSQueriesPerSecondGlobal, 5000)
	setDefaultInt64(&limits.DNSQueriesPerSecondPerUser, 500)
	setDefaultInt64(&limits.TargetCreatesPerSecondGlobal, 20000)
	setDefaultInt64(&limits.TargetCreatesPerSecondPerUser, 2000)
}

func setDefaultInt(value *int, fallback int) {
	if *value == 0 {
		*value = fallback
	}
}

func setDefaultInt64(value *int64, fallback int64) {
	if *value == 0 {
		*value = fallback
	}
}

func loadStrictJSON(configPath string, destination any) error {
	info, err := os.Lstat(configPath)
	if err != nil {
		return fmt.Errorf("%w: read config: %v", ErrInvalidConfig, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: config must be a regular file", ErrInvalidConfig)
	}
	file, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("%w: open config: %v", ErrInvalidConfig, err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(raw) > maxConfigBytes {
		return fmt.Errorf("%w: config exceeds size limit", ErrInvalidConfig)
	}
	return decodeStrictJSON(raw, destination)
}

func decodeStrictJSON(raw []byte, destination any) error {
	if err := rejectDuplicateObjectFields(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode config: %v", ErrInvalidConfig, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON value", ErrInvalidConfig)
	}
	return nil
}

func rejectDuplicateObjectFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := scanUniqueJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON value", ErrInvalidConfig)
	}
	return nil
}

func scanUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON", ErrInvalidConfig)
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%w: malformed object", ErrInvalidConfig)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%w: malformed object key", ErrInvalidConfig)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("%w: duplicate field %q", ErrInvalidConfig, key)
			}
			seen[key] = struct{}{}
			if err := scanUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("%w: malformed object", ErrInvalidConfig)
		}
		return nil
	case '[':
		for decoder.More() {
			if err := scanUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("%w: malformed array", ErrInvalidConfig)
		}
		return nil
	default:
		return fmt.Errorf("%w: top-level value must be an object", ErrInvalidConfig)
	}
}

func validateClient(config Client) error {
	strictHTTP3 := config.CarrierPolicy == CarrierPolicyHTTP3Only
	strictHTTPS := config.CarrierPolicy == CarrierPolicyHTTPSOnly
	if !validIdentity(config.ServerIdentity) || config.ProfileID() == 0 ||
		(config.CarrierPolicy != CarrierPolicyPerformance && config.CarrierPolicy != CarrierPolicyUDPFirst &&
			config.CarrierPolicy != CarrierPolicyHTTP3Only && config.CarrierPolicy != CarrierPolicyHTTPSOnly) ||
		len(config.ServerAddresses) > 8 || !validServerAddresses(config.ServerAddresses) ||
		config.MaxCoverOverheadPercent > 100 ||
		config.InitialWindowBytes < 16*1024 || config.InitialWindowBytes > session.MaxInitialWindow ||
		config.MaxStreams <= 0 || config.MaxStreams > maxStreams ||
		config.MaxParallelCarriers <= 0 || config.MaxParallelCarriers > 3 ||
		config.MaxSOCKSConnections <= 0 || config.MaxSOCKSConnections > config.MaxStreams ||
		!validDuration(config.CarrierCacheTTL.Duration, time.Second, 24*time.Hour) {
		return ErrInvalidConfig
	}
	if err := validateLoopback(config.SOCKSListen, true); err != nil {
		return err
	}
	if strictHTTP3 {
		if config.HTTPSURL != "" || config.WebRTCSignalingURL != "" ||
			config.HTTPSTimeout.Duration != 0 || config.WebRTCTimeout.Duration != 0 ||
			!config.HTTP3Configured() || config.MaxParallelCarriers != 1 ||
			!validDuration(config.HTTP3Timeout.Duration, 100*time.Millisecond, 30*time.Second) {
			return ErrInvalidConfig
		}
		_, err := validateEndpointURL(config.HTTP3URL, "https", config.ServerIdentity)
		return err
	}
	if strictHTTPS {
		if config.WebRTCSignalingURL != "" || config.HTTP3URL != "" ||
			config.WebRTCTimeout.Duration != 0 || config.HTTP3Timeout.Duration != 0 ||
			config.MaxParallelCarriers != 1 || config.RequireDatagrams ||
			!validDuration(config.HTTPSTimeout.Duration, 100*time.Millisecond, 60*time.Second) {
			return ErrInvalidConfig
		}
		_, err := validateEndpointURL(config.HTTPSURL, "wss", config.ServerIdentity)
		return err
	}
	if !validDuration(config.WebRTCTimeout.Duration, 100*time.Millisecond, 30*time.Second) ||
		!validDuration(config.HTTPSTimeout.Duration, 100*time.Millisecond, 60*time.Second) {
		return ErrInvalidConfig
	}
	httpsPath, err := validateEndpointURL(config.HTTPSURL, "wss", config.ServerIdentity)
	if err != nil {
		return err
	}
	webRTCPath, err := validateEndpointURL(config.WebRTCSignalingURL, "https", config.ServerIdentity)
	if err != nil || httpsPath == webRTCPath {
		return ErrInvalidConfig
	}
	if !config.HTTP3Configured() {
		if config.HTTP3Timeout.Duration != 0 {
			return ErrInvalidConfig
		}
		return nil
	}
	if !validDuration(config.HTTP3Timeout.Duration, 100*time.Millisecond, 30*time.Second) {
		return ErrInvalidConfig
	}
	http3Path, err := validateEndpointURL(config.HTTP3URL, "https", config.ServerIdentity)
	if err != nil || http3Path == httpsPath || http3Path == webRTCPath {
		return ErrInvalidConfig
	}
	return nil
}

func validServerAddresses(addresses []netip.Addr) bool {
	benchmarkIPv4 := netip.MustParsePrefix("198.18.0.0/15")
	documentationIPv4 := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
	}
	documentationIPv6 := netip.MustParsePrefix("2001:db8::/32")
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
			address.IsLoopback() || address.IsLinkLocalUnicast() ||
			benchmarkIPv4.Contains(address) || documentationIPv6.Contains(address) {
			return false
		}
		for _, prefix := range documentationIPv4 {
			if prefix.Contains(address) {
				return false
			}
		}
		if _, duplicate := seen[address]; duplicate {
			return false
		}
		seen[address] = struct{}{}
	}
	return true
}

func validateServer(config Server) error {
	if (config.Secret == (RootSecret{}) && len(config.Credentials) == 0) ||
		(config.UserPolicyFile == "") != (config.UsageStateFile == "") ||
		!validIdentity(config.ServerIdentity) || config.HTTPSPath == config.WebRTCPath ||
		!validPrivatePath(config.HTTPSPath) || !validPrivatePath(config.WebRTCPath) ||
		config.UDPPortMin < 1024 || config.UDPPortMax < config.UDPPortMin ||
		int(config.UDPPortMax)-int(config.UDPPortMin)+1 > 1000 ||
		config.MaxCoverOverheadPercent > 100 ||
		config.InitialWindowBytes < 16*1024 || config.InitialWindowBytes > session.MaxInitialWindow ||
		config.MaxStreams <= 0 || config.MaxStreams > maxStreams ||
		config.MaxSessions <= 0 || config.MaxSessions > maxSessions ||
		config.MaxWebRTCPeers <= 0 || config.MaxWebRTCPeers > config.MaxSessions ||
		config.MaxTargetConnections <= 0 || config.MaxTargetConnections > config.MaxStreams ||
		!validDuration(config.DialTimeout.Duration, 100*time.Millisecond, 60*time.Second) ||
		!validDuration(config.GatherTimeout.Duration, 100*time.Millisecond, 60*time.Second) ||
		!validDuration(config.ConnectTimeout.Duration, 100*time.Millisecond, 60*time.Second) ||
		!validDuration(config.ShutdownTimeout.Duration, time.Second, 60*time.Second) ||
		!validClusterCatalogConfig(config) ||
		!validClusterRelayConfig(config) ||
		!validOptionalAbsoluteDirectory(config.GeodataDirectory) ||
		!validServerResourceLimits(config) {
		return ErrInvalidConfig
	}
	if err := validateLoopback(config.Listen, false); err != nil {
		return err
	}
	if config.MetricsListen != "" {
		if err := validateLoopback(config.MetricsListen, false); err != nil {
			return err
		}
		if config.MetricsListen == config.Listen {
			return fmt.Errorf("%w: metrics listener must differ from backend listener", ErrInvalidConfig)
		}
	}
	if !config.HTTP3Configured() {
		if config.EnableHTTP3 || config.EnableHTTP3Datagrams {
			return ErrInvalidConfig
		}
		return nil
	}
	if config.EnableHTTP3Datagrams && !config.EnableHTTP3 ||
		!validPrivatePath(config.HTTP3Path) || config.HTTP3Path == config.HTTPSPath ||
		config.HTTP3Path == config.WebRTCPath || config.HTTP3CertFile == "" || config.HTTP3KeyFile == "" ||
		config.MaxHTTP3Sessions <= 0 || config.MaxHTTP3Sessions > config.MaxSessions ||
		!validDuration(config.HTTP3HandshakeTimeout.Duration, 100*time.Millisecond, 30*time.Second) ||
		!validDuration(config.HTTP3IdleTimeout.Duration, 5*time.Second, 10*time.Minute) {
		return ErrInvalidConfig
	}
	return validatePublicListener(config.HTTP3Listen)
}

func validOptionalAbsoluteDirectory(value string) bool {
	return value == "" || (filepath.IsAbs(value) && filepath.Clean(value) == value)
}

func validClusterRelayConfig(config Server) bool {
	values := []string{config.ClusterNodeID, config.ClusterMasterNodeID, config.ClusterPeerDirectory, config.ClusterPeerMapFile}
	empty := 0
	for _, value := range values {
		if value == "" {
			empty++
		}
	}
	if empty == len(values) {
		return true
	}
	if empty != 0 || !validClusterIdentifier(config.ClusterNodeID) || !validClusterIdentifier(config.ClusterMasterNodeID) {
		return false
	}
	if !filepath.IsAbs(config.ClusterPeerDirectory) || filepath.Clean(config.ClusterPeerDirectory) != config.ClusterPeerDirectory ||
		!filepath.IsAbs(config.ClusterPeerMapFile) || filepath.Clean(config.ClusterPeerMapFile) != config.ClusterPeerMapFile {
		return false
	}
	return config.ClusterNodeID != config.ClusterMasterNodeID || config.ClusterDirectory != ""
}

func validClusterIdentifier(value string) bool {
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

func validClusterCatalogConfig(config Server) bool {
	if config.ClusterDirectory == "" {
		return config.ClusterCatalogTTL.Duration == 0
	}
	return filepath.IsAbs(config.ClusterDirectory) && filepath.Clean(config.ClusterDirectory) == config.ClusterDirectory &&
		validDuration(config.ClusterCatalogTTL.Duration, 5*time.Minute, 24*time.Hour)
}

func validServerResourceLimits(config Server) bool {
	limits := config.ResourceLimits
	return limits.MaxSessionsPerUser > 0 && limits.MaxSessionsPerUser <= config.MaxSessions &&
		limits.MaxTCPConnectionsGlobal >= config.MaxTargetConnections &&
		limits.MaxTCPConnectionsGlobal <= maxGlobalResources &&
		limits.MaxTCPConnectionsPerUser >= config.MaxTargetConnections &&
		limits.MaxTCPConnectionsPerUser <= limits.MaxTCPConnectionsGlobal &&
		limits.MaxUDPAssociationsGlobal > 0 && limits.MaxUDPAssociationsGlobal <= maxGlobalResources &&
		limits.MaxUDPAssociationsPerUser > 0 &&
		limits.MaxUDPAssociationsPerUser <= limits.MaxUDPAssociationsGlobal &&
		validResourceRate(limits.UDPPacketsPerSecondGlobal, limits.UDPPacketsPerSecondPerUser) &&
		validResourceRate(limits.UDPBytesPerSecondGlobal, limits.UDPBytesPerSecondPerUser) &&
		validResourceRate(limits.DNSQueriesPerSecondGlobal, limits.DNSQueriesPerSecondPerUser) &&
		validResourceRate(limits.TargetCreatesPerSecondGlobal, limits.TargetCreatesPerSecondPerUser)
}

func validResourceRate(global, perUser int64) bool {
	return global > 0 && global <= maxResourceRate && perUser > 0 && perUser <= global
}

func validatePublicListener(raw string) error {
	host, portValue, err := net.SplitHostPort(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid public listener", ErrInvalidConfig)
	}
	port, err := strconv.ParseUint(portValue, 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("%w: invalid public listener port", ErrInvalidConfig)
	}
	if host == "" {
		return nil
	}
	address, err := netip.ParseAddr(host)
	if err != nil || address.IsMulticast() {
		return fmt.Errorf("%w: invalid public listener address", ErrInvalidConfig)
	}
	return nil
}

func validateLoopback(raw string, allowEphemeralPort bool) error {
	address, err := netip.ParseAddrPort(raw)
	if err != nil || !address.Addr().IsLoopback() || (!allowEphemeralPort && address.Port() == 0) {
		return fmt.Errorf("%w: listener must use a loopback IP and valid port", ErrInvalidConfig)
	}
	return nil
}

func validateEndpointURL(raw, scheme, identity string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != scheme || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.EqualFold(parsed.Hostname(), identity) || !validPrivatePath(parsed.EscapedPath()) {
		return "", ErrInvalidConfig
	}
	if parsed.Port() != "" {
		port, err := strconv.ParseUint(parsed.Port(), 10, 16)
		if err != nil || port == 0 {
			return "", ErrInvalidConfig
		}
	}
	return parsed.EscapedPath(), nil
}

func validPrivatePath(value string) bool {
	return len(value) >= minPrivatePath && strings.HasPrefix(value, "/") &&
		!strings.ContainsAny(value, "%\\") && !strings.Contains(value, "//") && path.Clean(value) == value
}

func validIdentity(value string) bool {
	if len(value) == 0 || len(value) > 253 || value != strings.ToLower(value) || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range []byte(label) {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validDuration(value, minimum, maximum time.Duration) bool {
	return value >= minimum && value <= maximum
}

func resolveRelative(configPath, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(configPath), value))
}
