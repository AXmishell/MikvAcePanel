package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/idgen"
)

// The administrator security entrance is mandatory. The panel never serves a
// usable /admin console without either the secret URL segment, the gate cookie
// issued by visiting that segment, or an explicit X-Admin-Entry header (for
// API clients and split frontend deployments).
const (
	adminEntryCookieName = "mikvace_admin_gate"
	adminEntryCookieKind = "admin-gate:"
	adminEntryFileName   = "admin-entry.txt"
)

var adminSecurityPathPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)

var adminSecurityReservedPaths = map[string]bool{
	"admin": true, "api": true, "assets": true, "panel": true, "user": true,
	"login": true, "logout": true, "dashboard": true, "users": true,
	"nodes": true, "groups": true, "logs": true, "settings": true,
}

func validAdminSecurityPath(p string) bool {
	return adminSecurityPathPattern.MatchString(p) && !adminSecurityReservedPaths[strings.ToLower(p)]
}

// ensureAdminSecurityEntry runs on every boot. It generates a secret entrance
// when none exists and re-enables the entrance for installations that still
// have the legacy disabled value, so an upgrade cannot leave /admin public.
func (s *Server) ensureAdminSecurityEntry(ctx context.Context) error {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	entrance := strings.TrimSpace(settings["admin_security_path"])
	generated := false
	if !validAdminSecurityPath(entrance) {
		entrance = idgen.Token(8)
		generated = true
	}
	values := map[string]string{
		"admin_security_path":    entrance,
		"admin_security_enabled": "true",
	}
	if err := s.store.UpdateSettings(ctx, values); err != nil {
		return err
	}
	s.writeAdminEntryFile(entrance)
	file := filepath.Join(s.cfg.DataDir, adminEntryFileName)
	if generated {
		// Do not print the secret to logs. The installer response and the
		// 0600 recovery file are the supported discovery paths.
		s.log.Warn("administrator security entrance generated", "file", file)
	} else {
		s.log.Info("administrator security entrance enabled", "file", file)
	}
	return nil
}

// writeAdminEntryFile keeps a recovery copy of the entrance on the data
// volume. It is the only supported way to recover a lost entrance without
// reading the database.
func (s *Server) writeAdminEntryFile(entrance string) {
	dir := strings.TrimSpace(s.cfg.DataDir)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.log.Warn("write administrator entrance file", "error", err)
		return
	}
	content := fmt.Sprintf(`MikvAce Panel 管理员安全入口

    /admin/%s

请在浏览器中访问一次该地址。面板会签发 HttpOnly 门禁 Cookie，之后
/admin、/admin/login 和管理 API 在当前浏览器可用；没有门禁口令时
普通 /admin 地址一律返回 404。

请保持本文件权限为 0600。如入口泄露，请在“设置 -> 安全”中重新生成。
`, entrance)
	target := filepath.Join(dir, adminEntryFileName)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		s.log.Warn("write administrator entrance file", "error", err)
		return
	}
	if err := os.Rename(tmp, target); err != nil {
		s.log.Warn("write administrator entrance file", "error", err)
	}
}

// adminSecurityEntry returns the mandatory entrance secret. An empty path is a
// misconfiguration and fails closed: every admin request is denied.
func (s *Server) adminSecurityEntry(ctx context.Context) (bool, string) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return true, ""
	}
	entrance := strings.TrimSpace(settings["admin_security_path"])
	if !validAdminSecurityPath(entrance) {
		return true, ""
	}
	return true, entrance
}

