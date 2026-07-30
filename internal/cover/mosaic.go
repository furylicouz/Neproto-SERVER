package cover

import "time"

const (
	mosaicObservationWindow = 500 * time.Millisecond
	mosaicIdleReset         = 3 * time.Second
	mosaicSmallCellBytes    = 1200
	mosaicStreamRate        = 1024 * 1024
	mosaicStreamBurstBytes  = 256 * 1024
	mosaicRealtimeMaxRate   = 512 * 1024
	mosaicRealtimeMinCells  = 10
	mosaicRealtimeSmallPct  = 80
	mosaicConfirmWindows    = 2

	// Observation counters saturate at conservative bounds so a malicious or
	// synthetic clock cannot turn the classifier into an overflow surface.
	mosaicMaxObservedBytes = uint64(1 << 30)
	mosaicMaxObservedCells = uint64(1 << 20)
)

type TrafficClass uint8

const (
	TrafficUnknown TrafficClass = iota
	TrafficWeb
	TrafficRealtime
	TrafficStream
)

func (c TrafficClass) String() string {
	switch c {
	case TrafficWeb:
		return "web"
	case TrafficRealtime:
		return "realtime"
	case TrafficStream:
		return "stream"
	default:
		return "unknown"
	}
}

type mosaicState struct {
	enabled bool
	class   TrafficClass

	windowStart     time.Time
	lastObservation time.Time
	windowBytes     uint64
	windowCells     uint64
	smallCells      uint64

	candidate        TrafficClass
	candidateWindows uint8
	transitions      uint64
}

func (e *Engine) observeMosaic(now time.Time, wireBytes int) {
	state := &e.mosaic
	if !state.lastObservation.IsZero() && now.Before(state.lastObservation) {
		state.resetWindow(now)
		state.candidate = TrafficUnknown
		state.candidateWindows = 0
	}
	if state.windowStart.IsZero() {
		state.resetWindow(now)
	}

	if !state.lastObservation.IsZero() && now.Sub(state.lastObservation) > mosaicIdleReset {
		e.proposeMosaicClass(TrafficWeb)
		state.resetWindow(now)
	}

	if elapsed := now.Sub(state.windowStart); elapsed >= mosaicObservationWindow {
		e.proposeMosaicClass(classifyMosaicWindow(state, elapsed))
		state.resetWindow(now)
	}

	state.windowBytes = boundedAdd(state.windowBytes, uint64(wireBytes), mosaicMaxObservedBytes)
	state.windowCells = boundedAdd(state.windowCells, 1, mosaicMaxObservedCells)
	if wireBytes <= mosaicSmallCellBytes {
		state.smallCells = boundedAdd(state.smallCells, 1, mosaicMaxObservedCells)
	}
	state.lastObservation = now

	elapsed := now.Sub(state.windowStart)
	if elapsed >= 0 && elapsed <= time.Second && state.windowBytes >= mosaicStreamBurstBytes {
		e.transitionMosaicClass(TrafficStream)
	}
}

func classifyMosaicWindow(state *mosaicState, elapsed time.Duration) TrafficClass {
	if elapsed <= 0 {
		return TrafficWeb
	}
	if rateAtLeast(state.windowBytes, elapsed, mosaicStreamRate) {
		return TrafficStream
	}
	if state.windowCells >= mosaicRealtimeMinCells &&
		state.smallCells*100 >= state.windowCells*mosaicRealtimeSmallPct &&
		rateAtMost(state.windowBytes, elapsed, mosaicRealtimeMaxRate) {
		return TrafficRealtime
	}
	return TrafficWeb
}

func (e *Engine) proposeMosaicClass(candidate TrafficClass) {
	state := &e.mosaic
	if candidate == state.class {
		state.candidate = TrafficUnknown
		state.candidateWindows = 0
		return
	}
	if candidate == TrafficStream {
		e.transitionMosaicClass(candidate)
		return
	}
	if state.candidate != candidate {
		state.candidate = candidate
		state.candidateWindows = 1
		return
	}
	if state.candidateWindows < mosaicConfirmWindows {
		state.candidateWindows++
	}
	if state.candidateWindows >= mosaicConfirmWindows {
		e.transitionMosaicClass(candidate)
	}
}

func (e *Engine) transitionMosaicClass(class TrafficClass) {
	state := &e.mosaic
	if class == TrafficUnknown || state.class == class {
		return
	}
	state.class = class
	state.candidate = TrafficUnknown
	state.candidateWindows = 0
	state.transitions = saturatingAdd(state.transitions, 1)
	e.setMosaicClass(class)
}

func (e *Engine) setMosaicClass(class TrafficClass) {
	profile := ProfileWeb
	switch class {
	case TrafficRealtime:
		profile = ProfileInteractive
	case TrafficStream:
		profile = ProfileStream
	}
	e.activeProfile = profile
	e.profile = profileDefinitions[profile]
}

func (s *mosaicState) resetWindow(now time.Time) {
	s.windowStart = now
	s.windowBytes = 0
	s.windowCells = 0
	s.smallCells = 0
}

func boundedAdd(value, increment, maximum uint64) uint64 {
	if value >= maximum || increment >= maximum-value {
		return maximum
	}
	return value + increment
}

func rateAtLeast(bytes uint64, elapsed time.Duration, threshold uint64) bool {
	if elapsed <= 0 {
		return false
	}
	return bytes*uint64(time.Second) >= threshold*uint64(elapsed)
}

func rateAtMost(bytes uint64, elapsed time.Duration, threshold uint64) bool {
	if elapsed <= 0 {
		return false
	}
	return bytes*uint64(time.Second) <= threshold*uint64(elapsed)
}
