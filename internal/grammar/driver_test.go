package grammar

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

func TestDriverEnforcesCarrierLeaseBudgetAndIdempotentRelease(t *testing.T) {
	manifest := DefaultManifest()
	manifest.WebRTC.MaxConcurrent = 1
	driver, err := NewDriver(manifest, bytes.NewReader(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	lease, err := driver.Acquire(protocol.CarrierWebRTC, now)
	if err != nil {
		t.Fatal(err)
	}
	if lease.ID == 0 || lease.Carrier != protocol.CarrierWebRTC ||
		lease.ExpiresAt.Before(now.Add(120*time.Second)) ||
		lease.ExpiresAt.After(now.Add(300*time.Second)) ||
		lease.IdleTimeout != 60*time.Second || lease.MaxBurstBytes != 512*1024 ||
		lease.MaxDatagramBytes != 16_384 {
		t.Fatalf("lease=%+v", lease)
	}
	if _, err := driver.Acquire(protocol.CarrierWebRTC, now); !errors.Is(err, ErrLeaseCapacity) {
		t.Fatalf("second acquire error=%v", err)
	}
	if !driver.Release(lease.ID) || driver.Release(lease.ID) {
		t.Fatal("release must be successful exactly once")
	}
	if _, err := driver.Acquire(protocol.CarrierWebRTC, now); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestLeaseRotationAndTrafficBoundsAreDeterministic(t *testing.T) {
	driver, err := NewDriver(DefaultManifest(), bytes.NewReader(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(200, 0)
	lease, err := driver.Acquire(protocol.CarrierHTTP3, now)
	if err != nil {
		t.Fatal(err)
	}
	if lease.ShouldRotate(now.Add(44*time.Second), now) {
		t.Fatal("active lease rotated before idle timeout")
	}
	if !lease.ShouldRotate(now.Add(46*time.Second), now) {
		t.Fatal("idle lease did not rotate")
	}
	if !lease.AllowsBurst(2*1024*1024) || lease.AllowsBurst(2*1024*1024+1) {
		t.Fatal("burst bound not enforced")
	}
	if !lease.AllowsDatagram(65_507) || lease.AllowsDatagram(65_508) {
		t.Fatal("datagram bound not enforced")
	}
}

func TestDriverRejectsInvalidManifestCarrierAndTime(t *testing.T) {
	manifest := DefaultManifest()
	manifest.HTTPS.MaxConcurrent = 0
	if _, err := NewDriver(manifest, nil); !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("invalid manifest error=%v", err)
	}
	driver, err := NewDriver(DefaultManifest(), bytes.NewReader(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Acquire(0, time.Now()); !errors.Is(err, ErrLeaseCarrier) {
		t.Fatalf("invalid carrier error=%v", err)
	}
	if _, err := driver.Acquire(protocol.CarrierHTTPS, time.Time{}); !errors.Is(err, ErrLeaseTime) {
		t.Fatalf("zero time error=%v", err)
	}
}
