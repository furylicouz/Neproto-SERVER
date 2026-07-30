package clusterprovision

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"strings"

	"neproto.local/chameleon/internal/cluster"
)

const BootstrapVersion = 1

// Bootstrap is the one-time, root-only enrolment document transferred over
// the pinned SSH session. PeerSecret must never be persisted by the master.
type Bootstrap struct {
	Version          int                `json:"version"`
	Mode             string             `json:"mode"`
	Domain           string             `json:"domain"`
	Addresses        []string           `json:"addresses"`
	ACMEEmail        string             `json:"acme_email"`
	HTTPSPath        string             `json:"https_path"`
	WebRTCPath       string             `json:"webrtc_path"`
	HTTP3Path        string             `json:"http3_path"`
	ClusterID        string             `json:"cluster_id"`
	NodeID           string             `json:"node_id"`
	Name             string             `json:"name"`
	Region           string             `json:"region"`
	Roles            []cluster.NodeRole `json:"roles"`
	MasterNodeID     string             `json:"master_node_id"`
	MasterDomain     string             `json:"master_domain"`
	MasterAddresses  []string           `json:"master_addresses"`
	MasterHTTPSPath  string             `json:"master_https_path"`
	MasterWebRTCPath string             `json:"master_webrtc_path"`
	MasterHTTP3Path  string             `json:"master_http3_path"`
	PeerCredentialID string             `json:"peer_credential_id"`
	PeerSecret       string             `json:"peer_secret"`
}

func (bootstrap Bootstrap) Validate() error {
	if bootstrap.Version != BootstrapVersion || (bootstrap.Mode != "bare-metal" && bootstrap.Mode != "docker") ||
		!validNodeID(bootstrap.ClusterID) || !validNodeID(bootstrap.NodeID) || !validNodeID(bootstrap.MasterNodeID) ||
		bootstrap.NodeID == bootstrap.MasterNodeID || !validLabel(bootstrap.Name, 96) || !validLabel(bootstrap.Region, 96) ||
		!validDomain(bootstrap.Domain) || !validDomain(bootstrap.MasterDomain) ||
		!validAddresses(bootstrap.Addresses) || !validAddresses(bootstrap.MasterAddresses) ||
		!validTransportSet(bootstrap.HTTPSPath, bootstrap.WebRTCPath, bootstrap.HTTP3Path) ||
		!validTransportSet(bootstrap.MasterHTTPSPath, bootstrap.MasterWebRTCPath, bootstrap.MasterHTTP3Path) ||
		!validCredential(bootstrap.PeerCredentialID, 16) || !validCredential(bootstrap.PeerSecret, 32) ||
		len(bootstrap.Roles) == 0 || len(bootstrap.Roles) > 3 {
		return ErrInvalidEnrollment
	}
	seen := make(map[cluster.NodeRole]struct{}, len(bootstrap.Roles))
	for _, role := range bootstrap.Roles {
		if role != cluster.RoleIngress && role != cluster.RoleRelay && role != cluster.RoleEgress {
			return ErrInvalidEnrollment
		}
		if _, duplicate := seen[role]; duplicate {
			return ErrInvalidEnrollment
		}
		seen[role] = struct{}{}
	}
	if bootstrap.ACMEEmail != "" && (strings.ContainsAny(bootstrap.ACMEEmail, "\x00\r\n\t ") || !strings.Contains(bootstrap.ACMEEmail, "@")) {
		return ErrInvalidEnrollment
	}
	return nil
}

func EncodeBootstrap(bootstrap Bootstrap) ([]byte, error) {
	if err := bootstrap.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(bootstrap, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func validDomain(value string) bool {
	if value == "" || len(value) > 253 || value != strings.ToLower(value) || !strings.Contains(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func validAddresses(values []string) bool {
	if len(values) == 0 || len(values) > 8 {
		return false
	}
	seen := make(map[netip.Addr]struct{}, len(values))
	for _, value := range values {
		address, err := netip.ParseAddr(value)
		if err != nil || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() {
			return false
		}
		address = address.Unmap()
		if _, duplicate := seen[address]; duplicate {
			return false
		}
		seen[address] = struct{}{}
	}
	return true
}

func validTransportSet(httpsPath, webRTCPath, http3Path string) bool {
	return privatePath(httpsPath) && privatePath(webRTCPath) && privatePath(http3Path) &&
		httpsPath != webRTCPath && httpsPath != http3Path && webRTCPath != http3Path
}

func privatePath(value string) bool {
	if len(value) != 49 || value[0] != '/' {
		return false
	}
	for _, character := range value[1:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validCredential(value string, size int) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != size || base64.RawURLEncoding.EncodeToString(raw) != value {
		return false
	}
	for _, character := range raw {
		if character != 0 {
			return true
		}
	}
	return false
}
