package usage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"neproto.local/chameleon/internal/protocol"
)

func TestTrackerCountsCarrierPoolAsOneDeviceAndRejectsOnlyNewDevice(t *testing.T) {
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "index.json")
	statePath := filepath.Join(directory, "usage", "state.json")
	writePolicy(t, policyPath, 0, map[string]int{"alice": 1})
	now := time.Date(2026, time.July, 30, 14, 0, 0, 0, time.UTC)
	tracker, err := New(Config{PolicyPath: policyPath, StatePath: statePath, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	deviceA := protocol.DeviceID{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xf0, 0x01}
	deviceB := protocol.DeviceID{0x21, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xf0, 0x02}
	firstCounters := Counters{}
	secondCounters := Counters{}
	first, err := tracker.Admit("alice", deviceA, func() Counters { return firstCounters })
	if err != nil {
		t.Fatal(err)
	}
	second, err := tracker.Admit("alice", deviceA, func() Counters { return secondCounters })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.Admit("alice", deviceB, func() Counters { return Counters{} }); !errors.Is(err, ErrDeviceLimit) {
		t.Fatalf("new device error=%v", err)
	}

	firstCounters = Counters{UploadBytes: 100, DownloadBytes: 300}
	secondCounters = Counters{UploadBytes: 40, DownloadBytes: 60}
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := tracker.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	user := requireUser(t, snapshot, "alice")
	if !user.Online || user.ActiveSessions != 2 || user.OnlineDevices != 1 || user.EnrolledDevices != 1 ||
		user.UploadBytes != 140 || user.DownloadBytes != 360 {
		t.Fatalf("active snapshot=%#v", user)
	}

	now = now.Add(time.Minute)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err = tracker.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	user = requireUser(t, snapshot, "alice")
	if user.Online || user.ActiveSessions != 0 || user.OnlineDevices != 0 || user.LastSeen == nil || !user.LastSeen.Equal(now) {
		t.Fatalf("closed snapshot=%#v", user)
	}
	if err := RemoveOfflineDevice(statePath, "alice", deviceA); err != nil {
		t.Fatalf("remove offline device: %v", err)
	}
	snapshot, err = ReadSnapshot(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if requireUser(t, snapshot, "alice").EnrolledDevices != 0 {
		t.Fatalf("offline device was not removed: %#v", snapshot)
	}
}

func TestTrackerAppliesTrafficResetDuringActiveSession(t *testing.T) {
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "index.json")
	statePath := filepath.Join(directory, "usage", "state.json")
	writePolicy(t, policyPath, 0, map[string]int{"alice": 0})
	tracker, err := New(Config{PolicyPath: policyPath, StatePath: statePath, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	device := protocol.DeviceID{0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e, 0x3f, 0x40}
	counters := Counters{UploadBytes: 100, DownloadBytes: 200}
	session, err := tracker.Admit("alice", device, func() Counters { return counters })
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}
	writePolicy(t, policyPath, 1, map[string]int{"alice": 0})
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}
	user := requireUser(t, mustSnapshot(t, tracker), "alice")
	if user.UploadBytes != 0 || user.DownloadBytes != 0 {
		t.Fatalf("reset retained bytes: %#v", user)
	}
	counters = Counters{UploadBytes: 125, DownloadBytes: 250}
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}
	user = requireUser(t, mustSnapshot(t, tracker), "alice")
	if user.UploadBytes != 25 || user.DownloadBytes != 50 {
		t.Fatalf("post-reset counters=%#v", user)
	}
}

func TestResetTrafficIsImmediateAndDoesNotRestorePreResetSessionBytes(t *testing.T) {
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "index.json")
	statePath := filepath.Join(directory, "usage", "state.json")
	writePolicy(t, policyPath, 0, map[string]int{"alice": 0})
	tracker, err := New(Config{PolicyPath: policyPath, StatePath: statePath, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	device := protocol.DeviceID{0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f, 0x50}
	counters := Counters{}
	session, err := tracker.Admit("alice", device, func() Counters { return counters })
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	counters = Counters{UploadBytes: 100, DownloadBytes: 200}
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}

	writePolicy(t, policyPath, 1, map[string]int{"alice": 0})
	if err := ResetTraffic(statePath, "alice"); err != nil {
		t.Fatal(err)
	}
	reset := requireUser(t, mustReadSnapshot(t, statePath), "alice")
	if reset.UploadBytes != 0 || reset.DownloadBytes != 0 {
		t.Fatalf("immediate reset=%#v", reset)
	}
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}
	reset = requireUser(t, mustSnapshot(t, tracker), "alice")
	if reset.UploadBytes != 0 || reset.DownloadBytes != 0 {
		t.Fatalf("pre-reset counters returned=%#v", reset)
	}
	counters = Counters{UploadBytes: 130, DownloadBytes: 240}
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}
	reset = requireUser(t, mustSnapshot(t, tracker), "alice")
	if reset.UploadBytes != 30 || reset.DownloadBytes != 40 {
		t.Fatalf("post-reset counters=%#v", reset)
	}
}

