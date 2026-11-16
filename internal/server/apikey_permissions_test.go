package server

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestAPIKeyPermissionMatching(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
		method string
		path   string
		want   bool
	}{
		{"read users", []string{"users:read"}, http.MethodGet, "/api/users", true},
		{"write users denied by read", []string{"users:read"}, http.MethodPost, "/api/users", false},
		{"legacy users write no longer implies delete", []string{"users:write"}, http.MethodDelete, "/api/users/9", false},
		{"legacy nodes write no longer reads config", []string{"nodes:write"}, http.MethodGet, "/api/nodes/9/config", false},
		{"fine nodes read cannot read secrets", []string{"nodes:read"}, http.MethodGet, "/api/nodes/9/config", false},
		{"node sync accepts sync scope", []string{"sync:write"}, http.MethodPost, "/api/nodes/9/sync", true},
		{"legacy stats reads dashboard", []string{"stats:read"}, http.MethodGet, "/api/dashboard", true},
		{"legacy stats reads groups", []string{"stats:read"}, http.MethodGet, "/api/groups", true},
		{"logs delete needs dedicated scope", []string{"logs:read"}, http.MethodDelete, "/api/logs", false},
		{"wildcard", []string{"*"}, http.MethodPut, "/api/settings", true},
		{"api keys stay jwt only", []string{"*"}, http.MethodGet, "/api/api-keys", false},
		{"me allowed for any key", []string{"nodes:read"}, http.MethodGet, "/api/me", true},
		{"unknown route denied", []string{"*"}, http.MethodGet, "/api/secret", false},
	}
	for _, tc := range cases {
		if got := apiKeyAllows(tc.scopes, tc.method, tc.path); got != tc.want {
			t.Errorf("%s: apiKeyAllows(%v, %s %s) = %v, want %v", tc.name, tc.scopes, tc.method, tc.path, got, tc.want)
		}
	}
}

