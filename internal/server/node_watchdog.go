package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// The node watchdog turns the heartbeat's core state into automatic recovery
// actions. A core is only restarted when the agent says it should be running
// but is not, which keeps an operator's explicit "stop" authoritative. When
// the whole agent is offline it falls back to the saved SSH credentials.

const (
	defaultAutoRestartGrace       = 60 * time.Second
	defaultAutoRestartCooldown    = 5 * time.Minute
	defaultAutoRestartMaxAttempts = 3
	autoRestartAttemptWindow      = time.Hour
	nodeWatchdogInterval          = 15 * time.Second
)

type nodeHealthState struct {
	desired      map[string]bool
	running      map[string]bool
	stoppedSince map[string]time.Time
	updated      time.Time
}

type nodeWatchdog struct {
	mu           sync.Mutex
	health       map[int64]*nodeHealthState
	offlineSince map[int64]time.Time
	attempts     map[int64][]time.Time
	inFlight     map[int64]bool
}

func newNodeWatchdog() *nodeWatchdog {
	return &nodeWatchdog{
		health:       map[int64]*nodeHealthState{},
		offlineSince: map[int64]time.Time{},
		attempts:     map[int64][]time.Time{},
		inFlight:     map[int64]bool{},
	}
}

// markCommand applies the operator's intent immediately so a tick that lands
// between the command and the next heartbeat cannot override a manual stop.
func (w *nodeWatchdog) markCommand(nodeID int64, action string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	state := w.health[nodeID]
	if state == nil {
		return
	}
	switch action {
	case "stop":
		for core := range state.desired {
			state.desired[core] = false
		}
		state.stoppedSince = map[string]time.Time{}
	case "start", "restart", "restart-node":
		for core := range state.desired {
			state.desired[core] = true
			delete(state.stoppedSince, core)
		}
	}
}

// observeHeartbeat records the latest core state for a connected node.
func (w *nodeWatchdog) observeHeartbeat(nodeID int64, running, desired map[string]bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	state := w.health[nodeID]
	if state == nil {
		state = &nodeHealthState{stoppedSince: map[string]time.Time{}}
		w.health[nodeID] = state
	}
	state.desired = copyBoolMap(desired)
	state.running = copyBoolMap(running)
	state.updated = time.Now().UTC()
	delete(w.offlineSince, nodeID)
	for core := range state.stoppedSince {
		if !state.desired[core] || state.running[core] {
			delete(state.stoppedSince, core)
		}
	}
	for core, want := range state.desired {
		if !want || state.running[core] {
			continue
		}
		if _, ok := state.stoppedSince[core]; !ok {
			state.stoppedSince[core] = state.updated
		}
	}
}

func copyBoolMap(src map[string]bool) map[string]bool {
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (w *nodeWatchdog) reset() {
	w.mu.Lock()
	w.health = map[int64]*nodeHealthState{}
	w.offlineSince = map[int64]time.Time{}
	// Keep attempts and in-flight markers: if the operator disables and
	// re-enables recovery, an active SSH attempt must not run twice and the
	// cooldown should still apply.
	w.mu.Unlock()
}

func (w *nodeWatchdog) clearLocked(nodeID int64) {
	delete(w.health, nodeID)
	delete(w.offlineSince, nodeID)
	delete(w.attempts, nodeID)
}

func (w *nodeWatchdog) attemptAllowedLocked(nodeID int64, now time.Time, cooldown time.Duration, maxAttempts int) bool {
	cutoff := now.Add(-autoRestartAttemptWindow)
	attempts := w.attempts[nodeID][:0]
	for _, at := range w.attempts[nodeID] {
		if at.After(cutoff) {
			attempts = append(attempts, at)
		}
	}
	w.attempts[nodeID] = attempts
	if maxAttempts > 0 && len(attempts) >= maxAttempts {
		return false
	}
	if len(attempts) > 0 && now.Sub(attempts[len(attempts)-1]) < cooldown {
		return false
	}
	return true
}

func (w *nodeWatchdog) markAttemptLocked(nodeID int64, now time.Time) {
	w.inFlight[nodeID] = true
	w.attempts[nodeID] = append(w.attempts[nodeID], now)
}

func (w *nodeWatchdog) finish(nodeID int64) {
	w.mu.Lock()
	delete(w.inFlight, nodeID)
	w.mu.Unlock()
}

type autoRestartPolicy struct {
	enabled     bool
	grace       time.Duration
	cooldown    time.Duration
	maxAttempts int
}

func (s *Server) autoRestartPolicy(ctx context.Context) autoRestartPolicy {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		settings = map[string]string{}
	}
	grace := parseIntSetting(settings["node_auto_restart_grace_seconds"], int(defaultAutoRestartGrace/time.Second))
	if grace < 10 {
		grace = 10
	}
	cooldown := parseIntSetting(settings["node_auto_restart_cooldown_seconds"], int(defaultAutoRestartCooldown/time.Second))
	if cooldown < 30 {
		cooldown = 30
	}
	maxAttempts := parseIntSetting(settings["node_auto_restart_max_attempts"], defaultAutoRestartMaxAttempts)
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if maxAttempts > 20 {
		maxAttempts = 20
	}
	return autoRestartPolicy{
		enabled:     parseBoolSetting(settings["node_auto_restart_enabled"], true),
		grace:       time.Duration(grace) * time.Second,
		cooldown:    time.Duration(cooldown) * time.Second,
		maxAttempts: maxAttempts,
	}
}

