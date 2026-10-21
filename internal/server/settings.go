package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/version"
)

var editableSettings = map[string]bool{
	"panel_name": true, "public_url": true, "logo_url": true, "page_title": true,
	"page_note": true, "support_url": true, "web_page_url": true,
	"subscription_title": true, "subscription_update_interval": true,
	"subscription_buttons": true, "device_grace_minutes": true,
	"happ_provider_id": true, "happ_routing_mode": true, "happ_routing_profile": true,
	"happ_announce":      true,
	"log_retention_days": true, "log_ingest_enabled": true,
	"analysis_window_hours": true, "alert_auth_fail_threshold": true,
	"alert_error_burst_threshold": true,
	"backup_enabled":              true, "backup_interval_hours": true, "backup_keep": true,
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": settings,
		"version":  version.String(),
		"platform": version.Platform(),
		"dataDir":  s.cfg.DataDir,
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	values := map[string]string{}
	for key, rv := range raw {
		if !editableSettings[key] {
			continue
		}
		var v any
		if err := json.Unmarshal(rv, &v); err != nil {
			continue
		}
		switch x := v.(type) {
		case string:
			values[key] = strings.TrimSpace(x)
		case bool:
			values[key] = strconv.FormatBool(x)
		case float64:
			values[key] = strconv.FormatFloat(x, 'f', -1, 64)
		case nil:
			values[key] = ""
		default:
			b, _ := json.Marshal(x)
			values[key] = string(b)
		}
	}
	if v, ok := values["public_url"]; ok {
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v != "" {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				writeError(w, http.StatusBadRequest, "public URL must be a full http(s) URL")
				return
			}
		}
		values["public_url"] = v
	}
	for _, key := range []string{"subscription_update_interval", "device_grace_minutes", "log_retention_days",
		"analysis_window_hours", "alert_auth_fail_threshold", "alert_error_burst_threshold",
		"backup_interval_hours", "backup_keep"} {
		if v, ok := values[key]; ok {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 100000 {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("%s must be a positive number", key))
				return
			}
		}
	}
	if v, ok := values["subscription_buttons"]; ok && strings.TrimSpace(v) != "" {
		var items []map[string]any
		if err := json.Unmarshal([]byte(v), &items); err != nil {
			writeError(w, http.StatusBadRequest, "subscription buttons must be a JSON array")
			return
		}
	}
	oldURL, _ := s.store.GetSetting(r.Context(), "public_url")
	if err := s.store.UpdateSettings(r.Context(), values); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if v, ok := values["public_url"]; ok && v != oldURL {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			s.syncAllNodes(ctx)
		}()
	}
	settings, _ := s.store.GetSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": settings})
}

func (s *Server) syncAllNodes(ctx context.Context) (total, pushed int) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return 0, 0
	}
	for i := range nodes {
		_, p := s.syncNodeConfig(ctx, &nodes[i], false)
		if p {
			pushed++
		}
	}
	return len(nodes), pushed
}
