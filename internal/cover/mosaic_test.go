package cover

import (
	"testing"
	"time"
)

func TestMosaicClassifiesRealtimeAndRequiresHysteresis(t *testing.T) {
	engine := newMosaicTestEngine(t, ProfileWeb, 30)
	if !engine.EnableMosaic() {
		t.Fatal("web profile did not enable Mosaic")
	}
	start := time.Unix(1_900_000_000, 0)

	start = emitMosaicWindow(t, engine, start, 12, 400)
	emitMosaicWindow(t, engine, start, 12, 400)
	if got := engine.Stats(); got.TrafficClass != TrafficWeb || got.ProfileTransitions != 0 {
		t.Fatalf("one candidate window changed class: %+v", got)
	}

	start = start.Add(mosaicObservationWindow)
	emitMosaicWindow(t, engine, start, 12, 400)
	got := engine.Stats()
	if !got.MosaicEnabled || got.TrafficClass != TrafficRealtime || got.ActiveProfile != ProfileInteractive || got.ProfileTransitions != 1 {
		t.Fatalf("realtime load was not classified: %+v", got)
	}
}

func TestMosaicEntersStreamImmediatelyForSustainedBurst(t *testing.T) {
	engine := newMosaicTestEngine(t, ProfileWeb, 30)
	engine.EnableMosaic()
	now := time.Unix(1_900_000_000, 0)
	for index := 0; index < 17; index++ {
		decision, err := engine.PlanReal(now.Add(time.Duration(index)*time.Millisecond), 16*1024)
		if err != nil {
			t.Fatalf("plan burst cell: %v", err)
		}
		if index == 16 && !decision.SendAt.Equal(now.Add(time.Duration(index)*time.Millisecond)) {
			t.Fatalf("stream fast path delayed real cell by %s", decision.SendAt.Sub(now))
		}
	}
	got := engine.Stats()
	if got.TrafficClass != TrafficStream || got.ActiveProfile != ProfileStream || got.ProfileTransitions != 1 {
		t.Fatalf("stream burst was not classified immediately: %+v", got)
	}
	if dummy := engine.PlanDummy(now.Add(time.Second)); dummy.Scheduled {
		t.Fatalf("stream profile scheduled dummy: %+v", dummy)
	}
}

func TestMosaicReturnsToWebAfterConfirmedLowRateWindows(t *testing.T) {
	engine := newMosaicTestEngine(t, ProfileWeb, 30)
	engine.EnableMosaic()
	now := time.Unix(1_900_000_000, 0)
	for index := 0; index < 65; index++ {
		if _, err := engine.PlanReal(now.Add(time.Duration(index)*time.Millisecond), 16*1024); err != nil {
			t.Fatal(err)
		}
	}
	if engine.Stats().TrafficClass != TrafficStream {
		t.Fatal("precondition: stream class not entered")
	}

	start := now.Add(500 * time.Millisecond)
	start = emitMosaicWindow(t, engine, start, 2, 4000)
	emitMosaicWindow(t, engine, start, 2, 4000)
	if got := engine.Stats().TrafficClass; got != TrafficStream {
		t.Fatalf("one low-rate candidate changed stream class to %s", got)
	}
	start = start.Add(mosaicObservationWindow)
	emitMosaicWindow(t, engine, start, 2, 4000)
	if got := engine.Stats(); got.TrafficClass != TrafficWeb || got.ActiveProfile != ProfileWeb || got.ProfileTransitions != 2 {
		t.Fatalf("confirmed web load did not leave stream: %+v", got)
	}
}

