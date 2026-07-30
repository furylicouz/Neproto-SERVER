package onboarding

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"path"
	"strings"
	"unicode/utf8"

	"neproto.local/chameleon/internal/config"
)

const (
	LegacyPrefix = "np2://import/v1/"
	Prefix       = "np2://import/v2/"
	MaxURIBytes  = 4096
	IDSize       = 16
)

var ErrInvalidProfile = errors.New("invalid NP/2 onboarding profile")

type Profile struct {
	Version              int      `json:"version"`
	CredentialID         string   `json:"credential_id"`
	Name                 string   `json:"name"`
	ServerIdentity       string   `json:"server_identity"`
	ServerAddresses      []string `json:"server_addresses"`
	HTTPSPath            string   `json:"https_path"`
	WebRTCPath           string   `json:"webrtc_path"`
	HTTP3Path            string   `json:"http3_path,omitempty"`
	RequireDatagrams     bool     `json:"require_datagrams,omitempty"`
	MaxParallelCarriers  int      `json:"max_parallel_carriers,omitempty"`
	EnableConstellation  bool     `json:"enable_constellation,omitempty"`
	EnableForwardSecrecy bool     `json:"enable_forward_secrecy,omitempty"`
	ClusterID            string   `json:"cluster_id,omitempty"`
	CatalogPublicKey     string   `json:"catalog_public_key,omitempty"`
	Profile              string   `json:"profile"`
	Secret               string   `json:"secret"`
}

func EncodeURI(profile Profile) (string, error) {
	if err := profile.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("%w: encode JSON", ErrInvalidProfile)
	}
	prefix := Prefix
	if profile.Version == 1 {
		prefix = LegacyPrefix
	}
	uri := prefix + encodeRaw(string(raw))
	if len(uri) > MaxURIBytes {
		return "", fmt.Errorf("%w: URI exceeds size limit", ErrInvalidProfile)
	}
	return uri, nil
}

func DecodeURI(uri string) (Profile, error) {
	if len(uri) == 0 || len(uri) > MaxURIBytes {
		return Profile{}, fmt.Errorf("%w: malformed URI", ErrInvalidProfile)
	}
	prefix := Prefix
	expectedVersion := 2
	if strings.HasPrefix(uri, LegacyPrefix) {
		prefix = LegacyPrefix
		expectedVersion = 1
	} else if !strings.HasPrefix(uri, Prefix) {
		return Profile{}, fmt.Errorf("%w: malformed URI", ErrInvalidProfile)
	}
	encoded := strings.TrimPrefix(uri, prefix)
	if encoded == "" || strings.ContainsAny(encoded, "=/+?#") {
		return Profile{}, fmt.Errorf("%w: malformed payload encoding", ErrInvalidProfile)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > MaxURIBytes {
		return Profile{}, fmt.Errorf("%w: malformed payload encoding", ErrInvalidProfile)
	}
	var profile Profile
	if err := decodeStrict(raw, &profile); err != nil {
		return Profile{}, err
	}
	if profile.Version != expectedVersion {
		return Profile{}, fmt.Errorf("%w: URI and payload versions differ", ErrInvalidProfile)
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (p Profile) Validate() error {
	if (p.Version != 1 && p.Version != 2) || !validCredentialID(p.CredentialID) || !validName(p.Name) ||
		!validIdentity(p.ServerIdentity) || !validAddresses(p.ServerAddresses) ||
		!validPrivatePath(p.HTTPSPath) || !validPrivatePath(p.WebRTCPath) ||
		p.HTTPSPath == p.WebRTCPath || !validProfile(p.Profile) {
		return ErrInvalidProfile
	}
	if p.Version == 1 {
		if p.HTTP3Path != "" || p.RequireDatagrams || p.MaxParallelCarriers != 0 ||
			p.EnableConstellation || p.EnableForwardSecrecy || p.ClusterID != "" || p.CatalogPublicKey != "" {
			return ErrInvalidProfile
		}
	} else if !validPrivatePath(p.HTTP3Path) || p.HTTP3Path == p.HTTPSPath || p.HTTP3Path == p.WebRTCPath ||
		(p.MaxParallelCarriers != 0 && (p.MaxParallelCarriers < 1 || p.MaxParallelCarriers > 3)) {
		return ErrInvalidProfile
	}
	if (p.ClusterID == "") != (p.CatalogPublicKey == "") ||
		(p.ClusterID != "" && (!validClusterID(p.ClusterID) || !validCatalogPublicKey(p.CatalogPublicKey))) {
		return ErrInvalidProfile
	}
	if _, err := config.ParseSecret(p.Secret); err != nil {
		return fmt.Errorf("%w: malformed credential", ErrInvalidProfile)
	}
	return nil
}

func validClusterID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			(index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func validCatalogPublicKey(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != value {
		return false
	}
	for _, value := range raw {
		if value != 0 {
			return true
		}
	}
	return false
}

func validCredentialID(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == IDSize && base64.RawURLEncoding.EncodeToString(raw) == value
}

func validName(value string) bool {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) {
		return false
	}
	count := utf8.RuneCountInString(value)
	if count < 1 || count > 64 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
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

func validAddresses(values []string) bool {
	if len(values) == 0 || len(values) > 8 {
		return false
	}
	benchmarkIPv4 := netip.MustParsePrefix("198.18.0.0/15")
	documentationIPv4 := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
	}
	documentationIPv6 := netip.MustParsePrefix("2001:db8::/32")
	seen := make(map[netip.Addr]struct{}, len(values))
	for _, value := range values {
		address, err := netip.ParseAddr(value)
		if err != nil {
			return false
		}
		address = address.Unmap()
		if value != address.String() || !address.IsGlobalUnicast() || address.IsPrivate() ||
			address.IsLoopback() || address.IsLinkLocalUnicast() || benchmarkIPv4.Contains(address) ||
			documentationIPv6.Contains(address) {
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

func validPrivatePath(value string) bool {
	return len(value) >= 16 && len(value) <= 129 && strings.HasPrefix(value, "/") &&
		!strings.ContainsAny(value, "%\\?#") && !strings.Contains(value, "//") && path.Clean(value) == value
}

func validProfile(value string) bool {
	return value == "quiet" || value == "web" || value == "interactive"
}

func decodeStrict(raw []byte, destination any) error {
	if err := rejectDuplicateFields(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode JSON", ErrInvalidProfile)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON", ErrInvalidProfile)
	}
	return nil
}

func rejectDuplicateFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("%w: JSON object required", ErrInvalidProfile)
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%w: malformed JSON", ErrInvalidProfile)
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("%w: malformed field", ErrInvalidProfile)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate field", ErrInvalidProfile)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("%w: malformed field", ErrInvalidProfile)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("%w: malformed JSON", ErrInvalidProfile)
	}
	return nil
}

func encodeRaw(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