func (s *Server) adminGateValue(entrance string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(adminEntryCookieKind + entrance))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) adminGateCookieValid(r *http.Request, entrance string) bool {
	if entrance == "" {
		return false
	}
	cookie, err := r.Cookie(adminEntryCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	expected := s.adminGateValue(entrance)
	return hmac.Equal([]byte(cookie.Value), []byte(expected))
}

func (s *Server) adminEntryHeaderValid(r *http.Request, entrance string) bool {
	if entrance == "" {
		return false
	}
	got := strings.TrimSpace(r.Header.Get("X-Admin-Entry"))
	return subtle.ConstantTimeCompare([]byte(got), []byte(entrance)) == 1
}

// setAdminGateCookie issues the gate token after a browser reaches the secret
// entrance. HttpOnly keeps it away from JavaScript; the SPA still sends the
// explicit X-Admin-Entry header for split-origin API deployments.
func (s *Server) setAdminGateCookie(w http.ResponseWriter, r *http.Request, entrance string) {
	if entrance == "" {
		return
	}
	ttl := s.cfg.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	cookie := &http.Cookie{
		Name:     adminEntryCookieName,
		Value:    s.adminGateValue(entrance),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
		Expires:  time.Now().Add(ttl),
	}
	if requestIsHTTPS(r) {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (s *Server) adminGateActive(r *http.Request) bool {
	_, entrance := s.adminSecurityEntry(r.Context())
	return s.adminGateCookieValid(r, entrance)
}

// adminEntryAllowed is used by the login endpoints and the administrator API.
// API keys are exempt at the call site, by design.
func (s *Server) adminEntryAllowed(r *http.Request) bool {
	_, entrance := s.adminSecurityEntry(r.Context())
	if entrance == "" {
		return false
	}
	return s.adminEntryHeaderValid(r, entrance) || s.adminGateCookieValid(r, entrance)
}

// normalizeAdminPath collapses duplicate separators, dot segments and matrix
// parameters before matching. This keeps //admin and /admin;foo from reaching
// the SPA fallback as an apparent /admin route.
func normalizeAdminPath(p string) string {
	if p == "" {
		return "/"
	}
	parts := strings.Split(p, "/")
	for i, part := range parts {
		if idx := strings.IndexByte(part, ';'); idx >= 0 {
			parts[i] = part[:idx]
		}
	}
	return path.Clean(strings.Join(parts, "/"))
}

// redactAdminEntrancePath keeps the secret entrance out of access logs while
// retaining enough of the path to debug routing.
func redactAdminEntrancePath(p string) string {
	segment, isAdmin := adminUIPathSegment(p)
	if !isAdmin || segment == "" {
		return p
	}
	normalized := normalizeAdminPath(p)
	lower := strings.ToLower(normalized)
	idx := strings.Index(lower, "/admin/")
	if idx < 0 {
		return normalized
	}
	start := idx + len("/admin/")
	end := strings.IndexByte(normalized[start:], '/')
	if end < 0 {
		return normalized[:start] + "<entrance>"
	}
	return normalized[:start] + "<entrance>" + normalized[start+end:]
}

// adminUIPathSegment returns the first path segment after /admin. The prefix is
// matched case-insensitively so /Admin and /ADMIN cannot fall through to the
// SPA fallback and bypass the entrance check.
func adminUIPathSegment(p string) (string, bool) {
	p = normalizeAdminPath(p)
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

// adminUIPathAllowed decides whether an /admin SPA route may be served. The
// secret segment is the entrance; a valid gate cookie (issued by visiting that
// segment) or an explicit header also allows the plain /admin console.
func (s *Server) adminUIPathAllowed(r *http.Request) bool {
	_, entrance := s.adminSecurityEntry(r.Context())
	if entrance == "" {
		return false
	}
	segment, isAdmin := adminUIPathSegment(r.URL.Path)
	if !isAdmin {
		return true
	}
	if subtle.ConstantTimeCompare([]byte(segment), []byte(entrance)) == 1 {
		return true
	}
	return s.adminEntryHeaderValid(r, entrance) || s.adminGateCookieValid(r, entrance)
}

// requireAdminUIEntrance enforces the entrance on every request that looks like
// an /admin route. Reaching the secret segment mints the gate cookie.
func (s *Server) requireAdminUIEntrance(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		segment, isAdmin := adminUIPathSegment(r.URL.Path)
		if !isAdmin {
			next.ServeHTTP(w, r)
			return
		}
		_, entrance := s.adminSecurityEntry(r.Context())
		if entrance == "" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if subtle.ConstantTimeCompare([]byte(segment), []byte(entrance)) == 1 {
			s.setAdminGateCookie(w, r, entrance)
			next.ServeHTTP(w, r)
			return
		}
		if s.adminEntryHeaderValid(r, entrance) || s.adminGateCookieValid(r, entrance) {
			next.ServeHTTP(w, r)
			return
		}
		// Return 404 rather than 401 so a scanner cannot tell whether the
		// path exists behind a different entrance.
		writeError(w, http.StatusNotFound, "not found")
	})
}

func (s *Server) serveAdminSPA(w http.ResponseWriter, r *http.Request) {
	if !s.adminUIPathAllowed(r) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.serveSPA(w, r)
}
