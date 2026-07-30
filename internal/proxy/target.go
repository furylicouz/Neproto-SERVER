package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"neproto.local/chameleon/internal/cluster"
)

const (
	targetNetworkTCP byte = 0x01
	targetDomain     byte = 0x11
	targetIPv4       byte = 0x24
	targetIPv6       byte = 0x36
	maxDomainLength       = 253
	maxDNSAnswers         = 32
)

type OpenCommand byte

const (
	CommandTCPConnect            OpenCommand = 0x01
	CommandUDPFixed              OpenCommand = 0x02
	CommandUDPAssociate          OpenCommand = 0x03
	CommandTCPClientRoute        OpenCommand = 0x04
	CommandUDPClientRoute        OpenCommand = 0x05
	CommandClusterCatalog        OpenCommand = 0x10
	CommandClusterRelay          OpenCommand = 0x11
	CommandClusterCatalogRelay   OpenCommand = 0x12
	CommandClusterCredentialSync OpenCommand = 0x13
	CommandClusterState          OpenCommand = 0x14
	CommandClusterGeoData        OpenCommand = 0x15
)

type OpenRequest struct {
	Command        OpenCommand
	Target         Target
	Relay          *cluster.RelayRequest
	CatalogUserID  string
	CredentialSync *cluster.CredentialSyncRequest
	GeoData        *cluster.GeoDataRequest
	ClientRoute    *cluster.ClientRouteRequest
}

var (
	ErrInvalidTarget     = errors.New("invalid target metadata")
	ErrDestinationDenied = errors.New("destination denied by policy")
	ErrResolution        = errors.New("destination resolution failed")
)

type Target struct {
	Host string
	Port uint16
}

func EncodeOpenRequest(request OpenRequest) ([]byte, error) {
	switch request.Command {
	case CommandTCPConnect:
		if request.Target == (Target{}) || request.Relay != nil || request.CatalogUserID != "" || request.CredentialSync != nil || request.GeoData != nil || request.ClientRoute != nil {
			return nil, ErrInvalidTarget
		}
		return EncodeTarget(request.Target)
	case CommandUDPFixed:
		if request.Target == (Target{}) || request.Relay != nil || request.CatalogUserID != "" || request.CredentialSync != nil || request.GeoData != nil || request.ClientRoute != nil {
			return nil, ErrInvalidTarget
		}
		raw, err := EncodeTarget(request.Target)
		if err != nil {
			return nil, err
		}
		raw[0] = byte(CommandUDPFixed)
		return raw, nil
	case CommandUDPAssociate:
		if request.Target != (Target{}) || request.Relay != nil || request.CatalogUserID != "" || request.CredentialSync != nil || request.GeoData != nil || request.ClientRoute != nil {
			return nil, ErrInvalidTarget
		}
		return []byte{byte(CommandUDPAssociate)}, nil
	case CommandTCPClientRoute, CommandUDPClientRoute:
		if request.Target == (Target{}) || request.Relay != nil || request.CatalogUserID != "" ||
			request.CredentialSync != nil || request.GeoData != nil || request.ClientRoute == nil ||
			cluster.ValidateClientRouteRequest(*request.ClientRoute) != nil {
			return nil, ErrInvalidTarget
		}
		target, err := EncodeTarget(request.Target)
		if err != nil {
			return nil, err
		}
		clientRoute, err := json.Marshal(request.ClientRoute)
		if err != nil || len(clientRoute) == 0 || len(clientRoute) > 1024 {
			return nil, ErrInvalidTarget
		}
		raw := []byte{byte(request.Command)}
		raw = binary.BigEndian.AppendUint16(raw, uint16(len(target)))
		raw = append(raw, target...)
		raw = binary.BigEndian.AppendUint16(raw, uint16(len(clientRoute)))
		return append(raw, clientRoute...), nil
	case CommandClusterCatalog, CommandClusterState:
		if request.Target != (Target{}) || request.Relay != nil || request.CatalogUserID != "" || request.CredentialSync != nil || request.GeoData != nil || request.ClientRoute != nil {
			return nil, ErrInvalidTarget
		}
		return []byte{byte(request.Command)}, nil
	case CommandClusterRelay:
		if request.Target != (Target{}) || request.Relay == nil || request.CatalogUserID != "" || request.CredentialSync != nil || request.GeoData != nil || request.ClientRoute != nil {
			return nil, ErrInvalidTarget
		}
		payload, err := json.Marshal(request.Relay)
		if err != nil || len(payload) == 0 || len(payload) > 16<<10 {
			return nil, ErrInvalidTarget
		}
		raw := []byte{byte(CommandClusterRelay)}
		raw = binary.BigEndian.AppendUint16(raw, uint16(len(payload)))
		return append(raw, payload...), nil
	case CommandClusterCatalogRelay:
		if request.Target != (Target{}) || request.Relay != nil || !validCatalogUserID(request.CatalogUserID) || request.CredentialSync != nil || request.GeoData != nil || request.ClientRoute != nil {
			return nil, ErrInvalidTarget
		}
		raw := []byte{byte(CommandClusterCatalogRelay)}
		raw = binary.BigEndian.AppendUint16(raw, uint16(len(request.CatalogUserID)))
		return append(raw, request.CatalogUserID...), nil
	case CommandClusterCredentialSync:
		if request.Target != (Target{}) || request.Relay != nil || request.CatalogUserID != "" || request.CredentialSync == nil || request.GeoData != nil || request.ClientRoute != nil || cluster.ValidateCredentialSync(*request.CredentialSync) != nil {
			return nil, ErrInvalidTarget
		}
		payload, err := json.Marshal(request.CredentialSync)
		if err != nil || len(payload) == 0 || len(payload) > 1024 {
			return nil, ErrInvalidTarget
		}
		raw := []byte{byte(CommandClusterCredentialSync)}
		raw = binary.BigEndian.AppendUint16(raw, uint16(len(payload)))
		return append(raw, payload...), nil
	case CommandClusterGeoData:
		if request.Target != (Target{}) || request.Relay != nil || request.CatalogUserID != "" || request.CredentialSync != nil || request.GeoData == nil || request.ClientRoute != nil || cluster.ValidateGeoDataRequest(*request.GeoData) != nil {
			return nil, ErrInvalidTarget
		}
		payload, err := json.Marshal(request.GeoData)
		if err != nil || len(payload) == 0 || len(payload) > 256 {
			return nil, ErrInvalidTarget
		}
		raw := []byte{byte(CommandClusterGeoData)}
		raw = binary.BigEndian.AppendUint16(raw, uint16(len(payload)))
		return append(raw, payload...), nil
	default:
		return nil, ErrInvalidTarget
	}
}

