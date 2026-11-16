package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/version"
)

var editableSettings = map[string]bool{
	"panel_name": true, "public_url": true, "logo_url": true, "page_title": true,
	"page_note": true, "support_url": true, "web_page_url": true,
	"subscription_title": true, "subscription_update_interval": true,
	"subscription_buttons": true, "subscription_hy2_endpoints": true, "device_grace_minutes": true,
	"happ_provider_id": true, "happ_routing_mode": true, "happ_routing_profile": true,
	"happ_announce":      true,
	"log_retention_days": true, "metric_retention_days": true, "log_ingest_enabled": true,
	"analysis_window_hours": true, "alert_auth_fail_threshold": true,
	"alert_error_burst_threshold": true,
	"backup_enabled":              true, "backup_interval_hours": true, "backup_keep": true,
	"admin_security_enabled": true, "admin_security_path": true,
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": redactSettings(settings),
		"version":  version.String(),
		"platform": version.Platform(),
		"dataDir":  s.cfg.DataDir,
	})
}

// redactSettings removes write-only secrets before settings are returned to a
// client. The credentials remain available through their dedicated endpoints,
// which report only whether they are configured.
func redactSettings(settings map[string]string) map[string]string {
	for _, key := range []string{"cert_dns_config", "cert_acme_eab_hmac"} {
		if token := strings.TrimSpace(settings[key]); token != "" {
			settings[key+"_set"] = "true"
		}
		delete(settings, key)
	}
	return settings
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	current, _ := s.store.GetSettings(r.Context())
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
		"metric_retention_days", "analysis_window_hours", "alert_auth_fail_threshold", "alert_error_burst_threshold",
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
	if v, ok := values["subscription_hy2_endpoints"]; ok {
		v = strings.ToLower(strings.TrimSpace(v))
		switch v {
		case "", "both":
			v = "both"
		case "tls", "hopping":
		default:
			writeError(w, http.StatusBadRequest, "subscription Hy2 endpoints must be both, tls or hopping")
			return
		}
		values["subscription_hy2_endpoints"] = v
	}
	securityPathChanged := false
	if v, ok := values["admin_security_path"]; ok {
		v = strings.TrimSpace(v)
		if !validAdminSecurityPath(v) {
			writeError(w, http.StatusBadRequest, "security entry must be 4-64 characters using letters, numbers, - or _, and must not be a reserved word")
			return
		}
		securityPathChanged = v != strings.TrimSpace(current["admin_security_path"])
		values["admin_security_path"] = v
	} else if !validAdminSecurityPath(strings.TrimSpace(current["admin_security_path"])) {
		writeError(w, http.StatusBadRequest, "a security entry path is required")
		return
	}
	if v, ok := values["admin_security_enabled"]; ok && !parseBoolSetting(v, true) {
		writeError(w, http.StatusBadRequest, "the administrator security entrance is mandatory and cannot be disabled")
		return
	}
	// Keep the legacy column as a mirror of the mandatory state so older
	// tooling and backups do not report the entrance as disabled.
	values["admin_security_enabled"] = "true"
	oldURL, _ := s.store.GetSetting(r.Context(), "public_url")
	if err := s.store.UpdateSettings(r.Context(), values); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if securityPathChanged {
		s.writeAdminEntryFile(values["admin_security_path"])
		// Rotate the gate in the same response so the current browser keeps
		// working without a full reload after changing the entrance.
		s.setAdminGateCookie(w, r, values["admin_security_path"])
		s.log.Info("administrator security entrance rotated",
			"file", filepath.Join(s.cfg.DataDir, adminEntryFileName))
	}
	if v, ok := values["public_url"]; ok && v != oldURL {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			s.syncAllNodes(ctx)
		}()
	}
	settings, _ := s.store.GetSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": redactSettings(settings)})
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

func (s *Server) requireAdminEntry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.adminEntryAllowed(r) {
			// Return 404 rather than 401 so a scanner cannot tell whether the
			// login endpoint exists behind a different entrance.
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	// The entrance is mandatory; only the gate status is public. The secret
	// itself is never returned by this probe.
	settings, _ := s.store.GetSettings(r.Context())
	installed := true
	if n, err := s.store.CountAdmins(r.Context()); err == nil {
		installed = n > 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"securityEntryEnabled": true,
		"gateActive":           s.adminGateActive(r),
		"panelName":            firstNonEmpty(settings["panel_name"], "MikvAce Panel"),
		"installed":            installed,
	})
}
