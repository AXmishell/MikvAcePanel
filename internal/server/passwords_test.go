package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func connectionURI(t *testing.T, conn map[string]any) string {
	t.Helper()
	nodes, _ := conn["nodes"].([]any)
	for _, raw := range nodes {
		n, _ := raw.(map[string]any)
		configs, _ := n["configs"].([]any)
		for _, craw := range configs {
			c, _ := craw.(map[string]any)
			if uri, _ := c["uri"].(string); uri != "" {
				return uri
			}
		}
	}
	t.Fatalf("no connection URI in %+v", conn)
	return ""
}

func TestDualPasswordsAndSubscriptionToken(t *testing.T) {
	env := newTestEnv(t)

	created := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "dual", "username": "Dual", "password": "connpass1", "loginPassword": "loginpass1",
		"enabled": true,
	}))
	userID := int64(created["id"].(float64))
	if created["loginPassword"] != "loginpass1" {
		t.Fatalf("create response loginPassword = %+v", created["loginPassword"])
	}
	oldToken, _ := created["subscriptionToken"].(string)

	decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "Dual Node", "type": "hysteria", "cores": []string{"hysteria"},
		"ip": "203.0.113.11", "port": 443, "tlsMode": "selfsigned", "active": true,
	}))

	// Portal login uses the login password, not the connection password.
	login := decode[map[string]any](t, env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": "dual", "password": "loginpass1",
	}))
	token, _ := login["token"].(string)
	if token == "" {
		t.Fatalf("login with login password failed: %+v", login)
	}
	if r := env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": "dual", "password": "connpass1",
	}); r.StatusCode != http.StatusUnauthorized {
		r.Body.Close()
		t.Fatalf("connection password must not log in: %d", r.StatusCode)
	}

	// Changing the login password does not touch the connection password.
	if r := env.do(t, "POST", "/api/portal/password", token, map[string]any{
		"oldPassword": "loginpass1", "newPassword": "loginpass2",
	}); r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("change login password = %d", r.StatusCode)
	}
	conn := decode[map[string]any](t, env.do(t, "GET", "/api/users/"+itoa(userID)+"/connections", env.token, nil))
	if uri := connectionURI(t, conn); !strings.Contains(uri, ":connpass1@") {
		t.Fatalf("login password change leaked into connection URI: %s", uri)
	}

	// Changing the connection password requires the login password.
	if r := env.do(t, "POST", "/api/portal/connection-password", token, map[string]any{
		"currentPassword": "wrong", "newPassword": "connpass2",
	}); r.StatusCode != http.StatusForbidden {
		r.Body.Close()
		t.Fatalf("wrong login password = %d", r.StatusCode)
	}
	if r := env.do(t, "POST", "/api/portal/connection-password", token, map[string]any{
		"currentPassword": "loginpass2", "newPassword": "connpass2",
	}); r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("change connection password = %d", r.StatusCode)
	}
	conn = decode[map[string]any](t, env.do(t, "GET", "/api/users/"+itoa(userID)+"/connections", env.token, nil))
	if uri := connectionURI(t, conn); !strings.Contains(uri, ":connpass2@") {
		t.Fatalf("connection URI not updated: %s", uri)
	}
	// The login password still works after changing only the connection one.
	if login := decode[map[string]any](t, env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": "dual", "password": "loginpass2",
	})); login["token"] == nil {
		t.Fatalf("login password changed unexpectedly: %+v", login)
	}

	// The administrator can change both passwords in one update.
	updated := decode[map[string]any](t, env.do(t, "PUT", "/api/users/"+itoa(userID), env.token, map[string]any{
		"userId": "dual", "username": "Dual", "password": "connpass3", "loginPassword": "loginpass3",
	}))
	if updated["id"] == nil {
		t.Fatalf("update user = %+v", updated)
	}
	if login := decode[map[string]any](t, env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": "dual", "password": "loginpass3",
	})); login["token"] == nil {
		t.Fatalf("admin login password not applied: %+v", login)
	}
	conn = decode[map[string]any](t, env.do(t, "GET", "/api/users/"+itoa(userID)+"/connections", env.token, nil))
	if uri := connectionURI(t, conn); !strings.Contains(uri, ":connpass3@") {
		t.Fatalf("admin connection password not applied: %s", uri)
	}

	// The administrator can set the subscription path token manually.
	updated = decode[map[string]any](t, env.do(t, "PUT", "/api/users/"+itoa(userID), env.token, map[string]any{
		"userId": "dual", "username": "Dual", "subscriptionToken": "customtoken456",
	}))
	if url, _ := updated["subscriptionUrl"].(string); !strings.Contains(url, "/api/files/customtoken456") {
		t.Fatalf("custom subscription token = %q", url)
	}
	if r := env.do(t, "GET", "/api/files/"+oldToken+"?format=uri", "", nil); r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("old subscription token still works: %d", r.StatusCode)
	}
	if r := env.do(t, "GET", "/api/files/customtoken456?format=uri", "", nil); r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("new subscription token = %d", r.StatusCode)
	}

	// Token shape and uniqueness are enforced.
	if r := env.do(t, "PUT", "/api/users/"+itoa(userID), env.token, map[string]any{
		"userId": "dual", "username": "Dual", "subscriptionToken": "bad token!",
	}); r.StatusCode != http.StatusBadRequest {
		r.Body.Close()
		t.Fatalf("invalid token = %d", r.StatusCode)
	}
	decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "dual2", "username": "Dual 2", "password": "connpass9", "subscriptionToken": "othertoken789",
	}))
	if r := env.do(t, "PUT", "/api/users/"+itoa(userID), env.token, map[string]any{
		"userId": "dual", "username": "Dual", "subscriptionToken": "othertoken789",
	}); r.StatusCode != http.StatusConflict {
		r.Body.Close()
		t.Fatalf("duplicate token = %d", r.StatusCode)
	}
}

func TestLegacyPortalPasswordFallback(t *testing.T) {
	env := newTestEnv(t)
	legacy, err := env.store.CreateUser(context.Background(), &model.User{
		UserID: "legacy", Username: "Legacy", Password: "legacypass", Enabled: true,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.LoginPasswordHash != "" {
		t.Fatal("legacy user should start without a login hash")
	}
	login := decode[map[string]any](t, env.do(t, "POST", "/api/portal/login", "", map[string]any{
		"userId": "legacy", "password": "legacypass",
	}))
	if login["token"] == nil {
		t.Fatalf("legacy fallback login failed: %+v", login)
	}
	// The first successful login migrates the account to a bcrypt hash.
	fresh, err := env.store.GetUserByID(context.Background(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.LoginPasswordHash == "" {
		t.Fatal("legacy login hash was not migrated")
	}
}
