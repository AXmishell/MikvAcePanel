package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/cache"
	"github.com/axmishell/mikvacepanel/internal/configfile"
	"github.com/axmishell/mikvacepanel/internal/dsn"
	"github.com/axmishell/mikvacepanel/internal/rediscache"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/store/pglogs"
)

// infraPlan is the runtime configuration for the two optional backends. It is
// shared by the first-run wizard and the administrator "storage & cache" page.
type infraPlan struct {
	TelemetryDriver   string
	TelemetryDSN      string
	TelemetrySchema   string
	TelemetryRequired bool

	RedisURL      string
	RedisPrefix   string
	RedisRequired bool
}

type infraRequest struct {
	Telemetry installTelemetryRequest `json:"telemetry"`
	Redis     installRedisRequest     `json:"redis"`
}

// buildInfraPlan validates optional PostgreSQL / Redis input. Empty secrets
// reuse the currently running configuration, which lets the UI show "leave
// blank to keep the saved secret" without ever sending it back to the browser.
func (s *Server) buildInfraPlan(telemetry installTelemetryRequest, redisCfg installRedisRequest) (infraPlan, error) {
	plan := infraPlan{
		TelemetryDriver: "sqlite",
		TelemetrySchema: "public",
		RedisPrefix:     "mikvace:",
	}
	if telemetry.Enabled {
		rawDSN := strings.TrimSpace(telemetry.DSN)
		if rawDSN == "" && telemetryDriverName(s.cfg.TelemetryDriver) == "postgres" {
			rawDSN = s.cfg.TelemetryDSN
		}
		if rawDSN == "" {
			return infraPlan{}, fmt.Errorf("PostgreSQL DSN is required when external log storage is enabled")
		}
		if len(rawDSN) > 4096 {
			return infraPlan{}, fmt.Errorf("PostgreSQL DSN is too long")
		}
		if err := dsn.ValidatePostgres(rawDSN); err != nil {
			return infraPlan{}, err
		}
		schema, err := pglogs.NormalizeSchema(telemetry.Schema)
		if err != nil {
			return infraPlan{}, err
		}
		plan.TelemetryDriver = "postgres"
		plan.TelemetryDSN = rawDSN
		plan.TelemetrySchema = schema
		plan.TelemetryRequired = telemetry.Required
	}
	if redisCfg.Enabled {
		rawURL := strings.TrimSpace(redisCfg.URL)
		if rawURL == "" && s.cfg.RedisURL != "" {
			rawURL = s.cfg.RedisURL
		}
		if rawURL == "" {
			return infraPlan{}, fmt.Errorf("Redis URL is required when Redis is enabled")
		}
		if len(rawURL) > 4096 {
			return infraPlan{}, fmt.Errorf("Redis URL is too long")
		}
		if err := rediscache.ValidateURL(rawURL); err != nil {
			return infraPlan{}, err
		}
		prefix := strings.TrimSpace(redisCfg.Prefix)
		if prefix == "" {
			prefix = "mikvace:"
		}
		if len(prefix) > 128 {
			return infraPlan{}, fmt.Errorf("Redis key prefix is too long")
		}
		plan.RedisURL = rawURL
		plan.RedisPrefix = prefix
		plan.RedisRequired = redisCfg.Required
	}
	return plan, nil
}

func (p infraPlan) requiresRestart(cfg Config) bool {
	if telemetryDriverName(cfg.TelemetryDriver) != p.TelemetryDriver {
		return true
	}
	if p.TelemetryDriver == "postgres" {
		if p.TelemetrySchema != telemetrySchemaName(cfg.TelemetrySchema) ||
			p.TelemetryDSN != cfg.TelemetryDSN ||
			p.TelemetryRequired != cfg.TelemetryRequired {
			return true
		}
	} else if telemetryDriverName(cfg.TelemetryDriver) == "postgres" {
		return true
	}
	if (p.RedisURL != "") != (cfg.RedisURL != "") {
		return true
	}
	if p.RedisURL != "" {
		if p.RedisURL != cfg.RedisURL ||
			p.RedisPrefix != redisPrefixName(cfg.RedisPrefix) ||
			p.RedisRequired != cfg.RedisRequired {
			return true
		}
	}
	return false
}

// infraConfigUpdates writes the secret files and returns the panel.yaml keys
// that describe them. Only fixed filenames under server.data_dir are used.
func (s *Server) infraConfigUpdates(plan infraPlan) (map[string]any, error) {
	updates := map[string]any{
		"telemetry.driver":   plan.TelemetryDriver,
		"telemetry.schema":   plan.TelemetrySchema,
		"telemetry.required": plan.TelemetryRequired,
		"redis.prefix":       plan.RedisPrefix,
		"redis.required":     plan.RedisRequired,
	}
	if plan.TelemetryDriver == "postgres" {
		path := s.telemetrySecretPath()
		if err := configfile.WriteSecret(path, []byte(plan.TelemetryDSN)); err != nil {
			return nil, fmt.Errorf("write PostgreSQL DSN file: %w", err)
		}
		updates["telemetry.dsn_file"] = path
	} else {
		updates["telemetry.dsn_file"] = ""
		_ = os.Remove(s.telemetrySecretPath())
	}
	if plan.RedisURL != "" {
		path := s.redisSecretPath()
		if err := configfile.WriteSecret(path, []byte(plan.RedisURL)); err != nil {
			return nil, fmt.Errorf("write Redis URL file: %w", err)
		}
		updates["redis.url_file"] = path
	} else {
		updates["redis.url_file"] = ""
		_ = os.Remove(s.redisSecretPath())
	}
	return updates, nil
}

