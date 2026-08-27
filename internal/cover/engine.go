package cover

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"
)

const realBurstIdleThreshold = 50 * time.Millisecond

const (
	MaxWireCellBytes      = 65_535
	defaultMaxBudgetBytes = 65_535
	jitterStep            = 250 * time.Microsecond
)

var (
	ErrInvalidCellSize = errors.New("invalid wire cell size")
	ErrInvalidConfig   = errors.New("invalid cover configuration")
)

type Config struct {
	Profile            ProfileID
	MaxOverheadPercent uint8
	MaxBudgetBytes     int
	Seed               [32]byte
}

type RealDecision struct {
	PaddingBytes  int
	SendAt        time.Time
	ScheduleDummy bool
}

type DummyDecision struct {
	Scheduled bool
	Bytes     int
	SendAt    time.Time
}

type Stats struct {
	RealBytes          uint64
	PaddingBytes       uint64
	DummyBytes         uint64
	MosaicEnabled      bool
	TrafficClass       TrafficClass
	ActiveProfile      ProfileID
	ProfileTransitions uint64
}

func (s Stats) OverheadBytes() uint64 {
	return saturatingAdd(s.PaddingBytes, s.DummyBytes)
}

type Engine struct {
	mu sync.Mutex

	baselineProfile    ProfileID
	activeProfile      ProfileID
	profile            profileDefinition
	profiles           profileSet
	maxOverheadPercent uint64
	maxCreditUnits     uint64
	creditUnits        uint64
	random             *coverRandom
	stats              Stats
	mosaic             mosaicState
	lastRealPlan       time.Time
}

func NewEngine(config Config) (*Engine, error) {
	profile, ok := profileDefinitions[config.Profile]
	if !ok || config.MaxOverheadPercent > 100 || config.Seed == ([32]byte{}) {
		return nil, ErrInvalidConfig
	}
	if config.MaxBudgetBytes < 0 || config.MaxBudgetBytes > MaxWireCellBytes {
		return nil, ErrInvalidConfig
	}
	profiles, err := deriveProfileSet(config.Seed)
	if err != nil {
		return nil, err
	}
	maxBudgetBytes := config.MaxBudgetBytes
	if maxBudgetBytes == 0 {
		maxBudgetBytes = defaultMaxBudgetBytes
	}
	return &Engine{
		baselineProfile:    config.Profile,
		activeProfile:      config.Profile,
		profile:            profile,
		profiles:           profiles,
		maxOverheadPercent: uint64(config.MaxOverheadPercent),
		maxCreditUnits:     uint64(maxBudgetBytes) * 100,
		random:             newCoverRandom(config.Seed),
	}, nil
}

func (e *Engine) PlanReal(now time.Time, wireBytes int) (RealDecision, error) {
	if wireBytes <= 0 || wireBytes > MaxWireCellBytes {
		return RealDecision{}, ErrInvalidCellSize
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mosaic.enabled {
		e.observeMosaic(now, wireBytes)
	}

	e.stats.RealBytes = saturatingAdd(e.stats.RealBytes, uint64(wireBytes))
	e.earnCredit(uint64(wireBytes) * e.maxOverheadPercent)

	desiredPadding := e.desiredPadding(wireBytes)
	availableBytes := int(e.creditUnits / 100)
	paddingBytes := min(desiredPadding, availableBytes, e.profile.limits.MaxPaddingBytes)
	e.creditUnits -= uint64(paddingBytes) * 100
	e.stats.PaddingBytes = saturatingAdd(e.stats.PaddingBytes, uint64(paddingBytes))

	delay := time.Duration(0)
	if e.lastRealPlan.IsZero() || now.Before(e.lastRealPlan) ||
		now.Sub(e.lastRealPlan) >= realBurstIdleThreshold {
		delay = e.randomDelay(e.profile.limits.MaxRealDelay)
	}
	e.lastRealPlan = now
	return RealDecision{
		PaddingBytes:  paddingBytes,
		SendAt:        now.Add(delay),
		ScheduleDummy: len(e.profile.dummySizes) != 0,
	}, nil
}

func (e *Engine) PlanDummy(now time.Time) DummyDecision {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.profile.dummySizes) == 0 {
		return DummyDecision{}
	}
	availableBytes := int(e.creditUnits / 100)
	affordable := 0
	for _, size := range e.profile.dummySizes {
		if size > availableBytes {
			break
		}
		affordable++
	}
	if affordable == 0 {
		return DummyDecision{}
	}
	size := e.profile.dummySizes[e.random.uniform(uint64(affordable))]
	e.creditUnits -= uint64(size) * 100
	e.stats.DummyBytes = saturatingAdd(e.stats.DummyBytes, uint64(size))
	return DummyDecision{
		Scheduled: true,
		Bytes:     size,
		SendAt:    now.Add(e.randomDelay(e.profile.limits.MaxRealDelay)),
	}
}

