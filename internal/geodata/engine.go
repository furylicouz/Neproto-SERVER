package geodata

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"neproto.local/chameleon/internal/cluster"
)

const (
	maxDatabaseBytes = 64 << 20
	maxGroups        = 16_384
	maxEntries       = 4_000_000
	maxDomainLength  = 253
	resolverTimeout  = 2 * time.Second
	cacheTTL         = 5 * time.Minute
	maxCacheEntries  = 4096
)

var ErrInvalidDatabase = errors.New("invalid NP/2 geodata database")

type Engine struct {
	geoIP    map[string]*prefixSet
	geoSite  map[string]*domainSet
	resolver *net.Resolver

	cacheMu sync.Mutex
	cache   map[string]cachedAddresses
}

type cachedAddresses struct {
	addresses []netip.Addr
	expires   time.Time
}

type prefixSet struct {
	v4 map[uint8]map[netip.Addr]struct{}
	v6 map[uint8]map[netip.Addr]struct{}
}

type domainSet struct {
	full    map[string]struct{}
	root    map[string]struct{}
	plain   []string
	regular []*regexp.Regexp
}

func Load(directory string) (*Engine, error) {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, ErrInvalidDatabase
	}
	geoIPRaw, err := readDatabase(filepath.Join(directory, "geoip.dat"))
	if err != nil {
		return nil, fmt.Errorf("load GeoIP: %w", err)
	}
	geoSiteRaw, err := readDatabase(filepath.Join(directory, "geosite.dat"))
	if err != nil {
		return nil, fmt.Errorf("load GeoSite: %w", err)
	}
	geoIP, err := parseGeoIPList(geoIPRaw)
	if err != nil {
		return nil, fmt.Errorf("parse GeoIP: %w", err)
	}
	geoSite, err := parseGeoSiteList(geoSiteRaw)
	if err != nil {
		return nil, fmt.Errorf("parse GeoSite: %w", err)
	}
	return &Engine{
		geoIP: geoIP, geoSite: geoSite, resolver: net.DefaultResolver,
		cache: make(map[string]cachedAddresses),
	}, nil
}

func readDatabase(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 || info.Size() > maxDatabaseBytes {
		return nil, ErrInvalidDatabase
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > maxDatabaseBytes {
		return nil, ErrInvalidDatabase
	}
	return raw, nil
}

func (engine *Engine) Match(ctx context.Context, match cluster.RouteMatch, target cluster.Target) bool {
	if engine == nil {
		return false
	}
	domain := strings.TrimSuffix(strings.ToLower(target.Domain), ".")
	if domain != "" {
		for _, category := range match.GeoSiteCategories {
			if rules := engine.geoSite[strings.ToLower(category)]; rules != nil && rules.matches(domain) {
				return true
			}
		}
	}
	// iOS PacketTunnel flows normally arrive as numeric destinations after the
	// system resolver has completed. Resolve an explicit administrator domain
	// selector on the server so the rule remains useful even before the client
	// supplies DNS attribution. This is an exact-domain fallback; the client DNS
	// cache remains responsible for preserving arbitrary subdomain names.
	if target.Address.IsValid() {
		address := target.Address.Unmap()
		for _, suffix := range match.DomainSuffixes {
			for _, resolved := range engine.resolve(ctx, suffix) {
				if resolved == address {
					return true
				}
			}
		}
	}
	if len(match.GeoIPCountries) == 0 {
		return false
	}
	addresses := make([]netip.Addr, 0, 4)
	if target.Address.IsValid() {
		addresses = append(addresses, target.Address.Unmap())
	} else if domain != "" {
		addresses = engine.resolve(ctx, domain)
	}
	for _, country := range match.GeoIPCountries {
		rules := engine.geoIP[strings.ToLower(country)]
		if rules == nil {
			continue
		}
		for _, address := range addresses {
			if rules.contains(address) {
				return true
			}
		}
	}
	return false
}

func (engine *Engine) HasGeoIP(country string) bool {
	if engine == nil {
		return false
	}
	_, ok := engine.geoIP[strings.ToLower(strings.TrimSpace(country))]
	return ok
}

// CountryCode returns the deterministic two-letter ISO country code whose
// installed GeoIP prefix set contains address. Aggregate and provider-specific
// GeoIP groups are intentionally ignored.
func (engine *Engine) CountryCode(address netip.Addr) (string, bool) {
	if engine == nil || !address.IsValid() {
		return "", false
	}
	address = address.Unmap()
	matches := make([]string, 0, 1)
	for code, rules := range engine.geoIP {
		if len(code) != 2 || code[0] < 'a' || code[0] > 'z' || code[1] < 'a' || code[1] > 'z' || !rules.contains(address) {
			continue
		}
		matches = append(matches, strings.ToUpper(code))
	}
	if len(matches) == 0 {
		return "", false
	}
	sort.Strings(matches)
	return matches[0], true
}

func (engine *Engine) HasGeoSite(category string) bool {
	if engine == nil {
		return false
	}
	_, ok := engine.geoSite[strings.ToLower(strings.TrimSpace(category))]
	return ok
}

func (engine *Engine) resolve(ctx context.Context, domain string) []netip.Addr {
	now := time.Now()
	engine.cacheMu.Lock()
	if cached, ok := engine.cache[domain]; ok && now.Before(cached.expires) {
		result := append([]netip.Addr(nil), cached.addresses...)
		engine.cacheMu.Unlock()
		return result
	}
	engine.cacheMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	resolveContext, cancel := context.WithTimeout(ctx, resolverTimeout)
	defer cancel()
	if engine.resolver == nil {
		return nil
	}
	addresses, err := engine.resolver.LookupNetIP(resolveContext, "ip", domain)
	if err != nil {
		return nil
	}
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		if address.IsValid() {
			result = append(result, address.Unmap())
		}
	}
	engine.cacheMu.Lock()
	if len(engine.cache) >= maxCacheEntries {
		engine.cache = make(map[string]cachedAddresses)
	}
	engine.cache[domain] = cachedAddresses{addresses: append([]netip.Addr(nil), result...), expires: now.Add(cacheTTL)}
	engine.cacheMu.Unlock()
	return result
}

