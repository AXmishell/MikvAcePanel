package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/configfile"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/store/dialect"
)

// These hooks are package variables so tests can exercise the config-writing
// path without touching the real filesystem.
var (
	testDatabaseConnection    = store.TestConnection
	prepareDatabaseConnection = store.Prepare
)

// installDatabaseRequest is the payload of the first-run database step.
type installDatabaseRequest struct {
	Driver string `json:"driver"`
	Path   string `json:"path"`
}

type installDatabasePlan struct {
	Driver string
	Path   string
}

// databasePlan validates the requested driver and resolves the SQLite path
// that will be written to panel.yaml.
func (s *Server) databasePlan(req installDatabaseRequest) (installDatabasePlan, error) {
	d, err := dialect.For(req.Driver)
	if err != nil {
		return installDatabasePlan{}, err
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = filepath.Join(s.cfg.DataDir, "panel.db")
	}
	return installDatabasePlan{Driver: d.Name(), Path: path}, nil
}

// requiresRestart reports whether the saved configuration differs from the
// connection this process is using. A restart is the only way to switch the
// store, its WAL files and every component that holds it.
func (p installDatabasePlan) requiresRestart(cfg Config) bool {
	return p.Driver != cfg.DBDriver || !samePath(p.Path, cfg.DBPath)
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

// handleInstallDatabaseTest checks that the SQLite path is writable without
// touching panel.yaml. It lets the wizard surface a bad path before the panel
// restarts.
func (s *Server) handleInstallDatabaseTest(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginAllowed(r.Context(), "install-db", ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	// Count every probe, successful or not, so the public install endpoint
	// cannot be used to enumerate filesystem paths. It is only useful before
	// the first administrator exists. Setup mode has no store by definition.
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
	started := time.Now()
	if err := testDatabaseConnection(store.Config{Path: plan.Path}); err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.recordLoginFailure(r.Context(), "install-db", ip)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "driver": plan.Driver, "latencyMs": time.Since(started).Milliseconds(),
	})
}

// handleInstallDatabase persists the SQLite path while the panel is still
// uninstalled. Changing it requires a restart, which cmd/server performs by
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
	if err := prepareDatabaseConnection(store.Config{Path: plan.Path}); err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	updates := map[string]any{
		"database.driver": plan.Driver,
		"database.path":   plan.Path,
	}
	if err := configfile.Set(s.cfg.ConfigPath, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "could not write config file "+s.cfg.ConfigPath+": "+err.Error())
		return
	}

	restart := plan.requiresRestart(s.cfg)
	if restart && s.cfg.Restart != nil {
		s.log.Warn("database configuration changed; restarting panel", "driver", plan.Driver)
		// Give net/http a moment to flush the JSON response before exec.
		time.AfterFunc(600*time.Millisecond, s.cfg.Restart)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"driver":          plan.Driver,
		"restartRequired": restart,
	})
}
