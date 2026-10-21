package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/axmishell/mikvacepanel/internal/logs"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type agentMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type helloPayload struct {
	Version     string `json:"version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Hy2Version  string `json:"hy2Version"`
	Runtime     string `json:"runtime"`
	OnlineUsers int    `json:"onlineUsers"`
}

type heartbeatPayload struct {
	CPUPct     float64 `json:"cpuPct"`
	MemUsed    int64   `json:"memUsed"`
	MemTotal   int64   `json:"memTotal"`
	DiskUsed   int64   `json:"diskUsed"`
	DiskTotal  int64   `json:"diskTotal"`
	Load1      float64 `json:"load1"`
	UptimeSec  int64   `json:"uptimeSec"`
	Online     int     `json:"online"`
	TrafficTx  int64   `json:"trafficTx"`
	TrafficRx  int64   `json:"trafficRx"`
	Hy2Running bool    `json:"hy2Running"`
}

type configAckPayload struct {
	Revision int64  `json:"revision"`
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
}

type commandAckPayload struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

// handleAgentWS authenticates and attaches a node agent to the control hub.
func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing node token")
		return
	}
	sum := sha256.Sum256([]byte(token))
	node, err := s.store.GetNodeByTokenHash(r.Context(), hex.EncodeToString(sum[:]))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid node token")
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("agent upgrade", "error", err)
		return
	}
	ac := s.hub.register(node.ID, node.Name, ws)
	defer func() {
		ac.close()
		current := s.hub.unregister(ac)
		if current {
			_ = s.store.MarkNodeOffline(context.Background(), node.ID, "offline")
			s.hub.ClearNode(node.ID)
			s.store.AddEvent(context.Background(), "node.offline", node.ID, "warning", "节点 "+node.Name+" 的 Agent 已断开")
			s.log.Info("agent disconnected", "node", node.Name, "id", node.ID)
		}
	}()

	hello := store.NodeHello{Version: "unknown", OS: "unknown", Arch: "unknown", Runtime: "process"}
	_ = s.store.MarkNodeOnline(r.Context(), node.ID, hello)
	s.store.AddEvent(r.Context(), "node.online", node.ID, "info", "节点 "+node.Name+" 的 Agent 已连接")
	s.log.Info("agent connected", "node", node.Name, "id", node.ID)

	ws.SetReadLimit(16 << 20)
	_ = ws.SetReadDeadline(time.Now().Add(90 * time.Second))
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	// Write pump.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case msg := <-ac.send:
				_ = ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
				if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
					ac.close()
					return
				}
			case <-ticker.C:
				_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
					ac.close()
					return
				}
			case <-ac.closed:
				return
			}
		}
	}()

	// Push the current (or freshly generated) configuration.
	go func(nodeID int64) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		n, err := s.store.GetNode(ctx, nodeID)
		if err != nil {
			return
		}
		s.syncNodeConfig(ctx, n, true)
	}(node.ID)

	for {
		var msg agentMessage
		if err := ws.ReadJSON(&msg); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				s.log.Debug("agent read", "node", node.Name, "error", err)
			}
			return
		}
		_ = ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		switch msg.Type {
		case "hello":
			var h helloPayload
			_ = json.Unmarshal(msg.Payload, &h)
			if h.Version == "" {
				h.Version = "unknown"
			}
			_ = s.store.MarkNodeOnline(r.Context(), node.ID, store.NodeHello{
				Version: h.Version, OS: h.OS, Arch: h.Arch, Hy2Version: h.Hy2Version,
				Runtime: h.Runtime, OnlineUsers: h.OnlineUsers,
			})
			s.log.Info("agent hello", "node", node.Name, "version", h.Version, "os", h.OS,
				"arch", h.Arch, "hy2", h.Hy2Version, "runtime", h.Runtime)
		case "heartbeat":
			s.handleHeartbeat(r.Context(), node.ID, node.Name, msg.Payload)
		case "logs":
			s.handleAgentLogs(r.Context(), node.ID, msg.Payload, ac)
		case "stats":
			s.handleAgentStats(r.Context(), node.ID, msg.Payload, ac)
		case "config.ack":
			var ack configAckPayload
			_ = json.Unmarshal(msg.Payload, &ack)
			if ack.OK {
				_ = s.store.MarkConfigApplied(r.Context(), node.ID, ack.Revision)
				_ = s.store.SetNodeAppliedRevision(r.Context(), node.ID, ack.Revision, "")
				s.log.Info("config applied", "node", node.Name, "revision", ack.Revision)
			} else {
				_ = s.store.SetNodeAppliedRevision(r.Context(), node.ID, ack.Revision, ack.Error)
				_ = s.store.SetNodeConfigError(r.Context(), node.ID, ack.Error)
				s.store.AddEvent(r.Context(), "node.config_rejected", node.ID, "error",
					"配置版本被拒绝："+ack.Error)
				s.log.Warn("config rejected", "node", node.Name, "revision", ack.Revision, "error", ack.Error)
			}
		case "command.ack":
			var ack commandAckPayload
			_ = json.Unmarshal(msg.Payload, &ack)
			if !ack.OK {
				s.store.AddEvent(r.Context(), "node.command_failed", node.ID, "warning",
					"命令 "+ack.Action+" 执行失败："+ack.Output)
			}
			s.log.Info("command ack", "node", node.Name, "action", ack.Action, "ok", ack.OK)
		case "ping":
			_ = ac.sendJSON(map[string]any{"type": "pong", "payload": map[string]any{"ts": time.Now().Unix()}})
		default:
			s.log.Debug("unknown agent message", "type", msg.Type, "node", node.Name)
		}
	}
}

func (s *Server) handleHeartbeat(ctx context.Context, nodeID int64, nodeName string, raw json.RawMessage) {
	var hb heartbeatPayload
	if err := json.Unmarshal(raw, &hb); err != nil {
		return
	}
	dtx, drx := s.hub.HeartbeatDelta(nodeID, hb.TrafficTx, hb.TrafficRx)
	if err := s.store.UpdateNodeHeartbeat(ctx, nodeID, hb.Online, dtx, drx); err != nil {
		s.log.Warn("update heartbeat", "node", nodeName, "error", err)
	}
	metric := &model.NodeMetric{
		NodeID: nodeID, TS: time.Now().UTC(), CPUPct: hb.CPUPct,
		MemUsed: hb.MemUsed, MemTotal: hb.MemTotal, DiskUsed: hb.DiskUsed, DiskTotal: hb.DiskTotal,
		Load1: hb.Load1, UptimeSec: hb.UptimeSec, Online: hb.Online,
		TrafficTx: dtx, TrafficRx: drx, Hy2Running: hb.Hy2Running,
	}
	if err := s.store.InsertMetric(ctx, metric); err != nil {
		s.log.Warn("insert metric", "node", nodeName, "error", err)
	}
}

func (s *Server) handleAgentLogs(ctx context.Context, nodeID int64, raw json.RawMessage, ac *AgentConn) {
	var batch model.LogBatch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return
	}
	if batch.BatchID == "" {
		batch.BatchID = store.RandomToken(16)
	}
	enabled := parseBoolSetting(settingOrContext(s, ctx, "log_ingest_enabled", "true"), true)
	if !enabled {
		_ = ac.sendJSON(map[string]any{"type": "log.ack", "payload": map[string]any{"batchId": batch.BatchID}})
		return
	}
	entries := make([]model.LogEntry, 0, len(batch.Entries))
	nowT := time.Now().UTC()
	for i, in := range batch.Entries {
		if i >= 1000 {
			break
		}
		fallback := in.TS
		if fallback.IsZero() {
			fallback = nowT
		}
		entry := logs.Parse(in.Raw, fallback)
		entry.NodeID = nodeID
		if entry.Event == "stats" {
			// Internal StatsService probes are noisy and not operator relevant.
			continue
		}
		if entry.UserID != "" && (entry.Event == "connect" || entry.Event == "auth_success") {
			// Immediate feedback for the dashboard; the authoritative online
			// counters arrive with the traffic stats batch.
			s.hub.MarkAuthOnline(entry.UserID)
		}
		entries = append(entries, entry)
	}
	_, err := s.store.InsertLogBatch(ctx, nodeID, batch.BatchID, entries)
	if err != nil {
		s.log.Warn("store logs", "nodeId", nodeID, "error", err)
		return
	}
	_ = ac.sendJSON(map[string]any{"type": "log.ack", "payload": map[string]any{"batchId": batch.BatchID, "count": len(entries)}})
}

func (s *Server) handleAgentStats(ctx context.Context, nodeID int64, raw json.RawMessage, ac *AgentConn) {
	var batch model.StatsBatch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return
	}
	if batch.BatchID == "" {
		batch.BatchID = store.RandomToken(16)
	}
	if len(batch.Users) > 20000 {
		return
	}
	if _, err := s.store.ApplyStatsBatch(ctx, nodeID, batch.BatchID, batch.Users); err != nil {
		s.log.Warn("apply stats", "nodeId", nodeID, "error", err)
		return
	}
	if batch.Online != nil {
		s.hub.UpdateNodeOnline(nodeID, batch.Online)
	}
	_ = ac.sendJSON(map[string]any{"type": "stats.ack", "payload": map[string]any{"batchId": batch.BatchID}})
}

func settingOrContext(s *Server, ctx context.Context, key, def string) string {
	v, err := s.store.GetSetting(ctx, key)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
