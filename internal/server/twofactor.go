package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"

	"github.com/axmishell/mikvacepanel/internal/certcrypto"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/totp"
	"github.com/axmishell/mikvacepanel/internal/version"
)

const (
	totpIssuer        = "MikvAce"
	challengeTTL      = 5 * time.Minute
	maxChallengeTries = 5
)

// issueTOTPChallenge creates a short-lived token that can only be used on the
// second login step. It is deliberately not registered as a session, and
// adminAuth rejects any token with a purpose claim.
func (s *Server) issueTOTPChallenge(admin *model.Admin) (string, time.Time, error) {
	expires := time.Now().Add(challengeTTL)
	claims := adminClaims{
		AdminID: admin.ID, Username: admin.Username, Purpose: "2fa",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: admin.Username, IssuedAt: jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expires), Issuer: "mikvace-panel",
			ID: store.RandomToken(16),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	str, err := tok.SignedString(s.secret)
	return str, expires, err
}

func (s *Server) parseChallenge(raw string) (*adminClaims, error) {
	claims := &adminClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil || !tok.Valid {
		return nil, errors.New("invalid or expired challenge")
	}
	if claims.Purpose != "2fa" || claims.ID == "" {
		return nil, errors.New("invalid challenge")
	}
	return claims, nil
}

func (s *Server) challengeState(jti string) (used bool, tries int) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	for id, exp := range s.challengeUsed {
		if now.After(exp) {
			delete(s.challengeUsed, id)
		}
	}
	if until, ok := s.challengeUsed[jti]; ok && now.Before(until) {
		used = true
	}
	return used, s.twoFactorTries[jti]
}

func (s *Server) markChallengeUsed(jti string, expires time.Time) {
	s.loginMu.Lock()
	s.challengeUsed[jti] = expires
	delete(s.twoFactorTries, jti)
	s.loginMu.Unlock()
}

func (s *Server) recordChallengeFailure(jti string) int {
	s.loginMu.Lock()
	if len(s.twoFactorTries) > 1024 {
		s.twoFactorTries = map[string]int{}
	}
	s.twoFactorTries[jti]++
	n := s.twoFactorTries[jti]
	if n > 16 {
		delete(s.twoFactorTries, jti)
	}
	s.loginMu.Unlock()
	return n
}

func (s *Server) decryptTOTPSecret(token string) (string, error) {
	return certcrypto.DecryptString(s.secretKey, token)
}

// verifySecondFactor accepts a current TOTP code or an unused recovery code.
// Recovery codes are consumed on success.
func (s *Server) verifySecondFactor(ctx context.Context, admin *model.Admin, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	if admin.TOTPSecret != "" {
		if secret, err := s.decryptTOTPSecret(admin.TOTPSecret); err == nil {
			if totp.Validate(secret, code, time.Now()) {
				return true
			}
		}
	}
	hash := totp.HashRecovery(s.secret, code)
	for i, stored := range admin.TOTPRecovery {
		if subtle.ConstantTimeCompare([]byte(stored), []byte(hash)) != 1 {
			continue
		}
		remaining := append([]string{}, admin.TOTPRecovery[:i]...)
		remaining = append(remaining, admin.TOTPRecovery[i+1:]...)
		if err := s.store.SetAdminTOTPRecovery(ctx, admin.ID, remaining); err != nil {
			return false
		}
		admin.TOTPRecovery = remaining
		return true
	}
	return false
}

func (s *Server) handleLogin2FA(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginAllowed(r.Context(), "admin", ip) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	claims, err := s.parseChallenge(req.Challenge)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	used, tries := s.challengeState(claims.ID)
	if used {
		writeError(w, http.StatusUnauthorized, "challenge already used")
		return
	}
	if tries >= maxChallengeTries {
		writeError(w, http.StatusTooManyRequests, "too many verification attempts, sign in again")
		return
	}
	admin, err := s.store.GetAdminByID(r.Context(), claims.AdminID)
	if err != nil || !admin.TOTPEnabled || admin.TOTPSecret == "" {
		s.recordLoginFailure(r.Context(), "admin", ip)
		s.recordChallengeFailure(claims.ID)
		writeError(w, http.StatusUnauthorized, "two-factor authentication is not available")
		return
	}
	if !s.verifySecondFactor(r.Context(), admin, req.Code) {
		s.recordLoginFailure(r.Context(), "admin", ip)
		s.recordChallengeFailure(claims.ID)
		writeError(w, http.StatusUnauthorized, "invalid verification code")
		return
	}
	expires := time.Now().Add(challengeTTL)
	if claims.ExpiresAt != nil {
		expires = claims.ExpiresAt.Time
	}
	s.markChallengeUsed(claims.ID, expires)

	token, sessionExpires, err := s.issueToken(admin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	s.sessions.Add(r.Context(), token, sessionExpires)
	s.clearLoginCaptcha(r.Context(), "admin", ip)
	_ = s.store.TouchAdminLogin(r.Context(), admin.ID)
	warning, _ := s.store.GetSetting(r.Context(), "bootstrap_warning")
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token, "expiresAt": sessionExpires,
		"admin":   map[string]any{"id": admin.ID, "username": admin.Username, "twoFactorEnabled": true},
		"warning": warning, "version": version.String(),
	})
}