// verifyOptionalConnections tests PostgreSQL and Redis. When force is false, a
// backend configured as non-required only produces a warning so the operator
// can save a configuration that will start in fallback mode.
func (s *Server) verifyOptionalConnections(ctx context.Context, plan infraPlan, force bool) error {
	if plan.TelemetryDriver == "postgres" {
		if err := testTelemetryConnection(ctx, plan.TelemetryDSN, plan.TelemetrySchema); err != nil {
			if force || plan.TelemetryRequired {
				return fmt.Errorf("PostgreSQL: %w", err)
			}
			s.log.Warn("PostgreSQL unavailable; saving with fallback enabled", "error", err)
		}
	}
	if plan.RedisURL != "" {
		if err := testRedisConnection(ctx, plan.RedisURL); err != nil {
			if force || plan.RedisRequired {
				return fmt.Errorf("Redis: %w", err)
			}
			s.log.Warn("Redis unavailable; saving without shared cache", "error", err)
		}
	}
	return nil
}

// ---- administrator API --------------------------------------------------

type infraTelemetryStatus struct {
	Driver       string               `json:"driver"`
	ActiveDriver string               `json:"activeDriver"`
	Schema       string               `json:"schema"`
	Required     bool                 `json:"required"`
	Configured   bool                 `json:"configured"`
	OK           bool                 `json:"ok"`
	Fallback     bool                 `json:"fallback"`
	Error        string               `json:"error,omitempty"`
	Stats        store.TelemetryStats `json:"stats"`
}

type infraRedisStatus struct {
	Enabled    bool   `json:"enabled"`
	Prefix     string `json:"prefix"`
	Required   bool   `json:"required"`
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

type infraStatus struct {
	Telemetry    infraTelemetryStatus `json:"telemetry"`
	Redis        infraRedisStatus     `json:"redis"`
	Cache        cache.Stats          `json:"cache"`
	DSNTemplates dsn.TemplateSet      `json:"dsnTemplates"`
}

func (s *Server) handleGetInfra(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	configuredDriver := telemetryDriverName(s.cfg.TelemetryDriver)
	activeDriver := s.logs.Driver()
	status := infraStatus{
		Telemetry: infraTelemetryStatus{
			Driver:       configuredDriver,
			ActiveDriver: activeDriver,
			Schema:       telemetrySchemaName(s.cfg.TelemetrySchema),
			Required:     s.cfg.TelemetryRequired,
			Configured:   configuredDriver == "postgres" && s.cfg.TelemetryDSN != "",
			Fallback:     configuredDriver == "postgres" && activeDriver != "postgres",
		},
		Redis: infraRedisStatus{
			Enabled:    s.cfg.RedisURL != "",
			Prefix:     redisPrefixName(s.cfg.RedisPrefix),
			Required:   s.cfg.RedisRequired,
			Configured: s.cfg.RedisURL != "",
		},
		Cache:        s.cache.Stats(),
		DSNTemplates: dsn.All(),
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	telemetryErr := s.logs.Ping(pingCtx)
	cancel()
	status.Telemetry.OK = telemetryErr == nil && !status.Telemetry.Fallback
	switch {
	case status.Telemetry.Fallback:
		status.Telemetry.Error = "PostgreSQL 不可用，当前使用 SQLite 日志表"
	case telemetryErr != nil:
		status.Telemetry.Error = telemetryErr.Error()
	}
	statsCtx, cancelStats := context.WithTimeout(ctx, 5*time.Second)
	stats, statsErr := s.logs.TelemetryStats(statsCtx)
	cancelStats()
	if statsErr == nil {
		status.Telemetry.Stats = stats
	} else if status.Telemetry.Error == "" {
		status.Telemetry.Error = statsErr.Error()
	}
	if !status.Redis.Enabled {
		status.Redis.OK = true
	} else if s.redis == nil {
		status.Redis.OK = false
		status.Redis.Error = "Redis 未连接，当前使用进程内存会话与缓存"
	} else {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := s.redis.Ping(pingCtx)
		cancel()
		status.Redis.OK = err == nil
		if err != nil {
			status.Redis.Error = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleTestInfra(w http.ResponseWriter, r *http.Request) {
	var req infraRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	plan, err := s.buildInfraPlan(req.Telemetry, req.Redis)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	started := time.Now()
	if err := s.verifyOptionalConnections(ctx, plan, true); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
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

func (s *Server) handleSaveInfra(w http.ResponseWriter, r *http.Request) {
	s.infraMu.Lock()
	defer s.infraMu.Unlock()

	if !configfile.Writable(s.cfg.ConfigPath) {
		writeError(w, http.StatusConflict, "config file is not writable: "+s.cfg.ConfigPath)
		return
	}
	var req infraRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	plan, err := s.buildInfraPlan(req.Telemetry, req.Redis)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	if err := s.verifyOptionalConnections(ctx, plan, false); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	updates, err := s.infraConfigUpdates(plan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := configfile.Set(s.cfg.ConfigPath, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "could not write config file "+s.cfg.ConfigPath+": "+err.Error())
		return
	}
	restart := plan.requiresRestart(s.cfg)
	if restart && s.cfg.Restart != nil {
		s.log.Warn("infrastructure configuration changed; restarting panel",
			"telemetry", plan.TelemetryDriver, "redis", plan.RedisURL != "")
		time.AfterFunc(600*time.Millisecond, s.cfg.Restart)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"telemetry":       plan.TelemetryDriver,
		"redis":           plan.RedisURL != "",
		"restartRequired": restart,
	})
}

// requireAdminSession rejects API keys for infrastructure configuration. A
// leaked automation token must not be able to point the panel at another
// database or Redis instance.
func (s *Server) requireAdminSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiKeyFrom(r) != nil {
			writeError(w, http.StatusForbidden, "administrator session required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
