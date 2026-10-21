package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/axmishell/mikvacepanel/internal/rediscache"
)

// sessionStore keeps administrator session IDs in Redis when it is enabled.
// The local map is an L1 cache so normal requests do not hit Redis and a
// valid JWT keeps working if Redis becomes temporarily unavailable.
type sessionStore struct {
	redis *rediscache.Client
	ttl   time.Duration
	log   *slog.Logger

	mu    sync.Mutex
	local map[string]time.Time
}

func newSessionStore(redis *rediscache.Client, ttl time.Duration, logger *slog.Logger) *sessionStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &sessionStore{redis: redis, ttl: ttl, log: logger, local: map[string]time.Time{}}
}

// Enabled reports whether sessions are enforced. Without Redis the panel keeps
// its original stateless JWT behavior.
func (s *sessionStore) Enabled() bool { return s != nil && s.redis != nil }

func sessionID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *sessionStore) Add(ctx context.Context, token string, expires time.Time) {
	if !s.Enabled() {
		return
	}
	id := sessionID(token)
	s.putLocal(id, expires)
	ttl := time.Until(expires)
	if ttl <= 0 {
		return
	}
	if err := s.redis.Set(ctx, "session:"+id, "1", ttl); err != nil && s.log != nil {
		s.log.Warn("could not persist session in redis", "error", err)
	}
}

// Valid checks a session. A Redis error falls back to the JWT validity, which
// matches the behavior when Redis is not configured.
func (s *sessionStore) Valid(ctx context.Context, token string, expires time.Time) bool {
	if !s.Enabled() {
		return true
	}
	id := sessionID(token)
	if s.validLocal(id) {
		return true
	}
	ok, err := s.redis.Exists(ctx, "session:"+id)
	if err != nil {
		if s.log != nil {
			if errors.Is(err, rediscache.ErrUnavailable) {
				s.log.Debug("redis session check unavailable", "error", err)
			} else {
				s.log.Warn("redis session check failed", "error", err)
			}
		}
		// Cache the fail-open decision briefly so an unreachable Redis does
		// not add a network timeout to every request.
		s.putLocal(id, expires)
		return true
	}
	if !ok {
		return false
	}
	s.putLocal(id, expires)
	return true
}

func (s *sessionStore) Delete(ctx context.Context, token string) {
	if !s.Enabled() {
		return
	}
	id := sessionID(token)
	s.mu.Lock()
	delete(s.local, id)
	s.mu.Unlock()
	if err := s.redis.Delete(ctx, "session:"+id); err != nil && s.log != nil {
		s.log.Warn("could not delete session from redis", "error", err)
	}
}

func (s *sessionStore) validLocal(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.local[id]
	if !ok {
		return false
	}
	if time.Now().After(expires) {
		delete(s.local, id)
		return false
	}
	return true
}

// sessionCacheTTL bounds how long a local process trusts a Redis session
// without re-checking it. Keeping it short lets logout/revocation propagate
// across panel instances while avoiding a Redis round trip on every request.
const sessionCacheTTL = 10 * time.Second

func (s *sessionStore) putLocal(id string, expires time.Time) {
	cacheUntil := time.Now().Add(sessionCacheTTL)
	if expires.Before(cacheUntil) {
		cacheUntil = expires
	}
	if !cacheUntil.After(time.Now()) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.local) > 512 {
		now := time.Now()
		for key, exp := range s.local {
			if now.After(exp) {
				delete(s.local, key)
			}
		}
	}
	s.local[id] = cacheUntil
}
