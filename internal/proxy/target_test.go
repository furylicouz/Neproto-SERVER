package proxy

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"neproto.local/chameleon/internal/cluster"
)

func TestClusterCatalogCommandHasCanonicalTargetFreeEncoding(t *testing.T) {
	raw, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterCatalog})
	if err != nil {
		t.Fatalf("EncodeOpenRequest() error = %v", err)
	}
	if !bytes.Equal(raw, []byte{byte(CommandClusterCatalog)}) {
		t.Fatalf("catalog metadata = %x", raw)
	}
	decoded, err := DecodeOpenRequest(raw)
	if err != nil || decoded.Command != CommandClusterCatalog || decoded.Target != (Target{}) {
		t.Fatalf("DecodeOpenRequest() = %+v, %v", decoded, err)
	}
	if _, err := DecodeOpenRequest(append(raw, 0)); err == nil {
		t.Fatal("accepted trailing catalog command bytes")
	}
}

func TestClusterStateCommandHasCanonicalTargetFreeEncoding(t *testing.T) {
	raw, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterState})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, []byte{byte(CommandClusterState)}) {
		t.Fatalf("state metadata = %x", raw)
	}
	decoded, err := DecodeOpenRequest(raw)
	if err != nil || decoded.Command != CommandClusterState || decoded.Target != (Target{}) {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if _, err := DecodeOpenRequest(append(raw, 0)); err == nil {
		t.Fatal("accepted trailing state command bytes")
	}
}

func TestClusterRelayCommandRoundTripsStrictBoundedMetadata(t *testing.T) {
	want := cluster.RelayRequest{
		Version: cluster.RelayVersion, RouteID: "media", UserID: "alice", RemainingHops: 2,
		VisitedNodeIDs: []string{"master"}, RemainingNodeIDs: []string{"edge-02"},
		TraceID: "0123456789abcdef", TargetHost: "example.com", TargetPort: 443, Protocol: cluster.ProtocolTCP,
	}
	raw, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterRelay, Relay: &want})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOpenRequest(raw)
	if err != nil || decoded.Command != CommandClusterRelay || decoded.Relay == nil || !reflect.DeepEqual(*decoded.Relay, want) {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if _, err := DecodeOpenRequest(append(raw, byte('x'))); err == nil {
		t.Fatal("accepted relay metadata with trailing bytes")
	}
}

func TestClusterCatalogRelayCommandBindsOriginalUser(t *testing.T) {
	raw, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterCatalogRelay, CatalogUserID: "user_01-token"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOpenRequest(raw)
	if err != nil || decoded.Command != CommandClusterCatalogRelay || decoded.CatalogUserID != "user_01-token" {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if _, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterCatalogRelay, CatalogUserID: "bad/user"}); err == nil {
		t.Fatal("accepted invalid relayed catalog user")
	}
	if _, err := DecodeOpenRequest(append(raw, 0)); err == nil {
		t.Fatal("accepted trailing relayed catalog bytes")
	}
}

func TestClusterCredentialSyncMetadataIsStrictAndBounded(t *testing.T) {
	request := cluster.CredentialSyncRequest{
		Version: 1, Operation: cluster.CredentialSyncUpsert,
		CredentialID: "AQEBAQEBAQEBAQEBAQEBAQ", Secret: "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI",
	}
	raw, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterCredentialSync, CredentialSync: &request})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOpenRequest(raw)
	if err != nil || decoded.CredentialSync == nil || !reflect.DeepEqual(*decoded.CredentialSync, request) {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if _, err := DecodeOpenRequest(append(raw, 0)); err == nil {
		t.Fatal("accepted trailing credential sync metadata")
	}
}

func TestClusterGeoDataControlMetadataIsStrictAndBounded(t *testing.T) {
	request := cluster.GeoDataRequest{Version: cluster.GeoDataControlVersion, Operation: cluster.GeoDataUpdate}
	raw, err := EncodeOpenRequest(OpenRequest{Command: CommandClusterGeoData, GeoData: &request})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOpenRequest(raw)
	if err != nil || decoded.GeoData == nil || !reflect.DeepEqual(*decoded.GeoData, request) {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if _, err := DecodeOpenRequest(append(raw, 0)); err == nil {
		t.Fatal("accepted trailing geodata control metadata")
	}
}

func TestTargetMetadataRoundTripsCanonicalAddresses(t *testing.T) {
	tests := []struct {
		name string
		in   Target
		want Target
	}{
		{name: "domain", in: Target{Host: "Example.COM.", Port: 443}, want: Target{Host: "example.com", Port: 443}},
		{name: "IPv4", in: Target{Host: "1.1.1.1", Port: 53}, want: Target{Host: "1.1.1.1", Port: 53}},
		{name: "IPv6", in: Target{Host: "2606:4700:4700::1111", Port: 853}, want: Target{Host: "2606:4700:4700::1111", Port: 853}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := EncodeTarget(test.in)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := DecodeTarget(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got != test.want {
				t.Fatalf("target mismatch: got %#v want %#v", got, test.want)
			}
			reencoded, err := EncodeTarget(got)
			if err != nil {
				t.Fatalf("reencode: %v", err)
			}
			if string(reencoded) != string(raw) {
				t.Fatal("target encoding is not canonical")
			}
		})
	}
}

func TestTargetMetadataRejectsMalformedInput(t *testing.T) {
	tests := [][]byte{
		nil,
		{1, 1},
		{2, 1, 1, 1, 1, 1, 0, 80},
		{1, 9, 0, 80},
		{1, 1, 1, 'a', 0, 0},
		{1, 1, 2, 'a', 0, 80},
		{1, 1, 0x81, 0x00, 'a', 0, 80},
	}
	for _, raw := range tests {
		if _, err := DecodeTarget(raw); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("expected malformed target for %x, got %v", raw, err)
		}
	}
	if _, err := EncodeTarget(Target{Host: "bad host", Port: 443}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("invalid hostname accepted: %v", err)
	}
	if _, err := EncodeTarget(Target{Host: "example.com", Port: 0}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("zero port accepted: %v", err)
	}
	if _, err := EncodeTarget(Target{Host: "fe80::1%eth0", Port: 443}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("zoned IPv6 target accepted: %v", err)
	}
}

