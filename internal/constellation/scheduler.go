package constellation

import (
	"bytes"
	"errors"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

type FlowClass uint8

const (
	FlowInteractive FlowClass = iota + 1
	FlowBulk
	FlowDatagram
)

const MaxScheduleCandidates = 8

var (
	ErrSchedulerConfig = errors.New("invalid constellation scheduler configuration")
	ErrNoHealthyLease  = errors.New("no healthy constellation lease")
)

type ScheduleCandidate struct {
	LeaseKey         protocol.ContinuityID
	Carrier          protocol.CarrierKind
	Healthy          bool
	SupportsDatagram bool
	ActiveStreams    uint64
	MaxStreams       uint64
	QueueBytes       uint64
	RTT              time.Duration
	LossPPM          uint32
	ThroughputBPS    uint64
}

type Scheduler struct {
	SwitchThreshold uint64
}

// Select uses payload- and destination-free local measurements. A healthy
// current lease is retained unless another lease wins by the hysteresis
// threshold, preventing rapid oscillation during noisy mobile transitions.
func (s Scheduler) Select(
	class FlowClass,
	current protocol.ContinuityID,
	candidates []ScheduleCandidate,
) (protocol.ContinuityID, error) {
	if class < FlowInteractive || class > FlowDatagram || len(candidates) == 0 ||
		len(candidates) > MaxScheduleCandidates {
		return protocol.ContinuityID{}, ErrSchedulerConfig
	}
	threshold := s.SwitchThreshold
	if threshold == 0 {
		threshold = 5_000
	}
	var best protocol.ContinuityID
	bestScore := ^uint64(0)
	currentScore := ^uint64(0)
	for _, candidate := range candidates {
		score, eligible := scheduleScore(class, candidate)
		if !eligible {
			continue
		}
		if candidate.LeaseKey == current {
			currentScore = score
		}
		if best == (protocol.ContinuityID{}) || score < bestScore ||
			score == bestScore && bytes.Compare(candidate.LeaseKey[:], best[:]) < 0 {
			best, bestScore = candidate.LeaseKey, score
		}
	}
	if best == (protocol.ContinuityID{}) {
		return protocol.ContinuityID{}, ErrNoHealthyLease
	}
	if current != (protocol.ContinuityID{}) && currentScore != ^uint64(0) &&
		(bestScore >= currentScore || currentScore-bestScore <= threshold) {
		return current, nil
	}
	return best, nil
}

func scheduleScore(class FlowClass, candidate ScheduleCandidate) (uint64, bool) {
	if candidate.LeaseKey == (protocol.ContinuityID{}) || !validCarrier(candidate.Carrier) ||
		!candidate.Healthy || candidate.MaxStreams == 0 ||
		candidate.ActiveStreams >= candidate.MaxStreams || candidate.RTT < 0 ||
		candidate.RTT > time.Minute || candidate.LossPPM > 1_000_000 ||
		(class == FlowDatagram && !candidate.SupportsDatagram) {
		return 0, false
	}
	rttMS := uint64(candidate.RTT / time.Millisecond)
	loss := uint64(candidate.LossPPM)
	load := candidate.ActiveStreams * 1_000 / candidate.MaxStreams
	queueKiB := min(candidate.QueueBytes/1024, uint64(1_000_000))
	switch class {
	case FlowInteractive:
		return rttMS*100 + loss/10 + load*20 + queueKiB*2, true
	case FlowBulk:
		throughputKiB := candidate.ThroughputBPS / 1024
		inverseThroughput := uint64(1_000_000_000) / (throughputKiB + 1)
		return inverseThroughput + loss/5 + load*10 + queueKiB, true
	case FlowDatagram:
		return rttMS*150 + loss/5 + load*10 + queueKiB*3, true
	default:
		return 0, false
	}
}