func DecodeOpenRequest(raw []byte) (OpenRequest, error) {
	if len(raw) == 0 {
		return OpenRequest{}, ErrInvalidTarget
	}
	command := OpenCommand(raw[0])
	switch command {
	case CommandTCPConnect:
		target, err := DecodeTarget(raw)
		if err != nil {
			return OpenRequest{}, err
		}
		return OpenRequest{Command: command, Target: target}, nil
	case CommandUDPFixed:
		candidate := append([]byte(nil), raw...)
		candidate[0] = targetNetworkTCP
		target, err := DecodeTarget(candidate)
		if err != nil {
			return OpenRequest{}, err
		}
		return OpenRequest{Command: command, Target: target}, nil
	case CommandUDPAssociate:
		if len(raw) != 1 {
			return OpenRequest{}, ErrInvalidTarget
		}
		return OpenRequest{Command: command}, nil
	case CommandTCPClientRoute, CommandUDPClientRoute:
		if len(raw) < 7 {
			return OpenRequest{}, ErrInvalidTarget
		}
		targetLength := int(binary.BigEndian.Uint16(raw[1:3]))
		if targetLength == 0 || 3+targetLength+2 > len(raw) {
			return OpenRequest{}, ErrInvalidTarget
		}
		target, err := DecodeTarget(raw[3 : 3+targetLength])
		if err != nil {
			return OpenRequest{}, ErrInvalidTarget
		}
		routeOffset := 3 + targetLength
		routeLength := int(binary.BigEndian.Uint16(raw[routeOffset : routeOffset+2]))
		if routeLength == 0 || routeLength > 1024 || routeOffset+2+routeLength != len(raw) {
			return OpenRequest{}, ErrInvalidTarget
		}
		decoder := json.NewDecoder(bytes.NewReader(raw[routeOffset+2:]))
		decoder.DisallowUnknownFields()
		var clientRoute cluster.ClientRouteRequest
		if err := decoder.Decode(&clientRoute); err != nil || cluster.ValidateClientRouteRequest(clientRoute) != nil {
			return OpenRequest{}, ErrInvalidTarget
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return OpenRequest{}, ErrInvalidTarget
		}
		return OpenRequest{Command: command, Target: target, ClientRoute: &clientRoute}, nil
	case CommandClusterCatalog, CommandClusterState:
		if len(raw) != 1 {
			return OpenRequest{}, ErrInvalidTarget
		}
		return OpenRequest{Command: command}, nil
	case CommandClusterCatalogRelay:
		if len(raw) < 4 || int(binary.BigEndian.Uint16(raw[1:3])) != len(raw)-3 || !validCatalogUserID(string(raw[3:])) {
			return OpenRequest{}, ErrInvalidTarget
		}
		return OpenRequest{Command: command, CatalogUserID: string(raw[3:])}, nil
	case CommandClusterRelay:
		if len(raw) < 3 || int(binary.BigEndian.Uint16(raw[1:3])) != len(raw)-3 || len(raw)-3 > 16<<10 {
			return OpenRequest{}, ErrInvalidTarget
		}
		decoder := json.NewDecoder(bytes.NewReader(raw[3:]))
		decoder.DisallowUnknownFields()
		var relay cluster.RelayRequest
		if err := decoder.Decode(&relay); err != nil {
			return OpenRequest{}, ErrInvalidTarget
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return OpenRequest{}, ErrInvalidTarget
		}
		return OpenRequest{Command: command, Relay: &relay}, nil
	case CommandClusterCredentialSync:
		if len(raw) < 3 || int(binary.BigEndian.Uint16(raw[1:3])) != len(raw)-3 || len(raw)-3 > 1024 {
			return OpenRequest{}, ErrInvalidTarget
		}
		decoder := json.NewDecoder(bytes.NewReader(raw[3:]))
		decoder.DisallowUnknownFields()
		var request cluster.CredentialSyncRequest
		if err := decoder.Decode(&request); err != nil || cluster.ValidateCredentialSync(request) != nil {
			return OpenRequest{}, ErrInvalidTarget
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return OpenRequest{}, ErrInvalidTarget
		}
		return OpenRequest{Command: command, CredentialSync: &request}, nil
	case CommandClusterGeoData:
		if len(raw) < 3 || int(binary.BigEndian.Uint16(raw[1:3])) != len(raw)-3 || len(raw)-3 > 256 {
			return OpenRequest{}, ErrInvalidTarget
		}
		decoder := json.NewDecoder(bytes.NewReader(raw[3:]))
		decoder.DisallowUnknownFields()
		var request cluster.GeoDataRequest
		if err := decoder.Decode(&request); err != nil || cluster.ValidateGeoDataRequest(request) != nil {
			return OpenRequest{}, ErrInvalidTarget
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return OpenRequest{}, ErrInvalidTarget
		}
		return OpenRequest{Command: command, GeoData: &request}, nil
	default:
		return OpenRequest{}, ErrInvalidTarget
	}
}

