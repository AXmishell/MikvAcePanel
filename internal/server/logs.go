package server

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/logs"
	"github.com/axmishell/mikvacepanel/internal/model"
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

// handleExportLogs streams the current log filter as a downloadable file.
// Supported formats: log (one structured line per entry), csv and jsonl.
func (s *Server) handleExportLogs(w http.ResponseWriter, r *http.Request) {
	f := logFilterFromRequest(r)
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

	total, err := s.store.CountLogsFiltered(r.Context(), f)
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

	_, err = s.store.StreamLogs(r.Context(), f, maxLogExportRows, func(e model.LogEntry) error {
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
