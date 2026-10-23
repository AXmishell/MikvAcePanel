package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestPortalEndToEnd(t *testing.T) {
	env := newTestEnv(t)

	created := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "portaluser", "username": "Portal User", "password": "portalpass",
		"enabled": true, "trafficLimitGb": 1,
	}))
	userID, _ := created["userId"].(string)
	if userID == "" {
		t.Fatalf("user not created: %+v", created)
	}

	// The login page can discover branding and availability without a token.
	cfg := decode[map[string]any](t, env.do(t, "GET", "/api/portal/config", "", nil))
	if cfg["enabled"] != true || cfg["panelName"] == "" {
		t.Fatalf("portal config = %+v", cfg)
	}

	// Invalid credentials must not create a session.
	bad := env.do(t, "POST", "/api/portal/login", "", map[string]any{"userId": userID, "password": "wrong"})
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login = %d", bad.StatusCode)
	}

	login := decode[map[string]any](t, env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": userID, "password": "portalpass",
	}))
	token, _ := login["token"].(string)
	if token == "" {
		t.Fatalf("no portal token: %+v", login)
	}

	me := decode[map[string]any](t, env.do(t, "GET", "/api/portal/me", token, nil))
	userObj, _ := me["user"].(map[string]any)
	if userObj["userId"] != userID || userObj["status"] != "active" {
		t.Fatalf("me user = %+v", userObj)
	}
	sub, _ := me["subscription"].(map[string]any)
	if url, _ := sub["url"].(string); !strings.Contains(url, "/api/files/") {
		t.Fatalf("subscription = %+v", sub)
	}
	caps, _ := me["capabilities"].(map[string]any)
	if caps["subscription"] != false || caps["password"] != true {
		t.Fatalf("capabilities = %+v", caps)
	}
	meResp := env.do(t, "GET", "/api/portal/me", token, nil)
	if !strings.Contains(meResp.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("portal response must not be cached: %q", meResp.Header.Get("Cache-Control"))
	}
	meResp.Body.Close()

	// Tokens are not interchangeable between the administrator and portal APIs.
	if r := env.do(t, "GET", "/api/portal/me", env.token, nil); r.StatusCode != http.StatusUnauthorized {
		r.Body.Close()
		t.Fatalf("admin token on portal = %d", r.StatusCode)
	}
	if r := env.do(t, "GET", "/api/users", token, nil); r.StatusCode != http.StatusUnauthorized {
		r.Body.Close()
		t.Fatalf("portal token on admin API = %d", r.StatusCode)
	}

	// Password change validates the current password.
	if r := env.do(t, "POST", "/api/portal/password", token, map[string]any{
		"oldPassword": "wrong", "newPassword": "newportalpass",
	}); r.StatusCode != http.StatusForbidden {
		r.Body.Close()
		t.Fatalf("wrong current password = %d", r.StatusCode)
	}
	if r := env.do(t, "POST", "/api/portal/password", token, map[string]any{
		"oldPassword": "portalpass", "newPassword": "newportalpass",
	}); r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("change password = %d", r.StatusCode)
	}
	if r := env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": userID, "password": "portalpass",
	}); r.StatusCode != http.StatusUnauthorized {
		r.Body.Close()
		t.Fatalf("old password still works = %d", r.StatusCode)
	}
	relogin := decode[map[string]any](t, env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": userID, "password": "newportalpass",
	}))
	token, _ = relogin["token"].(string)
	if token == "" {
		t.Fatal("login with new password failed")
	}

	// Device reset is enabled by default; subscription reset is opt-in.
	if r := env.do(t, "POST", "/api/portal/devices/reset", token, nil); r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("reset devices = %d", r.StatusCode)
	}
	if r := env.do(t, "POST", "/api/portal/subscription/reset", token, nil); r.StatusCode != http.StatusForbidden {
		r.Body.Close()
		t.Fatalf("reset subscription should be disabled = %d", r.StatusCode)
	}
	env.do(t, "PUT", "/api/settings", env.token, map[string]any{"portal_allow_sub_reset": true}).Body.Close()
	reset := decode[map[string]any](t, env.do(t, "POST", "/api/portal/subscription/reset", token, nil))
	newURL, _ := reset["url"].(string)
	if newURL == "" || newURL == sub["url"] {
		t.Fatalf("subscription reset = %+v", reset)
	}

	// Logout revokes the portal session when Redis is enabled; without Redis
	// the token remains stateless, so only assert the endpoint succeeds.
	if r := env.do(t, "POST", "/api/portal/logout", token, nil); r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("logout = %d", r.StatusCode)
	}

	// Closing the portal blocks login and is reflected by the config endpoint.
	env.do(t, "PUT", "/api/settings", env.token, map[string]any{"portal_enabled": false}).Body.Close()
	if cfg := decode[map[string]any](t, env.do(t, "GET", "/api/portal/config", "", nil)); cfg["enabled"] != false {
		t.Fatalf("portal should be disabled: %+v", cfg)
	}
	if r := env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": userID, "password": "newportalpass",
	}); r.StatusCode != http.StatusForbidden {
		r.Body.Close()
		t.Fatalf("login while disabled = %d", r.StatusCode)
	}
}

func TestPortalStatusForExpiredUser(t *testing.T) {
	env := newTestEnv(t)
	created := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "expireduser", "password": "portalpass", "enabled": true,
		"expireAt": "2020-01-01T00:00:00Z",
	}))
	userID, _ := created["userId"].(string)
	login := decode[map[string]any](t, env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": userID, "password": "portalpass",
	}))
	token, _ := login["token"].(string)
	if token == "" {
		t.Fatalf("expired user cannot sign in to view status: %+v", login)
	}
	me := decode[map[string]any](t, env.do(t, "GET", "/api/portal/me", token, nil))
	userObj, _ := me["user"].(map[string]any)
	if userObj["status"] != "expired" {
		t.Fatalf("status = %+v", userObj)
	}
}
