package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/axmishell/mikvacepanel/internal/cache"
	"github.com/axmishell/mikvacepanel/internal/store"
)

type dataUsageResponse struct {
	store.DataUsage
	DatabaseBytes int64 `json:"databaseBytes"`
	WALBytes      int64 `json:"walBytes"`
	// LiveBytes is how much of the logical database (file + WAL) actually holds
	// data; FreeBytes is space that DELETE already released to the freelist but
	// not to the file.
	LiveBytes           int64 `json:"liveBytes"`
	FreeBytes           int64 `json:"freeBytes"`
	FreelistPages       int64 `json:"freelistPages"`
	PageSize            int64 `json:"pageSize"`
	AutoVacuum          int   `json:"autoVacuum"`
	LogRetentionDays    int   `json:"logRetentionDays"`
	MetricRetentionDays int   `json:"metricRetentionDays"`
	// Telemetry describes the log backend, which may live outside SQLite.
	TelemetryDriver     string      `json:"telemetryDriver"`
	TelemetryConfigured string      `json:"telemetryConfigured"`
	TelemetryFallback   bool        `json:"telemetryFallback"`
	TelemetryBytes      int64       `json:"telemetryBytes"`
	TelemetryRows       int64       `json:"telemetryRows"`
	TelemetryPartitions int         `json:"telemetryPartitions"`
	TelemetryDetail     string      `json:"telemetryDetail,omitempty"`
	TelemetryOK         bool        `json:"telemetryOk"`
	TelemetryError      string      `json:"telemetryError,omitempty"`
	Cache               cache.Stats `json:"cache"`
}

type cleanupRequest struct {
	Vacuum              bool `json:"vacuum"`
	LogRetentionDays    int  `json:"logRetentionDays"`
	MetricRetentionDays int  `json:"metricRetentionDays"`
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func (s *Server) retentionDays(ctx context.Context) (int, int) {
	settings, _ := s.store.GetSettings(ctx)
	logDays := parseIntSetting(settings["log_retention_days"], 14)
	if logDays < 1 {
		logDays = 14
	}
	metricDays := parseIntSetting(settings["metric_retention_days"], 30)
	if metricDays < 1 {
		metricDays = 30
	}
	return logDays, metricDays
}

// PruneData applies the retention policy across the control database and the
// telemetry backend. When PostgreSQL is active, stale rows left in the old
// SQLite log tables are expired too so switching back does not revive them.
func (s *Server) PruneData(ctx context.Context, logDays, metricDays int) (store.PruneResult, error) {
	res, err := s.store.PruneControl(ctx, logDays, metricDays)
	if err != nil {
		return res, err
	}
	if logDays <= 0 {
		return res, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -logDays)
	logs, batches, logErr := s.logs.PruneLogs(ctx, cutoff)
	res.Logs += logs
	res.LogBatches += batches
	if logErr != nil {
		return res, logErr
	}
	if !s.localLogs() {
		if legacyLogs, legacyBatches, legacyErr := s.store.PruneLogs(ctx, cutoff); legacyErr == nil {
			res.Logs += legacyLogs
			res.LogBatches += legacyBatches
		}
	}
	return res, nil
}

func (s *Server) handleDataUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.store.ControlDataUsage(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	telemetry := store.TelemetryStats{Driver: s.logs.Driver()}
	telemetryError := ""
	if logs, batches, logErr := s.logs.LogUsage(r.Context()); logErr != nil {
		telemetryError = logErr.Error()
	} else {
		usage.Logs, usage.LogBatches = logs, batches
	}
	if stats, statsErr := s.logs.TelemetryStats(r.Context()); statsErr != nil {
		if telemetryError == "" {
			telemetryError = statsErr.Error()
		}
	} else {
		telemetry = stats
	}
	stats, err := s.store.StorageStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logDays, metricDays := s.retentionDays(r.Context())
	writeJSON(w, http.StatusOK, dataUsageResponse{
		DataUsage:           usage,
		DatabaseBytes:       fileSize(s.cfg.DBPath),
		WALBytes:            fileSize(s.cfg.DBPath + "-wal"),
		LiveBytes:           stats.LiveBytes,
		FreeBytes:           stats.FreeBytes,
		FreelistPages:       stats.FreelistPages,
		PageSize:            stats.PageSize,
		AutoVacuum:          stats.AutoVacuum,
		LogRetentionDays:    logDays,
		MetricRetentionDays: metricDays,
		TelemetryDriver:     s.logs.Driver(),
		TelemetryConfigured: s.cfg.TelemetryDriver,
		TelemetryFallback:   s.cfg.TelemetryDriver == "postgres" && s.localLogs(),
		TelemetryBytes:      telemetry.Bytes,
		TelemetryRows:       telemetry.Rows,
		TelemetryPartitions: telemetry.Partitions,
		TelemetryDetail:     telemetry.Detail,
		TelemetryOK:         telemetryError == "",
		TelemetryError:      telemetryError,
		Cache:               s.cache.Stats(),
	})
}

// handleCleanupData runs one cleanup pass immediately. The request may override
// the saved retention policy so an operator can shrink the database once
// without changing the automatic schedule.
func (s *Server) handleCleanupData(w http.ResponseWriter, r *http.Request) {
	var req cleanupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.LogRetentionDays < 0 || req.LogRetentionDays > 3650 ||
		req.MetricRetentionDays < 0 || req.MetricRetentionDays > 3650 {
		writeError(w, http.StatusBadRequest, "retention days must be between 0 and 3650")
		return
	}
	logDays, metricDays := s.retentionDays(r.Context())
	if req.LogRetentionDays > 0 {
		logDays = req.LogRetentionDays
	}
	if req.MetricRetentionDays > 0 {
		metricDays = req.MetricRetentionDays
	}
	// Finish the maintenance pass even if the browser disconnects.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()

	res, err := s.PruneData(ctx, logDays, metricDays)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Deleted rows do not shrink the file by themselves. An explicit vacuum
	// rewrites it; otherwise the automatic policy reclaims what it can without
	// blocking writers for long.
	vacuumed := false
	vacuumError := ""
	reclaimed := int64(0)
	if req.Vacuum {
		if err := s.store.Compact(ctx); err != nil {
			vacuumError = err.Error()
		} else {
			vacuumed = true
		}
	} else if result, err := s.store.Maintain(ctx); err != nil {
		vacuumError = err.Error()
	} else {
		vacuumed = result.Vacuumed
		reclaimed = result.ReclaimedPages
	}

	freeBytes := int64(0)
	if stats, err := s.store.StorageStats(ctx); err == nil {
		freeBytes = stats.FreeBytes
	}

	s.store.AddEvent(ctx, "maintenance.cleanup", 0, "info",
		fmt.Sprintf("数据清理完成：日志 %d、日志批次 %d、事件 %d、流量批次 %d、监控采样 %d；保留策略 %d/%d 天",
			res.Logs, res.LogBatches, res.Events, res.StatsBatches, res.NodeMetrics, logDays, metricDays))

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"deleted":             res,
		"totalDeleted":        res.Total(),
		"vacuumed":            vacuumed,
		"vacuumError":         vacuumError,
		"reclaimedPages":      reclaimed,
		"freeBytes":           freeBytes,
		"logRetentionDays":    logDays,
		"metricRetentionDays": metricDays,
	})
}
