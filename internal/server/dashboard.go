package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/axmishell/mikvacepanel/internal/logs"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/version"
)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	usersTotal, usersActive, err := s.store.CountUsers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	expired, _ := s.store.CountExpiredUsers(ctx)
	nodesTotal, nodesActive, _ := s.store.CountNodes(ctx)
	usersTx, usersRx, _ := s.store.SumUserTraffic(ctx)
	nowT := time.Now().UTC()
	todayTx, todayRx, _ := s.store.TrafficSince(ctx, startOfDay(nowT), 0)
	series, _ := s.store.TrafficSeries(ctx, nowT.Add(-24*time.Hour), 0, false)
	latest, _ := s.store.LatestMetrics(ctx)
	nodes, _ := s.store.ListNodes(ctx)
	onlineMap := s.hub.UserOnlineMap()
	devices := s.hub.DeviceCounts()

	nodeItems := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		port := n.Port
		if n.HasCore("hysteria") {
			port = n.EffectiveListenPort()
		}
		item := map[string]any{
			"id": n.ID, "name": n.Name, "flag": n.Flag, "status": n.Status,
			"active": n.Active, "onlineUsers": n.OnlineUsers, "maxOnline": n.MaxOnline,
			"trafficTx": n.TrafficTx, "trafficRx": n.TrafficRx,
			"configRevision": n.ConfigRevision, "appliedRevision": n.AppliedRevision,
			"configError": n.ConfigError, "lastSeenAt": n.LastSeenAt,
			"agentVersion": n.AgentVersion, "os": n.OS, "arch": n.Arch,
			"hy2Version": n.Hy2Version, "runtime": n.Runtime, "connected": s.hub.Connected(n.ID),
			"domain": n.Domain, "ip": n.IP, "port": port,
		}
		if m, ok := latest[n.ID]; ok {
			item["metrics"] = m
		}
		nodeItems = append(nodeItems, item)
	}
	recent, _ := s.store.RecentLogs(ctx, 0, 8)
	events, _ := s.store.ListEvents(ctx, 0, 10)

	settings, _ := s.store.GetSettings(ctx)
	summary, _ := s.analyzer.Analyze(ctx, model.LogStatsOptions{From: nowT.Add(-24 * time.Hour), To: nowT},
		s.hub.OnlineUserCount(), logs.Thresholds{
			AuthFailPerIP:   int64(parseIntSetting(settings["alert_auth_fail_threshold"], 10)),
			ErrorBurstCount: int64(parseIntSetting(settings["alert_error_burst_threshold"], 20)),
		})
	findings := []model.Finding{}
	if summary != nil {
		findings = summary.Findings
		if len(findings) > 5 {
			findings = findings[:5]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stats": map[string]any{
			"usersTotal": usersTotal, "usersActive": usersActive, "usersExpired": expired,
			"usersOnline": len(onlineMap), "devicesOnline": sumDeviceCounts(devices),
			"nodesTotal": nodesTotal, "nodesActive": nodesActive,
			"nodesOnline": s.countOnlineNodes(nodes),
			"trafficTx":   usersTx, "trafficRx": usersRx,
			"todayTx": todayTx, "todayRx": todayRx,
		},
		"nodes":         nodeItems,
		"trafficSeries": series,
		"recentLogs":    recent,
		"events":        events,
		"findings":      findings,
		"analysis":      summary,
		"system": map[string]any{
			"version": version.String(), "platform": version.Platform(),
			"uptime": int64(time.Since(s.started).Seconds()), "hub": s.hub.Stats(),
			"now": nowT,
		},
	})
}

func (s *Server) countOnlineNodes(nodes []model.Node) int {
	n := 0
	for _, node := range nodes {
		if s.hub.Connected(node.ID) {
			n++
		}
	}
	return n
}

func sumDeviceCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	nodeID, _ := strconv.ParseInt(r.URL.Query().Get("nodeId"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.store.ListEvents(r.Context(), nodeID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events})
}
