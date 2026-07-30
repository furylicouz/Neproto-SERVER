package proxy

import "sync/atomic"

// UDPStatsSnapshot contains aggregate, destination-free runtime counters. It
// deliberately excludes addresses, payloads, packet identifiers, and user
// credentials so it is safe to expose through production observability.
type UDPStatsSnapshot struct {
	ActiveAssociations      int64
	OpenedAssociations      uint64
	AssociationLimitRejects uint64
	ClientDatagrams         uint64
	TargetDatagrams         uint64
	ClientBytes             uint64
	TargetBytes             uint64
	PolicyDrops             uint64
	TargetLimitDrops        uint64
	UnexpectedSourceDrops   uint64
	OversizedDrops          uint64
	IdleExpirations         uint64
	RelayErrors             uint64
	RateLimitDrops          uint64
	AmplificationDrops      uint64
}

// UDPStatistics owns lock-free counters shared by all UDP associations of one
// proxy server. A Server may be given a pointer to this value when its owner
// needs snapshots; nil disables collection without changing relay behavior.
type UDPStatistics struct {
	activeAssociations      atomic.Int64
	openedAssociations      atomic.Uint64
	associationLimitRejects atomic.Uint64
	clientDatagrams         atomic.Uint64
	targetDatagrams         atomic.Uint64
	clientBytes             atomic.Uint64
	targetBytes             atomic.Uint64
	policyDrops             atomic.Uint64
	targetLimitDrops        atomic.Uint64
	unexpectedSourceDrops   atomic.Uint64
	oversizedDrops          atomic.Uint64
	idleExpirations         atomic.Uint64
	relayErrors             atomic.Uint64
	rateLimitDrops          atomic.Uint64
	amplificationDrops      atomic.Uint64
}

func (s *UDPStatistics) Snapshot() UDPStatsSnapshot {
	if s == nil {
		return UDPStatsSnapshot{}
	}
	return UDPStatsSnapshot{
		ActiveAssociations:      s.activeAssociations.Load(),
		OpenedAssociations:      s.openedAssociations.Load(),
		AssociationLimitRejects: s.associationLimitRejects.Load(),
		ClientDatagrams:         s.clientDatagrams.Load(),
		TargetDatagrams:         s.targetDatagrams.Load(),
		ClientBytes:             s.clientBytes.Load(),
		TargetBytes:             s.targetBytes.Load(),
		PolicyDrops:             s.policyDrops.Load(),
		TargetLimitDrops:        s.targetLimitDrops.Load(),
		UnexpectedSourceDrops:   s.unexpectedSourceDrops.Load(),
		OversizedDrops:          s.oversizedDrops.Load(),
		IdleExpirations:         s.idleExpirations.Load(),
		RelayErrors:             s.relayErrors.Load(),
		RateLimitDrops:          s.rateLimitDrops.Load(),
		AmplificationDrops:      s.amplificationDrops.Load(),
	}
}

func (s *UDPStatistics) associationOpened() {
	if s != nil {
		s.openedAssociations.Add(1)
		s.activeAssociations.Add(1)
	}
}

func (s *UDPStatistics) associationClosed() {
	if s != nil {
		s.activeAssociations.Add(-1)
	}
}
