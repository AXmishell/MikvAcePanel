// Package cache provides a small two-level cache used for expensive,
// recomputable panel data. The local map is always present; when Redis is
// configured it becomes a shared L2 so several panel instances reuse the same
// analysis results.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/axmishell/mikvacepanel/internal/rediscache"
)

const (
	// DefaultMaxEntries bounds the local map. Redis itself is unbounded except
	// for per-key TTL, so the local layer only holds hot entries.
	DefaultMaxEntries = 64
	// DefaultL1TTL keeps the local copy short so another instance's
	// invalidation or a Redis restart becomes visible quickly.
	DefaultL1TTL = 5 * time.Second
	// keyPrefix namespaces cache entries inside the configured Redis prefix.
	keyPrefix = "cache:v1:"
)

// Stats is a point-in-time cache snapshot for health and the admin UI.
type Stats struct {
	Backend string  `json:"backend"`
	Entries int     `json:"entries"`
	Hits    uint64  `json:"hits"`
	Misses  uint64  `json:"misses"`
	Sets    uint64  `json:"sets"`
	Deletes uint64  `json:"deletes"`
	Errors  uint64  `json:"errors"`
	HitRate float64 `json:"hitRate"`
}

type entry struct {
	data    []byte
	expires time.Time
}

// Store is a local LRU-ish cache with an optional Redis L2.
type Store struct {
	redis *rediscache.Client
	l1TTL time.Duration
	max   int

	mu      sync.Mutex
	entries map[string]entry

	hits    atomic.Uint64
	misses  atomic.Uint64
	sets    atomic.Uint64
	deletes atomic.Uint64
	errs    atomic.Uint64
}

// New creates a cache. A nil Redis client keeps the local-only behavior, which
// is what existing single-process deployments had before Redis support.
func New(redis *rediscache.Client, max int, l1TTL time.Duration) *Store {
	if max <= 0 {
		max = DefaultMaxEntries
	}
	if l1TTL <= 0 {
		l1TTL = DefaultL1TTL
	}
	return &Store{redis: redis, l1TTL: l1TTL, max: max, entries: map[string]entry{}}
}

// Backend reports where entries are stored.
func (c *Store) Backend() string {
	if c == nil {
		return "disabled"
	}
	if c.redis != nil {
		return "redis"
	}
	return "memory"
}

func redisKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return keyPrefix + hex.EncodeToString(sum[:])
}

// Get decodes a cached JSON value into dst. It returns false on a miss, an
// expired entry, or any decode/Redis error; callers then recompute.
func (c *Store) Get(ctx context.Context, key string, dst any) bool {
	if c == nil {
		return false
	}
	now := time.Now()
	if raw, ok := c.getLocal(key, now); ok {
		if err := json.Unmarshal(raw, dst); err == nil {
			c.hits.Add(1)
			return true
		}
		c.errs.Add(1)
		c.deleteLocal(key)
	}
	if c.redis != nil {
		raw, found, err := c.redis.Get(ctx, redisKey(key))
		if err != nil {
			c.errs.Add(1)
		} else if found {
			if err := json.Unmarshal([]byte(raw), dst); err == nil {
				c.putLocal(key, []byte(raw), now.Add(c.l1TTL))
				c.hits.Add(1)
				return true
			}
			c.errs.Add(1)
		}
	}
	c.misses.Add(1)
	return false
}

// Set stores a JSON value locally and, when configured, in Redis.
func (c *Store) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if c == nil || ttl <= 0 {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		c.errs.Add(1)
		return err
	}
	localTTL := ttl
	if c.redis != nil && c.l1TTL < localTTL {
		localTTL = c.l1TTL
	}
	c.putLocal(key, raw, time.Now().Add(localTTL))
	c.sets.Add(1)
	if c.redis != nil {
		if err := c.redis.Set(ctx, redisKey(key), string(raw), ttl); err != nil {
			c.errs.Add(1)
			return err
		}
	}
	return nil
}

// Delete removes keys from both layers.
func (c *Store) Delete(ctx context.Context, keys ...string) {
	if c == nil || len(keys) == 0 {
		return
	}
	for _, key := range keys {
		c.deleteLocal(key)
	}
	c.deletes.Add(uint64(len(keys)))
	if c.redis != nil {
		names := make([]string, len(keys))
		for i, key := range keys {
			names[i] = redisKey(key)
		}
		if err := c.redis.Delete(ctx, names...); err != nil {
			c.errs.Add(1)
		}
	}
}

// Stats returns a consistent snapshot. HitRate is hits/(hits+misses).
func (c *Store) Stats() Stats {
	if c == nil {
		return Stats{Backend: "disabled"}
	}
	c.mu.Lock()
	entries := len(c.entries)
	c.mu.Unlock()
	hits, misses := c.hits.Load(), c.misses.Load()
	rate := 0.0
	if total := hits + misses; total > 0 {
		rate = float64(hits) / float64(total)
	}
	return Stats{
		Backend: c.Backend(),
		Entries: entries,
		Hits:    hits,
		Misses:  misses,
		Sets:    c.sets.Load(),
		Deletes: c.deletes.Load(),
		Errors:  c.errs.Load(),
		HitRate: rate,
	}
}

func (c *Store) getLocal(key string, now time.Time) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !now.Before(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return e.data, true
}

func (c *Store) putLocal(key string, data []byte, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok && len(c.entries) >= c.max {
		var oldestKey string
		var oldest time.Time
		for k, e := range c.entries {
			if oldestKey == "" || e.expires.Before(oldest) {
				oldestKey, oldest = k, e.expires
			}
		}
		if oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}
	c.entries[key] = entry{data: data, expires: expires}
}

func (c *Store) deleteLocal(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}
