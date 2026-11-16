package server

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/logs"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

const maxLogExportRows = 100000

func logFilterFromRequest(r *http.Request) model.LogFilter {
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
	return f
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	f := logFilterFromRequest(r)
	if nodeIDs, restricted := s.apiKeyAllowedNodes(r); restricted {
		if f.NodeID > 0 {
			if !containsID(nodeIDs, f.NodeID) {
				writeError(w, http.StatusForbidden, "API key is restricted to other nodes")
				return
			}
		} else {
			f.NodeIDs = nodeIDs
		}
	}
	items, total, err := s.logs.QueryLogs(r.Context(), f)
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
	opts := model.LogStatsOptions{NodeID: nodeID, From: from, To: to}
	if nodeIDs, restricted := s.apiKeyAllowedNodes(r); restricted {
		if nodeID > 0 {
			if !containsID(nodeIDs, nodeID) {
				writeError(w, http.StatusForbidden, "API key is restricted to other nodes")
				return
			}
		} else {
			opts.NodeIDs = nodeIDs
		}
	}
	settings, _ := s.store.GetSettings(r.Context())
	th := logs.Thresholds{
		AuthFailPerIP:   int64(parseIntSetting(settings["alert_auth_fail_threshold"], 10)),
		ErrorBurstCount: int64(parseIntSetting(settings["alert_error_burst_threshold"], 20)),
	}
	// The range and node scope are part of the key so a restricted API key can
	// never receive a cached summary computed for another scope.
	online := s.hub.OnlineUserCount()
	if opts.NodeIDs != nil {
		// The global online counter cannot be node-filtered, so hide it from
		// resource-restricted keys instead of leaking unrelated activity.
		online = 0
	}
	key := fmt.Sprintf("logs|%s|%s|%s|%d|%s|%d|%d", q.Get("range"), q.Get("from"), q.Get("to"),
		nodeID, nodeScopeKey(opts.NodeIDs), th.AuthFailPerIP, th.ErrorBurstCount)
	summary, err := s.analyzeCached(r.Context(), key, logSummaryAnalysisTTL, opts, online, th)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleLogOptions(w http.ResponseWriter, r *http.Request) {
	nodes, _ := s.store.ListNodes(r.Context())
	allowedNodes, restricted := s.apiKeyAllowedNodes(r)
	allowed := map[int64]bool{}
	for _, id := range allowedNodes {
		allowed[id] = true
	}
	nodeOpts := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		if restricted && !allowed[n.ID] {
			continue
		}
		nodeOpts = append(nodeOpts, map[string]any{"id": n.ID, "name": n.Name, "flag": n.Flag, "status": n.Status})
	}
	total, _ := s.logs.CountLogs(r.Context())
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
	n, err := s.logs.DeleteLogsBefore(r.Context(), before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"deleted": n}
	if n > 0 && s.localLogs() {
		// Clearing rows does not shrink the SQLite file: their pages stay on the
		// freelist. Reclaim them right away so the reported database size drops
		// without waiting for the hourly maintenance pass. PostgreSQL manages
		// its own storage, so this only runs for the embedded backend.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
		defer cancel()
		resp["reclaimedPages"], resp["freeBytes"], resp["maintenanceError"] = s.reclaimAfterDelete(ctx)
	}
	writeJSON(w, http.StatusOK, resp)
}

// localLogs reports whether the telemetry backend shares the SQLite file with
// the control database.
func (s *Server) localLogs() bool {
	return s.logs == store.LogStore(s.store)
}

// reclaimAfterDelete runs a maintenance pass and flattens the result for JSON
// responses. A failed pass is reported, not fatal: the rows are already gone.
func (s *Server) reclaimAfterDelete(ctx context.Context) (int64, int64, string) {
	result, err := s.store.Maintain(ctx)
	if err != nil {
		stats, statErr := s.store.StorageStats(ctx)
		if statErr != nil {
			return 0, 0, err.Error()
		}
		return 0, stats.FreeBytes, err.Error()
	}
	return result.ReclaimedPages, result.FreeBytes, ""
}

