package cover

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"time"
)

const (
	PulseMaxOverheadPercent = 5
	PulseMaxPaddingBytes    = 512
	PulseMaxRealDelay       = 2 * time.Millisecond
)

type pulseProfile struct {
	derived         derivedProfile
	paddingGate     dummyGate
	spendMinimumPct uint8
	spendMaximumPct uint8
}

type pulseState struct {
	enabled bool
	profile pulseProfile
}

var pulseBucketFamilies = [][]int{
	{96, 128, 192, 256, 384, 512, 768, 1024, 1200, 1440, 2048, 4096, 8192, 16_384, 32_768, 49_152, 61_440},
	{80, 112, 160, 224, 320, 448, 640, 896, 1152, 1400, 1792, 3072, 6144, 12_288, 24_576, 40_960, 57_344},
	{104, 152, 208, 288, 416, 576, 832, 1088, 1280, 1536, 2304, 4608, 9216, 18_432, 36_864, 53_248, 62_464},
	{120, 176, 240, 336, 480, 672, 960, 1200, 1360, 1664, 2560, 5120, 10_240, 20_480, 32_768, 45_056, 59_392},
	{88, 136, 200, 272, 400, 544, 736, 992, 1232, 1472, 2176, 4352, 8704, 17_408, 34_816, 51_200, 60_416},
	{128, 184, 248, 352, 496, 704, 928, 1184, 1424, 1728, 2688, 5376, 10_752, 21_504, 39_936, 55_296, 63_488},
}

var pulsePaddingGates = []dummyGate{
	{1, 4}, {1, 3}, {2, 5}, {1, 2},
}

var pulseSpendRanges = [][2]uint8{
	{45, 75}, {50, 85}, {55, 90}, {60, 100},
}

var pulsePaddingLimits = []int{192, 256, 384, PulseMaxPaddingBytes}

func derivePulseProfile(seed [32]byte) (pulseProfile, error) {
	if seed == ([32]byte{}) {
		return pulseProfile{}, ErrInvalidConfig
	}
	mac := hmac.New(sha256.New, seed[:])
	_, _ = mac.Write([]byte("NP2 Pulse v1 profile family"))
	material := mac.Sum(nil)

	gate := pulsePaddingGates[int(material[1])%len(pulsePaddingGates)]
	spend := pulseSpendRanges[int(material[2])%len(pulseSpendRanges)]
	definition := profileDefinition{
		limits: Limits{
			MaxRealDelay:    PulseMaxRealDelay,
			MaxPaddingBytes: pulsePaddingLimits[int(material[3])%len(pulsePaddingLimits)],
		},
		buckets:              pulseBucketFamilies[int(material[4])%len(pulseBucketFamilies)],
		delayShape:           []delayShape{delayUniform, delayFrontLoaded, delayTailLoaded}[int(material[5])%3],
		bucketLookahead:      1 + material[6]%3,
		dummyGateDenominator: 1,
	}
	derived := derivedProfile{variantID: material[0]&63 + 1, definition: definition}
	profile := pulseProfile{
		derived: derived, paddingGate: gate,
		spendMinimumPct: spend[0], spendMaximumPct: spend[1],
	}
	if err := validatePulseProfile(profile); err != nil {
		return pulseProfile{}, errors.Join(ErrInvalidConfig, err)
	}
	return profile, nil
}

func validatePulseProfile(profile pulseProfile) error {
	definition := profile.derived.definition
	if profile.derived.variantID == 0 ||
		definition.limits.MaxRealDelay < 0 || definition.limits.MaxRealDelay > PulseMaxRealDelay ||
		definition.limits.MaxPaddingBytes <= 0 || definition.limits.MaxPaddingBytes > PulseMaxPaddingBytes ||
		definition.bucketLookahead < 1 || definition.bucketLookahead > 3 ||
		profile.paddingGate.denominator == 0 ||
		profile.paddingGate.numerator == 0 ||
		profile.paddingGate.numerator > profile.paddingGate.denominator ||
		profile.spendMinimumPct == 0 || profile.spendMinimumPct > profile.spendMaximumPct ||
		profile.spendMaximumPct > 100 || !validIncreasingSizes(definition.buckets) ||
		len(definition.dummySizes) != 0 || definition.dummyGateNumerator != 0 {
		return ErrInvalidConfig
	}
	return nil
}

// EnablePulse activates the low-overhead sender-local Pulse scheduler. It must
// be selected before real traffic is planned and is mutually exclusive with
// the legacy negotiated Mosaic scheduler.
func (e *Engine) EnablePulse() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mosaic.enabled || e.stats.RealBytes != 0 {
		return false
	}
	if e.pulse.enabled {
		return true
	}
	e.pulse.enabled = true
	e.activeProfile = ProfileWeb
	e.activeVariantID = e.pulse.profile.derived.variantID
	e.profile = e.pulse.profile.derived.definition
	if e.maxOverheadPercent > PulseMaxOverheadPercent {
		e.maxOverheadPercent = PulseMaxOverheadPercent
	}
	return true
}

func (e *Engine) pulseScalePadding(bytes int, sample uint64) int {
	if bytes <= 0 {
		return 0
	}
	minimum := e.pulse.profile.spendMinimumPct
	maximum := e.pulse.profile.spendMaximumPct
	selected := minimum
	if maximum > minimum {
		selected += uint8(uint16(sample) % uint16(maximum-minimum+1))
	}
	return int(uint64(bytes) * uint64(selected) / 100)
}
