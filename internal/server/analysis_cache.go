package server

import (
	"context"
	"fmt"
	"sync"
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
	// maxCachedAnalyses bounds memory when clients request many windows.
	maxCachedAnalyses = 16
)

// analysisCache keeps recent log analysis results. Findings and counters are
// immutable once built, so entries are shared and only the live online counter
// is refreshed per call.
type analysisCache struct {
	mu      sync.Mutex
	max     int
	entries map[string]analysisEntry
}

type analysisEntry struct {
	summary *logs.Summary
	expires time.Time
}

func newAnalysisCache(max int) *analysisCache {
	if max <= 0 {
		max = maxCachedAnalyses
	}
	return &analysisCache{max: max, entries: map[string]analysisEntry{}}
}

// get returns a cached summary while it has not expired. The returned copy
// carries the current online count so callers do not need to invalidate an
// entry for that counter alone.
func (c *analysisCache) get(key string, online int, now time.Time) (*logs.Summary, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !now.Before(entry.expires) {
		delete(c.entries, key)
		return nil, false
	}
	out := *entry.summary
	out.Online = online
	return &out, true
}

func (c *analysisCache) put(key string, summary *logs.Summary, now time.Time, ttl time.Duration) {
	if summary == nil {
		return
	}
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok && len(c.entries) >= c.max {
		var oldestKey string
		var oldest time.Time
		for k, entry := range c.entries {
			if oldestKey == "" || entry.expires.Before(oldest) {
				oldestKey, oldest = k, entry.expires
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = analysisEntry{summary: summary, expires: now.Add(ttl)}
}

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
func (s *Server) analyzeCached(ctx context.Context, key string, ttl time.Duration, opts model.LogStatsOptions, online int, th logs.Thresholds) (*logs.Summary, error) {
	now := time.Now()
	if s.analysis != nil {
		if summary, ok := s.analysis.get(key, online, now); ok {
			return summary, nil
		}
	}
	summary, err := s.analyzer.Analyze(ctx, opts, online, th)
	if err != nil {
		return nil, err
	}
	if s.analysis != nil {
		s.analysis.put(key, summary, now, ttl)
	}
	return summary, nil
}
