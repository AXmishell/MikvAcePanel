package cache

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/rediscache"
)

func TestLocalSetGetDelete(t *testing.T) {
	ctx := context.Background()
	c := New(nil, 4, time.Minute)
	var value struct {
		Total int `json:"total"`
	}
	if err := c.Set(ctx, "k", map[string]int{"total": 7}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if !c.Get(ctx, "k", &value) || value.Total != 7 {
		t.Fatalf("get = %+v", value)
	}
	c.Delete(ctx, "k")
	if c.Get(ctx, "k", &value) {
		t.Fatal("deleted key still cached")
	}
	stats := c.Stats()
	if stats.Backend != "memory" || stats.Sets != 1 || stats.Hits != 1 || stats.Misses != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestLocalExpiryAndEviction(t *testing.T) {
	ctx := context.Background()
	c := New(nil, 2, time.Minute)
	if err := c.Set(ctx, "a", 1, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, "b", 2, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, "c", 3, time.Minute); err != nil {
		t.Fatal(err)
	}
	var n int
	if !c.Get(ctx, "b", &n) || n != 2 {
		t.Fatalf("b = %d", n)
	}
	if !c.Get(ctx, "c", &n) || n != 3 {
		t.Fatalf("c = %d", n)
	}
	if c.Get(ctx, "a", &n) {
		t.Fatal("earliest-expiry entry was not evicted")
	}
	c.Delete(ctx)
	if c.Stats().Entries != 2 {
		t.Fatalf("entries = %d", c.Stats().Entries)
	}
}

func TestRedisSharedL2(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("MIKVACE_TEST_REDIS_URL"))
	if url == "" {
		t.Skip("set MIKVACE_TEST_REDIS_URL to run the Redis cache test")
	}
	ctx := context.Background()
	client, err := rediscache.Open(ctx, url, "mikvace-test:", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.Delete(ctx, "shared-key")
	writer := New(client, 4, time.Millisecond)
	reader := New(client, 4, time.Minute)
	if err := writer.Set(ctx, "shared-key", map[string]string{"v": "from-redis"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // let the writer's L1 copy expire
	var out map[string]string
	if !reader.Get(ctx, "shared-key", &out) || out["v"] != "from-redis" {
		t.Fatalf("reader = %+v", out)
	}
	if writer.Stats().Backend != "redis" {
		t.Fatalf("backend = %q", writer.Stats().Backend)
	}
	client.Delete(ctx, "shared-key")
}
