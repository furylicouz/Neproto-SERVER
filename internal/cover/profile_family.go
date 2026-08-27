package cover

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"time"
)

const maxMosaicDummyDelay = 250 * time.Millisecond

type delayShape uint8

const (
	delayUniform delayShape = iota
	delayFrontLoaded
	delayTailLoaded
)

type derivedProfile struct {
	variantID  uint8
	definition profileDefinition
}

type profileSet struct {
	profiles [int(ProfileStream) + 1]derivedProfile
}

func (s profileSet) definition(profile ProfileID) (derivedProfile, bool) {
	if profile < ProfileQuiet || profile > ProfileStream {
		return derivedProfile{}, false
	}
	return s.profiles[int(profile)], true
}

type dummyGate struct {
	numerator   uint8
	denominator uint8
}

type delayRange struct {
	minimum time.Duration
	maximum time.Duration
}

type profileFamily struct {
	buckets          [][]int
	dummies          [][]int
	delayShapes      []delayShape
	bucketLookaheads []uint8
	dummyGates       []dummyGate
	dummyDelays      []delayRange
}

var profileFamilies = map[ProfileID]profileFamily{
	ProfileWeb: {
		buckets: [][]int{
			{128, 512, 1200, 4096, 16_384, 32_768, 49_152},
			{96, 384, 768, 1200, 3072, 12_288, 24_576, 49_152},
			{160, 640, 1024, 1440, 8192, 16_384, 32_768, 57_344},
			{256, 1024, 1200, 2048, 6144, 24_576, 40_960, 61_440},
		},
		dummies: [][]int{
			{96, 256, 512, 1200}, {128, 384, 768, 1200},
			{160, 512, 1024}, {96, 320, 640, 1024},
		},
		delayShapes:      []delayShape{delayUniform, delayFrontLoaded, delayTailLoaded},
		bucketLookaheads: []uint8{1, 2, 3},
		dummyGates:       []dummyGate{{1, 4}, {1, 3}, {1, 2}, {2, 3}},
		dummyDelays: []delayRange{
			{12 * time.Millisecond, 90 * time.Millisecond},
			{20 * time.Millisecond, 140 * time.Millisecond},
			{35 * time.Millisecond, 220 * time.Millisecond},
			{8 * time.Millisecond, 60 * time.Millisecond},
		},
	},
	ProfileInteractive: {
		buckets: [][]int{
			{96, 160, 256, 512, 1024, 1200, 4096},
			{80, 128, 224, 384, 768, 1200, 2048, 4096},
			{112, 192, 320, 640, 960, 1400, 3072},
			{128, 256, 384, 768, 1200, 2048, 4096},
		},
		dummies: [][]int{
			{96, 160, 256}, {80, 128, 224}, {112, 192, 320}, {128, 256, 384},
		},
		delayShapes:      []delayShape{delayUniform, delayFrontLoaded, delayTailLoaded},
		bucketLookaheads: []uint8{1, 2, 3},
		dummyGates:       []dummyGate{{1, 3}, {1, 2}, {2, 3}, {3, 4}},
		dummyDelays: []delayRange{
			{8 * time.Millisecond, 50 * time.Millisecond},
			{12 * time.Millisecond, 75 * time.Millisecond},
			{20 * time.Millisecond, 110 * time.Millisecond},
			{5 * time.Millisecond, 40 * time.Millisecond},
		},
	},
	ProfileStream: {
		buckets: [][]int{
			{1200, 4096, 16_384, 32_768, 49_152},
			{1200, 8192, 24_576, 49_152, 61_440},
			{1400, 4096, 12_288, 32_768, 57_344},
			{1024, 6144, 16_384, 40_960, 61_440},
		},
		dummies:          [][]int{nil},
		delayShapes:      []delayShape{delayUniform},
		bucketLookaheads: []uint8{1, 2, 3},
		dummyGates:       []dummyGate{{0, 1}},
		dummyDelays:      []delayRange{{}},
	},
}

func deriveProfileSet(seed [32]byte) (profileSet, error) {
	if seed == ([32]byte{}) {
		return profileSet{}, ErrInvalidConfig
	}
	var result profileSet
	quiet := derivedProfile{variantID: 1, definition: profileDefinitions[ProfileQuiet]}
	result.profiles[int(ProfileQuiet)] = quiet
	for _, profileID := range []ProfileID{ProfileWeb, ProfileInteractive, ProfileStream} {
		derived, err := deriveProfile(seed, profileID)
		if err != nil {
			return profileSet{}, err
		}
		result.profiles[int(profileID)] = derived
	}
	return result, nil
}

func deriveProfile(seed [32]byte, profileID ProfileID) (derivedProfile, error) {
	family, ok := profileFamilies[profileID]
	if !ok {
		return derivedProfile{}, ErrInvalidConfig
	}
	mac := hmac.New(sha256.New, seed[:])
	_, _ = mac.Write([]byte("NP2 Mosaic profile variant"))
	_, _ = mac.Write([]byte{byte(profileID)})
	material := mac.Sum(nil)
	definition := profileDefinitions[profileID]
	definition.buckets = family.buckets[int(material[1])%len(family.buckets)]
	definition.dummySizes = family.dummies[int(material[2])%len(family.dummies)]
	definition.delayShape = family.delayShapes[int(material[3])%len(family.delayShapes)]
	definition.bucketLookahead = family.bucketLookaheads[int(material[4])%len(family.bucketLookaheads)]
	gate := family.dummyGates[int(material[5])%len(family.dummyGates)]
	definition.dummyGateNumerator = gate.numerator
	definition.dummyGateDenominator = gate.denominator
	dummyDelay := family.dummyDelays[int(material[6])%len(family.dummyDelays)]
	definition.minDummyDelay = dummyDelay.minimum
	definition.maxDummyDelay = dummyDelay.maximum
	derived := derivedProfile{variantID: material[0]&63 + 1, definition: definition}
	if err := validateDerivedProfile(profileID, derived); err != nil {
		return derivedProfile{}, errors.Join(ErrInvalidConfig, err)
	}
	return derived, nil
}

func validateDerivedProfile(profileID ProfileID, profile derivedProfile) error {
	limits := ProfileLimits(profileID)
	definition := profile.definition
	if profile.variantID == 0 || definition.limits.MaxRealDelay > limits.MaxRealDelay ||
		definition.limits.MaxPaddingBytes > limits.MaxPaddingBytes ||
		definition.bucketLookahead < 1 || definition.bucketLookahead > 3 ||
		definition.dummyGateDenominator == 0 ||
		definition.dummyGateNumerator > definition.dummyGateDenominator ||
		definition.minDummyDelay < 0 || definition.maxDummyDelay < definition.minDummyDelay ||
		definition.maxDummyDelay > maxMosaicDummyDelay ||
		!validIncreasingSizes(definition.buckets) || !validIncreasingSizes(definition.dummySizes) {
		return ErrInvalidConfig
	}
	if (profileID == ProfileQuiet || profileID == ProfileStream) &&
		(len(definition.dummySizes) != 0 || definition.dummyGateNumerator != 0 ||
			definition.limits.MaxRealDelay != 0) {
		return ErrInvalidConfig
	}
	return nil
}

func validIncreasingSizes(sizes []int) bool {
	previous := 0
	for _, size := range sizes {
		if size <= previous || size > MaxWireCellBytes {
			return false
		}
		previous = size
	}
	return true
}
