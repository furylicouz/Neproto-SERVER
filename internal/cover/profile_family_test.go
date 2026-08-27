package cover

import (
	"reflect"
	"testing"
	"time"
)

func TestDerivedProfileSetIsDeterministicAndBounded(t *testing.T) {
	seed := [32]byte{0x4d, 0x6f, 0x73, 0x61, 0x69, 0x63, 0x23}
	first, err := deriveProfileSet(seed)
	if err != nil {
		t.Fatalf("derive first profile set: %v", err)
	}
	second, err := deriveProfileSet(seed)
	if err != nil {
		t.Fatalf("derive second profile set: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same directional seed produced different profile sets")
	}

	for _, profileID := range []ProfileID{ProfileQuiet, ProfileWeb, ProfileInteractive, ProfileStream} {
		profile, ok := first.definition(profileID)
		if !ok {
			t.Fatalf("profile %s was not derived", profileID)
		}
		assertDerivedProfileBounded(t, profileID, profile)
	}
}

func TestDerivedProfileSetCoversManySharedVariants(t *testing.T) {
	seen := make(map[uint8]int)
	for index := 0; index < 256; index++ {
		seed := [32]byte{byte(index), byte(index >> 8), 0xa7, 0x31}
		profiles, err := deriveProfileSet(seed)
		if err != nil {
			t.Fatalf("derive corpus profile %d: %v", index, err)
		}
		profile, ok := profiles.definition(ProfileWeb)
		if !ok {
			t.Fatalf("web profile missing for seed %d", index)
		}
		seen[profile.variantID]++
	}
	if len(seen) < 16 {
		t.Fatalf("seed corpus selected only %d web variants", len(seen))
	}
	repeated := false
	for _, count := range seen {
		if count > 1 {
			repeated = true
			break
		}
	}
	if !repeated {
		t.Fatal("every session received a unique variant identifier")
	}
}

func TestMosaicUsesDerivedProfilesOnlyAfterNegotiation(t *testing.T) {
	seed := [32]byte{0x73, 0x65, 0x73, 0x73, 0x69, 0x6f, 0x6e}
	engine, err := NewEngine(Config{
		Profile: ProfileWeb, MaxOverheadPercent: 20, MaxBudgetBytes: MaxWireCellBytes,
		Seed: seed,
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	if !reflect.DeepEqual(engine.profile, profileDefinitions[ProfileWeb]) {
		t.Fatal("legacy fixed profile changed before Mosaic negotiation")
	}
	if !engine.EnableMosaic() {
		t.Fatal("enable Mosaic")
	}
	derived, ok := engine.profiles.definition(ProfileWeb)
	if !ok {
		t.Fatal("derived web profile missing")
	}
	if !reflect.DeepEqual(engine.profile, derived.definition) {
		t.Fatal("negotiated Mosaic did not activate the derived web profile")
	}
}

func assertDerivedProfileBounded(t *testing.T, profileID ProfileID, profile derivedProfile) {
	t.Helper()
	limits := ProfileLimits(profileID)
	if profile.variantID == 0 {
		t.Fatalf("profile %s has zero variant identifier", profileID)
	}
	if profile.definition.limits.MaxRealDelay > limits.MaxRealDelay ||
		profile.definition.limits.MaxPaddingBytes > limits.MaxPaddingBytes {
		t.Fatalf("profile %s exceeds class limits: %+v > %+v", profileID, profile.definition.limits, limits)
	}
	assertStrictlyIncreasingSizes(t, profileID, "bucket", profile.definition.buckets)
	assertStrictlyIncreasingSizes(t, profileID, "dummy", profile.definition.dummySizes)
	if profile.definition.bucketLookahead < 1 || profile.definition.bucketLookahead > 3 {
		t.Fatalf("profile %s bucket look-ahead=%d", profileID, profile.definition.bucketLookahead)
	}
	if profile.definition.dummyGateDenominator == 0 ||
		profile.definition.dummyGateNumerator > profile.definition.dummyGateDenominator {
		t.Fatalf("profile %s invalid dummy gate %d/%d", profileID,
			profile.definition.dummyGateNumerator, profile.definition.dummyGateDenominator)
	}
	if profile.definition.minDummyDelay < 0 ||
		profile.definition.maxDummyDelay < profile.definition.minDummyDelay ||
		profile.definition.maxDummyDelay > 250*time.Millisecond {
		t.Fatalf("profile %s invalid dummy delay range %s..%s", profileID,
			profile.definition.minDummyDelay, profile.definition.maxDummyDelay)
	}
	if profileID == ProfileQuiet || profileID == ProfileStream {
		if len(profile.definition.dummySizes) != 0 || profile.definition.dummyGateNumerator != 0 ||
			profile.definition.limits.MaxRealDelay != 0 {
			t.Fatalf("profile %s introduced delay or dummy traffic: %+v", profileID, profile)
		}
	}
}

func assertStrictlyIncreasingSizes(t *testing.T, profileID ProfileID, kind string, sizes []int) {
	t.Helper()
	previous := 0
	for _, size := range sizes {
		if size <= previous || size > MaxWireCellBytes {
			t.Fatalf("profile %s invalid %s sizes: %v", profileID, kind, sizes)
		}
		previous = size
	}
}
