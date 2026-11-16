package server

import (
	"context"
	"net/http"
	"time"

	"github.com/axmishell/mikvacepanel/internal/version"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	ok := true
	if err := s.store.Ping(r.Context()); err != nil {
		status = http.StatusServiceUnavailable
		ok = false
	}
	telemetryInfo := map[string]any{
		"driver": s.logs.Driver(), "configured": s.cfg.TelemetryDriver, "required": s.cfg.TelemetryRequired,
	}
	telemetryOK := true
	if s.cfg.TelemetryDriver == "postgres" && s.localLogs() {
		// Startup fell back to the SQLite log tables because PostgreSQL was
		// unreachable. Control-plane requests still work, but logs are local.
		telemetryOK = false
		telemetryInfo["fallback"] = true
		telemetryInfo["ok"] = false
		telemetryInfo["error"] = "postgres was unreachable at startup; using SQLite logs"
	}
	if !s.localLogs() {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		err := s.logs.Ping(ctx)
		cancel()
		telemetryOK = err == nil
		telemetryInfo["ok"] = telemetryOK
		if err != nil {
			telemetryInfo["error"] = err.Error()
			if s.cfg.TelemetryRequired {
				status = http.StatusServiceUnavailable
				ok = false
			}
		}
	} else {
		if telemetryOK {
			telemetryInfo["ok"] = ok
		}
	}
	redisInfo := map[string]any{"enabled": s.redis != nil}
	if s.redis != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		err := s.redis.Ping(ctx)
		cancel()
		redisInfo["ok"] = err == nil
		if err != nil {
			if s.cfg.RedisRequired {
				status = http.StatusServiceUnavailable
				ok = false
			}
		}
	}
	writeJSON(w, status, map[string]any{
		"ok": ok, "version": version.String(), "uptime": int64(time.Since(s.started).Seconds()),
		"hub": s.hub.Stats(), "redis": redisInfo,
		"database":  map[string]any{"driver": s.store.Dialect().Name()},
		"telemetry": telemetryInfo,
		"cache":     s.cache.Stats(),
	})
}

// handleReadyz is the readiness probe: it fails when the panel cannot serve
// requests because the database or a required Redis instance is unavailable.
// It intentionally returns no internal error details.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "database": false})
		return
	}
	if !s.localLogs() && s.cfg.TelemetryRequired {
		if err := s.logs.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "database": true, "telemetry": false})
			return
		}
	}
	if s.redis != nil {
		if err := s.redis.Ping(ctx); err != nil && s.cfg.RedisRequired {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "database": true, "redis": false})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": true, "database": true, "telemetry": true})
}

func (s *Server) handleAgentVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": version.String(), "platform": version.Platform()})
}
