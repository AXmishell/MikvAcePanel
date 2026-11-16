package server

import (
	"net/http"
	"strings"
	"testing"
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

func TestUserPasswordAndSubscriptionToken(t *testing.T) {
	env := newTestEnv(t)

	created := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "dual", "username": "Dual", "password": "connpass1",
		"enabled": true,
	}))
	userID := int64(created["id"].(float64))
	oldToken, _ := created["subscriptionToken"].(string)

	decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "Dual Node", "type": "hysteria", "cores": []string{"hysteria"},
		"ip": "203.0.113.11", "port": 443, "tlsMode": "selfsigned", "active": true,
	}))

	conn := decode[map[string]any](t, env.do(t, "GET", "/api/users/"+itoa(userID)+"/connections", env.token, nil))
	if uri := connectionURI(t, conn); !strings.Contains(uri, ":connpass1@") {
		t.Fatalf("connection URI does not use the connection password: %s", uri)
	}

	// The administrator can change the connection password in one update.
	updated := decode[map[string]any](t, env.do(t, "PUT", "/api/users/"+itoa(userID), env.token, map[string]any{
		"userId": "dual", "username": "Dual", "password": "connpass3",
	}))
	if updated["id"] == nil {
		t.Fatalf("update user = %+v", updated)
	}
	conn = decode[map[string]any](t, env.do(t, "GET", "/api/users/"+itoa(userID)+"/connections", env.token, nil))
	if uri := connectionURI(t, conn); !strings.Contains(uri, ":connpass3@") {
		t.Fatalf("admin connection password not applied: %s", uri)
	}

	// Resetting the password returns a one-time plaintext value.
	reset := decode[map[string]any](t, env.do(t, "POST", "/api/users/"+itoa(userID)+"/reset-password", env.token, map[string]any{}))
	if pw, _ := reset["password"].(string); pw == "" {
		t.Fatalf("reset password response = %+v", reset)
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
