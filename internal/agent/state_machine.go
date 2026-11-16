package agent

import "sync"

// Agent lifecycle states. They are intentionally coarse: the panel only needs
// to distinguish "connected and serving" from "reconnecting" and "degraded".
const (
	StateInit       = "init"
	StateStarting   = "starting"
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateRunning    = "running"
	StateDegraded   = "degraded"
	StateBackoff    = "backoff"
	StateStopped    = "stopped"
)

type agentState struct {
	mu    sync.RWMutex
	value string
}

func (s *agentState) set(v string) {
	s.mu.Lock()
	s.value = v
	s.mu.Unlock()
}

func (s *agentState) get() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.value == "" {
		return StateInit
	}
	return s.value
}

// State returns the current lifecycle state for diagnostics and hello frames.
func (a *Agent) State() string { return a.state.get() }
