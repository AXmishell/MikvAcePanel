package server

import (
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/axmishell/mikvacepanel/internal/version"
)

// minInstallPasswordLength mirrors the administrator password policy used by
// the account settings page.
const minInstallPasswordLength = 12

// installStatus is public. It only reveals whether first-run setup is pending,
// which is already obvious from the /install page being reachable.
type installStatus struct {
	Installed  bool   `json:"installed"`
	NeedsSetup bool   `json:"needsSetup"`
	PanelName  string `json:"panelName"`
	PublicURL  string `json:"publicUrl"`
	Driver     string `json:"database"`
	Version    string `json:"version"`
}

func (s *Server) installStatus(r *http.Request) installStatus {
	status := installStatus{
		Installed: true,
		PanelName: "MikvAce Panel",
		Driver:    s.store.Dialect().Name(),
		Version:   version.String(),
	}
	if n, err := s.store.CountAdmins(r.Context()); err == nil {
		status.Installed = n > 0
	}
	status.NeedsSetup = !status.Installed
	if settings, err := s.store.GetSettings(r.Context()); err == nil {
		if name := strings.TrimSpace(settings["panel_name"]); name != "" {
			status.PanelName = name
		}
		status.PublicURL = strings.TrimSpace(settings["public_url"])
	}
	return status
}

func (s *Server) handleInstallStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.installStatus(r))
}

// handleInstall creates the first administrator and closes the setup wizard.
// It is disabled as soon as one administrator exists.
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginAllowed(r.Context(), "install", ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}

	s.installMu.Lock()
	defer s.installMu.Unlock()

	if n, err := s.store.CountAdmins(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not check installation state")
		return
	} else if n > 0 {
		writeError(w, http.StatusConflict, "panel is already installed")
		return
	}

	var req struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		PanelName string `json:"panelName"`
		PublicURL string `json:"publicUrl"`
	}
	if err := decodeJSON(r, &req); err != nil {
		s.recordLoginFailure(r.Context(), "install", ip)
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	username := strings.TrimSpace(req.Username)
	if !adminUsernameRE.MatchString(username) {
		s.recordLoginFailure(r.Context(), "install", ip)
		writeError(w, http.StatusBadRequest, "username must be 3-32 characters (letters, digits, dot, underscore or dash)")
		return
	}
	if len(req.Password) < minInstallPasswordLength {
		s.recordLoginFailure(r.Context(), "install", ip)
		writeError(w, http.StatusBadRequest, "password must be at least 12 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	created, err := s.store.EnsureAdmin(r.Context(), username, string(hash))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create administrator")
		return
	}
	if !created {
		writeError(w, http.StatusConflict, "panel is already installed")
		return
	}
	admin, err := s.store.GetAdminByUsername(r.Context(), username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load administrator")
		return
	}

	values := map[string]string{
		"bootstrap_warning":    "",
		"install_completed_at": time.Now().UTC().Format(time.RFC3339),
	}
	if name := strings.TrimSpace(req.PanelName); name != "" {
		if len(name) > 64 {
			name = name[:64]
		}
		values["panel_name"] = name
	}
	publicURL := strings.TrimRight(strings.TrimSpace(req.PublicURL), "/")
	if publicURL == "" {
		publicURL = requestOrigin(r)
	}
	if publicURL != "" && (strings.HasPrefix(publicURL, "http://") || strings.HasPrefix(publicURL, "https://")) {
		values["public_url"] = publicURL
	}
	if err := s.store.UpdateSettings(r.Context(), values); err != nil {
		s.log.Warn("install: save settings", "error", err)
	}
	s.store.AddEvent(r.Context(), "install.completed", 0, "info", "Panel installation completed")

	token, expires, err := s.issueToken(admin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	s.sessions.Add(r.Context(), token, expires)
	_ = s.store.TouchAdminLogin(r.Context(), admin.ID)
	s.log.Warn("panel installed", "username", admin.Username, "remote", ip)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": expires,
		"admin":     map[string]any{"id": admin.ID, "username": admin.Username},
	})
}

// requestOrigin reconstructs the public origin when the installer leaves the
// URL field empty. The operator can correct it later in Settings.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto == "https" {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}
