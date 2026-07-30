package proxy

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidResourceLimits = errors.New("invalid resource limits")
	ErrResourceLimit         = errors.New("resource limit reached")
)

// ResourceLimitConfig defines process-wide and authenticated-user resource
// ceilings. Per-user ceilings must not exceed the matching global ceiling.
// Rate values use a one-second token-bucket burst and steady refill rate.
type ResourceLimitConfig struct {
	MaxSessionsPerUser            int
	MaxTCPConnectionsGlobal       int
	MaxTCPConnectionsPerUser      int
	MaxUDPAssociationsGlobal      int
	MaxUDPAssociationsPerUser     int
	UDPPacketsPerSecondGlobal     int64
	UDPPacketsPerSecondPerUser    int64
	UDPBytesPerSecondGlobal       int64
	UDPBytesPerSecondPerUser      int64
	DNSQueriesPerSecondGlobal     int64
	DNSQueriesPerSecondPerUser    int64
	TargetCreatesPerSecondGlobal  int64
	TargetCreatesPerSecondPerUser int64
}

type ResourceLimitSnapshot struct {
	ActiveSessions             int64
	ActiveTCPConnections       int64
	ActiveUDPAssociations      int64
	SessionLimitRejects        uint64
	TCPLimitRejects            uint64
	UDPAssociationLimitRejects uint64
	UDPRateLimitDrops          uint64
	DNSRateLimitDrops          uint64
	TargetRateLimitDrops       uint64
}

type resourceUserState struct {
	sessions int
	tcp      int
	udp      int
	packets  tokenBucket
	bytes    tokenBucket
	dns      tokenBucket
	targets  tokenBucket
}

type resourceTotals struct {
	sessions int64
	tcp      int64
	udp      int64
	packets  tokenBucket
	bytes    tokenBucket
	dns      tokenBucket
	targets  tokenBucket
}

type resourceRejects struct {
	sessions uint64
	tcp      uint64
	udp      uint64
	rate     uint64
	dns      uint64
	targets  uint64
}

// ResourceLimiter provides atomic global plus per-user accounting. User IDs
// are established by NP/2 authentication; an empty ID is rejected so callers
// cannot accidentally collapse unauthenticated traffic into a shared bucket.
type ResourceLimiter struct {
	mu      sync.Mutex
	config  ResourceLimitConfig
	now     func() time.Time
	global  resourceTotals
	users   map[string]*resourceUserState
	rejects resourceRejects
}

func NewResourceLimiter(config ResourceLimitConfig) (*ResourceLimiter, error) {
	return newResourceLimiterWithClock(config, time.Now)
}

func newResourceLimiterWithClock(config ResourceLimitConfig, now func() time.Time) (*ResourceLimiter, error) {
	if now == nil || !validResourceLimitConfig(config) {
		return nil, ErrInvalidResourceLimits
	}
	started := now()
	return &ResourceLimiter{
		config: config,
		now:    now,
		users:  make(map[string]*resourceUserState),
		global: resourceTotals{
			packets: newTokenBucket(config.UDPPacketsPerSecondGlobal, started),
			bytes:   newTokenBucket(config.UDPBytesPerSecondGlobal, started),
			dns:     newTokenBucket(config.DNSQueriesPerSecondGlobal, started),
			targets: newTokenBucket(config.TargetCreatesPerSecondGlobal, started),
		},
	}, nil
}

func validResourceLimitConfig(config ResourceLimitConfig) bool {
	return config.MaxSessionsPerUser > 0 &&
		config.MaxTCPConnectionsGlobal > 0 && config.MaxTCPConnectionsPerUser > 0 &&
		config.MaxTCPConnectionsPerUser <= config.MaxTCPConnectionsGlobal &&
		config.MaxUDPAssociationsGlobal > 0 && config.MaxUDPAssociationsPerUser > 0 &&
		config.MaxUDPAssociationsPerUser <= config.MaxUDPAssociationsGlobal &&
		validRatePair(config.UDPPacketsPerSecondGlobal, config.UDPPacketsPerSecondPerUser) &&
		validRatePair(config.UDPBytesPerSecondGlobal, config.UDPBytesPerSecondPerUser) &&
		validRatePair(config.DNSQueriesPerSecondGlobal, config.DNSQueriesPerSecondPerUser) &&
		validRatePair(config.TargetCreatesPerSecondGlobal, config.TargetCreatesPerSecondPerUser)
}

func validRatePair(global, perUser int64) bool {
	return global > 0 && perUser > 0 && perUser <= global
}

func (l *ResourceLimiter) userLocked(id string, now time.Time) *resourceUserState {
	user := l.users[id]
	if user != nil {
		return user
	}
	user = &resourceUserState{
		packets: newTokenBucket(l.config.UDPPacketsPerSecondPerUser, now),
		bytes:   newTokenBucket(l.config.UDPBytesPerSecondPerUser, now),
		dns:     newTokenBucket(l.config.DNSQueriesPerSecondPerUser, now),
		targets: newTokenBucket(l.config.TargetCreatesPerSecondPerUser, now),
	}
	l.users[id] = user
	return user
}

