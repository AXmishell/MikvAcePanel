package server

import (
	"net/http"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/axmishell/mikvacepanel/internal/totp"
)

func TestAdminUsernameUpdateRotatesSession(t *testing.T) {
	env := newTestEnv(t)

	// Invalid names and wrong credentials are rejected.
	if resp := env.do(t, "PUT", "/api/admin/account", env.token, map[string]any{
		"username": "x", "currentPassword": "testpass123",
	}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short username status = %d", resp.StatusCode)
	}
	if resp := env.do(t, "PUT", "/api/admin/account", env.token, map[string]any{
		"username": "operator", "currentPassword": "wrong-password",
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong password status = %d", resp.StatusCode)
	}

	// Re-saving the same username must not rotate the session.
	if resp := env.do(t, "PUT", "/api/admin/account", env.token, map[string]any{
		"username": "admin", "currentPassword": "testpass123",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("same username status = %d", resp.StatusCode)
	}
	if resp := env.do(t, "GET", "/api/me", env.token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("session invalidated by a no-op update: %d", resp.StatusCode)
	}

	updated := decode[map[string]any](t, env.do(t, "PUT", "/api/admin/account", env.token, map[string]any{
		"username": "operator", "currentPassword": "testpass123",
	}))
	token, _ := updated["token"].(string)
	if token == "" {
		t.Fatalf("update response = %+v", updated)
	}

	// The old session is revoked and the new one identifies the new name.
	if resp := env.do(t, "GET", "/api/me", env.token, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token still valid: %d", resp.StatusCode)
	}
	me := decode[map[string]any](t, env.do(t, "GET", "/api/me", token, nil))
	admin := me["admin"].(map[string]any)
	if admin["username"] != "operator" {
		t.Fatalf("username = %+v", admin["username"])
	}

	// Login uses the new username, not the old one.
	if resp := env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "testpass123",
	}); resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("old username login status = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := env.loginWithCaptcha(t, "operator", "testpass123"); resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("new username login status = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

func TestAdminUsernameConflict(t *testing.T) {
	env := newTestEnv(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("otherpass123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB.Exec(
		`INSERT INTO admins (username, password_hash, created_at) VALUES (?, ?, ?)`,
		"taken", string(hash), time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}
	if resp := env.do(t, "PUT", "/api/admin/account", env.token, map[string]any{
		"username": "taken", "currentPassword": "testpass123",
	}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate username status = %d", resp.StatusCode)
	}
}

func TestAdminPasswordUpdateRotatesSession(t *testing.T) {
	env := newTestEnv(t)

	if resp := env.do(t, "POST", "/api/change-password", env.token, map[string]any{
		"oldPassword": "testpass123", "newPassword": "short",
	}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short password status = %d", resp.StatusCode)
	}
	if resp := env.do(t, "POST", "/api/change-password", env.token, map[string]any{
		"oldPassword": "testpass123", "newPassword": "testpass123",
	}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unchanged password status = %d", resp.StatusCode)
	}
	if resp := env.do(t, "POST", "/api/change-password", env.token, map[string]any{
		"oldPassword": "wrong-password", "newPassword": "a-new-password-123",
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong password status = %d", resp.StatusCode)
	}

	updated := decode[map[string]any](t, env.do(t, "POST", "/api/change-password", env.token, map[string]any{
		"oldPassword": "testpass123", "newPassword": "a-new-password-123",
	}))
	token, _ := updated["token"].(string)
	if token == "" {
		t.Fatalf("password change response = %+v", updated)
	}
	if resp := env.do(t, "GET", "/api/me", env.token, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token still valid: %d", resp.StatusCode)
	}
	if resp := env.do(t, "GET", "/api/me", token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("new token status = %d", resp.StatusCode)
	}
	if resp := env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "testpass123",
	}); resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("old password login status = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := env.loginWithCaptcha(t, "admin", "a-new-password-123"); resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("new password login status = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

func TestAdminPasswordUpdateRequiresSecondFactor(t *testing.T) {
	env := newTestEnv(t)
	setup := decode[map[string]any](t, env.do(t, "POST", "/api/2fa/setup", env.token, nil))
	secret, _ := setup["secret"].(string)
	code, err := totp.Code(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if resp := env.do(t, "POST", "/api/2fa/enable", env.token, map[string]any{"code": code}); resp.StatusCode != http.StatusOK {
		t.Fatalf("enable status = %d", resp.StatusCode)
	}
	codes, _ := setup["recoveryCodes"].([]any)
	if len(codes) == 0 {
		t.Fatal("missing recovery codes")
	}

	if resp := env.do(t, "POST", "/api/change-password", env.token, map[string]any{
		"oldPassword": "testpass123", "newPassword": "a-new-password-123",
	}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing code status = %d", resp.StatusCode)
	}
	if resp := env.do(t, "POST", "/api/change-password", env.token, map[string]any{
		"oldPassword": "testpass123", "newPassword": "a-new-password-123", "code": "000000",
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong code status = %d", resp.StatusCode)
	}
	updated := decode[map[string]any](t, env.do(t, "POST", "/api/change-password", env.token, map[string]any{
		"oldPassword": "testpass123", "newPassword": "a-new-password-123", "code": codes[0],
	}))
	if updated["token"] == nil {
		t.Fatalf("recovery code password change = %+v", updated)
	}
}
