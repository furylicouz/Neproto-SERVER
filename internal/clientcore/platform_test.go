package clientcore

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"neproto.local/chameleon/internal/clienthost"
	"neproto.local/chameleon/internal/tunstack"
)

func TestCoreExposesOnlyActiveRuntimePlatformAdapter(t *testing.T) {
	runtime := newPlatformFakeRuntime()
	core, err := New(Options{Connect: connectorReturning(runtime)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = core.Close(context.Background()) }()

	if err := core.SetClientRoutesJSON([]byte(`[]`)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("routes before connect error = %v", err)
	}
	if _, err := core.Connect(context.Background(), validRequest("platform-connect")); err != nil {
		t.Fatal(err)
	}
	if err := core.SetClientRoutesJSON([]byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	endpoint := &fakePacketDevice{}
	if err := core.AttachPacketDevice(context.Background(), endpoint, 1500); err != nil {
		t.Fatal(err)
	}

	details := core.RuntimeSnapshot()
	if details.Carrier != clienthost.CarrierHTTP3WebTransport ||
		len(details.ServerAddresses) != 1 || details.ServerAddresses[0] != "203.0.113.7" ||
		details.UploadTotalBytes != 42 || details.CarrierPoolTarget != 1 {
		t.Fatalf("runtime snapshot = %+v", details)
	}
	catalog, err := core.FetchCatalog(context.Background())
	if err != nil || string(catalog) != `{"version":1}` {
		t.Fatalf("catalog=%q error=%v", catalog, err)
	}
	if runtime.routes == nil || runtime.endpoint != endpoint || runtime.mtu != 1500 {
		t.Fatalf("platform adapter routes=%v endpoint=%v mtu=%d", runtime.routes, runtime.endpoint, runtime.mtu)
	}
}

func TestCorePlatformAdapterRejectsInvalidRoutesBeforeRuntimeMutation(t *testing.T) {
	runtime := newPlatformFakeRuntime()
	core, err := New(Options{Connect: connectorReturning(runtime)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = core.Close(context.Background()) }()
	if _, err := core.Connect(context.Background(), validRequest("invalid-routes")); err != nil {
		t.Fatal(err)
	}

	for _, raw := range [][]byte{nil, []byte(`{"not":"routes"}`), []byte(`[{}] trailing`)} {
		if err := core.SetClientRoutesJSON(raw); err == nil {
			t.Fatalf("accepted invalid routes %q", raw)
		}
	}
	if runtime.routes != nil {
		t.Fatal("invalid route input reached runtime")
	}
}

func TestSafeServerAddressesMergesConnectedHTTP3EndpointAndRejectsUnsafeOnlySet(t *testing.T) {
	addresses, err := safeServerAddresses(
		[]netip.Addr{netip.MustParseAddr("37.252.23.223"), netip.MustParseAddr("127.0.0.1")},
		[]netip.Addr{netip.MustParseAddr("104.171.136.10"), netip.MustParseAddr("37.252.23.223")},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"104.171.136.10", "37.252.23.223"}
	if len(addresses) != len(want) || addresses[0] != want[0] || addresses[1] != want[1] {
		t.Fatalf("addresses=%v want=%v", addresses, want)
	}
	if _, err := safeServerAddresses([]netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil); err == nil {
		t.Fatal("accepted address set with no safe route exclusion")
	}
}

type platformFakeRuntime struct {
	*fakeRuntime
	routes   *tunstack.ClientRoutePolicy
	endpoint device.Device
	mtu      uint32
}

func newPlatformFakeRuntime() *platformFakeRuntime {
	return &platformFakeRuntime{fakeRuntime: newFakeRuntime()}
}

func (r *platformFakeRuntime) SetClientRoutes(routes *tunstack.ClientRoutePolicy) error {
	r.routes = routes
	return nil
}

func (r *platformFakeRuntime) AttachPacketDevice(endpoint device.Device, mtu uint32) error {
	r.endpoint, r.mtu = endpoint, mtu
	return nil
}

func (*platformFakeRuntime) RuntimeSnapshot() RuntimeSnapshot {
	return RuntimeSnapshot{
		Carrier: clienthost.CarrierHTTP3WebTransport, ServerAddresses: []string{"203.0.113.7"},
		UploadTotalBytes: 42, CarrierPoolTarget: 1, CarrierPoolHealthy: 1,
	}
}

func (*platformFakeRuntime) FetchCatalog(context.Context) ([]byte, error) {
	return []byte(`{"version":1}`), nil
}

type fakePacketDevice struct{ stack.LinkEndpoint }

func (*fakePacketDevice) Type() string { return "fake" }
func (*fakePacketDevice) Name() string { return "fake" }
