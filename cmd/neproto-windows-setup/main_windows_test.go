//go:build windows

package main

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestWaitForRunningServiceObservesRunningState(t *testing.T) {
	statuses := []svc.Status{{State: svc.StartPending}, {State: svc.Running}}
	index := 0
	err := waitForRunningService(func() (svc.Status, error) {
		status := statuses[index]
		if index < len(statuses)-1 {
			index++
		}
		return status, nil
	}, 3, func(time.Duration) {})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWaitForRunningServiceReportsEarlyExit(t *testing.T) {
	err := waitForRunningService(func() (svc.Status, error) {
		return svc.Status{State: svc.Stopped, Win32ExitCode: 1066, ServiceSpecificExitCode: 1}, nil
	}, 1, func(time.Duration) {})
	if err == nil || !strings.Contains(err.Error(), "service-specific code 1") {
		t.Fatalf("err=%v", err)
	}
}
