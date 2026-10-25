package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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
	"log_retention_days": true, "log_ingest_enabled": true,
	"analysis_window_hours": true, "alert_auth_fail_threshold": true,
	"alert_error_burst_threshold": true,
	"backup_enabled":              true, "backup_interval_hours": true, "backup_keep": true,
	"portal_enabled": true, "portal_title": true, "portal_note": true,
	"portal_allow_password_change": true,
	"portal_allow_sub_reset":       true, "portal_session_hours": true,
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
		"analysis_window_hours", "alert_auth_fail_threshold", "alert_error_burst_threshold",
		"backup_interval_hours", "backup_keep", "portal_session_hours"} {
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
	if v, ok := values["admin_security_path"]; ok {
		v = strings.TrimSpace(v)
		if v != "" && !validAdminSecurityPath(v) {
			writeError(w, http.StatusBadRequest, "security entry must be 4-64 characters using letters, numbers, - or _")
			return
		}
		values["admin_security_path"] = v
	}
	securityEnabled := parseBoolSetting(current["admin_security_enabled"], false)
	securityPath := strings.TrimSpace(current["admin_security_path"])
	if v, ok := values["admin_security_enabled"]; ok {
		securityEnabled = parseBoolSetting(v, false)
	}
	if v, ok := values["admin_security_path"]; ok {
		securityPath = v
	}
	if securityEnabled && securityPath == "" {
		writeError(w, http.StatusBadRequest, "a security entry path is required when the entrance is enabled")
		return
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

// The administrator security entrance is an optional secret URL segment
// (for example /admin/a1b2c3d4/login). It is disabled by default. When it is
// enabled the admin API also requires the X-Admin-Entry header so guessing
// /admin alone does not expose a usable login endpoint.
var adminSecurityPathPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)

var adminSecurityReservedPaths = map[string]bool{
	"admin": true, "api": true, "assets": true, "panel": true, "user": true,
	"login": true, "logout": true, "dashboard": true, "users": true,
	"nodes": true, "groups": true, "logs": true, "settings": true,
}

func validAdminSecurityPath(p string) bool {
	return adminSecurityPathPattern.MatchString(p) && !adminSecurityReservedPaths[strings.ToLower(p)]
}

func (s *Server) adminSecurityEntry(ctx context.Context) (bool, string) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		// Fail closed: when the entrance configuration cannot be read we
		// cannot prove the admin area is public, so keep it hidden.
		return true, ""
	}
	if !parseBoolSetting(settings["admin_security_enabled"], false) {
		return false, ""
	}
	// An enabled entrance with an empty path is a misconfiguration. Treat it
	// as enabled (and deny every path) instead of silently falling back to
	// the public /admin console.
	return true, strings.TrimSpace(settings["admin_security_path"])
}

func (s *Server) adminEntryAllowed(r *http.Request) bool {
	enabled, path := s.adminSecurityEntry(r.Context())
	if !enabled {
		return true
	}
	if path == "" {
		return false
	}
	got := strings.TrimSpace(r.Header.Get("X-Admin-Entry"))
	return subtle.ConstantTimeCompare([]byte(got), []byte(path)) == 1
}

func (s *Server) requireAdminEntry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.adminEntryAllowed(r) {
			// Return 404 rather than 401 so a scanner cannot tell whether the
			// path exists behind a different entrance.
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// adminUIPathSegment returns the first path segment after /admin. The prefix is
// matched case-insensitively so /Admin and /ADMIN cannot fall through to the
// SPA fallback and bypass the entrance check.
func adminUIPathSegment(p string) (string, bool) {
	const prefix = "/admin"
	if len(p) < len(prefix) || !strings.EqualFold(p[:len(prefix)], prefix) {
		return "", false
	}
	rest := p[len(prefix):]
	if rest == "" {
		return "", true
	}
	if rest[0] != '/' {
		// /administrator and similar paths are not the admin console.
		return "", false
	}
	return strings.SplitN(rest[1:], "/", 2)[0], true
}

// adminUIPathAllowed decides whether an /admin SPA route may be served. When
// the entrance is enabled the first path segment must match the configured
// secret, so /admin and /admin/login also return 404 at the HTTP layer instead
// of exposing the login shell.
func (s *Server) adminUIPathAllowed(r *http.Request) bool {
	enabled, path := s.adminSecurityEntry(r.Context())
	if !enabled {
		return true
	}
	if path == "" {
		return false
	}
	segment, isAdmin := adminUIPathSegment(r.URL.Path)
	if !isAdmin {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(segment), []byte(path)) == 1
}

// requireAdminUIEntrance enforces the optional entrance on every request that
// looks like an /admin route, including case variants that chi's case-sensitive
// routes would otherwise send to the SPA fallback.
func (s *Server) requireAdminUIEntrance(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, isAdmin := adminUIPathSegment(r.URL.Path); isAdmin && !s.adminUIPathAllowed(r) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveAdminSPA(w http.ResponseWriter, r *http.Request) {
	if !s.adminUIPathAllowed(r) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.serveSPA(w, r)
}

func (s *Server) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	enabled, _ := s.adminSecurityEntry(r.Context())
	settings, _ := s.store.GetSettings(r.Context())
	installed := true
	if n, err := s.store.CountAdmins(r.Context()); err == nil {
		installed = n > 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"securityEntryEnabled": enabled,
		"panelName":            firstNonEmpty(settings["panel_name"], "MikvAce Panel"),
		"installed":            installed,
	})
}