func (set *prefixSet) add(prefix netip.Prefix) {
	prefix = prefix.Masked()
	address := prefix.Addr().Unmap()
	bits := uint8(prefix.Bits())
	levels := set.v4
	if address.Is6() {
		levels = set.v6
	}
	if levels[bits] == nil {
		levels[bits] = make(map[netip.Addr]struct{})
	}
	levels[bits][address] = struct{}{}
}

func (set *prefixSet) contains(address netip.Addr) bool {
	if set == nil || !address.IsValid() {
		return false
	}
	address = address.Unmap()
	levels, maximum := set.v4, 32
	if address.Is6() {
		levels, maximum = set.v6, 128
	}
	for bits := maximum; bits >= 0; bits-- {
		entries := levels[uint8(bits)]
		if len(entries) == 0 {
			continue
		}
		masked := netip.PrefixFrom(address, bits).Masked().Addr()
		if _, ok := entries[masked]; ok {
			return true
		}
	}
	return false
}

func (set *domainSet) matches(domain string) bool {
	if set == nil || domain == "" {
		return false
	}
	if _, ok := set.full[domain]; ok {
		return true
	}
	for suffix := domain; suffix != ""; {
		if _, ok := set.root[suffix]; ok {
			return true
		}
		dot := strings.IndexByte(suffix, '.')
		if dot < 0 {
			break
		}
		suffix = suffix[dot+1:]
	}
	for _, keyword := range set.plain {
		if strings.Contains(domain, keyword) {
			return true
		}
	}
	for _, expression := range set.regular {
		if expression.MatchString(domain) {
			return true
		}
	}
	return false
}

func parseGeoIPList(raw []byte) (map[string]*prefixSet, error) {
	result := make(map[string]*prefixSet)
	entries := 0
	err := forEachMessage(raw, func(number protowire.Number, wireType protowire.Type, value []byte) error {
		if number != 1 || wireType != protowire.BytesType {
			return nil
		}
		entries++
		if entries > maxGroups {
			return ErrInvalidDatabase
		}
		code, prefixes, err := parseGeoIP(value)
		if err != nil {
			return err
		}
		if code == "" {
			return nil
		}
		set := result[code]
		if set == nil {
			set = &prefixSet{v4: make(map[uint8]map[netip.Addr]struct{}), v6: make(map[uint8]map[netip.Addr]struct{})}
			result[code] = set
		}
		for _, prefix := range prefixes {
			set.add(prefix)
		}
		return nil
	})
	return result, err
}

func parseGeoIP(raw []byte) (string, []netip.Prefix, error) {
	code := ""
	prefixes := make([]netip.Prefix, 0)
	count := 0
	err := forEachMessage(raw, func(number protowire.Number, wireType protowire.Type, value []byte) error {
		switch number {
		case 1:
			if wireType != protowire.BytesType || len(value) == 0 || len(value) > 16 {
				return ErrInvalidDatabase
			}
			code = strings.ToLower(string(value))
		case 2:
			if wireType != protowire.BytesType {
				return ErrInvalidDatabase
			}
			count++
			if count > maxEntries {
				return ErrInvalidDatabase
			}
			prefix, err := parseCIDR(value)
			if err != nil {
				return err
			}
			prefixes = append(prefixes, prefix)
		case 3:
			if wireType == protowire.VarintType {
				inverse, n := protowire.ConsumeVarint(value)
				if n < 0 || inverse != 0 {
					return ErrInvalidDatabase
				}
			}
		}
		return nil
	})
	return code, prefixes, err
}