func validCatalogUserID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func EncodeTarget(target Target) ([]byte, error) {
	normalized, address, err := normalizeTarget(target)
	if err != nil {
		return nil, err
	}
	raw := []byte{targetNetworkTCP}
	if address.IsValid() {
		address = address.Unmap()
		if address.Is4() {
			raw = append(raw, targetIPv4)
			bytes4 := address.As4()
			raw = append(raw, bytes4[:]...)
		} else {
			raw = append(raw, targetIPv6)
			bytes16 := address.As16()
			raw = append(raw, bytes16[:]...)
		}
	} else {
		raw = append(raw, targetDomain)
		raw = binary.AppendUvarint(raw, uint64(len(normalized.Host)))
		raw = append(raw, normalized.Host...)
	}
	raw = binary.BigEndian.AppendUint16(raw, normalized.Port)
	return raw, nil
}

func DecodeTarget(raw []byte) (Target, error) {
	if len(raw) < 2 || raw[0] != targetNetworkTCP {
		return Target{}, ErrInvalidTarget
	}
	cursor := 2
	var host string
	switch raw[1] {
	case targetDomain:
		length, consumed, err := readCanonicalUvarint(raw[cursor:])
		if err != nil || length == 0 || length > maxDomainLength {
			return Target{}, ErrInvalidTarget
		}
		cursor += consumed
		if uint64(len(raw)-cursor) < length+2 {
			return Target{}, ErrInvalidTarget
		}
		host = string(raw[cursor : cursor+int(length)])
		cursor += int(length)
	case targetIPv4:
		if len(raw)-cursor < 4+2 {
			return Target{}, ErrInvalidTarget
		}
		var address [4]byte
		copy(address[:], raw[cursor:cursor+4])
		host = netip.AddrFrom4(address).String()
		cursor += 4
	case targetIPv6:
		if len(raw)-cursor < 16+2 {
			return Target{}, ErrInvalidTarget
		}
		var address [16]byte
		copy(address[:], raw[cursor:cursor+16])
		host = netip.AddrFrom16(address).String()
		cursor += 16
	default:
		return Target{}, ErrInvalidTarget
	}
	if len(raw)-cursor != 2 {
		return Target{}, ErrInvalidTarget
	}
	target := Target{Host: host, Port: binary.BigEndian.Uint16(raw[cursor:])}
	normalized, parsedAddress, err := normalizeTarget(target)
	kindMismatch := (raw[1] == targetDomain && parsedAddress.IsValid()) ||
		(raw[1] != targetDomain && !parsedAddress.IsValid())
	if err != nil || normalized != target || kindMismatch {
		return Target{}, ErrInvalidTarget
	}
	return target, nil
}

