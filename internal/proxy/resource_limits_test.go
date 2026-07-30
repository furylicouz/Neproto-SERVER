package proxy

import (
	"net/netip"
	"testing"
	"time"
)

func TestResourceLimiterEnforcesAndReleasesConcurrentLimits(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter, err := newResourceLimiterWithClock(ResourceLimitConfig{
		MaxSessionsPerUser:            1,
		MaxTCPConnectionsGlobal:       2,
		MaxTCPConnectionsPerUser:      1,
		MaxUDPAssociationsGlobal:      2,
		MaxUDPAssociationsPerUser:     1,
		UDPPacketsPerSecondGlobal:     100,
		UDPPacketsPerSecondPerUser:    100,
		UDPBytesPerSecondGlobal:       1 << 20,
		UDPBytesPerSecondPerUser:      1 << 20,
		DNSQueriesPerSecondGlobal:     100,
		DNSQueriesPerSecondPerUser:    100,
		TargetCreatesPerSecondGlobal:  100,
		TargetCreatesPerSecondPerUser: 100,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}

	if !limiter.AcquireSession("alice") || limiter.AcquireSession("alice") {
		t.Fatal("per-user session limit was not enforced")
	}
	if !limiter.AcquireSession("bob") {
		t.Fatal("independent user session was rejected")
	}
	limiter.ReleaseSession("alice")
	if !limiter.AcquireSession("alice") {
		t.Fatal("released session capacity was not reusable")
	}

	if !limiter.AcquireTCP("alice") || limiter.AcquireTCP("alice") {
		t.Fatal("per-user TCP limit was not enforced")
	}
	if !limiter.AcquireTCP("bob") || limiter.AcquireTCP("carol") {
		t.Fatal("global TCP limit was not enforced")
	}
	limiter.ReleaseTCP("alice")
	if !limiter.AcquireTCP("carol") {
		t.Fatal("released TCP capacity was not reusable")
	}

	if !limiter.AcquireUDP("alice") || limiter.AcquireUDP("alice") {
		t.Fatal("per-user UDP association limit was not enforced")
	}
	if !limiter.AcquireUDP("bob") || limiter.AcquireUDP("carol") {
		t.Fatal("global UDP association limit was not enforced")
	}
	limiter.ReleaseUDP("alice")
	if !limiter.AcquireUDP("carol") {
		t.Fatal("released UDP association capacity was not reusable")
	}

	snapshot := limiter.Snapshot()
	if snapshot.ActiveSessions != 2 || snapshot.ActiveTCPConnections != 2 ||
		snapshot.ActiveUDPAssociations != 2 || snapshot.SessionLimitRejects != 1 ||
		snapshot.TCPLimitRejects != 2 || snapshot.UDPAssociationLimitRejects != 2 {
		t.Fatalf("unexpected limiter snapshot: %+v", snapshot)
	}
}

func mustAddrPort(t *testing.T, raw string) netip.AddrPort {
	t.Helper()
	address, err := netip.ParseAddrPort(raw)
	if err != nil {
		t.Fatalf("parse address %q: %v", raw, err)
	}
	return address
}

func TestResourceLimiterAppliesGlobalAndPerUserUDPRatesAtomically(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	config := ResourceLimitConfig{
		MaxSessionsPerUser:            2,
		MaxTCPConnectionsGlobal:       2,
		MaxTCPConnectionsPerUser:      2,
		MaxUDPAssociationsGlobal:      2,
		MaxUDPAssociationsPerUser:     2,
		UDPPacketsPerSecondGlobal:     3,
		UDPPacketsPerSecondPerUser:    2,
		UDPBytesPerSecondGlobal:       30,
		UDPBytesPerSecondPerUser:      20,
		DNSQueriesPerSecondGlobal:     2,
		DNSQueriesPerSecondPerUser:    1,
		TargetCreatesPerSecondGlobal:  2,
		TargetCreatesPerSecondPerUser: 1,
	}
	limiter, err := newResourceLimiterWithClock(config, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}

	if !limiter.AllowUDPPacket("alice", 10, true, true) {
		t.Fatal("first packet was rejected")
	}
	if limiter.AllowUDPPacket("alice", 10, true, false) {
		t.Fatal("per-user DNS rate was not enforced")
	}
	if limiter.AllowUDPPacket("alice", 10, false, true) {
		t.Fatal("per-user target creation rate was not enforced")
	}
	if !limiter.AllowUDPPacket("alice", 10, false, false) {
		t.Fatal("independent packet within packet/byte rate was rejected")
	}
	if limiter.AllowUDPPacket("alice", 1, false, false) {
		t.Fatal("per-user packet rate was not enforced")
	}

	now = now.Add(time.Second)
	if !limiter.AllowUDPPacket("alice", 20, true, true) {
		t.Fatal("rate budget did not refill after one second")
	}
	if limiter.AllowUDPPacket("bob", 11, false, false) {
		t.Fatal("global byte rate was not enforced")
	}

	snapshot := limiter.Snapshot()
	if snapshot.UDPRateLimitDrops < 4 || snapshot.DNSRateLimitDrops == 0 ||
		snapshot.TargetRateLimitDrops == 0 {
		t.Fatalf("missing rate-limit accounting: %+v", snapshot)
	}
}

func TestResourceLimiterRejectsUnsafeConfiguration(t *testing.T) {
	valid := ResourceLimitConfig{
		MaxSessionsPerUser: 1, MaxTCPConnectionsGlobal: 2, MaxTCPConnectionsPerUser: 1,
		MaxUDPAssociationsGlobal: 2, MaxUDPAssociationsPerUser: 1,
		UDPPacketsPerSecondGlobal: 2, UDPPacketsPerSecondPerUser: 1,
		UDPBytesPerSecondGlobal: 2, UDPBytesPerSecondPerUser: 1,
		DNSQueriesPerSecondGlobal: 2, DNSQueriesPerSecondPerUser: 1,
		TargetCreatesPerSecondGlobal: 2, TargetCreatesPerSecondPerUser: 1,
	}
	invalid := valid
	invalid.MaxTCPConnectionsPerUser = invalid.MaxTCPConnectionsGlobal + 1
	if _, err := NewResourceLimiter(invalid); err == nil {
		t.Fatal("per-user limit above global limit was accepted")
	}
	invalid = valid
	invalid.UDPBytesPerSecondGlobal = 0
	if _, err := NewResourceLimiter(invalid); err == nil {
		t.Fatal("zero rate was accepted")
	}
}

func TestUDPFirstReplyAntiAmplification(t *testing.T) {
	allowed := newAllowedUDPAddresses()
	address := mustAddrPort(t, "203.0.113.10:443")
	if ok, added := allowed.Add(address); !ok || !added {
		t.Fatal("target was not added")
	}
	allowed.AddClientBytes(address, 40)
	if allowed.AllowReply(address, 1401, 1280) {
		t.Fatal("oversized first reply bypassed anti-amplification")
	}
	if !allowed.AllowReply(address, 1400, 1280) {
		t.Fatal("bounded first reply was rejected")
	}
	if !allowed.AllowReply(address, 65_507, 1280) {
		t.Fatal("validated target remained amplification-limited")
	}
}
