package cover

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestEngineIsDeterministicFromSessionSeed(t *testing.T) {
	config := Config{
		Profile:            ProfileInteractive,
		MaxOverheadPercent: 30,
		MaxBudgetBytes:     4096,
		Seed:               [32]byte{0x41, 0x72},
	}
	first, err := NewEngine(config)
	if err != nil {
		t.Fatalf("create first engine: %v", err)
	}
	second, err := NewEngine(config)
	if err != nil {
		t.Fatalf("create second engine: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)

	for index := 0; index < 100; index++ {
		firstReal, err := first.PlanReal(now, 80+index%700)
		if err != nil {
			t.Fatalf("plan first real cell: %v", err)
		}
		secondReal, err := second.PlanReal(now, 80+index%700)
		if err != nil {
			t.Fatalf("plan second real cell: %v", err)
		}
		if firstReal != secondReal {
			t.Fatalf("real decisions differ at %d: %#v != %#v", index, firstReal, secondReal)
		}

		firstDummy := first.PlanDummy(now)
		secondDummy := second.PlanDummy(now)
		if firstDummy != secondDummy {
			t.Fatalf("dummy decisions differ at %d: %#v != %#v", index, firstDummy, secondDummy)
		}
		now = now.Add(5 * time.Millisecond)
	}
}

func TestProfilesRespectRealCellLatencyCeilings(t *testing.T) {
	profiles := []ProfileID{ProfileQuiet, ProfileWeb, ProfileInteractive, ProfileStream}
	now := time.Unix(1_800_000_000, 0)
	for _, profile := range profiles {
		t.Run(profile.String(), func(t *testing.T) {
			engine, err := NewEngine(Config{
				Profile:            profile,
				MaxOverheadPercent: 100,
				MaxBudgetBytes:     65_535,
				Seed:               [32]byte{0x55, byte(profile)},
			})
			if err != nil {
				t.Fatalf("create engine: %v", err)
			}
			limits := ProfileLimits(profile)
			for index := 0; index < 1000; index++ {
				decision, err := engine.PlanReal(now, 64+index%32_000)
				if err != nil {
					t.Fatalf("plan real cell: %v", err)
				}
				delay := decision.SendAt.Sub(now)
				if delay < 0 || delay > limits.MaxRealDelay {
					t.Fatalf("delay %s exceeds [0,%s]", delay, limits.MaxRealDelay)
				}
				if decision.PaddingBytes > limits.MaxPaddingBytes {
					t.Fatalf("padding %d exceeds profile max %d", decision.PaddingBytes, limits.MaxPaddingBytes)
				}
			}
		})
	}
}

func TestWebProfileAddsDelayOncePerBurstInsteadOfEveryCell(t *testing.T) {
	engine, err := NewEngine(Config{
		Profile: ProfileWeb, MaxOverheadPercent: 20, MaxBudgetBytes: 65_535,
		Seed: [32]byte{0x62, 0x75, 0x72, 0x73, 0x74},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_900_000_000, 0)
	for index := 0; index < 64; index++ {
		plannedAt := now.Add(time.Duration(index) * time.Millisecond)
		decision, planErr := engine.PlanReal(plannedAt, 1500)
		if planErr != nil {
			t.Fatal(planErr)
		}
		if index > 0 && !decision.SendAt.Equal(plannedAt) {
			t.Fatalf("cell %d in one burst received per-cell delay %s", index, decision.SendAt.Sub(plannedAt))
		}
	}
}

func TestOverheadNeverExceedsConfiguredBudget(t *testing.T) {
	const budgetPercent = 30
	engine, err := NewEngine(Config{
		Profile:            ProfileInteractive,
		MaxOverheadPercent: budgetPercent,
		MaxBudgetBytes:     65_535,
		Seed:               [32]byte{0x66},
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)

	for index := 0; index < 10_000; index++ {
		if _, err := engine.PlanReal(now, 50+index%4000); err != nil {
			t.Fatalf("plan real cell: %v", err)
		}
		for attempts := 0; attempts < 3; attempts++ {
			engine.PlanDummy(now)
		}
		now = now.Add(time.Millisecond)
	}

	stats := engine.Stats()
	if stats.OverheadBytes()*100 > stats.RealBytes*budgetPercent {
		t.Fatalf(
			"overhead budget exceeded: real=%d padding=%d dummy=%d",
			stats.RealBytes,
			stats.PaddingBytes,
			stats.DummyBytes,
		)
	}
}

func TestBudgetExhaustionSuppressesDummyCells(t *testing.T) {
	engine, err := NewEngine(Config{
		Profile:            ProfileInteractive,
		MaxOverheadPercent: 50,
		MaxBudgetBytes:     1024,
		Seed:               [32]byte{0x77},
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)
	if decision := engine.PlanDummy(now); decision.Scheduled {
		t.Fatalf("dummy scheduled without earned budget: %#v", decision)
	}
	if _, err := engine.PlanReal(now, 4096); err != nil {
		t.Fatalf("plan real cell: %v", err)
	}

	scheduled := 0
	for attempts := 0; attempts < 1000; attempts++ {
		decision := engine.PlanDummy(now)
		if !decision.Scheduled {
			break
		}
		scheduled++
	}
	if scheduled == 0 {
		t.Fatal("earned budget never scheduled a dummy cell")
	}
	if decision := engine.PlanDummy(now); decision.Scheduled {
		t.Fatalf("dummy remained schedulable after budget exhaustion: %#v", decision)
	}
}

func TestQuietProfileNeverPadsOrSchedulesDummy(t *testing.T) {
	engine, err := NewEngine(Config{
		Profile:            ProfileQuiet,
		MaxOverheadPercent: 100,
		MaxBudgetBytes:     65_535,
		Seed:               [32]byte{0x88},
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)
	for index := 0; index < 100; index++ {
		decision, err := engine.PlanReal(now, 64+index)
		if err != nil {
			t.Fatalf("plan real cell: %v", err)
		}
		if decision.PaddingBytes != 0 || !decision.SendAt.Equal(now) {
			t.Fatalf("quiet profile changed real cell: %#v", decision)
		}
		if dummy := engine.PlanDummy(now); dummy.Scheduled {
			t.Fatalf("quiet profile scheduled dummy: %#v", dummy)
		}
	}
}

func TestEngineRejectsInvalidConfigurationAndInput(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "unknown profile", config: Config{Profile: ProfileID(99), MaxOverheadPercent: 30, Seed: [32]byte{1}}},
		{name: "overhead above 100", config: Config{Profile: ProfileWeb, MaxOverheadPercent: 101, Seed: [32]byte{1}}},
		{name: "zero seed", config: Config{Profile: ProfileWeb, MaxOverheadPercent: 30}},
		{name: "negative max budget", config: Config{Profile: ProfileWeb, MaxOverheadPercent: 30, MaxBudgetBytes: -1, Seed: [32]byte{1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewEngine(tt.config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected invalid config error, got %v", err)
			}
		})
	}

	engine, err := NewEngine(Config{Profile: ProfileWeb, MaxOverheadPercent: 30, Seed: [32]byte{1}})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	for _, size := range []int{0, -1, MaxWireCellBytes + 1} {
		if _, err := engine.PlanReal(time.Now(), size); !errors.Is(err, ErrInvalidCellSize) {
			t.Fatalf("size %d: expected invalid cell size, got %v", size, err)
		}
	}
}

func TestEngineIsSafeForConcurrentPlanning(t *testing.T) {
	engine, err := NewEngine(Config{
		Profile:            ProfileWeb,
		MaxOverheadPercent: 25,
		MaxBudgetBytes:     65_535,
		Seed:               [32]byte{0x99},
	})
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)
	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for index := 0; index < 1000; index++ {
				_, _ = engine.PlanReal(now, 64+(worker+index)%2048)
				engine.PlanDummy(now)
			}
		}(worker)
	}
	wait.Wait()

	stats := engine.Stats()
	if stats.OverheadBytes()*100 > stats.RealBytes*25 {
		t.Fatalf("concurrent planning exceeded budget: %#v", stats)
	}
}

func BenchmarkPlanRealInteractive(b *testing.B) {
	engine, err := NewEngine(Config{
		Profile:            ProfileInteractive,
		MaxOverheadPercent: 30,
		MaxBudgetBytes:     65_535,
		Seed:               [32]byte{0xaa},
	})
	if err != nil {
		b.Fatalf("create engine: %v", err)
	}
	now := time.Unix(1_800_000_000, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := engine.PlanReal(now, 256+index%1024); err != nil {
			b.Fatal(err)
		}
	}
}
