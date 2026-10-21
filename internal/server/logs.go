package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/logs"
	"github.com/axmishell/mikvacepanel/internal/model"
)

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if size == 0 {
		size, _ = strconv.Atoi(q.Get("size"))
	}
	nodeID, _ := strconv.ParseInt(q.Get("nodeId"), 10, 64)
	f := model.LogFilter{
		NodeID: nodeID, Level: strings.ToUpper(q.Get("level")), Event: q.Get("event"),
		Query: q.Get("q"), Page: page, Size: size,
	}
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = t.UTC()
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = t.UTC()
		}
	}
	items, total, err := s.store.QueryLogs(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Size <= 0 {
		f.Size = 50
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": f.Page, "pageSize": f.Size,
	})
}

func (s *Server) handleLogSummary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := logs.ParseRange(q.Get("range"), time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nodeID, _ := strconv.ParseInt(q.Get("nodeId"), 10, 64)
	settings, _ := s.store.GetSettings(r.Context())
	th := logs.Thresholds{
		AuthFailPerIP:   int64(parseIntSetting(settings["alert_auth_fail_threshold"], 10)),
		ErrorBurstCount: int64(parseIntSetting(settings["alert_error_burst_threshold"], 20)),
	}
	summary, err := s.analyzer.Analyze(r.Context(), model.LogStatsOptions{NodeID: nodeID, From: from, To: to},
		s.hub.OnlineUserCount(), th)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleLogOptions(w http.ResponseWriter, r *http.Request) {
	nodes, _ := s.store.ListNodes(r.Context())
	nodeOpts := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		nodeOpts = append(nodeOpts, map[string]any{"id": n.ID, "name": n.Name, "flag": n.Flag, "status": n.Status})
	}
	total, _ := s.store.CountLogs(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":  nodeOpts,
		"levels": []string{"DEBUG", "INFO", "WARN", "ERROR", "FATAL"},
		"events": []map[string]string{
			{"value": "auth_success", "label": "Auth success"},
			{"value": "auth_fail", "label": "Auth failure"},
			{"value": "connect", "label": "Client connected"},
			{"value": "disconnect", "label": "Client disconnected"},
			{"value": "start", "label": "Server start"},
			{"value": "shutdown", "label": "Server shutdown"},
			{"value": "error", "label": "Error"},
			{"value": "warning", "label": "Warning"},
			{"value": "network_error", "label": "Network error"},
			{"value": "tls", "label": "TLS / QUIC"},
			{"value": "acme", "label": "ACME / certificate"},
			{"value": "stats", "label": "Traffic stats"},
			{"value": "log", "label": "Other"},
		},
		"totalLogs": total,
	})
}

func (s *Server) handleDeleteLogs(w http.ResponseWriter, r *http.Request) {
	before := time.Time{}
	if v := r.URL.Query().Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = t
		}
	}
	if before.IsZero() {
		before = time.Now().UTC()
	}
	n, err := s.store.DeleteLogsBefore(r.Context(), before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
