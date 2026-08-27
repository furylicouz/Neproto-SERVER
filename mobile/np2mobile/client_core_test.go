package np2mobile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"neproto.local/chameleon/internal/clientcore"
	"neproto.local/chameleon/internal/clienthost"
)

func TestInstanceClientCoreAppliesRoutesAfterStrictAuthentication(t *testing.T) {
	events := []string{}
	fake := &fakeStrictCore{events: &events}
	core := newStrictClientCore(fake)
	if err := core.SetClientRoutesJSON(`[]`); err != nil {
		t.Fatal(err)
	}
	profile, secret := strictClientCoreProfile(t)
	if err := core.Connect(profile, secret, "ios-connect", "79d6ac07-a320-42d7-8f8f-1b8576ee7bd1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"connect", "routes"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events=%v want=%v", events, want)
	}
	if fake.request.Profile.CarrierPolicy != "http3-only" || fake.request.Profile.MaxParallelCarriers != 1 {
		t.Fatalf("request=%+v", fake.request.Profile)
	}
	if err := core.AttachPacketTunnel(9, 1500); err != nil {
		t.Fatal(err)
	}
	if fake.fileDescriptor != 9 || fake.mtu != 1500 {
		t.Fatalf("fd=%d mtu=%d", fake.fileDescriptor, fake.mtu)
	}
}

func TestNewStrictHTTPSClientCoreConstructsDisconnected(t *testing.T) {
	core, err := NewStrictHTTPSClientCore()
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if got := core.SnapshotJSON(); !strings.Contains(got, `"state":"disconnected"`) {
		t.Fatalf("snapshot = %s", got)
	}
}

func TestInstanceClientCoreCloseCancelsInFlightConnect(t *testing.T) {
	started := make(chan struct{})
	fake := &fakeStrictCore{connect: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	core := newStrictClientCore(fake)
	profile, secret := strictClientCoreProfile(t)
	result := make(chan error, 1)
	go func() {
		result <- core.Connect(profile, secret, "ios-cancel", "79d6ac07-a320-42d7-8f8f-1b8576ee7bd1")
	}()
	<-started
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("connect error=%v", err)
	}
}

func TestInstanceClientCoreSnapshotIncludesPayloadFreeQUICHealth(t *testing.T) {
	core := newStrictClientCore(&fakeStrictCore{})
	raw := core.SnapshotJSON()
	var snapshot struct {
		QUICSmoothedRTTMS int64  `json:"quic_smoothed_rtt_ms"`
		QUICPacketsSent   uint64 `json:"quic_packets_sent"`
		QUICPacketsLost   uint64 `json:"quic_packets_lost"`
		QUICBytesSent     uint64 `json:"quic_bytes_sent"`
		QUICBytesLost     uint64 `json:"quic_bytes_lost"`
	}
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.QUICSmoothedRTTMS != 37 || snapshot.QUICPacketsSent != 1000 ||
		snapshot.QUICPacketsLost != 25 || snapshot.QUICBytesSent != 900_000 ||
		snapshot.QUICBytesLost != 20_000 {
		t.Fatalf("QUIC snapshot=%+v raw=%s", snapshot, raw)
	}
}

type fakeStrictCore struct {
	events         *[]string
	connect        func(context.Context) error
	request        clientcore.ConnectRequest
	fileDescriptor int
	mtu            uint32
}

func (f *fakeStrictCore) Connect(ctx context.Context, request clientcore.ConnectRequest) (clienthost.Snapshot, error) {
	if f.events != nil {
		*f.events = append(*f.events, "connect")
	}
	f.request = request
	if f.connect != nil {
		if err := f.connect(ctx); err != nil {
			return clienthost.Snapshot{}, err
		}
	}
	return clienthost.Snapshot{State: clienthost.StateConnected, Carrier: clienthost.CarrierHTTP3WebTransport}, nil
}
func (f *fakeStrictCore) SetClientRoutesJSON([]byte) error {
	if f.events != nil {
		*f.events = append(*f.events, "routes")
	}
	return nil
}
func (f *fakeStrictCore) AttachPacketTunnel(_ context.Context, fd int, mtu uint32) error {
	f.fileDescriptor, f.mtu = fd, mtu
	return nil
}
func (*fakeStrictCore) NetworkChanged(context.Context, string) (clienthost.Snapshot, error) {
	return clienthost.Snapshot{State: clienthost.StateConnected}, nil
}
func (*fakeStrictCore) Snapshot() clienthost.Snapshot {
	return clienthost.Snapshot{State: clienthost.StateConnected, Carrier: clienthost.CarrierHTTP3WebTransport}
}
func (*fakeStrictCore) RuntimeSnapshot() clientcore.RuntimeSnapshot {
	return clientcore.RuntimeSnapshot{
		Carrier: clienthost.CarrierHTTP3WebTransport, ServerAddresses: []string{"8.8.8.8"},
		QUICSmoothedRTTMS: 37, QUICPacketsSent: 1000, QUICPacketsLost: 25,
		QUICBytesSent: 900_000, QUICBytesLost: 20_000,
	}
}
func (*fakeStrictCore) FetchCatalog(context.Context) ([]byte, error) { return []byte(`{}`), nil }
func (*fakeStrictCore) Close(context.Context) error                  { return nil }

func strictClientCoreProfile(t *testing.T) (string, string) {
	t.Helper()
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	profile := `{"server_identity":"vpn.example.com","server_addresses":["8.8.8.8"],` +
		`"secret_file":"keychain",` +
		`"http3_url":"https://vpn.example.com/private/http3/session","profile":"web",` +
		`"carrier_policy":"http3-only","max_cover_overhead_percent":30,"initial_window_bytes":2097152,` +
		`"max_streams":128,"max_parallel_carriers":1,` +
		`"http3_timeout":"5s","carrier_cache_ttl":"10m"}`
	return profile, secret
}
