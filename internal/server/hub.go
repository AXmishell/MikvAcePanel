package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// Hub tracks connected node agents and live online state.
type Hub struct {
	store *store.Store
	log   *slog.Logger

	mu         sync.RWMutex
	agents     map[int64]*AgentConn
	lastMetric map[int64]trafficPair
	online     map[int64]map[string]int
	authOnline map[string]time.Time
	devices    map[string]map[string]time.Time
	touched    map[string]time.Time
	started    time.Time
}

type trafficPair struct{ Tx, Rx int64 }

// AgentConn is one connected node agent.
type AgentConn struct {
	NodeID int64
	ws     *websocket.Conn
	send   chan []byte
	hub    *Hub
	closed chan struct{}
	once   sync.Once
	Name   string
}

// NewHub creates a hub.
func NewHub(st *store.Store, logger *slog.Logger) *Hub {
	return &Hub{
		store: st, log: logger,
		agents:     map[int64]*AgentConn{},
		lastMetric: map[int64]trafficPair{},
		online:     map[int64]map[string]int{},
		authOnline: map[string]time.Time{},
		devices:    map[string]map[string]time.Time{},
		touched:    map[string]time.Time{},
		started:    time.Now().UTC(),
	}
}

func (h *Hub) register(nodeID int64, name string, ws *websocket.Conn) *AgentConn {
	ac := &AgentConn{
		NodeID: nodeID, ws: ws, send: make(chan []byte, 128),
		hub: h, closed: make(chan struct{}), Name: name,
	}
	h.mu.Lock()
	old := h.agents[nodeID]
	h.agents[nodeID] = ac
	h.mu.Unlock()
	if old != nil {
		old.close()
	}
	return ac
}

func (h *Hub) unregister(ac *AgentConn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.agents[ac.NodeID] == ac {
		delete(h.agents, ac.NodeID)
		delete(h.online, ac.NodeID)
		return true
	}
	return false
}

func (a *AgentConn) close() {
	a.once.Do(func() {
		close(a.closed)
		_ = a.ws.Close()
	})
}

func (a *AgentConn) sendJSON(v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	select {
	case a.send <- b:
		return true
	case <-a.closed:
		return false
	default:
		// A full buffer means the agent is not reading; drop the connection so
		// it reconnects and re-syncs state rather than silently missing config.
		a.close()
		return false
	}
}

// Agent returns the live connection of a node, if any.
func (h *Hub) Agent(nodeID int64) *AgentConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agents[nodeID]
}

// Connected reports whether a node agent is currently connected.
func (h *Hub) Connected(nodeID int64) bool { return h.Agent(nodeID) != nil }

// SendToNode sends a protocol message to a node. It returns false when the
// node is offline.
func (h *Hub) SendToNode(nodeID int64, msgType string, payload any) bool {
	ac := h.Agent(nodeID)
	if ac == nil {
		return false
	}
	return ac.sendJSON(map[string]any{"type": msgType, "payload": payload})
}

// PushConfig sends a configuration revision to a node.
func (h *Hub) PushConfig(nodeID, revision int64, protocol, config, tlsMode, domain, host, portRange string, port int) bool {
	return h.SendToNode(nodeID, "config", map[string]any{
		"revision": revision, "protocol": protocol, "config": config, "yaml": config,
		"tlsMode": tlsMode, "domain": domain, "host": host, "portRange": portRange, "port": port,
	})
}

// PushConfigBundle sends one configuration per core to a multi-core agent.
func (h *Hub) PushConfigBundle(nodeID, revision int64, configs map[string]string, cores []string, tlsMode, domain, host, portRange string, port int) bool {
	primary := ""
	primaryConfig := ""
	if len(cores) > 0 {
		primary = cores[0]
		primaryConfig = configs[primary]
	}
	return h.SendToNode(nodeID, "config", map[string]any{
		"revision": revision, "cores": cores, "configs": configs,
		"protocol": primary, "config": primaryConfig, "yaml": primaryConfig,
		"tlsMode": tlsMode, "domain": domain, "host": host, "portRange": portRange, "port": port,
	})
}

// SendCommand issues a runtime command.
func (h *Hub) SendCommand(nodeID int64, action string) (string, bool) {
	id := store.RandomToken(8)
	ok := h.SendToNode(nodeID, "command", map[string]any{"id": id, "action": action})
	return id, ok
}

// UpdateNodeOnline stores online user counts reported by an agent.
func (h *Hub) UpdateNodeOnline(nodeID int64, counts map[string]int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.online[nodeID] = counts
	nowT := time.Now().UTC()
	for uid := range counts {
		if last, ok := h.touched[uid]; !ok || nowT.Sub(last) > time.Minute {
			h.touched[uid] = nowT
			go h.store.TouchUserSeen(context.Background(), uid)
		}
	}
}

// OnlineUserCount returns the number of distinct users online across nodes.
func (h *Hub) OnlineUserCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	set := map[string]bool{}
	for _, counts := range h.online {
		for uid, n := range counts {
			if n > 0 {
				set[uid] = true
			}
		}
	}
	nowT := time.Now()
	for uid, seen := range h.authOnline {
		if nowT.Sub(seen) < 5*time.Minute {
			set[uid] = true
		}
	}
	return len(set)
}

// UserOnline reports whether a user has an active session.
func (h *Hub) UserOnline(userID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, counts := range h.online {
		if counts[userID] > 0 {
			return true
		}
	}
	if seen, ok := h.authOnline[userID]; ok && time.Since(seen) < 5*time.Minute {
		return true
	}
	return false
}

// OnlineDevices returns the live connection count of a user.
func (h *Hub) OnlineDevices(userID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	max := 0
	for _, counts := range h.online {
		if counts[userID] > max {
			max = counts[userID]
		}
	}
	return max
}