func adminSessionClaims(w http.ResponseWriter, r *http.Request) *adminClaims {
	c := claimsFrom(r)
	if c == nil {
		writeError(w, http.StatusForbidden, "this endpoint requires an administrator session")
		return nil
	}
	return c
}

func (s *Server) handleTwoFactorStatus(w http.ResponseWriter, r *http.Request) {
	c := adminSessionClaims(w, r)
	if c == nil {
		return
	}
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":           admin.TOTPEnabled,
		"configured":        admin.TOTPSecret != "",
		"recoveryRemaining": len(admin.TOTPRecovery),
	})
}

func (s *Server) handleTwoFactorSetup(w http.ResponseWriter, r *http.Request) {
	c := adminSessionClaims(w, r)
	if c == nil {
		return
	}
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	if admin.TOTPEnabled {
		writeError(w, http.StatusBadRequest, "two-factor authentication is already enabled")
		return
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate secret")
		return
	}
	codes, err := totp.GenerateRecoveryCodes(10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate recovery codes")
		return
	}
	cipher, err := certcrypto.EncryptString(s.secretKey, secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not protect secret")
		return
	}
	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		hashes = append(hashes, totp.HashRecovery(s.secret, code))
	}
	if err := s.store.SetAdminTOTPPending(r.Context(), admin.ID, cipher, hashes); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	uri := totp.OTPAuthURL(totpIssuer, admin.Username, secret)
	png, err := qrcode.Encode(uri, qrcode.Medium, 220)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not render QR code")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"secret": secret, "otpauthUrl": uri,
		"qr":            "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		"recoveryCodes": codes,
	})
}

func (s *Server) handleTwoFactorEnable(w http.ResponseWriter, r *http.Request) {
	c := adminSessionClaims(w, r)
	if c == nil {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	if admin.TOTPEnabled {
		writeError(w, http.StatusBadRequest, "two-factor authentication is already enabled")
		return
	}
	if admin.TOTPPending == "" {
		writeError(w, http.StatusBadRequest, "start two-factor setup first")
		return
	}
	secret, err := s.decryptTOTPSecret(admin.TOTPPending)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stored secret is unreadable")
		return
	}
	if !totp.Validate(secret, strings.TrimSpace(req.Code), time.Now()) {
		writeError(w, http.StatusBadRequest, "invalid verification code")
		return
	}
	if err := s.store.EnableAdminTOTP(r.Context(), admin.ID, admin.TOTPPending, admin.TOTPPendingRecovery); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.AddEvent(r.Context(), "admin.2fa_enabled", 0, "info", "管理员已启用两步验证")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTwoFactorDisable(w http.ResponseWriter, r *http.Request) {
	c := adminSessionClaims(w, r)
	if c == nil {
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	if !admin.TOTPEnabled {
		writeError(w, http.StatusBadRequest, "two-factor authentication is not enabled")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "password is incorrect")
		return
	}
	if !s.verifySecondFactor(r.Context(), admin, req.Code) {
		writeError(w, http.StatusUnauthorized, "invalid verification code")
		return
	}
	if err := s.store.DisableAdminTOTP(r.Context(), admin.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.AddEvent(r.Context(), "admin.2fa_disabled", 0, "warning", "管理员关闭了两步验证")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTwoFactorRecovery(w http.ResponseWriter, r *http.Request) {
	c := adminSessionClaims(w, r)
	if c == nil {
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	if !admin.TOTPEnabled {
		writeError(w, http.StatusBadRequest, "two-factor authentication is not enabled")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "password is incorrect")
		return
	}
	if !s.verifySecondFactor(r.Context(), admin, req.Code) {
		writeError(w, http.StatusUnauthorized, "invalid verification code")
		return
	}
	codes, err := totp.GenerateRecoveryCodes(10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate recovery codes")
		return
	}
	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		hashes = append(hashes, totp.HashRecovery(s.secret, code))
	}
	if err := s.store.SetAdminTOTPRecovery(r.Context(), admin.ID, hashes); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}
