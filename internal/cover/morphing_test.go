package cover

import (
	"testing"
	"time"
)

func TestMosaicDelayShapesDifferAndOnlyDelayBurstStarts(t *testing.T) {
	front := newWebShapeEngine(t, delayFrontLoaded)
	tail := newWebShapeEngine(t, delayTailLoaded)
	start := time.Unix(2_000_000_000, 0)
	frontTotal := time.Duration(0)
	tailTotal := time.Duration(0)
	const bursts = 512
	for index := 0; index < bursts; index++ {
		now := start.Add(time.Duration(index) * 100 * time.Millisecond)
		frontDecision, err := front.PlanReal(now, 400)
		if err != nil {
			t.Fatalf("plan front-loaded burst %d: %v", index, err)
		}
		tailDecision, err := tail.PlanReal(now, 400)
		if err != nil {
			t.Fatalf("plan tail-loaded burst %d: %v", index, err)
		}
		frontTotal += frontDecision.SendAt.Sub(now)
		tailTotal += tailDecision.SendAt.Sub(now)
	}
	if frontTotal >= tailTotal {
		t.Fatalf("delay shapes did not diverge: front=%s tail=%s", frontTotal/bursts, tailTotal/bursts)
	}
	frontAverage := frontTotal / bursts
	tailAverage := tailTotal / bursts
	maximum := front.profile.limits.MaxRealDelay
	if frontAverage >= maximum*45/100 || tailAverage <= maximum*55/100 {
		t.Fatalf("delay shapes look uniform: front=%s tail=%s maximum=%s", frontAverage, tailAverage, maximum)
	}

	now := start.Add(bursts * 100 * time.Millisecond)
	first, err := front.PlanReal(now, 400)
	if err != nil {
		t.Fatal(err)
	}
	secondAt := now.Add(time.Millisecond)
	second, err := front.PlanReal(secondAt, 400)
	if err != nil {
		t.Fatal(err)
	}
	if first.SendAt.Before(now) || !second.SendAt.Equal(secondAt) {
		t.Fatalf("burst delay was applied per cell: first=%s second=%s", first.SendAt.Sub(now), second.SendAt.Sub(secondAt))
	}
}

func TestMosaicDummyGateIsSessionDerivedAndNonDeterministicPerRealCell(t *testing.T) {
	engine := newMosaicTestEngine(t, ProfileWeb, 100)
	if !engine.EnableMosaic() {
		t.Fatal("enable Mosaic")
	}
	derived, ok := engine.profiles.definition(ProfileWeb)
	if !ok || derived.definition.dummyGateNumerator >= derived.definition.dummyGateDenominator {
		t.Fatalf("test requires a gated web variant: %+v", derived)
	}

	start := time.Unix(2_000_000_000, 0)
	selected := 0
	const cells = 512
	for index := 0; index < cells; index++ {
		decision, err := engine.PlanReal(start.Add(time.Duration(index)*100*time.Millisecond), 400)
		if err != nil {
			t.Fatalf("plan gated cell %d: %v", index, err)
		}
		if decision.ScheduleDummy {
			selected++
		}
	}
	if selected == 0 || selected == cells {
		t.Fatalf("dummy gate selected %d/%d cells", selected, cells)
	}
}

func TestMosaicDummyUsesDerivedGapDelay(t *testing.T) {
	engine, definition := newWebDummyGapEngine(t, 20*time.Millisecond)
	start := time.Unix(2_000_000_000, 0)
	for index := 0; index < 8; index++ {
		if _, err := engine.PlanReal(start.Add(time.Duration(index)*time.Second), 60_000); err != nil {
			t.Fatalf("earn dummy credit: %v", err)
		}
	}

	planned := 0
	for index := 0; index < 32; index++ {
		now := start.Add(10*time.Second + time.Duration(index)*time.Second)
		decision := engine.PlanDummy(now)
		if !decision.Scheduled {
			continue
		}
		planned++
		delay := decision.SendAt.Sub(now)
		if delay < definition.minDummyDelay || delay > definition.maxDummyDelay {
			t.Fatalf("dummy delay %s outside %s..%s", delay,
				definition.minDummyDelay, definition.maxDummyDelay)
		}
	}
	if planned < 8 {
		t.Fatalf("only %d dummy cells were planned", planned)
	}
}

func newWebShapeEngine(t *testing.T, shape delayShape) *Engine {
	t.Helper()
	for index := 0; index < 256; index++ {
		seed := [32]byte{byte(index), 0x91, 0x22}
		engine, err := NewEngine(Config{
			Profile: ProfileWeb, MaxOverheadPercent: 100, MaxBudgetBytes: MaxWireCellBytes,
			Seed: seed,
		})
		if err != nil {
			t.Fatal(err)
		}
		derived, _ := engine.profiles.definition(ProfileWeb)
		if derived.definition.delayShape == shape {
			engine.EnableMosaic()
			return engine
		}
	}
	t.Fatalf("no web profile selected delay shape %d", shape)
	return nil
}

func newWebDummyGapEngine(t *testing.T, minimum time.Duration) (*Engine, profileDefinition) {
	t.Helper()
	for index := 0; index < 256; index++ {
		seed := [32]byte{byte(index), 0x44, 0x88}
		engine, err := NewEngine(Config{
			Profile: ProfileWeb, MaxOverheadPercent: 100, MaxBudgetBytes: MaxWireCellBytes,
			Seed: seed,
		})
		if err != nil {
			t.Fatal(err)
		}
		derived, _ := engine.profiles.definition(ProfileWeb)
		if derived.definition.minDummyDelay >= minimum {
			engine.EnableMosaic()
			return engine, derived.definition
		}
	}
	t.Fatalf("no web profile selected minimum dummy delay >= %s", minimum)
	return nil, profileDefinition{}
}
