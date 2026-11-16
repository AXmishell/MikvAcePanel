package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/rediscache"
	"github.com/golang-jwt/jwt/v5"
)

// ---- authentication -----------------------------------------------------

type adminClaims struct {
	AdminID      int64  `json:"aid"`
	Username     string `json:"username"`
	TokenVersion int    `json:"tv,omitempty"`
	Purpose      string `json:"purpose,omitempty"`
	jwt.RegisteredClaims
}

type ctxKey string

const (
	claimsKey      ctxKey = "claims"
	apiKeyKey      ctxKey = "apiKey"
	apiKeyScopeKey ctxKey = "apiKeyScope"
)

func (s *Server) issueToken(a *model.Admin) (string, time.Time, error) {
	expires := time.Now().Add(s.cfg.SessionTTL)
	claims := adminClaims{
		AdminID: a.ID, Username: a.Username, TokenVersion: a.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: a.Username, IssuedAt: jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expires), Issuer: "mikvace-panel",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	str, err := tok.SignedString(s.secret)
	return str, expires, err
}

func (s *Server) adminTokenVersion(ctx context.Context, id int64) (int, error) {
	s.adminVerMu.Lock()
	if e, ok := s.adminVer[id]; ok && time.Now().Before(e.expires) {
		s.adminVerMu.Unlock()
		return e.version, nil
	}
	s.adminVerMu.Unlock()

	version, err := s.store.GetAdminTokenVersion(ctx, id)
	if err != nil {
		return 0, err
	}
	s.setAdminTokenVersion(id, version)
	return version, nil
}

func (s *Server) setAdminTokenVersion(id int64, version int) {
	s.adminVerMu.Lock()
	s.adminVer[id] = adminVersionEntry{version: version, expires: time.Now().Add(adminVersionTTL)}
	s.adminVerMu.Unlock()
}

func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		// Check the optional entrance before authentication so an anonymous
		// request cannot distinguish a hidden admin endpoint (404) from one
		// that requires a session (401). API keys are exempt by design.
		if !strings.HasPrefix(raw, "ck_") && !s.adminEntryAllowed(r) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if strings.HasPrefix(raw, "ck_") {
			sum := sha256.Sum256([]byte(raw))
			key, err := s.store.GetAPIKeyByHash(r.Context(), hex.EncodeToString(sum[:]))
			if err != nil || !key.Enabled || (key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now())) {
				writeError(w, http.StatusUnauthorized, "invalid or expired API key")
				return
			}
			limit := key.RateLimit
			if limit <= 0 {
				limit = 60
			}
			remaining, allowed := s.checkAPIKeyRate(r.Context(), key.ID, limit)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			if !allowed {
				w.Header().Set("Retry-After", "60")
				writeError(w, http.StatusTooManyRequests, "API key rate limit exceeded")
				return
			}
			if !apiKeyAllows(key.Scopes, r.Method, r.URL.Path) {
				writeError(w, http.StatusForbidden, "API key lacks the required scope")
				return
			}
			scope, err := s.resolveAPIKeyScope(r.Context(), key)
			if err != nil {
				s.log.Warn("resolve api key resource scope failed", "error", err)
				writeError(w, http.StatusInternalServerError, "failed to resolve API key scope")
				return
			}
			ctx := context.WithValue(r.Context(), apiKeyKey, key)
			ctx = context.WithValue(ctx, apiKeyScopeKey, scope)
			r = r.WithContext(ctx)
			if denied, err := s.apiKeyResourceDenied(r, key, scope); err != nil {
				s.log.Warn("api key resource check failed", "error", err)
				writeError(w, http.StatusInternalServerError, "failed to verify API key resource scope")
				return
			} else if denied {
				writeError(w, http.StatusForbidden, "API key is restricted to other resources")
				return
			}
			go s.store.TouchAPIKey(context.Background(), key.ID, s.clientIP(r))
			next.ServeHTTP(w, r)
			return
		}
		claims := &adminClaims{}
		tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, errors.New("unexpected signing method")
			}
			return s.secret, nil
		})
		if err != nil || !tok.Valid {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		if claims.Purpose != "" {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		sessionExpires := time.Now().Add(s.cfg.SessionTTL)
		if claims.ExpiresAt != nil {
			sessionExpires = claims.ExpiresAt.Time
		}
		if !s.sessions.Valid(r.Context(), raw, sessionExpires) {
			writeError(w, http.StatusUnauthorized, "session expired or revoked")
			return
		}
		version, err := s.adminTokenVersion(r.Context(), claims.AdminID)
		if err != nil || claims.TokenVersion != version {
			writeError(w, http.StatusUnauthorized, "session expired or revoked")
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) logRedisError(msg string, err error) {
	if errors.Is(err, rediscache.ErrUnavailable) {
		s.log.Debug(msg, "error", err)
		return
	}
	s.log.Warn(msg, "error", err)
}

func (s *Server) checkAPIKeyRate(ctx context.Context, keyID int64, limit int) (int, bool) {
	if s.redis != nil {
		window := time.Now().Unix() / 60
		name := fmt.Sprintf("ratelimit:api:%d:%d", keyID, window)
		n, err := s.redis.Incr(ctx, name, 2*time.Minute)
		if err == nil {
			remaining := limit - int(n)
			if remaining < 0 {
				remaining = 0
			}
			return remaining, n <= int64(limit)
		}
		s.logRedisError("redis api key rate limit failed, falling back to memory", err)
	}
	s.apiKeyMu.Lock()
	defer s.apiKeyMu.Unlock()
	nowT := time.Now()
	cutoff := nowT.Add(-time.Minute)
	tries := s.apiKeyRate[keyID]
	kept := tries[:0]
	for _, t := range tries {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		s.apiKeyRate[keyID] = kept
		return 0, false
	}
	kept = append(kept, nowT)
	s.apiKeyRate[keyID] = kept
	return limit - len(kept), true
}

func apiKeyFrom(r *http.Request) *model.APIKey {
	k, _ := r.Context().Value(apiKeyKey).(*model.APIKey)
	return k
}

func claimsFrom(r *http.Request) *adminClaims {
	c, _ := r.Context().Value(claimsKey).(*adminClaims)
	return c
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k
	}
	return ""
}

// loginAllowed reports whether scope+ip may attempt a login. The scope keeps
// independent limiters for different login surfaces, so a noisy client cannot
// lock operators out of the panel.
