package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

type dataUsageResponse struct {
	store.DataUsage
	DatabaseBytes       int64 `json:"databaseBytes"`
	WALBytes            int64 `json:"walBytes"`
	LogRetentionDays    int   `json:"logRetentionDays"`
	MetricRetentionDays int   `json:"metricRetentionDays"`
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

func (s *Server) handleDataUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.store.DataUsage(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logDays, metricDays := s.retentionDays(r.Context())
	writeJSON(w, http.StatusOK, dataUsageResponse{
		DataUsage:           usage,
		DatabaseBytes:       fileSize(s.cfg.DBPath),
		WALBytes:            fileSize(s.cfg.DBPath + "-wal"),
		LogRetentionDays:    logDays,
		MetricRetentionDays: metricDays,
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

	res, err := s.store.Prune(ctx, logDays, metricDays)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	vacuumed := false
	vacuumError := ""
	if req.Vacuum {
		if err := s.store.Compact(ctx); err != nil {
			vacuumError = err.Error()
		} else {
			vacuumed = true
		}
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
		"logRetentionDays":    logDays,
		"metricRetentionDays": metricDays,
	})
}
