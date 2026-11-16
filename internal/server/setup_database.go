package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/configfile"
	"github.com/axmishell/mikvacepanel/internal/rediscache"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/store/dialect"
	"github.com/axmishell/mikvacepanel/internal/store/pglogs"
)

// These hooks are package variables so tests can exercise the config-writing
// path without touching PostgreSQL, Redis or the real filesystem.
var (
	testDatabaseConnection    = store.TestConnection
	prepareDatabaseConnection = store.Prepare
	testTelemetryConnection   = pglogs.TestConnection
	testRedisConnection       = rediscache.TestConnection
)

// installDatabaseRequest is the payload of the first-run database step. The
// main database is always SQLite; PostgreSQL only stores proxy logs.
type installDatabaseRequest struct {
	Driver    string                  `json:"driver"`
	Path      string                  `json:"path"`
	Telemetry installTelemetryRequest `json:"telemetry"`
	Redis     installRedisRequest     `json:"redis"`
}

type installTelemetryRequest struct {
	Enabled  bool   `json:"enabled"`
	DSN      string `json:"dsn"`
	Schema   string `json:"schema"`
	Required bool   `json:"required"`
}

type installRedisRequest struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	Prefix   string `json:"prefix"`
	Required bool   `json:"required"`
}

type installDatabasePlan struct {
	Driver string
	Path   string
	infraPlan
}

// databasePlan validates the requested main database and the optional
// PostgreSQL log store / Redis cache. Secret files are written later, so a
// failed validation cannot change the running configuration.
func (s *Server) databasePlan(req installDatabaseRequest) (installDatabasePlan, error) {
	d, err := dialect.For(req.Driver)
	if err != nil {
		return installDatabasePlan{}, err
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = filepath.Join(s.cfg.DataDir, "panel.db")
	}
	infra, err := s.buildInfraPlan(req.Telemetry, req.Redis)
	if err != nil {
		return installDatabasePlan{}, err
	}
	return installDatabasePlan{Driver: d.Name(), Path: path, infraPlan: infra}, nil
}

// requiresRestart reports whether the saved configuration differs from the
// connection this process is using. A restart is the only way to switch the
// store, its WAL files, the telemetry backend, Redis and every component that
// holds them.
func (p installDatabasePlan) requiresRestart(cfg Config) bool {
	if p.Driver != cfg.DBDriver || !samePath(p.Path, cfg.DBPath) {
		return true
	}
	return p.infraPlan.requiresRestart(cfg)
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

// dataSecretPath returns an absolute path under server.data_dir. The installer
// only ever writes these fixed filenames, never a user-supplied path.
func (s *Server) dataSecretPath(name string) string {
	path := filepath.Join(s.cfg.DataDir, name)
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func (s *Server) telemetrySecretPath() string { return s.dataSecretPath("telemetry.pg.url") }
func (s *Server) redisSecretPath() string     { return s.dataSecretPath("redis.url") }

// verifyInstallConnections tests every configured backend for the wizard's
// connection test. Errors are prefixed so the operator can tell which
// component failed.
func (s *Server) verifyInstallConnections(ctx context.Context, plan installDatabasePlan) error {
	if err := testDatabaseConnection(store.Config{Path: plan.Path}); err != nil {
		return fmt.Errorf("SQLite: %w", err)
	}
	return s.verifyOptionalConnections(ctx, plan.infraPlan, true)
}

// handleInstallDatabaseTest checks every selected backend without touching
// panel.yaml or writing secret files. It lets the wizard surface a bad DSN or
// Redis URL before the panel restarts.
func (s *Server) handleInstallDatabaseTest(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginAllowed(r.Context(), "install-db", ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	// Count every probe, successful or not, so the public install endpoint
	// cannot be used to enumerate filesystem paths or internal services. It is
	// only useful before the first administrator exists.
	if s.store != nil {
		if n, err := s.store.CountAdmins(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "could not check installation state")
			return
		} else if n > 0 {
			writeError(w, http.StatusConflict, "panel is already installed")
			return
		}
	}
	var req installDatabaseRequest
	if err := decodeJSON(r, &req); err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	plan, err := s.databasePlan(req)
	if err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	started := time.Now()
	if err := s.verifyInstallConnections(ctx, plan); err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.recordLoginFailure(r.Context(), "install-db", ip)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "driver": plan.Driver, "path": plan.Path,
		"latencyMs": time.Since(started).Milliseconds(),
		"telemetry": map[string]any{
			"enabled": plan.TelemetryDriver == "postgres",
			"driver":  plan.TelemetryDriver,
			"schema":  plan.TelemetrySchema,
		},
		"redis": map[string]any{
			"enabled": plan.RedisURL != "",
			"prefix":  plan.RedisPrefix,
		},
	})
}

// handleInstallDatabase persists the main database and optional PostgreSQL /
// Redis settings while the panel is still uninstalled. Secrets are written to
// fixed files under server.data_dir; panel.yaml only stores their paths.
// Changing any backend requires a restart, which cmd/server performs by
// re-executing the process after the response is flushed.
func (s *Server) handleInstallDatabase(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginAllowed(r.Context(), "install-db", ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}

	s.installMu.Lock()
	defer s.installMu.Unlock()

	if s.store != nil {
		if n, err := s.store.CountAdmins(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "could not check installation state")
			return
		} else if n > 0 {
			writeError(w, http.StatusConflict, "panel is already installed")
			return
		}
	}
	if !configfile.Writable(s.cfg.ConfigPath) {
		writeError(w, http.StatusConflict, "config file is not writable: "+s.cfg.ConfigPath)
		return
	}

	var req installDatabaseRequest
	if err := decodeJSON(r, &req); err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	plan, err := s.databasePlan(req)
	if err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	if err := prepareDatabaseConnection(store.Config{Path: plan.Path}); err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.verifyOptionalConnections(ctx, plan.infraPlan, false); err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	updates := map[string]any{
		"database.driver": plan.Driver,
		"database.path":   plan.Path,
	}
	infraUpdates, err := s.infraConfigUpdates(plan.infraPlan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for key, value := range infraUpdates {
		updates[key] = value
	}
	if err := configfile.Set(s.cfg.ConfigPath, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "could not write config file "+s.cfg.ConfigPath+": "+err.Error())
		return
	}

	restart := plan.requiresRestart(s.cfg)
	if restart && s.cfg.Restart != nil {
		s.log.Warn("installation configuration changed; restarting panel",
			"database", plan.Driver, "telemetry", plan.TelemetryDriver,
			"redis", plan.RedisURL != "")
		// Give net/http a moment to flush the JSON response before exec.
		time.AfterFunc(600*time.Millisecond, s.cfg.Restart)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"driver":          plan.Driver,
		"telemetry":       plan.TelemetryDriver,
		"redis":           plan.RedisURL != "",
		"restartRequired": restart,
	})
}
