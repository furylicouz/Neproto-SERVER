package cluster

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const (
	StateVersion = 1
	MaxNodes     = 32
	MaxRoutes    = 512
	MaxUsers     = 10_000
	MaxRouteHops = 3
)

var ErrInvalidState = errors.New("invalid cluster state")

type NodeRole string

const (
	RoleMaster  NodeRole = "master"
	RoleIngress NodeRole = "ingress"
	RoleRelay   NodeRole = "relay"
	RoleEgress  NodeRole = "egress"
)

type Node struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Region           string     `json:"region"`
	Roles            []NodeRole `json:"roles"`
	PublicIdentity   string     `json:"public_identity"`
	PublicAddresses  []string   `json:"public_addresses"`
	NP2Endpoint      string     `json:"np2_endpoint"`
	HTTPSPath        string     `json:"https_path,omitempty"`
	WebRTCPath       string     `json:"webrtc_path,omitempty"`
	HTTP3Path        string     `json:"http3_path,omitempty"`
	RequireDatagrams bool       `json:"require_datagrams,omitempty"`
	Enabled          bool       `json:"enabled"`
	ClientVisible    bool       `json:"client_visible"`
	CredentialID     string     `json:"credential_id"`
	HostKeySHA256    string     `json:"host_key_sha256"`
	ProvisionedAt    time.Time  `json:"provisioned_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type RouteSource string

const (
	RouteSourceAdmin  RouteSource = "admin"
	RouteSourceClient RouteSource = "client"
)

type NetworkProtocol string

const (
	ProtocolTCP NetworkProtocol = "tcp"
	ProtocolUDP NetworkProtocol = "udp"
)

type PortRange struct {
	From uint16 `json:"from"`
	To   uint16 `json:"to"`
}

type RouteMatch struct {
	DomainSuffixes    []string          `json:"domain_suffixes,omitempty"`
	CIDRs             []string          `json:"cidrs,omitempty"`
	GeoIPCountries    []string          `json:"geoip_countries,omitempty"`
	GeoSiteCategories []string          `json:"geosite_categories,omitempty"`
	PortRanges        []PortRange       `json:"port_ranges,omitempty"`
	Protocols         []NetworkProtocol `json:"protocols,omitempty"`
}

type RouteActionKind string

const (
	RouteActionDirect  RouteActionKind = "direct"
	RouteActionCurrent RouteActionKind = "current"
	RouteActionNode    RouteActionKind = "node"
	RouteActionChain   RouteActionKind = "chain"
	RouteActionBlock   RouteActionKind = "block"
	RouteActionAuto    RouteActionKind = "auto"
)

type RouteAction struct {
	Kind    RouteActionKind `json:"kind"`
	NodeIDs []string        `json:"node_ids,omitempty"`
}

type Route struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Priority  int         `json:"priority"`
	Enabled   bool        `json:"enabled"`
	Source    RouteSource `json:"source"`
	Mandatory bool        `json:"mandatory,omitempty"`
	Match     RouteMatch  `json:"match"`
	Action    RouteAction `json:"action"`
}

type UserAccess struct {
	UserID             string   `json:"user_id"`
	AllowedNodeIDs     []string `json:"allowed_node_ids"`
	AllowedRouteIDs    []string `json:"allowed_route_ids"`
	AllowAutoSelection bool     `json:"allow_auto_selection"`
	AllowClientRoutes  bool     `json:"allow_client_routes"`
	Revision           uint64   `json:"revision"`
}

type State struct {
	Version   int          `json:"version"`
	ClusterID string       `json:"cluster_id"`
	Revision  uint64       `json:"revision"`
	Nodes     []Node       `json:"nodes"`
	Routes    []Route      `json:"routes"`
	Access    []UserAccess `json:"access"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type Target struct {
	Domain   string
	Address  netip.Addr
	Port     uint16
	Protocol NetworkProtocol
}

