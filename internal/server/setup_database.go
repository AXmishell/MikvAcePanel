package server

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/configfile"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/store/dialect"
)

// These hooks are package variables so tests can exercise the config-writing
// path without a live PostgreSQL or MySQL server.
var (
	testDatabaseConnection    = store.TestConnection
	prepareDatabaseConnection = store.Prepare
)

// installDatabaseRequest is the payload of the first-run database step. DSN is
// only used for PostgreSQL and MySQL; it is written to a secret file and never
// persisted to panel.yaml.
type installDatabaseRequest struct {
	Driver string `json:"driver"`
	Path   string `json:"path"`
	DSN    string `json:"dsn"`
}

type installDatabasePlan struct {
	Driver  string
	Path    string
	DSN     string
	DSNFile string
	Secret  []byte
}

// databasePlan validates the requested backend and resolves the paths that
// will be written to panel.yaml and the DSN secret file.
func (s *Server) databasePlan(req installDatabaseRequest) (installDatabasePlan, error) {
	d, err := dialect.For(req.Driver)
	if err != nil {
		return installDatabasePlan{}, err
	}
	plan := installDatabasePlan{Driver: d.Name()}
	if plan.Driver == dialect.SQLite {
		plan.Path = strings.TrimSpace(req.Path)
		if plan.Path == "" {
			plan.Path = filepath.Join(s.cfg.DataDir, "panel.db")
		}
		// Clear a stale remote DSN reference so the next start cannot read an
		// unrelated secret file.
		plan.DSNFile = ""
		return plan, nil
	}

	plan.DSN = strings.TrimSpace(req.DSN)
	if plan.DSN == "" {
		// Re-saving the active backend without retyping the password keeps the
		// existing DSN. The UI normally offers "use current database" instead.
		if plan.Driver != s.cfg.DBDriver || strings.TrimSpace(s.cfg.DBDSN) == "" {
			return plan, fmt.Errorf("%s DSN is required", plan.Driver)
		}
		plan.DSN = s.cfg.DBDSN
		plan.DSNFile = s.cfg.DBDSNFile
		if plan.DSNFile == "" {
			plan.DSNFile = s.defaultDSNFile()
			plan.Secret = []byte(plan.DSN + "\n")
		}
		return plan, nil
	}
	if inferred := dialect.ForDSN(plan.DSN); inferred != "" && inferred != plan.Driver {
		return plan, fmt.Errorf("DSN looks like %s but the selected driver is %s", inferred, plan.Driver)
	}
	plan.DSNFile = s.defaultDSNFile()
	plan.Secret = []byte(plan.DSN + "\n")
	return plan, nil
}

// defaultDSNFile keeps the DSN inside data_dir, which is writable in both the
// binary and container deployments.
func (s *Server) defaultDSNFile() string {
	path := filepath.Join(s.cfg.DataDir, "database.dsn")
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// requiresRestart reports whether the saved configuration differs from the
// connection this process is using. A restart is the only way to switch the
// store, its migration lock and every component that holds it.
func (p installDatabasePlan) requiresRestart(cfg Config) bool {
	if p.Driver != cfg.DBDriver {
		return true
	}
	if p.Driver == dialect.SQLite {
		return !samePath(p.Path, cfg.DBPath)
	}
	return p.DSN != cfg.DBDSN
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

// handleInstallDatabaseTest checks a candidate backend without touching
// panel.yaml. It lets the wizard surface a bad DSN before the panel restarts.
func (s *Server) handleInstallDatabaseTest(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginAllowed(r.Context(), "install-db", ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	// Count every probe, successful or not, so the public test endpoint cannot
	// be used as an unrestricted port scanner. It is only useful before the
	// first administrator exists. Setup mode has no store by definition.
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
	if err := testDatabaseConnection(store.Config{Driver: plan.Driver, Path: plan.Path, DSN: plan.DSN}); err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadGateway, redactDatabaseError(err, plan.DSN))
		return
	}
	s.recordLoginFailure(r.Context(), "install-db", ip)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "driver": plan.Driver, "latencyMs": time.Since(started).Milliseconds(),
	})
}

// handleInstallDatabase persists the chosen backend while the panel is still
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
	if err := prepareDatabaseConnection(store.Config{Driver: plan.Driver, Path: plan.Path, DSN: plan.DSN}); err != nil {
		s.recordLoginFailure(r.Context(), "install-db", ip)
		writeError(w, http.StatusBadGateway, redactDatabaseError(err, plan.DSN))
		return
	}

	// Write the secret first: if panel.yaml then fails to update, the panel
	// keeps running on the old configuration and the orphan file is harmless.
	if len(plan.Secret) > 0 {
		if err := configfile.WriteSecret(plan.DSNFile, plan.Secret); err != nil {
			writeError(w, http.StatusInternalServerError, "could not write database secret: "+err.Error())
			return
		}
	}
	updates := map[string]any{
		"database.driver":   plan.Driver,
		"database.dsn_file": plan.DSNFile,
	}
	if plan.Driver == dialect.SQLite {
		updates["database.path"] = plan.Path
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

// redactDatabaseError removes literal DSNs from driver errors before they are
// returned to the browser or written to logs.
func redactDatabaseError(err error, dsn string) string {
	msg := err.Error()
	if dsn = strings.TrimSpace(dsn); dsn != "" {
		msg = strings.ReplaceAll(msg, dsn, "<dsn>")
	}
	return msg
}