func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	stats := e.stats
	stats.MosaicEnabled = e.mosaic.enabled
	stats.TrafficClass = e.mosaic.class
	stats.ActiveProfile = e.activeProfile
	stats.ProfileTransitions = e.mosaic.transitions
	return stats
}

// EnableMosaic enables the authenticated adaptive scheduler for non-quiet
// profiles. Calling it more than once is idempotent.
func (e *Engine) EnableMosaic() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.baselineProfile == ProfileQuiet {
		return false
	}
	if e.mosaic.enabled {
		return true
	}
	e.mosaic.enabled = true
	e.mosaic.class = TrafficWeb
	if e.baselineProfile == ProfileInteractive {
		e.mosaic.class = TrafficRealtime
	}
	e.setMosaicClass(e.mosaic.class)
	return true
}

func (e *Engine) earnCredit(units uint64) {
	if units >= e.maxCreditUnits-e.creditUnits {
		e.creditUnits = e.maxCreditUnits
		return
	}
	e.creditUnits += units
}

func (e *Engine) desiredPadding(wireBytes int) int {
	if len(e.profile.buckets) == 0 {
		return 0
	}
	first := len(e.profile.buckets)
	for index, bucket := range e.profile.buckets {
		if bucket >= wireBytes {
			first = index
			break
		}
	}
	if first == len(e.profile.buckets) {
		return 0
	}
	choices := min(2, len(e.profile.buckets)-first)
	target := e.profile.buckets[first+int(e.random.uniform(uint64(choices)))]
	return target - wireBytes
}

func (e *Engine) randomDelay(maximum time.Duration) time.Duration {
	if maximum <= 0 {
		return 0
	}
	slots := uint64(maximum/jitterStep) + 1
	return time.Duration(e.random.uniform(slots)) * jitterStep
}

func saturatingAdd(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}

type coverRandom struct {
	key     [32]byte
	counter uint64
	block   [sha256.Size]byte
	offset  int
}

func newCoverRandom(seed [32]byte) *coverRandom {
	return &coverRandom{key: seed, offset: sha256.Size}
}

func (r *coverRandom) uniform(limit uint64) uint64 {
	if limit == 0 {
		panic("cover: zero uniform limit")
	}
	threshold := math.MaxUint64 - (math.MaxUint64 % limit)
	for {
		candidate := r.uint64()
		if candidate < threshold {
			return candidate % limit
		}
	}
}

func (r *coverRandom) uint64() uint64 {
	if r.offset+8 > len(r.block) {
		mac := hmac.New(sha256.New, r.key[:])
		_, _ = mac.Write([]byte("NP2 cover schedule"))
		var counter [8]byte
		binary.BigEndian.PutUint64(counter[:], r.counter)
		_, _ = mac.Write(counter[:])
		copy(r.block[:], mac.Sum(nil))
		r.counter++
		r.offset = 0
	}
	value := binary.BigEndian.Uint64(r.block[r.offset : r.offset+8])
	r.offset += 8
	return value
}