func ValidateState(state State) error {
	if state.Version != StateVersion || !validIdentifier(state.ClusterID, 64) || state.Revision == 0 ||
		state.UpdatedAt.IsZero() || len(state.Nodes) == 0 || len(state.Nodes) > MaxNodes ||
		len(state.Routes) > MaxRoutes || len(state.Access) > MaxUsers {
		return ErrInvalidState
	}
	nodes := make(map[string]Node, len(state.Nodes))
	masterCount := 0
	for _, node := range state.Nodes {
		if err := validateNode(node); err != nil {
			return fmt.Errorf("%w: node %q: %v", ErrInvalidState, node.ID, err)
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("%w: duplicate node %q", ErrInvalidState, node.ID)
		}
		nodes[node.ID] = node
		if containsRole(node.Roles, RoleMaster) {
			masterCount++
		}
	}
	if masterCount != 1 {
		return fmt.Errorf("%w: expected one master", ErrInvalidState)
	}
	routes := make(map[string]Route, len(state.Routes))
	for _, route := range state.Routes {
		if err := ValidateRoute(route); err != nil {
			return fmt.Errorf("%w: route %q: %v", ErrInvalidState, route.ID, err)
		}
		if _, exists := routes[route.ID]; exists {
			return fmt.Errorf("%w: duplicate route %q", ErrInvalidState, route.ID)
		}
		for _, nodeID := range route.Action.NodeIDs {
			if _, exists := nodes[nodeID]; !exists {
				return fmt.Errorf("%w: route %q references node %q", ErrInvalidState, route.ID, nodeID)
			}
		}
		routes[route.ID] = route
	}
	users := make(map[string]struct{}, len(state.Access))
	for _, access := range state.Access {
		if !validOpaqueID(access.UserID, 128) || access.Revision == 0 {
			return fmt.Errorf("%w: invalid user access", ErrInvalidState)
		}
		if _, exists := users[access.UserID]; exists {
			return fmt.Errorf("%w: duplicate user access", ErrInvalidState)
		}
		users[access.UserID] = struct{}{}
		if hasDuplicates(access.AllowedNodeIDs) || hasDuplicates(access.AllowedRouteIDs) {
			return fmt.Errorf("%w: duplicate access reference", ErrInvalidState)
		}
		for _, nodeID := range access.AllowedNodeIDs {
			if _, exists := nodes[nodeID]; !exists {
				return fmt.Errorf("%w: access references node %q", ErrInvalidState, nodeID)
			}
		}
		for _, routeID := range access.AllowedRouteIDs {
			if _, exists := routes[routeID]; !exists {
				return fmt.Errorf("%w: access references route %q", ErrInvalidState, routeID)
			}
		}
	}
	return nil
}

func ValidateRoute(route Route) error {
	if !validIdentifier(route.ID, 64) || !validDisplay(route.Name, 96) || route.Priority < 0 || route.Priority > 1_000_000 {
		return ErrInvalidState
	}
	if route.Source != RouteSourceAdmin && route.Source != RouteSourceClient {
		return ErrInvalidState
	}
	if route.Mandatory && route.Source != RouteSourceAdmin {
		return ErrInvalidState
	}
	if len(route.Match.DomainSuffixes) > 64 || len(route.Match.CIDRs) > 64 ||
		len(route.Match.GeoIPCountries) > 16 || len(route.Match.GeoSiteCategories) > 16 ||
		len(route.Match.PortRanges) > 32 || len(route.Match.Protocols) > 2 {
		return ErrInvalidState
	}
	for _, suffix := range route.Match.DomainSuffixes {
		if suffix != strings.ToLower(suffix) || !validDomain(suffix) {
			return ErrInvalidState
		}
	}
	for _, raw := range route.Match.CIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.String() != raw {
			return ErrInvalidState
		}
	}
	for _, country := range route.Match.GeoIPCountries {
		if !validGeoRuleName(country, 16) || country != strings.ToLower(country) {
			return ErrInvalidState
		}
	}
	for _, category := range route.Match.GeoSiteCategories {
		if !validGeoRuleName(category, 96) || category != strings.ToLower(category) {
			return ErrInvalidState
		}
	}
	for _, ports := range route.Match.PortRanges {
		if ports.From == 0 || ports.To < ports.From {
			return ErrInvalidState
		}
	}
	for _, protocol := range route.Match.Protocols {
		if protocol != ProtocolTCP && protocol != ProtocolUDP {
			return ErrInvalidState
		}
	}
	switch route.Action.Kind {
	case RouteActionNode:
		if len(route.Action.NodeIDs) != 1 {
			return ErrInvalidState
		}
	case RouteActionChain:
		if len(route.Action.NodeIDs) < 2 || len(route.Action.NodeIDs) > MaxRouteHops || hasDuplicates(route.Action.NodeIDs) {
			return ErrInvalidState
		}
	case RouteActionDirect, RouteActionCurrent, RouteActionBlock, RouteActionAuto:
		if len(route.Action.NodeIDs) != 0 {
			return ErrInvalidState
		}
	default:
		return ErrInvalidState
	}
	return nil
}

type GeoMatchEvaluator func(RouteMatch, Target) bool

func (route Route) Matches(target Target) bool {
	return route.MatchesWith(target, nil)
}