// MarkAuthOnline records a fresh authentication for immediate UI feedback.
func (h *Hub) MarkAuthOnline(userID string) {
	h.mu.Lock()
	h.authOnline[userID] = time.Now()
	h.mu.Unlock()
}

// CheckDeviceLimit enforces the unique-IP device limit with a grace period.
// It returns allowed and the number of active addresses.
func (h *Hub) CheckDeviceLimit(userID, ip string, limit, graceMinutes int) (bool, int) {
	if limit <= 0 || ip == "" {
		return true, 0
	}
	if graceMinutes <= 0 {
		graceMinutes = 15
	}
	grace := time.Duration(graceMinutes) * time.Minute
	nowT := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	devices := h.devices[userID]
	if devices == nil {
		devices = map[string]time.Time{}
		h.devices[userID] = devices
	}
	for addr, last := range devices {
		if nowT.Sub(last) > grace {
			delete(devices, addr)
		}
	}
	devices[ip] = nowT
	if len(devices) > limit {
		delete(devices, ip)
		return false, len(devices) + 1
	}
	return true, len(devices)
}

// ResetDeviceLimit clears device tracking for a user (admin action).
func (h *Hub) ResetDeviceLimit(userID string) {
	h.mu.Lock()
	delete(h.devices, userID)
	h.mu.Unlock()
}

// OnlineNodes returns node ids that have an agent connection.
func (h *Hub) OnlineNodes() map[int64]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := map[int64]bool{}
	for id := range h.agents {
		out[id] = true
	}
	return out
}

// HeartbeatDelta stores node traffic counters and returns deltas since the last
// sample. A counter reset yields the current value.
func (h *Hub) HeartbeatDelta(nodeID, tx, rx int64) (int64, int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prev, ok := h.lastMetric[nodeID]
	h.lastMetric[nodeID] = trafficPair{Tx: tx, Rx: rx}
	if !ok {
		return 0, 0
	}
	dtx, drx := tx-prev.Tx, rx-prev.Rx
	if dtx < 0 {
		dtx = tx
	}
	if drx < 0 {
		drx = rx
	}
	return dtx, drx
}

// Disconnect closes the live agent connection of a node. It is used when a
// token is rotated or a node is deleted.
func (h *Hub) Disconnect(nodeID int64) {
	if ac := h.Agent(nodeID); ac != nil {
		ac.close()
	}
}

// ClearNode removes live state for a disconnected node.
func (h *Hub) ClearNode(nodeID int64) {
	h.mu.Lock()
	delete(h.online, nodeID)
	h.mu.Unlock()
}

// Monitor marks stale agents offline and expires auth-online entries.
func (h *Hub) Monitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.monitorOnce(ctx)
		}
	}
}

func (h *Hub) monitorOnce(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-45 * time.Second)
	stale, err := h.store.MarkStaleNodesOffline(ctx, cutoff)
	if err != nil {
		h.log.Warn("mark stale nodes", "error", err)
		return
	}
	for _, n := range stale {
		h.ClearNode(n.ID)
		h.store.AddEvent(ctx, "node.offline", n.ID, "warning", "节点 "+n.Name+" 已停止上报心跳")
		h.log.Info("node offline", "node", n.Name, "id", n.ID)
	}
	h.mu.Lock()
	nowT := time.Now()
	for uid, seen := range h.authOnline {
		if nowT.Sub(seen) > 15*time.Minute {
			delete(h.authOnline, uid)
		}
	}
	for uid := range h.devices {
		// Devices are cleaned lazily in CheckDeviceLimit.
		_ = uid
	}
	h.mu.Unlock()
}

// NodeAgentInfo is bookkeeping info used by the dashboard.
type NodeAgentInfo struct {
	Connected bool      `json:"connected"`
	Online    int       `json:"online"`
	LastSeen  time.Time `json:"lastSeen"`
}

// Info returns live agent information for a node.
func (h *Hub) Info(nodeID int64) NodeAgentInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ac := h.agents[nodeID]
	if ac == nil {
		return NodeAgentInfo{}
	}
	counts := h.online[nodeID]
	total := 0
	for _, n := range counts {
		total += n
	}
	return NodeAgentInfo{Connected: true, Online: total, LastSeen: time.Now().UTC()}
}

// UserOnlineMap returns a snapshot used to decorate user responses.
func (h *Hub) UserOnlineMap() map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := map[string]bool{}
	for _, counts := range h.online {
		for uid, n := range counts {
			if n > 0 {
				out[uid] = true
			}
		}
	}
	nowT := time.Now()
	for uid, seen := range h.authOnline {
		if nowT.Sub(seen) < 5*time.Minute {
			out[uid] = true
		}
	}
	return out
}

// DeviceCounts returns a snapshot of device counts per user.
func (h *Hub) DeviceCounts() map[string]int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := map[string]int{}
	for uid, devices := range h.devices {
		out[uid] = len(devices)
	}
	return out
}

// TrafficSnapshot is a helper for tests.
func (h *Hub) TrafficSnapshot(nodeID int64) (int64, int64) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p := h.lastMetric[nodeID]
	return p.Tx, p.Rx
}

// HubStats is a lightweight health snapshot.
type HubStats struct {
	Agents int   `json:"agents"`
	Online int   `json:"online"`
	Uptime int64 `json:"uptime"`
}

// Stats returns hub health counters.
func (h *Hub) Stats() HubStats {
	return HubStats{Agents: len(h.OnlineNodes()), Online: h.OnlineUserCount(), Uptime: int64(time.Since(h.started).Seconds())}
}

var _ = model.Node{}