func normalizeTarget(target Target) (Target, netip.Addr, error) {
	if target.Port == 0 {
		return Target{}, netip.Addr{}, ErrInvalidTarget
	}
	if address, err := netip.ParseAddr(target.Host); err == nil {
		if address.Zone() != "" {
			return Target{}, netip.Addr{}, ErrInvalidTarget
		}
		address = address.Unmap()
		return Target{Host: address.String(), Port: target.Port}, address, nil
	}
	host := strings.ToLower(strings.TrimSuffix(target.Host, "."))
	if !validDomain(host) {
		return Target{}, netip.Addr{}, ErrInvalidTarget
	}
	return Target{Host: host, Port: target.Port}, netip.Addr{}, nil
}

func validDomain(host string) bool {
	if len(host) == 0 || len(host) > maxDomainLength {
		return false
	}
	for _, label := range strings.Split(host, ".") {
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

func readCanonicalUvarint(raw []byte) (uint64, int, error) {
	value, consumed := binary.Uvarint(raw)
	if consumed <= 0 {
		return 0, 0, ErrInvalidTarget
	}
	var canonical [binary.MaxVarintLen64]byte
	length := binary.PutUvarint(canonical[:], value)
	if consumed != length || !bytes.Equal(raw[:consumed], canonical[:length]) {
		return 0, 0, ErrInvalidTarget
	}
	return value, consumed, nil
}

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type DestinationPolicy struct {
	AllowPrivate bool
}

func (p DestinationPolicy) Allows(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	if p.AllowPrivate && (address.IsPrivate() || address.IsLoopback()) {
		return true
	}
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range deniedSpecialPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func (p DestinationPolicy) Resolve(ctx context.Context, target Target, resolver Resolver) ([]netip.Addr, error) {
	normalized, literal, err := normalizeTarget(target)
	if err != nil {
		return nil, err
	}
	if literal.IsValid() {
		if !p.Allows(literal) {
			return nil, ErrDestinationDenied
		}
		return []netip.Addr{literal.Unmap()}, nil
	}
	if resolver == nil {
		return nil, fmt.Errorf("%w: nil resolver", ErrResolution)
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip", normalized.Host)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrResolution, err)
	}
	if len(addresses) == 0 || len(addresses) > maxDNSAnswers {
		return nil, ErrResolution
	}
	unique := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !p.Allows(address) {
			return nil, ErrDestinationDenied
		}
		if _, exists := seen[address]; !exists {
			seen[address] = struct{}{}
			unique = append(unique, address)
		}
	}
	return unique, nil
}

var deniedSpecialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
}
