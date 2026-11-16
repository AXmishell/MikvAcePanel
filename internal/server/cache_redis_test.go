package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/logs"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// TestAnalysisCacheSharedThroughRedis verifies that a second panel instance
// with an empty local cache reuses the summary stored by the first instance.
func TestAnalysisCacheSharedThroughRedis(t *testing.T) {
	redisURL := strings.TrimSpace(os.Getenv("MIKVACE_TEST_REDIS_URL"))
	if redisURL == "" {
		t.Skip("set MIKVACE_TEST_REDIS_URL to run the shared cache test")
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	prefix := fmt.Sprintf("cache-test-%d:", time.Now().UnixNano())
	cfg := Config{
		Addr: ":0", DataDir: dir, DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin", AdminPassword: "testpass123",
		SessionTTL: time.Hour, RedisURL: redisURL, RedisPrefix: prefix,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := New(cfg, st, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	b, err := New(cfg, st, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	key := "shared-" + fmt.Sprint(time.Now().UnixNano())
	opts := model.LogStatsOptions{From: time.Now().UTC().Add(-time.Hour), To: time.Now().UTC()}
	if _, err := a.analyzeCached(context.Background(), key, time.Minute, opts, 0, logs.Thresholds{}); err != nil {
		t.Fatal(err)
	}
	if a.cache.Stats().Backend != "redis" {
		t.Fatalf("backend = %q", a.cache.Stats().Backend)
	}
	summary, err := b.analyzeCached(context.Background(), key, time.Minute, opts, 3, logs.Thresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if summary == nil || summary.Online != 3 {
		t.Fatalf("summary = %+v", summary)
	}
	stats := b.cache.Stats()
	if stats.Backend != "redis" || stats.Hits == 0 {
		t.Fatalf("second instance stats = %+v", stats)
	}
}
