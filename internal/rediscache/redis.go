// Package rediscache provides a small optional Redis client used by the panel
// for shared sessions, rate limits and cache entries. A nil Client means the
// caller should fall back to its local in-memory implementation.
package rediscache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// ErrUnavailable is returned while the circuit breaker is open.
var ErrUnavailable = errors.New("redis temporarily unavailable")

const breakerCooldown = 15 * time.Second

// Client is a thin wrapper around go-redis with a configurable key prefix and
// a small circuit breaker, so an unreachable Redis does not slow down every
// request.
type Client struct {
	rdb    *goredis.Client
	prefix string

	mu        sync.Mutex
	downUntil time.Time
}

type redisLogger struct{ log *slog.Logger }

func (l redisLogger) Printf(_ context.Context, format string, v ...any) {
	if l.log != nil {
		l.log.Debug("redis: " + fmt.Sprintf(format, v...))
	}
}

// Open connects to Redis. It returns (nil, nil) when rawURL is empty so the
// caller can run without Redis. A non-empty URL that cannot be reached returns
// an error.
func Open(ctx context.Context, rawURL, prefix string, logger *slog.Logger) (*Client, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, nil
	}
	opt, err := goredis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}
	opt.DialTimeout = 5 * time.Second
	opt.ReadTimeout = 3 * time.Second
	opt.WriteTimeout = 3 * time.Second
	opt.MaxRetries = 0
	rdb := goredis.NewClient(opt)
	goredis.SetLogger(redisLogger{log: logger})

	var pingErr error
	for attempt := 0; attempt < 5; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		pingErr = rdb.Ping(pingCtx).Err()
		cancel()
		if pingErr == nil || ctx.Err() != nil {
			break
		}
		if attempt < 4 {
			time.Sleep(time.Second)
		}
	}
	if pingErr != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", pingErr)
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "mikvace:"
	}
	if logger != nil {
		logger.Debug("redis client ready", "addr", opt.Addr, "db", opt.DB, "prefix", prefix)
	}
	return &Client{rdb: rdb, prefix: prefix}, nil
}

// Close releases the underlying connection pool.
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// Ping checks connectivity and also closes/reopens the circuit breaker.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis is not configured")
	}
	return c.record(c.rdb.Ping(ctx).Err())
}

func (c *Client) available() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.downUntil) {
		return ErrUnavailable
	}
	return nil
}

func (c *Client) record(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.downUntil = time.Now().Add(breakerCooldown)
	} else {
		c.downUntil = time.Time{}
	}
	return err
}

// Prefix returns the configured key prefix.
func (c *Client) Prefix() string {
	if c == nil {
		return ""
	}
	return c.prefix
}

func (c *Client) key(name string) string {
	return c.prefix + name
}

// Get returns a cached value and whether it exists.
func (c *Client) Get(ctx context.Context, name string) (string, bool, error) {
	if c == nil || c.rdb == nil {
		return "", false, errors.New("redis is not configured")
	}
	if err := c.available(); err != nil {
		return "", false, err
	}
	v, err := c.rdb.Get(ctx, c.key(name)).Result()
	if errors.Is(err, goredis.Nil) {
		return "", false, c.record(nil)
	}
	if err != nil {
		return "", false, c.record(err)
	}
	return v, true, c.record(nil)
}

// Set stores a value with the given TTL. A non-positive TTL stores without
// expiration.
func (c *Client) Set(ctx context.Context, name, value string, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis is not configured")
	}
	if err := c.available(); err != nil {
		return err
	}
	return c.record(c.rdb.Set(ctx, c.key(name), value, ttl).Err())
}

// Delete removes one or more keys.
func (c *Client) Delete(ctx context.Context, names ...string) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis is not configured")
	}
	if len(names) == 0 {
		return nil
	}
	if err := c.available(); err != nil {
		return err
	}
	keys := make([]string, 0, len(names))
	for _, name := range names {
		keys = append(keys, c.key(name))
	}
	return c.record(c.rdb.Del(ctx, keys...).Err())
}

// Exists reports whether a key exists.
func (c *Client) Exists(ctx context.Context, name string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("redis is not configured")
	}
	if err := c.available(); err != nil {
		return false, err
	}
	n, err := c.rdb.Exists(ctx, c.key(name)).Result()
	if err != nil {
		return false, c.record(err)
	}
	return n > 0, c.record(nil)
}

// Incr atomically increments a counter and applies the TTL. The TTL is
// refreshed on every call, which makes it suitable for fixed-window counters.
func (c *Client) Incr(ctx context.Context, name string, ttl time.Duration) (int64, error) {
	if c == nil || c.rdb == nil {
		return 0, errors.New("redis is not configured")
	}
	if err := c.available(); err != nil {
		return 0, err
	}
	pipe := c.rdb.TxPipeline()
	incr := pipe.Incr(ctx, c.key(name))
	if ttl > 0 {
		pipe.Expire(ctx, c.key(name), ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, c.record(err)
	}
	c.record(nil)
	return incr.Val(), nil
}

// GetInt returns an integer counter, or zero when the key does not exist.
func (c *Client) GetInt(ctx context.Context, name string) (int64, error) {
	if c == nil || c.rdb == nil {
		return 0, errors.New("redis is not configured")
	}
	if err := c.available(); err != nil {
		return 0, err
	}
	v, err := c.rdb.Get(ctx, c.key(name)).Int64()
	if errors.Is(err, goredis.Nil) {
		return 0, c.record(nil)
	}
	if err != nil {
		return 0, c.record(err)
	}
	return v, c.record(nil)
}
