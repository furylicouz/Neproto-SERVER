package geodata

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"neproto.local/chameleon/internal/cluster"
)

func TestEngineMatchesOfficialV2FlyWireShape(t *testing.T) {
	directory := t.TempDir()
	cidr := message(
		bytesField(1, []byte{203, 0, 113, 0}),
		varintField(2, 24),
	)
	geoIP := message(bytesField(1, []byte("NL")), bytesField(2, cidr))
	if err := os.WriteFile(filepath.Join(directory, "geoip.dat"), bytesField(1, geoIP), 0o600); err != nil {
		t.Fatal(err)
	}
	domain := message(varintField(1, 2), bytesField(2, []byte("youtube.com")))
	geoSite := message(bytesField(1, []byte("YOUTUBE")), bytesField(2, domain))
	if err := os.WriteFile(filepath.Join(directory, "geosite.dat"), bytesField(1, geoSite), 0o600); err != nil {
		t.Fatal(err)
	}
	engine, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !engine.Match(context.Background(), cluster.RouteMatch{GeoSiteCategories: []string{"youtube"}}, cluster.Target{Domain: "www.youtube.com"}) {
		t.Fatal("GeoSite root-domain rule did not match subdomain")
	}
	if engine.Match(context.Background(), cluster.RouteMatch{GeoSiteCategories: []string{"youtube"}}, cluster.Target{Domain: "notyoutube.com"}) {
		t.Fatal("GeoSite rule crossed a domain boundary")
	}
	if !engine.Match(context.Background(), cluster.RouteMatch{GeoIPCountries: []string{"nl"}}, cluster.Target{Address: netip.MustParseAddr("203.0.113.9")}) {
		t.Fatal("GeoIP CIDR did not match")
	}
	if engine.Match(context.Background(), cluster.RouteMatch{GeoIPCountries: []string{"nl"}}, cluster.Target{Address: netip.MustParseAddr("198.51.100.9")}) {
		t.Fatal("GeoIP CIDR matched unrelated address")
	}
	country, ok := engine.CountryCode(netip.MustParseAddr("203.0.113.9"))
	if !ok || country != "NL" {
		t.Fatalf("CountryCode()=(%q, %v)", country, ok)
	}
	if _, ok := engine.CountryCode(netip.MustParseAddr("198.51.100.9")); ok {
		t.Fatal("CountryCode() matched unrelated address")
	}
}

func TestEngineLoadsCurrentV2FlyRelease(t *testing.T) {
	directory := os.Getenv("NEPROTO_REAL_GEODATA")
	if directory == "" {
		t.Skip("set NEPROTO_REAL_GEODATA for the release-data integration test")
	}
	engine, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, country := range []string{"ru", "nl", "private"} {
		if !engine.HasGeoIP(country) {
			t.Fatalf("current GeoIP release does not contain %q", country)
		}
	}
	for _, category := range []string{"youtube", "telegram", "openai", "category-media"} {
		if !engine.HasGeoSite(category) {
			t.Fatalf("current GeoSite release does not contain %q", category)
		}
	}
	if !engine.Match(context.Background(), cluster.RouteMatch{GeoSiteCategories: []string{"youtube"}}, cluster.Target{Domain: "www.youtube.com"}) {
		t.Fatal("current GeoSite YouTube category did not match youtube.com")
	}
	if !engine.Match(context.Background(), cluster.RouteMatch{GeoSiteCategories: []string{"openai"}}, cluster.Target{Domain: "chatgpt.com"}) {
		t.Fatal("current GeoSite OpenAI category did not match chatgpt.com")
	}
}

func TestEngineMatchesDomainRuleAgainstNumericTunnelTarget(t *testing.T) {
	address := netip.MustParseAddr("203.0.113.27")
	engine := &Engine{
		geoIP:   map[string]*prefixSet{},
		geoSite: map[string]*domainSet{},
		cache: map[string]cachedAddresses{
			"2ip.ru": {addresses: []netip.Addr{address}, expires: time.Now().Add(time.Minute)},
		},
	}
	matched := engine.Match(
		context.Background(),
		cluster.RouteMatch{DomainSuffixes: []string{"2ip.ru"}},
		cluster.Target{Address: address, Port: 443, Protocol: cluster.ProtocolTCP},
	)
	if !matched {
		t.Fatal("domain route did not match the numeric target supplied by the iOS TUN")
	}
}

func message(fields ...[]byte) []byte {
	var result []byte
	for _, field := range fields {
		result = append(result, field...)
	}
	return result
}

func bytesField(number protowire.Number, value []byte) []byte {
	result := protowire.AppendTag(nil, number, protowire.BytesType)
	return protowire.AppendBytes(result, value)
}

func varintField(number protowire.Number, value uint64) []byte {
	result := protowire.AppendTag(nil, number, protowire.VarintType)
	return protowire.AppendVarint(result, value)
}
