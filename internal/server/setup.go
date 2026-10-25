package server

import (
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/axmishell/mikvacepanel/internal/configfile"
	"github.com/axmishell/mikvacepanel/internal/version"
)

// minInstallPasswordLength mirrors the administrator password policy used by
// the account settings page.
const minInstallPasswordLength = 12

// installStatus is public. It exposes setup state and non-secret database
// paths, which are already obvious from the /install page being reachable.
// DSN contents are never included.
type installStatus struct {
	Installed  bool `json:"installed"`
	NeedsSetup bool `json:"needsSetup"`
	// SetupMode is true until the first database has been selected. In this
	// mode the process has no store and has not opened SQLite.
	SetupMode      bool           `json:"setupMode"`
	PanelName      string         `json:"panelName"`
	PublicURL      string         `json:"publicUrl"`
	Driver         string         `json:"database"`
	DatabaseConfig databaseStatus `json:"databaseConfig"`
	Version        string         `json:"version"`
	ConfigPath     string         `json:"configPath"`
	ConfigWritable bool           `json:"configWritable"`
	// StartedAt changes after the process re-executes, which lets the wizard
	// wait for a database switch instead of polling a still-running old store.
	StartedAt time.Time `json:"startedAt"`
}

// databaseStatus describes the on-disk database configuration. It never
// contains the DSN itself; the installer only needs to know whether one exists.
type databaseStatus struct {
	Driver        string `json:"driver"`
	Path          string `json:"path,omitempty"`
	DSNFile       string `json:"dsnFile,omitempty"`
	DSNConfigured bool   `json:"dsnConfigured"`
}

func (s *Server) installStatus(r *http.Request) installStatus {
	status := installStatus{
		Installed:  true,
		NeedsSetup: false,
		PanelName:  s.cfg.PanelName,
		PublicURL:  s.cfg.PublicURL,
		DatabaseConfig: databaseStatus{
			Driver:        s.cfg.DBDriver,
			Path:          s.cfg.DBPath,
			DSNFile:       s.cfg.DBDSNFile,
			DSNConfigured: strings.TrimSpace(s.cfg.DBDSN) != "",
		},
		Version:        version.String(),
		ConfigPath:     s.cfg.ConfigPath,
		ConfigWritable: configfile.Writable(s.cfg.ConfigPath),
		StartedAt:      s.started,
	}
	if status.PanelName == "" {
		status.PanelName = "MikvAce Panel"
	}
	if s.store == nil {
		// Setup-only process: no database has been opened, so there is no
		// administrator table to inspect.
		status.Installed = false
		status.NeedsSetup = true
		status.SetupMode = true
		status.Driver = SetupDriver
		return status
	}
	status.Driver = s.store.Dialect().Name()
	if status.DatabaseConfig.Driver == "" {
		status.DatabaseConfig.Driver = status.Driver
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

	panelName := strings.TrimSpace(req.PanelName)
	if panelName != "" && len(panelName) > 64 {
		panelName = panelName[:64]
	}
	publicURL := strings.TrimRight(strings.TrimSpace(req.PublicURL), "/")
	if publicURL == "" {
		publicURL = requestOrigin(r)
	}
	if publicURL != "" && !strings.HasPrefix(publicURL, "http://") && !strings.HasPrefix(publicURL, "https://") {
		writeError(w, http.StatusBadRequest, "public URL must start with http:// or https://")
		return
	}
	updates := map[string]any{
		"admin.username": username,
	}
	if panelName != "" {
		updates["panel.name"] = panelName
	}
	if publicURL != "" {
		updates["panel.public_url"] = publicURL
	}
	if err := configfile.Set(s.cfg.ConfigPath, updates); err != nil {
		writeError(w, http.StatusInternalServerError,
			"could not write config file "+s.cfg.ConfigPath+": "+err.Error())
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
	if panelName != "" {
		values["panel_name"] = panelName
	}
	if publicURL != "" {
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
		"version":   version.String(),
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
