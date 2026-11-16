package server

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var adminUsernameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)

// verifyAdminCode enforces the second factor when it is enabled. It returns
// false after writing the error response.
func (s *Server) verifyAdminCode(w http.ResponseWriter, r *http.Request, admin *model.Admin, code string) bool {
	if !admin.TOTPEnabled {
		return true
	}
	if strings.TrimSpace(code) == "" {
		writeError(w, http.StatusBadRequest, "verification code is required")
		return false
	}
	if !s.verifySecondFactor(r.Context(), admin, code) {
		writeError(w, http.StatusForbidden, "invalid verification code")
		return false
	}
	return true
}

// rotateAdminSession bumps the token version and returns a fresh token. The
// previous token is revoked immediately when Redis-backed sessions are enabled;
// otherwise it stops working once the version cache refreshes.
func (s *Server) rotateAdminSession(w http.ResponseWriter, r *http.Request, admin *model.Admin) bool {
	version, err := s.store.BumpAdminTokenVersion(r.Context(), admin.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not rotate session")
		return false
	}
	s.setAdminTokenVersion(admin.ID, version)
	admin.TokenVersion = version
	token, expires, err := s.issueToken(admin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return false
	}
	s.sessions.Add(r.Context(), token, expires)
	if old := bearerToken(r); old != "" {
		s.sessions.Delete(r.Context(), old)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "token": token, "expiresAt": expires,
		"admin": map[string]any{"id": admin.ID, "username": admin.Username},
	})
	return true
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	var req struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"currentPassword"`
		Code            string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	username := strings.TrimSpace(req.Username)
	if !adminUsernameRE.MatchString(username) {
		writeError(w, http.StatusBadRequest, "username must be 3-32 characters and may only contain letters, digits, dots, underscores and hyphens")
		return
	}
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.CurrentPassword)) != nil {
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	if !s.verifyAdminCode(w, r, admin, req.Code) {
		return
	}
	if username == admin.Username {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "admin": map[string]any{"id": admin.ID, "username": admin.Username},
		})
		return
	}
	// A consumed recovery code changed the stored state; reload before issuing
	// the new session so it carries the current token version.
	if admin.TOTPEnabled {
		if fresh, err := s.store.GetAdminByID(r.Context(), admin.ID); err == nil {
			admin = fresh
		}
	}
	if err := s.store.UpdateAdminUsername(r.Context(), admin.ID, username); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "username is already taken")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update username")
		return
	}
	admin.Username = username
	s.store.AddEvent(r.Context(), "admin.username_updated", 0, "info", "管理员用户名已更新为 "+username)
	s.rotateAdminSession(w, r, admin)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
		Code        string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.OldPassword)) != nil {
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	if req.NewPassword == req.OldPassword {
		writeError(w, http.StatusBadRequest, "new password must differ from the current password")
		return
	}
	if !s.verifyAdminCode(w, r, admin, req.Code) {
		return
	}
	if admin.TOTPEnabled {
		if fresh, err := s.store.GetAdminByID(r.Context(), admin.ID); err == nil {
			admin = fresh
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := s.store.UpdateAdminPassword(r.Context(), admin.ID, string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update password")
		return
	}
	_ = s.store.SetSetting(r.Context(), "bootstrap_warning", "")
	s.store.AddEvent(r.Context(), "admin.password_updated", 0, "info", "管理员密码已更新")
	s.rotateAdminSession(w, r, admin)
}

// ---- helpers ------------------------------------------------------------
