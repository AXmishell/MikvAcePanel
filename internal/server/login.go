package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/version"
	"golang.org/x/crypto/bcrypt"
)

func (s *Server) loginAllowed(ctx context.Context, scope, ip string) bool {
	if s.redis != nil {
		window := time.Now().Unix() / 60
		n, err := s.redis.GetInt(ctx, fmt.Sprintf("ratelimit:login:%s:%s:%d", scope, ip, window))
		if err == nil {
			return n < 10
		}
		s.logRedisError("redis login rate limit failed, falling back to memory", err)
	}
	key := scope + "|" + ip
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	nowT := time.Now()
	tries := s.loginTries[key]
	kept := tries[:0]
	for _, t := range tries {
		if nowT.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(s.loginTries, key)
	} else {
		s.loginTries[key] = kept
	}
	s.pruneLoginTriesLocked(nowT)
	return len(kept) < 10
}

// pruneLoginTriesLocked keeps the fallback limiter from growing without bound.
// Callers must hold loginMu.
func (s *Server) pruneLoginTriesLocked(nowT time.Time) {
	if len(s.loginTries) <= 1024 {
		return
	}
	for key, tries := range s.loginTries {
		fresh := false
		for _, t := range tries {
			if nowT.Sub(t) < time.Minute {
				fresh = true
				break
			}
		}
		if !fresh {
			delete(s.loginTries, key)
		}
	}
}

func (s *Server) recordLoginFailure(ctx context.Context, scope, ip string) {
	if s.redis != nil {
		window := time.Now().Unix() / 60
		name := fmt.Sprintf("ratelimit:login:%s:%s:%d", scope, ip, window)
		_, err := s.redis.Incr(ctx, name, 2*time.Minute)
		if err == nil {
			return
		}
		s.logRedisError("redis login rate limit failed, falling back to memory", err)
	}
	key := scope + "|" + ip
	s.loginMu.Lock()
	s.loginTries[key] = append(s.loginTries[key], time.Now())
	s.loginMu.Unlock()
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginAllowed(r.Context(), "admin", ip) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}
	var req struct {
		Username      string `json:"username"`
		Password      string `json:"password"`
		CaptchaID     string `json:"captchaId"`
		CaptchaAnswer string `json:"captchaAnswer"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	// The first attempt is password-only. Once a login fails, every further
	// attempt from this client must also solve an image captcha.
	if s.loginCaptchaRequired(r.Context(), "admin", ip) {
		if !s.verifyLoginCaptcha(r.Context(), req.CaptchaID, req.CaptchaAnswer) {
			s.recordLoginFailure(r.Context(), "admin", ip)
			s.requireLoginCaptcha(r.Context(), "admin", ip)
			s.writeCaptchaChallenge(w, r, http.StatusUnauthorized, "invalid captcha")
			return
		}
	}
	admin, err := s.store.GetAdminByUsername(r.Context(), strings.TrimSpace(req.Username))
	if err != nil {
		s.recordLoginFailure(r.Context(), "admin", ip)
		s.requireLoginCaptcha(r.Context(), "admin", ip)
		s.writeCaptchaChallenge(w, r, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
		s.recordLoginFailure(r.Context(), "admin", ip)
		s.requireLoginCaptcha(r.Context(), "admin", ip)
		s.writeCaptchaChallenge(w, r, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.clearLoginCaptcha(r.Context(), "admin", ip)
	if admin.TOTPEnabled {
		challenge, expires, err := s.issueTOTPChallenge(admin)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not create challenge")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"twoFactorRequired": true,
			"challenge":         challenge,
			"expiresAt":         expires,
		})
		return
	}
	token, expires, err := s.issueToken(admin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	s.sessions.Add(r.Context(), token, expires)
	_ = s.store.TouchAdminLogin(r.Context(), admin.ID)
	warning, _ := s.store.GetSetting(r.Context(), "bootstrap_warning")
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token, "expiresAt": expires,
		"admin":   map[string]any{"id": admin.ID, "username": admin.Username},
		"warning": warning, "version": version.String(),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if raw := bearerToken(r); raw != "" {
		s.sessions.Delete(r.Context(), raw)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if key := apiKeyFrom(r); key != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"apiKey": map[string]any{
				"id": key.ID, "name": key.Name, "description": key.Description,
				"scopes": key.Scopes, "nodeIds": key.NodeIDs, "groupIds": key.GroupIDs,
				"rateLimit": key.RateLimit, "expiresAt": key.ExpiresAt,
			},
			"version": version.String(),
		})
		return
	}
	c := claimsFrom(r)
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	warning, _ := s.store.GetSetting(r.Context(), "bootstrap_warning")
	writeJSON(w, http.StatusOK, map[string]any{
		"admin": map[string]any{
			"id": admin.ID, "username": admin.Username, "createdAt": admin.CreatedAt,
			"lastLoginAt": admin.LastLoginAt, "twoFactorEnabled": admin.TOTPEnabled,
		},
		"warning": warning, "version": version.String(),
	})
}
