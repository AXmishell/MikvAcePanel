package server

import (
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/logs"
)

func TestAnalysisCacheRefreshesOnlineCount(t *testing.T) {
	c := newAnalysisCache(4)
	now := time.Now()
	c.put("k", &logs.Summary{Total: 5, Online: 1}, now, 10*time.Second)

	got, ok := c.get("k", 7, now.Add(5*time.Second))
	if !ok {
		t.Fatal("fresh entry missing")
	}
	if got.Total != 5 || got.Online != 7 {
		t.Fatalf("summary = %+v", got)
	}

	// The returned copy must not mutate the cached entry.
	got.Total = 99
	again, ok := c.get("k", 3, now.Add(6*time.Second))
	if !ok || again.Total != 5 || again.Online != 3 {
		t.Fatalf("cached entry changed: %+v ok=%v", again, ok)
	}
}

func TestAnalysisCacheExpires(t *testing.T) {
	c := newAnalysisCache(4)
	now := time.Now()
	c.put("k", &logs.Summary{}, now, 10*time.Second)
	if _, ok := c.get("k", 0, now.Add(9*time.Second)); !ok {
		t.Fatal("entry expired too early")
	}
	if _, ok := c.get("k", 0, now.Add(10*time.Second)); ok {
		t.Fatal("expired entry served")
	}
}

func TestAnalysisCacheEvictsEarliestExpiry(t *testing.T) {
	c := newAnalysisCache(2)
	now := time.Now()
	c.put("a", &logs.Summary{}, now, 30*time.Second)
	c.put("b", &logs.Summary{}, now, time.Minute)
	c.put("c", &logs.Summary{}, now, time.Minute)

	if _, ok := c.get("a", 0, now.Add(time.Second)); ok {
		t.Fatal("entry with the earliest expiry was not evicted")
	}
	if _, ok := c.get("c", 0, now.Add(time.Second)); !ok {
		t.Fatal("newest entry missing")
	}
	if got := len(c.entries); got != 2 {
		t.Fatalf("entries = %d", got)
	}
}

func TestNodeScopeKeySeparatesNilAndEmpty(t *testing.T) {
	if nodeScopeKey(nil) == nodeScopeKey([]int64{}) {
		t.Fatal("unrestricted and empty node scopes must not share a cache key")
	}
	if nodeScopeKey([]int64{1, 2}) == nodeScopeKey([]int64{1}) {
		t.Fatal("different node scopes must not share a cache key")
	}
}

func TestAnalysisCacheIgnoresNilSummaryAndZeroTTL(t *testing.T) {
	c := newAnalysisCache(2)
	now := time.Now()
	c.put("nil", nil, now, time.Minute)
	c.put("zero", &logs.Summary{}, now, 0)
	if got := len(c.entries); got != 0 {
		t.Fatalf("entries = %d", got)
	}
}