func (l *ResourceLimiter) AcquireSession(userID string) bool {
	if l == nil || userID == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	user := l.userLocked(userID, l.now())
	if user.sessions >= l.config.MaxSessionsPerUser {
		l.rejects.sessions++
		return false
	}
	user.sessions++
	l.global.sessions++
	return true
}

func (l *ResourceLimiter) ReleaseSession(userID string) {
	if l == nil || userID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if user := l.users[userID]; user != nil && user.sessions > 0 {
		user.sessions--
		l.global.sessions--
	}
}

func (l *ResourceLimiter) AcquireTCP(userID string) bool {
	if l == nil || userID == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	user := l.userLocked(userID, now)
	if l.global.tcp >= int64(l.config.MaxTCPConnectionsGlobal) ||
		user.tcp >= l.config.MaxTCPConnectionsPerUser {
		l.rejects.tcp++
		return false
	}
	if !takeBoth(now, 1, &l.global.targets, &user.targets) {
		l.rejects.tcp++
		l.rejects.targets++
		return false
	}
	l.global.tcp++
	user.tcp++
	return true
}

func (l *ResourceLimiter) ReleaseTCP(userID string) {
	if l == nil || userID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if user := l.users[userID]; user != nil && user.tcp > 0 {
		user.tcp--
		l.global.tcp--
	}
}

func (l *ResourceLimiter) AcquireUDP(userID string) bool {
	if l == nil || userID == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	user := l.userLocked(userID, l.now())
	if l.global.udp >= int64(l.config.MaxUDPAssociationsGlobal) ||
		user.udp >= l.config.MaxUDPAssociationsPerUser {
		l.rejects.udp++
		return false
	}
	l.global.udp++
	user.udp++
	return true
}

func (l *ResourceLimiter) ReleaseUDP(userID string) {
	if l == nil || userID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if user := l.users[userID]; user != nil && user.udp > 0 {
		user.udp--
		l.global.udp--
	}
}

// AllowUDPPacket atomically consumes global and per-user packet/byte budgets,
// plus DNS and target-creation budgets when those properties apply.
func (l *ResourceLimiter) AllowUDPPacket(userID string, byteCount int, dns, newTarget bool) bool {
	if l == nil || userID == "" || byteCount < 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	user := l.userLocked(userID, now)
	type debit struct {
		global *tokenBucket
		user   *tokenBucket
		amount float64
	}
	debits := []debit{
		{&l.global.packets, &user.packets, 1},
		{&l.global.bytes, &user.bytes, float64(byteCount)},
	}
	if dns {
		debits = append(debits, debit{&l.global.dns, &user.dns, 1})
	}
	if newTarget {
		debits = append(debits, debit{&l.global.targets, &user.targets, 1})
	}
	for _, item := range debits {
		if !item.global.canTake(now, item.amount) || !item.user.canTake(now, item.amount) {
			l.rejects.rate++
			if dns && (item.global == &l.global.dns || item.user == &user.dns) {
				l.rejects.dns++
			}
			if newTarget && (item.global == &l.global.targets || item.user == &user.targets) {
				l.rejects.targets++
			}
			return false
		}
	}
	for _, item := range debits {
		item.global.take(item.amount)
		item.user.take(item.amount)
	}
	return true
}

func (l *ResourceLimiter) Snapshot() ResourceLimitSnapshot {
	if l == nil {
		return ResourceLimitSnapshot{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return ResourceLimitSnapshot{
		ActiveSessions: l.global.sessions, ActiveTCPConnections: l.global.tcp,
		ActiveUDPAssociations: l.global.udp, SessionLimitRejects: l.rejects.sessions,
		TCPLimitRejects: l.rejects.tcp, UDPAssociationLimitRejects: l.rejects.udp,
		UDPRateLimitDrops: l.rejects.rate, DNSRateLimitDrops: l.rejects.dns,
		TargetRateLimitDrops: l.rejects.targets,
	}
}

type tokenBucket struct {
	rate   float64
	tokens float64
	last   time.Time
}

func newTokenBucket(rate int64, now time.Time) tokenBucket {
	value := float64(rate)
	return tokenBucket{rate: value, tokens: value, last: now}
}

func (b *tokenBucket) canTake(now time.Time, amount float64) bool {
	b.refill(now)
	return amount >= 0 && amount <= b.tokens
}

func (b *tokenBucket) take(amount float64) { b.tokens -= amount }

func (b *tokenBucket) refill(now time.Time) {
	if now.Before(b.last) {
		return
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.rate {
		b.tokens = b.rate
	}
	b.last = now
}

func takeBoth(now time.Time, amount float64, first, second *tokenBucket) bool {
	if !first.canTake(now, amount) || !second.canTake(now, amount) {
		return false
	}
	first.take(amount)
	second.take(amount)
	return true
}
