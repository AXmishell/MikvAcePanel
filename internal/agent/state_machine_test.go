package agent

import "testing"

func TestAgentStateTransitions(t *testing.T) {
	var state agentState
	if got := state.get(); got != StateInit {
		t.Fatalf("initial state = %q, want %q", got, StateInit)
	}
	for _, want := range []string{StateStarting, StateConnecting, StateConnected, StateRunning, StateDegraded, StateBackoff, StateStopped} {
		state.set(want)
		if got := state.get(); got != want {
			t.Fatalf("state = %q, want %q", got, want)
		}
	}
}
