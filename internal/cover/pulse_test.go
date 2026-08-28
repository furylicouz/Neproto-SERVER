package cover

import (
	"reflect"
	"testing"
	"time"
)

func TestPulseDecisionSequenceIsSessionPolymorphicAndDeterministic(t *testing.T) {
	first := newPulseTestEngine(t, [32]byte{0x10, 0x20, 0x30}, 5)
	replay := newPulseTestEngine(t, [32]byte{0x10, 0x20, 0x30}, 5)
	second := newPulseTestEngine(t, [32]byte{0x90, 0x80, 0x70}, 5)

	firstTrace := pulseTrace(t, first)
	replayTrace := pulseTrace(t, replay)
	secondTrace := pulseTrace(t, second)
	if !reflect.DeepEqual(firstTrace, replayTrace) {
		t.Fatal("Pulse is not deterministic for one authenticated session seed")
	}
	if reflect.DeepEqual(firstTrace, secondTrace) {
		t.Fatal("independent Pulse session seeds produced the same complete trace")
	}
	if first.Stats().VariantID == second.Stats().VariantID {
		t.Fatal("test seeds unexpectedly selected the same Pulse family variant")
	}
}

func TestPulseEnforcesPerformanceEnvelope(t *testing.T) {
	engine := newPulseTestEngine(t, [32]byte{0x51, 0x52, 0x53}, 100)
	start := time.Unix(2_100_000_000, 0)
	var padding int
	var real int

	for index := 0; index < 4096; index++ {
		now := start.Add(time.Duration(index) * time.Millisecond)
		wireBytes := 80 + index%32_000
		decision, err := engine.PlanReal(now, wireBytes)
		if err != nil {
			t.Fatalf("plan Pulse cell %d: %v", index, err)
		}
		delay := decision.SendAt.Sub(now)
		if delay < 0 || delay > PulseMaxRealDelay {
			t.Fatalf("cell %d delay %s exceeds Pulse ceiling %s", index, delay, PulseMaxRealDelay)
		}
		if index > 0 && delay != 0 {
			t.Fatalf("cell %d in an active burst received %s delay", index, delay)
		}
		if decision.PaddingBytes < 0 || decision.PaddingBytes > PulseMaxPaddingBytes {
			t.Fatalf("cell %d padding %d exceeds Pulse ceiling", index, decision.PaddingBytes)
		}
		if decision.ScheduleDummy {
			t.Fatalf("Pulse v1 scheduled a dummy for real cell %d", index)
		}
		padding += decision.PaddingBytes
		real += wireBytes
	}
	if padding == 0 {
		t.Fatal("Pulse did not morph any real-cell sizes")
	}
	if padding*100 > real*PulseMaxOverheadPercent {
		t.Fatalf("Pulse overhead %d/%d exceeds %d%%", padding, real, PulseMaxOverheadPercent)
	}

	stats := engine.Stats()
	if !stats.PulseEnabled || stats.MosaicEnabled || stats.ActiveProfile != ProfileWeb ||
		stats.VariantID == 0 || stats.DummyBytes != 0 || stats.DummyRequestsSelected != 0 ||
		stats.MaxPlannedDelayMicros > uint64(PulseMaxRealDelay/time.Microsecond) {
		t.Fatalf("unexpected Pulse stats: %+v", stats)
	}
}

func TestPulseOnlyDelaysBurstStarts(t *testing.T) {
	engine := newPulseTestEngine(t, [32]byte{0x61, 0x62, 0x63}, 5)
	start := time.Unix(2_100_000_000, 0)
	for burst := 0; burst < 256; burst++ {
		burstStart := start.Add(time.Duration(burst) * 100 * time.Millisecond)
		first, err := engine.PlanReal(burstStart, 900)
		if err != nil {
			t.Fatal(err)
		}
		if delay := first.SendAt.Sub(burstStart); delay < 0 || delay > PulseMaxRealDelay {
			t.Fatalf("burst %d start delay=%s", burst, delay)
		}
		for cell := 1; cell < 32; cell++ {
			now := burstStart.Add(time.Duration(cell) * time.Millisecond)
			decision, err := engine.PlanReal(now, 16_384)
			if err != nil {
				t.Fatal(err)
			}
			if !decision.SendAt.Equal(now) {
				t.Fatalf("burst %d cell %d did not use zero-delay bulk path", burst, cell)
			}
		}
	}
	stats := engine.Stats()
	if stats.BurstCount != 256 {
		t.Fatalf("burst count=%d, want 256", stats.BurstCount)
	}
}

func TestPulseAndMosaicAreMutuallyExclusive(t *testing.T) {
	pulse := newPulseTestEngine(t, [32]byte{0x71}, 5)
	if pulse.EnableMosaic() {
		t.Fatal("Mosaic activated over Pulse")
	}

	mosaic, err := NewEngine(Config{
		Profile: ProfileWeb, MaxOverheadPercent: 20, Seed: [32]byte{0x72},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mosaic.EnableMosaic() {
		t.Fatal("enable Mosaic")
	}
	if mosaic.EnablePulse() {
		t.Fatal("Pulse activated over Mosaic")
	}
}

func pulseTrace(t *testing.T, engine *Engine) []RealDecision {
	t.Helper()
	now := time.Unix(2_100_000_000, 0)
	trace := make([]RealDecision, 0, 512)
	for index := 0; index < 512; index++ {
		if index > 0 {
			now = now.Add(5 * time.Millisecond)
		}
		if index > 0 && index%40 == 0 {
			now = now.Add(100 * time.Millisecond)
		}
		decision, err := engine.PlanReal(now, 96+(index*7919)%30_000)
		if err != nil {
			t.Fatalf("plan trace cell %d: %v", index, err)
		}
		trace = append(trace, decision)
	}
	return trace
}

func newPulseTestEngine(t *testing.T, seed [32]byte, overhead uint8) *Engine {
	t.Helper()
	engine, err := NewEngine(Config{
		Profile: ProfileWeb, MaxOverheadPercent: overhead,
		MaxBudgetBytes: MaxWireCellBytes, Seed: seed,
	})
	if err != nil {
		t.Fatalf("create Pulse engine: %v", err)
	}
	if !engine.EnablePulse() {
		t.Fatal("enable Pulse")
	}
	return engine
}

func BenchmarkPlanRealPulse(b *testing.B) {
	engine, err := NewEngine(Config{
		Profile: ProfileWeb, MaxOverheadPercent: PulseMaxOverheadPercent,
		MaxBudgetBytes: MaxWireCellBytes,
		Seed:           [32]byte{0x70, 0x75, 0x6c, 0x73, 0x65},
	})
	if err != nil {
		b.Fatal(err)
	}
	if !engine.EnablePulse() {
		b.Fatal("enable Pulse")
	}
	now := time.Unix(2_100_000_000, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := engine.PlanReal(now.Add(time.Duration(index)*time.Millisecond), 1200+index%16_384); err != nil {
			b.Fatal(err)
		}
	}
}