// RunNodeWatchdog periodically inspects node health until the context ends.
func (s *Server) RunNodeWatchdog(ctx context.Context) {
	ticker := time.NewTicker(nodeWatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runNodeWatchdogOnce(ctx)
		}
	}
}

type autoRestartCandidate struct {
	node model.Node
	core string
	via  string
}

func (s *Server) runNodeWatchdogOnce(ctx context.Context) {
	if s.watchdog == nil {
		return
	}
	policy := s.autoRestartPolicy(ctx)
	if !policy.enabled {
		s.watchdog.reset()
		return
	}
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		s.log.Warn("node watchdog list nodes", "error", err)
		return
	}
	now := time.Now().UTC()
	var work []autoRestartCandidate
	w := s.watchdog
	w.mu.Lock()
	for i := range nodes {
		n := &nodes[i]
		if !n.Active || !n.AutoRestart {
			w.clearLocked(n.ID)
			continue
		}
		if s.hub.Connected(n.ID) {
			delete(w.offlineSince, n.ID)
			state := w.health[n.ID]
			if state == nil {
				continue
			}
			for _, core := range n.EffectiveCores() {
				if !state.desired[core] || state.running[core] {
					continue
				}
				since, ok := state.stoppedSince[core]
				if !ok || since.IsZero() {
					state.stoppedSince[core] = now
					continue
				}
				if now.Sub(since) < policy.grace {
					continue
				}
				if !w.attemptAllowedLocked(n.ID, now, policy.cooldown, policy.maxAttempts) {
					continue
				}
				w.markAttemptLocked(n.ID, now)
				work = append(work, autoRestartCandidate{node: *n, core: core, via: "command"})
				break
			}
			continue
		}

		// The agent is offline. Without a previous heartbeat there is nothing
		// to recover (for example a node that was never installed).
		delete(w.health, n.ID)
		if n.LastSeenAt == nil {
			w.clearLocked(n.ID)
			continue
		}
		if !nodeSSHReady(n) {
			// Start the grace period only once SSH recovery is possible.
			delete(w.offlineSince, n.ID)
			continue
		}
		since, ok := w.offlineSince[n.ID]
		if !ok {
			w.offlineSince[n.ID] = now
			continue
		}
		if now.Sub(since) < policy.grace {
			continue
		}
		if !w.attemptAllowedLocked(n.ID, now, policy.cooldown, policy.maxAttempts) {
			continue
		}
		w.markAttemptLocked(n.ID, now)
		work = append(work, autoRestartCandidate{node: *n, via: "ssh"})
	}
	w.mu.Unlock()

	for _, candidate := range work {
		go s.executeAutoRestart(candidate)
	}
}

func (s *Server) executeAutoRestart(candidate autoRestartCandidate) {
	defer s.watchdog.finish(candidate.node.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	name := candidate.node.Name
	if candidate.via == "command" {
		if !s.hub.Connected(candidate.node.ID) {
			return
		}
		if _, ok := s.hub.SendCommand(candidate.node.ID, "restart"); !ok {
			s.store.AddEvent(ctx, "node.auto_restart_failed", candidate.node.ID, "warning",
				fmt.Sprintf("节点 %s 的核心 %s 异常停止，自动重启指令下发失败", name, candidate.core))
			return
		}
		s.store.AddEvent(ctx, "node.auto_restart", candidate.node.ID, "warning",
			fmt.Sprintf("节点 %s 的核心 %s 异常停止，已自动下发重启指令", name, candidate.core))
		s.log.Warn("node core auto restart", "node", name, "id", candidate.node.ID, "core", candidate.core)
		return
	}

	// The agent may have reconnected while the watchdog was evaluating it.
	if s.hub.Connected(candidate.node.ID) {
		return
	}
	result := s.runNodeSSHRestart(&candidate.node)
	if !result.OK {
		reason := result.Error
		if reason == "" {
			reason = "unknown error"
		}
		s.store.AddEvent(ctx, "node.auto_restart_failed", candidate.node.ID, "error",
			fmt.Sprintf("节点 %s 离线，SSH 自动重启失败：%s", name, reason))
		s.log.Warn("node ssh auto restart failed", "node", name, "id", candidate.node.ID, "error", reason)
		return
	}
	s.store.AddEvent(ctx, "node.auto_restart", candidate.node.ID, "warning",
		fmt.Sprintf("节点 %s 的 Agent 已离线，已通过 SSH 自动重启", name))
	s.log.Warn("node ssh auto restart", "node", name, "id", candidate.node.ID)
}