func parseCIDR(raw []byte) (netip.Prefix, error) {
	var addressBytes []byte
	prefixBits := uint64(0)
	err := forEachMessage(raw, func(number protowire.Number, wireType protowire.Type, value []byte) error {
		switch number {
		case 1:
			if wireType != protowire.BytesType || (len(value) != 4 && len(value) != 16) {
				return ErrInvalidDatabase
			}
			addressBytes = append([]byte(nil), value...)
		case 2:
			if wireType != protowire.VarintType {
				return ErrInvalidDatabase
			}
			value, n := protowire.ConsumeVarint(value)
			if n < 0 {
				return ErrInvalidDatabase
			}
			prefixBits = value
		}
		return nil
	})
	if err != nil || len(addressBytes) == 0 {
		return netip.Prefix{}, ErrInvalidDatabase
	}
	address, ok := netip.AddrFromSlice(addressBytes)
	if !ok || prefixBits > uint64(address.BitLen()) {
		return netip.Prefix{}, ErrInvalidDatabase
	}
	return netip.PrefixFrom(address.Unmap(), int(prefixBits)).Masked(), nil
}

func parseGeoSiteList(raw []byte) (map[string]*domainSet, error) {
	result := make(map[string]*domainSet)
	groups := 0
	err := forEachMessage(raw, func(number protowire.Number, wireType protowire.Type, value []byte) error {
		if number != 1 || wireType != protowire.BytesType {
			return nil
		}
		groups++
		if groups > maxGroups {
			return ErrInvalidDatabase
		}
		code, domains, err := parseGeoSite(value)
		if err != nil {
			return err
		}
		if code == "" {
			return nil
		}
		set := result[code]
		if set == nil {
			set = &domainSet{full: make(map[string]struct{}), root: make(map[string]struct{})}
			result[code] = set
		}
		for _, domain := range domains {
			switch domain.kind {
			case 0:
				set.plain = append(set.plain, domain.value)
			case 1:
				expression, compileErr := regexp.Compile(domain.value)
				if compileErr != nil {
					return ErrInvalidDatabase
				}
				set.regular = append(set.regular, expression)
			case 2:
				set.root[domain.value] = struct{}{}
			case 3:
				set.full[domain.value] = struct{}{}
			default:
				return ErrInvalidDatabase
			}
		}
		return nil
	})
	return result, err
}

type domainRule struct {
	kind  uint64
	value string
}

func parseGeoSite(raw []byte) (string, []domainRule, error) {
	code := ""
	domains := make([]domainRule, 0)
	err := forEachMessage(raw, func(number protowire.Number, wireType protowire.Type, value []byte) error {
		switch number {
		case 1:
			if wireType != protowire.BytesType || len(value) == 0 || len(value) > 96 {
				return ErrInvalidDatabase
			}
			code = strings.ToLower(string(value))
		case 2:
			if wireType != protowire.BytesType || len(domains) >= maxEntries {
				return ErrInvalidDatabase
			}
			domain, err := parseDomain(value)
			if err != nil {
				return err
			}
			domains = append(domains, domain)
		}
		return nil
	})
	return code, domains, err
}

func parseDomain(raw []byte) (domainRule, error) {
	rule := domainRule{}
	err := forEachMessage(raw, func(number protowire.Number, wireType protowire.Type, value []byte) error {
		switch number {
		case 1:
			if wireType != protowire.VarintType {
				return ErrInvalidDatabase
			}
			kind, n := protowire.ConsumeVarint(value)
			if n < 0 || kind > 3 {
				return ErrInvalidDatabase
			}
			rule.kind = kind
		case 2:
			if wireType != protowire.BytesType || len(value) == 0 || len(value) > maxDomainLength*4 {
				return ErrInvalidDatabase
			}
			rule.value = strings.ToLower(string(value))
		}
		return nil
	})
	if err != nil || rule.value == "" || strings.ContainsAny(rule.value, "\x00\r\n") {
		return domainRule{}, ErrInvalidDatabase
	}
	return rule, nil
}

func forEachMessage(raw []byte, visit func(protowire.Number, protowire.Type, []byte) error) error {
	for len(raw) > 0 {
		number, wireType, tagBytes := protowire.ConsumeTag(raw)
		if tagBytes < 0 {
			return ErrInvalidDatabase
		}
		raw = raw[tagBytes:]
		var value []byte
		var consumed int
		switch wireType {
		case protowire.BytesType:
			value, consumed = protowire.ConsumeBytes(raw)
		case protowire.VarintType:
			_, consumed = protowire.ConsumeVarint(raw)
			if consumed >= 0 {
				value = raw[:consumed]
			}
		default:
			consumed = protowire.ConsumeFieldValue(number, wireType, raw)
			if consumed >= 0 {
				value = raw[:consumed]
			}
		}
		if consumed < 0 || consumed > len(raw) {
			return ErrInvalidDatabase
		}
		if err := visit(number, wireType, value); err != nil {
			return err
		}
		raw = raw[consumed:]
	}
	return nil
}