func TestOpenRequestSupportsTCPAndUDPWithoutChangingLegacyEncoding(t *testing.T) {
	target := Target{Host: "1.1.1.1", Port: 53}
	legacy, err := EncodeTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := EncodeOpenRequest(OpenRequest{Command: CommandTCPConnect, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if string(tcp) != string(legacy) || tcp[0] != 0x01 {
		t.Fatalf("TCP encoding changed: legacy=%x request=%x", legacy, tcp)
	}

	udpFixed, err := EncodeOpenRequest(OpenRequest{Command: CommandUDPFixed, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if udpFixed[0] != 0x02 || string(udpFixed[1:]) != string(legacy[1:]) {
		t.Fatalf("UDP fixed encoding=%x", udpFixed)
	}
	decodedFixed, err := DecodeOpenRequest(udpFixed)
	if err != nil || decodedFixed.Command != CommandUDPFixed || decodedFixed.Target != target {
		t.Fatalf("decoded UDP fixed=%+v err=%v", decodedFixed, err)
	}

	udpAssociate, err := EncodeOpenRequest(OpenRequest{Command: CommandUDPAssociate})
	if err != nil {
		t.Fatal(err)
	}
	if string(udpAssociate) != string([]byte{0x03}) {
		t.Fatalf("UDP associate encoding=%x", udpAssociate)
	}
	decodedAssociate, err := DecodeOpenRequest(udpAssociate)
	if err != nil || decodedAssociate.Command != CommandUDPAssociate || decodedAssociate.Target != (Target{}) {
		t.Fatalf("decoded UDP associate=%+v err=%v", decodedAssociate, err)
	}
}

func TestOpenRequestCarriesBoundedClientRouteHint(t *testing.T) {
	for _, command := range []OpenCommand{CommandTCPClientRoute, CommandUDPClientRoute} {
		request := OpenRequest{
			Command: command, Target: Target{Host: "203.0.113.20", Port: 443},
			ClientRoute: &cluster.ClientRouteRequest{
				Version: cluster.ClientRouteVersion, RouteID: "local-media",
				Action: cluster.RouteAction{Kind: cluster.RouteActionNode, NodeIDs: []string{"edge-01"}},
			},
		}
		raw, err := EncodeOpenRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeOpenRequest(raw)
		if err != nil || decoded.Command != command || decoded.Target != request.Target ||
			decoded.ClientRoute == nil || !reflect.DeepEqual(*decoded.ClientRoute, *request.ClientRoute) {
			t.Fatalf("decoded=%+v err=%v", decoded, err)
		}
		if _, err := DecodeOpenRequest(append(raw, 0)); err == nil {
			t.Fatal("client route metadata accepted trailing bytes")
		}
	}
}

func TestOpenRequestRejectsCommandShapeConfusion(t *testing.T) {
	if _, err := DecodeTarget([]byte{0x02, 0x24, 1, 1, 1, 1, 0, 53}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("legacy decoder accepted UDP command: %v", err)
	}
	for _, request := range []OpenRequest{
		{Command: CommandTCPConnect},
		{Command: CommandUDPFixed},
		{Command: CommandUDPAssociate, Target: Target{Host: "1.1.1.1", Port: 53}},
		{Command: OpenCommand(0xff)},
	} {
		if _, err := EncodeOpenRequest(request); !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("invalid request accepted: %+v err=%v", request, err)
		}
	}
	if _, err := DecodeOpenRequest([]byte{0x03, 0}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("UDP associate trailing bytes accepted: %v", err)
	}
}

func TestDefaultPolicyBlocksUnsafeAndSpecialAddresses(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.1.1",
		"192.0.2.1", "192.168.1.1", "198.18.0.1", "224.0.0.1", "240.0.0.1",
		"::", "::1", "100::1", "fc00::1", "fe80::1", "ff02::1", "2001:db8::1",
	}
	policy := DestinationPolicy{}
	for _, raw := range blocked {
		address := netip.MustParseAddr(raw)
		if policy.Allows(address) {
			t.Errorf("default policy allowed %s", address)
		}
	}
	allowed := []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"}
	for _, raw := range allowed {
		address := netip.MustParseAddr(raw)
		if !policy.Allows(address) {
			t.Errorf("default policy blocked public address %s", address)
		}
	}
}

func TestResolveRejectsDomainWithAnyUnsafeAnswer(t *testing.T) {
	resolver := staticResolver{addresses: []netip.Addr{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("127.0.0.1"),
	}}
	_, err := (DestinationPolicy{}).Resolve(context.Background(), Target{Host: "mixed.example", Port: 443}, resolver)
	if !errors.Is(err, ErrDestinationDenied) {
		t.Fatalf("expected mixed answer denial, got %v", err)
	}

	resolved, err := (DestinationPolicy{AllowPrivate: true}).Resolve(
		context.Background(), Target{Host: "mixed.example", Port: 443}, resolver,
	)
	if err != nil {
		t.Fatalf("explicit test override failed: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("resolved %d addresses", len(resolved))
	}
}

type staticResolver struct {
	addresses []netip.Addr
	err       error
}

func (r staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r.addresses...), r.err
}