func TestMosaicIdleAndRegressedClockRemainBounded(t *testing.T) {
	engine := newMosaicTestEngine(t, ProfileInteractive, 30)
	if !engine.EnableMosaic() || engine.Stats().TrafficClass != TrafficRealtime {
		t.Fatal("interactive profile did not start Mosaic in realtime class")
	}
	now := time.Unix(1_900_000_000, 0)
	if _, err := engine.PlanReal(now, 300); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.PlanReal(now.Add(-time.Second), 300); err != nil {
		t.Fatalf("regressed clock failed planning: %v", err)
	}
	if _, err := engine.PlanReal(now.Add(mosaicIdleReset+time.Second), 300); err != nil {
		t.Fatalf("idle reset failed planning: %v", err)
	}
	if got := engine.Stats(); !got.MosaicEnabled || got.RealBytes != 900 {
		t.Fatalf("unexpected stats after clock resets: %+v", got)
	}
}

func TestMosaicTransitionsPreserveGlobalOverheadBudget(t *testing.T) {
	const budgetPercent = 20
	engine := newMosaicTestEngine(t, ProfileWeb, budgetPercent)
	engine.EnableMosaic()
	now := time.Unix(1_900_000_000, 0)
	for index := 0; index < 5000; index++ {
		size := 320
		step := 20 * time.Millisecond
		if index >= 1000 && index < 4000 {
			size = 16 * 1024
			step = time.Millisecond
		}
		if _, err := engine.PlanReal(now, size); err != nil {
			t.Fatal(err)
		}
		for attempts := 0; attempts < 2; attempts++ {
			engine.PlanDummy(now)
		}
		now = now.Add(step)
	}
	stats := engine.Stats()
	if stats.OverheadBytes()*100 > stats.RealBytes*budgetPercent {
		t.Fatalf("Mosaic exceeded global overhead budget: %+v", stats)
	}
}

func TestQuietProfileCannotEnableMosaic(t *testing.T) {
	engine := newMosaicTestEngine(t, ProfileQuiet, 100)
	if engine.EnableMosaic() {
		t.Fatal("quiet profile became adaptive")
	}
	if got := engine.Stats(); got.MosaicEnabled || got.ActiveProfile != ProfileQuiet {
		t.Fatalf("quiet state changed: %+v", got)
	}
}

func emitMosaicWindow(t *testing.T, engine *Engine, start time.Time, cells, size int) time.Time {
	t.Helper()
	spacing := mosaicObservationWindow / time.Duration(cells)
	for index := 0; index < cells; index++ {
		if _, err := engine.PlanReal(start.Add(time.Duration(index)*spacing), size); err != nil {
			t.Fatalf("plan Mosaic window: %v", err)
		}
	}
	return start.Add(mosaicObservationWindow)
}

func newMosaicTestEngine(t *testing.T, profile ProfileID, overhead uint8) *Engine {
	t.Helper()
	engine, err := NewEngine(Config{
		Profile: profile, MaxOverheadPercent: overhead, MaxBudgetBytes: MaxWireCellBytes,
		Seed: [32]byte{0x6d, 0x6f, 0x73, 0x61, 0x69, 0x63},
	})
	if err != nil {
		t.Fatalf("create Mosaic engine: %v", err)
	}
	return engine
}

func BenchmarkPlanRealMosaic(b *testing.B) {
	engine, err := NewEngine(Config{
		Profile: ProfileWeb, MaxOverheadPercent: 20, MaxBudgetBytes: MaxWireCellBytes,
		Seed: [32]byte{0x6d, 0x6f, 0x73, 0x61, 0x69, 0x63},
	})
	if err != nil {
		b.Fatal(err)
	}
	engine.EnableMosaic()
	now := time.Unix(1_900_000_000, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := engine.PlanReal(now.Add(time.Duration(index)*time.Millisecond), 1200+index%16_384); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMosaicClassifier(b *testing.B) {
	engine, err := NewEngine(Config{
		Profile: ProfileWeb, MaxOverheadPercent: 20, MaxBudgetBytes: MaxWireCellBytes,
		Seed: [32]byte{0x6d, 0x6f, 0x73, 0x61, 0x69, 0x63},
	})
	if err != nil {
		b.Fatal(err)
	}
	engine.EnableMosaic()
	now := time.Unix(1_900_000_000, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		engine.observeMosaic(now.Add(time.Duration(index)*time.Millisecond), 1200+index%16_384)
	}
}