// handleExportLogs streams the current log filter as a downloadable file.
// Supported formats: log (one structured line per entry), csv and jsonl.
func (s *Server) handleExportLogs(w http.ResponseWriter, r *http.Request) {
	f := logFilterFromRequest(r)
	if nodeIDs, restricted := s.apiKeyAllowedNodes(r); restricted {
		if f.NodeID > 0 {
			if !containsID(nodeIDs, f.NodeID) {
				writeError(w, http.StatusForbidden, "API key is restricted to other nodes")
				return
			}
		} else {
			f.NodeIDs = nodeIDs
		}
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "log"
	}
	var (
		contentType string
		ext         string
	)
	switch format {
	case "log", "txt":
		format, contentType, ext = "log", "text/plain; charset=utf-8", "log"
	case "csv":
		contentType, ext = "text/csv; charset=utf-8", "csv"
	case "jsonl", "json":
		format, contentType, ext = "jsonl", "application/x-ndjson", "jsonl"
	default:
		writeError(w, http.StatusBadRequest, "format must be log, csv or jsonl")
		return
	}

	total, err := s.logs.CountLogsFiltered(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filename := fmt.Sprintf("mikvace-logs-%s.%s", time.Now().UTC().Format("20060102-150405"), ext)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Log-Export-Limit", strconv.Itoa(maxLogExportRows))
	w.Header().Set("X-Log-Export-Truncated", strconv.FormatBool(total > maxLogExportRows))
	w.WriteHeader(http.StatusOK)

	bw := bufio.NewWriterSize(w, 32*1024)
	defer bw.Flush()
	var (
		cw      *csv.Writer
		enc     *json.Encoder
		written int
	)
	switch format {
	case "csv":
		cw = csv.NewWriter(bw)
		_ = cw.Write([]string{
			"time", "nodeId", "nodeName", "level", "event", "userId", "clientIp",
			"host", "port", "bytesTx", "bytesRx", "durationMs", "error", "message", "raw",
		})
	case "jsonl":
		enc = json.NewEncoder(bw)
	}

	_, err = s.logs.StreamLogs(r.Context(), f, maxLogExportRows, func(e model.LogEntry) error {
		written++
		ts := e.TS.Format(time.RFC3339Nano)
		switch format {
		case "csv":
			return cw.Write([]string{
				ts, strconv.FormatInt(e.NodeID, 10), e.NodeName, e.Level, e.Event, e.UserID, e.ClientIP,
				e.Host, strconv.Itoa(e.Port), strconv.FormatInt(e.BytesTx, 10), strconv.FormatInt(e.BytesRx, 10),
				strconv.FormatInt(e.DurationMS, 10), e.Error, e.Message, e.Raw,
			})
		case "jsonl":
			return enc.Encode(e)
		default:
			_, err := fmt.Fprintf(bw, "%s %-5s node=%s(%d) event=%s user=%s ip=%s host=%s msg=%s",
				ts, e.Level, oneLine(e.NodeName), e.NodeID, e.Event, e.UserID, e.ClientIP, e.Host, oneLine(e.Message))
			if err != nil {
				return err
			}
			if e.Error != "" {
				_, err = fmt.Fprintf(bw, " error=%s", oneLine(e.Error))
			}
			if err != nil {
				return err
			}
			_, err = bw.WriteString("\n")
			return err
		}
	})
	if cw != nil {
		cw.Flush()
		if err == nil {
			err = cw.Error()
		}
	}
	if err != nil {
		s.log.Debug("log export interrupted", "written", written, "error", err)
	}
}

func oneLine(v string) string {
	v = strings.ReplaceAll(v, "\r", " ")
	return strings.ReplaceAll(v, "\n", " ")
}
