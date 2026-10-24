package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/axmishell/mikvacepanel/internal/version"
)

// NewSetup creates the minimal first-run application used when panel.yaml
// selects database.driver: setup. It never opens SQLite or any other database;
// the wizard only serves the backend picker and writes the chosen config before
// restarting into the full server.
func NewSetup(cfg Config, logger *slog.Logger) *Server {
	s := &Server{
		cfg:            cfg,
		log:            logger,
		started:        time.Now().UTC(),
		loginTries:     map[string][]time.Time{},
		trustedProxies: parseTrustedProxies(cfg.TrustedProxies),
		apiKeyRate:     map[int64][]time.Time{},
		twoFactorTries: map[string]int{},
		challengeUsed:  map[string]time.Time{},
		authLogWindow:  map[int64]authLogBucket{},
		quotaBlocked:   map[string]bool{},
		adminVer:       map[int64]adminVersionEntry{},
	}
	s.mux = s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.recoverer)
	r.Use(s.requestLogger)
	r.Use(s.securityHeaders)
	r.Use(s.cors)

	r.Get("/api/health", s.handleSetupHealth)
	r.Get("/api/admin/config", s.handleSetupAdminConfig)
	r.With(noStore).Get("/api/install/status", s.handleInstallStatus)
	r.With(noStore).Post("/api/install/database/test", s.handleInstallDatabaseTest)
	r.With(noStore).Post("/api/install/database", s.handleInstallDatabase)
	r.With(noStore).Post("/api/install", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusConflict, "select and save a database before creating an administrator")
	})

	if s.cfg.ServeWeb {
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/install", http.StatusFound)
		})
		r.Get("/install", s.serveSPA)
		r.Get("/assets/*", s.serveSPA)
		r.Get("/favicon.ico", s.serveSPA)
		r.NotFound(s.serveSPA)
	} else {
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "database setup required; open /install or POST /api/install/database")
		})
	}
	return r
}

// handleSetupHealth keeps container health checks green while the panel waits
// for a database choice.
func (s *Server) handleSetupHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": version.String(),
		"uptime":  int64(time.Since(s.started).Seconds()),
		"setup":   true,
		"database": map[string]any{
			"driver": SetupDriver,
		},
	})
}

func (s *Server) handleSetupAdminConfig(w http.ResponseWriter, _ *http.Request) {
	name := s.cfg.PanelName
	if name == "" {
		name = "MikvAce Panel"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"securityEntryEnabled": false,
		"panelName":            name,
		"installed":            false,
		"setupMode":            true,
	})
}
