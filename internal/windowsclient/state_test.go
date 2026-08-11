package windowsclient

import "testing"

func TestLifecycleAllowsOnlyExplicitTransitions(t *testing.T) {
	tests := []struct {
		from, to State
		allowed  bool
	}{
		{StateStopped, StateConnecting, true},
		{StateConnecting, StateConnected, true},
		{StateConnecting, StateFailed, true},
		{StateConnected, StateDisconnecting, true},
		{StateDisconnecting, StateStopped, true},
		{StateFailed, StateConnecting, true},
		{StateStopped, StateConnected, false},
		{StateConnected, StateConnecting, false},
		{StateDisconnecting, StateConnected, false},
	}
	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.allowed {
			t.Errorf("CanTransition(%q, %q)=%v want %v", tt.from, tt.to, got, tt.allowed)
		}
	}
}
