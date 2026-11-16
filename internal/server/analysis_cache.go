package server

import (
	"context"
	"fmt"
	"time"

	"github.com/axmishell/mikvacepanel/internal/logs"
	"github.com/axmishell/mikvacepanel/internal/model"
)

const (
	// dashboardAnalysisTTL keeps the heavy 24h analysis to at most one run per
	// minute while the dashboard polls every 30 seconds.
	dashboardAnalysisTTL = 60 * time.Second
	// logSummaryAnalysisTTL loosely matches the log page's 12 second poll: the
	// first poll computes, the next one reuses the result. Results stay close
	// enough to live for an operator watching the page.
	logSummaryAnalysisTTL = 20 * time.Second
)

// nodeScopeKey renders an optional node restriction so a nil (unrestricted)
// scope cannot collide with an empty (nothing allowed) scope in cache keys.
func nodeScopeKey(ids []int64) string {
	if ids == nil {
		return "-"
	}
	return fmt.Sprint(ids)
}

// analyzeCached returns a cached log analysis for key, or runs the analyzer and
// stores the result. key must describe the query parameters without a moving
// timestamp for rolling windows, otherwise every poll would miss the cache.
//
// The cache is process-local by default and backed by Redis when Redis is
// configured, so several panel instances share one analysis result.
func (s *Server) analyzeCached(ctx context.Context, key string, ttl time.Duration, opts model.LogStatsOptions, online int, th logs.Thresholds) (*logs.Summary, error) {
	cacheKey := "analysis:" + key
	if s.cache != nil {
		var cached logs.Summary
		if s.cache.Get(ctx, cacheKey, &cached) {
			// The online counter is live state, not part of the analysis.
			cached.Online = online
			return &cached, nil
		}
	}
	summary, err := s.analyzer.Analyze(ctx, opts, online, th)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, cacheKey, summary, ttl)
	}
	return summary, nil
}
