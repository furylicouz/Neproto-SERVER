package constellation

import (
	"errors"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

func TestSchedulerSelectsByFlowClassWithoutPayloadInspection(t *testing.T) {
	httpsID := controllerID(1)
	http3ID := controllerID(2)
	candidates := []ScheduleCandidate{
		{
			LeaseKey: httpsID, Carrier: protocol.CarrierHTTPS, Healthy: true,
			ActiveStreams: 1, MaxStreams: 16, RTT: 20 * time.Millisecond,
			LossPPM: 100, ThroughputBPS: 8 * 1024 * 1024,
		},
		{
			LeaseKey: http3ID, Carrier: protocol.CarrierHTTP3, Healthy: true, SupportsDatagram: true,
			ActiveStreams: 2, MaxStreams: 16, RTT: 35 * time.Millisecond,
			LossPPM: 300, ThroughputBPS: 80 * 1024 * 1024,
		},
	}
	scheduler := Scheduler{SwitchThreshold: 1}
	if got, err := scheduler.Select(FlowInteractive, protocol.ContinuityID{}, candidates); err != nil || got != httpsID {
		t.Fatalf("interactive=%x err=%v", got, err)
	}
	if got, err := scheduler.Select(FlowBulk, protocol.ContinuityID{}, candidates); err != nil || got != http3ID {
		t.Fatalf("bulk=%x err=%v", got, err)
	}
	if got, err := scheduler.Select(FlowDatagram, protocol.ContinuityID{}, candidates); err != nil || got != http3ID {
		t.Fatalf("datagram=%x err=%v", got, err)
	}
}

func TestSchedulerHysteresisRetainsHealthyCurrentLease(t *testing.T) {
	current := controllerID(1)
	other := controllerID(2)
	candidates := []ScheduleCandidate{
		{LeaseKey: current, Carrier: protocol.CarrierHTTPS, Healthy: true, MaxStreams: 8, RTT: 30 * time.Millisecond},
		{LeaseKey: other, Carrier: protocol.CarrierHTTPS, Healthy: true, MaxStreams: 8, RTT: 29 * time.Millisecond},
	}
	got, err := (Scheduler{SwitchThreshold: 500}).Select(FlowInteractive, current, candidates)
	if err != nil || got != current {
		t.Fatalf("selected=%x err=%v", got, err)
	}
}

func TestSchedulerEnforcesHealthCapacityAndDatagramBudgets(t *testing.T) {
	candidates := []ScheduleCandidate{
		{LeaseKey: controllerID(1), Carrier: protocol.CarrierHTTPS, Healthy: true, ActiveStreams: 8, MaxStreams: 8},
		{LeaseKey: controllerID(2), Carrier: protocol.CarrierHTTP3, Healthy: false, SupportsDatagram: true, MaxStreams: 8},
	}
	if _, err := (Scheduler{}).Select(FlowInteractive, protocol.ContinuityID{}, candidates); !errors.Is(err, ErrNoHealthyLease) {
		t.Fatalf("capacity error=%v", err)
	}
	candidates[0].ActiveStreams = 0
	if _, err := (Scheduler{}).Select(FlowDatagram, protocol.ContinuityID{}, candidates); !errors.Is(err, ErrNoHealthyLease) {
		t.Fatalf("datagram budget error=%v", err)
	}
}