func (route Route) MatchesWith(target Target, evaluateGeo GeoMatchEvaluator) bool {
	if !route.Enabled || target.Port == 0 || (target.Protocol != ProtocolTCP && target.Protocol != ProtocolUDP) {
		return false
	}
	if len(route.Match.Protocols) > 0 && !containsProtocol(route.Match.Protocols, target.Protocol) {
		return false
	}
	if len(route.Match.PortRanges) > 0 {
		matched := false
		for _, ports := range route.Match.PortRanges {
			if target.Port >= ports.From && target.Port <= ports.To {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(route.Match.DomainSuffixes) == 0 && len(route.Match.CIDRs) == 0 &&
		len(route.Match.GeoIPCountries) == 0 && len(route.Match.GeoSiteCategories) == 0 {
		return true
	}
	domain := strings.TrimSuffix(strings.ToLower(target.Domain), ".")
	for _, suffix := range route.Match.DomainSuffixes {
		if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
			return true
		}
	}
	if target.Address.IsValid() {
		for _, raw := range route.Match.CIDRs {
			prefix, err := netip.ParsePrefix(raw)
			if err == nil && prefix.Contains(target.Address.Unmap()) {
				return true
			}
		}
	}
	if evaluateGeo != nil && evaluateGeo(route.Match, target) {
		return true
	}
	return false
}

func validGeoRuleName(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			(index > 0 && (character == '-' || character == '_' || character == '!' || character == '@')) {
			continue
		}
		return false
	}
	return true
}

func EffectiveRoutes(adminRoutes, localRoutes []Route, allowClientRoutes bool) []Route {
	result := make([]Route, 0, len(adminRoutes)+len(localRoutes))
	for _, route := range adminRoutes {
		if route.Enabled && route.Source == RouteSourceAdmin {
			result = append(result, cloneRoute(route))
		}
	}
	if allowClientRoutes {
		for _, route := range localRoutes {
			if route.Enabled && route.Source == RouteSourceClient && !route.Mandatory {
				result = append(result, cloneRoute(route))
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Mandatory != result[right].Mandatory {
			return result[left].Mandatory
		}
		if result[left].Priority != result[right].Priority {
			return result[left].Priority < result[right].Priority
		}
		return result[left].ID < result[right].ID
	})
	return result
}

func validateNode(node Node) error {
	if !validIdentifier(node.ID, 64) || !validDisplay(node.Name, 96) || !validDisplay(node.Region, 96) ||
		!validDomain(node.PublicIdentity) || len(node.PublicAddresses) == 0 || len(node.PublicAddresses) > 8 ||
		!validOpaqueID(node.CredentialID, 128) || !strings.HasPrefix(node.HostKeySHA256, "SHA256:") ||
		node.ProvisionedAt.IsZero() || node.UpdatedAt.IsZero() || len(node.Roles) == 0 || len(node.Roles) > 4 {
		return ErrInvalidState
	}
	if host, port, err := net.SplitHostPort(node.NP2Endpoint); err != nil || host == "" || port == "" {
		return ErrInvalidState
	}
	for _, raw := range node.PublicAddresses {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.IsGlobalUnicast() {
			return ErrInvalidState
		}
	}
	seenRoles := map[NodeRole]struct{}{}
	for _, role := range node.Roles {
		if role != RoleMaster && role != RoleIngress && role != RoleRelay && role != RoleEgress {
			return ErrInvalidState
		}
		if _, exists := seenRoles[role]; exists {
			return ErrInvalidState
		}
		seenRoles[role] = struct{}{}
	}
	return nil
}

func validIdentifier(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
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

func validOpaqueID(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
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

func validDisplay(value string, maximum int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == value && len(value) > 0 && len(value) <= maximum && !strings.ContainsAny(value, "\r\n\x00")
}

func validDomain(value string) bool {
	if value == "" || len(value) > 253 || value != strings.ToLower(value) || strings.HasSuffix(value, ".") {
		return false
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
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

func containsRole(roles []NodeRole, wanted NodeRole) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}

func containsProtocol(protocols []NetworkProtocol, wanted NetworkProtocol) bool {
	for _, protocol := range protocols {
		if protocol == wanted {
			return true
		}
	}
	return false
}

func hasDuplicates(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validIdentifier(value, 128) {
			return true
		}
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func cloneRoute(route Route) Route {
	copy := route
	copy.Match.DomainSuffixes = append([]string(nil), route.Match.DomainSuffixes...)
	copy.Match.CIDRs = append([]string(nil), route.Match.CIDRs...)
	copy.Match.GeoIPCountries = append([]string(nil), route.Match.GeoIPCountries...)
	copy.Match.GeoSiteCategories = append([]string(nil), route.Match.GeoSiteCategories...)
	copy.Match.PortRanges = append([]PortRange(nil), route.Match.PortRanges...)
	copy.Match.Protocols = append([]NetworkProtocol(nil), route.Match.Protocols...)
	copy.Action.NodeIDs = append([]string(nil), route.Action.NodeIDs...)
	return copy
}