func TestAPIKeyResourceRestriction(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	group, err := env.store.CreateGroup(ctx, &model.Group{Name: "team-a"})
	if err != nil {
		t.Fatal(err)
	}
	otherGroup, err := env.store.CreateGroup(ctx, &model.Group{Name: "team-b"})
	if err != nil {
		t.Fatal(err)
	}
	nodeA, err := env.store.CreateNode(ctx, &model.Node{Name: "node-a", Active: true}, "hash-a", "prefix-a", []int64{group.ID})
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := env.store.CreateNode(ctx, &model.Node{Name: "node-b", Active: true}, "hash-b", "prefix-b", []int64{otherGroup.ID})
	if err != nil {
		t.Fatal(err)
	}
	userA, err := env.store.CreateUser(ctx, &model.User{UserID: "user-a", Password: "password1", Enabled: true}, []int64{group.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	userB, err := env.store.CreateUser(ctx, &model.User{UserID: "user-b", Password: "password1", Enabled: true}, []int64{otherGroup.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}

	key := createAPIKeyForTest(t, env, apiKeyRequest{
		Name: "node-reader", Scopes: []string{"nodes:read", "users:read", "groups:read"},
		NodeIDs: []int64{nodeA.ID},
	})

	resp := env.do(t, http.MethodGet, "/api/nodes", key.Token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list nodes: %d", resp.StatusCode)
	}
	nodesResp := decode[struct {
		Items []map[string]any `json:"items"`
	}](t, resp)
	if len(nodesResp.Items) != 1 || int64(nodesResp.Items[0]["id"].(float64)) != nodeA.ID {
		t.Fatalf("expected only node-a, got %+v", nodesResp.Items)
	}

	resp = env.do(t, http.MethodGet, "/api/nodes/"+itoaTest(nodeA.ID), key.Token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get node-a: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodGet, "/api/nodes/"+itoaTest(nodeB.ID), key.Token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("get node-b: got %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	// Global aggregates cannot be filtered safely.
	resp = env.do(t, http.MethodGet, "/api/dashboard", key.Token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("dashboard: got %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	// Users are filtered by group/node relation.
	resp = env.do(t, http.MethodGet, "/api/users", key.Token, nil)
	users := decode[struct {
		Items []map[string]any `json:"items"`
	}](t, resp)
	if len(users.Items) != 1 || int64(users.Items[0]["id"].(float64)) != userA.ID {
		t.Fatalf("expected only user-a, got %+v", users.Items)
	}
	resp = env.do(t, http.MethodGet, "/api/users/"+itoaTest(userB.ID), key.Token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("get user-b: got %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	// Group list is filtered too.
	resp = env.do(t, http.MethodGet, "/api/groups", key.Token, nil)
	groups := decode[struct {
		Items []model.Group `json:"items"`
	}](t, resp)
	if len(groups.Items) != 1 || groups.Items[0].ID != group.ID {
		t.Fatalf("expected only team-a, got %+v", groups.Items)
	}
}

func TestAPIKeyUpdateAndRotate(t *testing.T) {
	env := newTestEnv(t)
	key := createAPIKeyForTest(t, env, apiKeyRequest{
		Name: "rotate-me", Scopes: []string{"nodes:read"}, RateLimit: 60,
	})

	// Update permissions and make the key resource-restricted.
	resp := env.do(t, http.MethodPut, "/api/api-keys/"+itoaTest(key.ID), env.token, map[string]any{
		"name": "rotate-me", "description": "updated", "scopes": []string{"nodes:read", "logs:read"},
		"nodeIds": []int64{}, "groupIds": []int64{}, "rateLimit": 120,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update key: %d", resp.StatusCode)
	}
	updated := decode[struct {
		Key model.APIKey `json:"key"`
	}](t, resp)
	if updated.Key.RateLimit != 120 || updated.Key.Description != "updated" || len(updated.Key.Scopes) != 2 {
		t.Fatalf("unexpected updated key: %+v", updated.Key)
	}

	// Rotating invalidates the old token and returns the new one.
	resp = env.do(t, http.MethodPost, "/api/api-keys/"+itoaTest(key.ID)+"/rotate", env.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate key: %d", resp.StatusCode)
	}
	rotated := decode[struct {
		Token string `json:"token"`
	}](t, resp)
	if rotated.Token == "" || rotated.Token == key.Token {
		t.Fatalf("expected a new token, got %q", rotated.Token)
	}

	resp = env.do(t, http.MethodGet, "/api/me", key.Token, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token: got %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodGet, "/api/me", rotated.Token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("new token: got %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestAPIKeySettingsScopeAndRestriction(t *testing.T) {
	env := newTestEnv(t)
	node, err := env.store.CreateNode(context.Background(), &model.Node{Name: "solo", Active: true}, "solo-hash", "solo-prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	key := createAPIKeyForTest(t, env, apiKeyRequest{Name: "settings", Scopes: []string{"settings:read"}})

	resp := env.do(t, http.MethodGet, "/api/settings", key.Token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unrestricted settings: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Once restricted, global settings are denied even with the scope.
	resp = env.do(t, http.MethodPut, "/api/api-keys/"+itoaTest(key.ID), env.token, map[string]any{
		"name": "settings", "scopes": []string{"settings:read"}, "nodeIds": []int64{node.ID},
		"groupIds": []int64{}, "rateLimit": 60,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restrict key: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodGet, "/api/settings", key.Token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("restricted settings: got %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()
}

type testAPIKey struct {
	*model.APIKey
	Token string
}

func createAPIKeyForTest(t *testing.T, env *testEnv, req apiKeyRequest) testAPIKey {
	t.Helper()
	resp := env.do(t, http.MethodPost, "/api/api-keys", env.token, req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create api key: %d", resp.StatusCode)
	}
	out := decode[struct {
		Key   model.APIKey `json:"key"`
		Token string       `json:"token"`
	}](t, resp)
	return testAPIKey{APIKey: &out.Key, Token: out.Token}
}

func itoaTest(id int64) string {
	return strconv.FormatInt(id, 10)
}