func TestTrackerRequiresIdentityOnlyWhenPolicyHasDeviceLimit(t *testing.T) {
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "index.json")
	statePath := filepath.Join(directory, "usage", "state.json")
	writePolicy(t, policyPath, 0, map[string]int{"legacy": 0, "limited": 1})
	tracker, err := New(Config{PolicyPath: policyPath, StatePath: statePath, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := tracker.Admit("legacy", protocol.DeviceID{}, func() Counters { return Counters{} })
	if err != nil {
		t.Fatalf("unlimited legacy session rejected: %v", err)
	}
	defer legacy.Close()
	if _, err := tracker.Admit("limited", protocol.DeviceID{}, func() Counters { return Counters{} }); !errors.Is(err, ErrDeviceIdentityRequired) {
		t.Fatalf("limited legacy error=%v", err)
	}
}

func TestTrackerPrunesDeletedOfflineUser(t *testing.T) {
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "index.json")
	statePath := filepath.Join(directory, "usage", "state.json")
	writePolicy(t, policyPath, 0, map[string]int{"alice": 0})
	tracker, err := New(Config{PolicyPath: policyPath, StatePath: statePath, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	device := protocol.DeviceID{0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x5b, 0x5c, 0x5d, 0x5e, 0x5f, 0x60}
	session, err := tracker.Admit("alice", device, func() Counters { return Counters{} })
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	writePolicy(t, policyPath, 0, map[string]int{})
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}
	if snapshot := mustSnapshot(t, tracker); len(snapshot.Users) != 0 {
		t.Fatalf("deleted user state survived: %#v", snapshot)
	}
}

func TestTrackerDoesNotRewriteUnchangedStateOnEverySample(t *testing.T) {
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "index.json")
	statePath := filepath.Join(directory, "usage", "state.json")
	writePolicy(t, policyPath, 0, map[string]int{"alice": 0})
	tracker, err := New(Config{PolicyPath: policyPath, StatePath: statePath, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	before := mustSnapshot(t, tracker).Revision
	if err := tracker.Sample(); err != nil {
		t.Fatal(err)
	}
	after := mustSnapshot(t, tracker).Revision
	if after != before {
		t.Fatalf("unchanged sample revision=%d, want %d", after, before)
	}
}

func TestUnlimitedPolicyDoesNotRejectAfterBoundedDeviceHistoryIsFull(t *testing.T) {
	directory := t.TempDir()
	policyPath := filepath.Join(directory, "index.json")
	statePath := filepath.Join(directory, "usage", "state.json")
	writePolicy(t, policyPath, 0, map[string]int{"alice": 0})
	tracker, err := New(Config{PolicyPath: policyPath, StatePath: statePath, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maximumDevicesPerUser+1; index++ {
		device := protocol.DeviceID{byte(index + 1), 0x62, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x6b, 0x6c, 0x6d, 0x6e, 0x6f, 0x70}
		session, admitErr := tracker.Admit("alice", device, func() Counters { return Counters{} })
		if admitErr != nil {
			t.Fatalf("unlimited device %d rejected: %v", index+1, admitErr)
		}
		if closeErr := session.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	user := requireUser(t, mustSnapshot(t, tracker), "alice")
	if user.EnrolledDevices != maximumDevicesPerUser {
		t.Fatalf("bounded device history=%d, want %d", user.EnrolledDevices, maximumDevicesPerUser)
	}
}

func writePolicy(t *testing.T, path string, resetGeneration uint64, users map[string]int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	type policyUser struct {
		ID                     string `json:"id"`
		Status                 string `json:"status"`
		MaxDevices             int    `json:"max_devices,omitempty"`
		TrafficResetGeneration uint64 `json:"traffic_reset_generation,omitempty"`
	}
	list := make([]policyUser, 0, len(users))
	for id, maximum := range users {
		list = append(list, policyUser{ID: id, Status: "active", MaxDevices: maximum, TrafficResetGeneration: resetGeneration})
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "users": list})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireUser(t *testing.T, snapshot Snapshot, id string) UserSnapshot {
	t.Helper()
	for _, user := range snapshot.Users {
		if user.UserID == id {
			return user
		}
	}
	t.Fatalf("user %q not found in %#v", id, snapshot)
	return UserSnapshot{}
}

func mustSnapshot(t *testing.T, tracker *Tracker) Snapshot {
	t.Helper()
	snapshot, err := tracker.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func mustReadSnapshot(t *testing.T, path string) Snapshot {
	t.Helper()
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
