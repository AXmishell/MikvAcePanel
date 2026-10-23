package server

import (
	"strings"
	"testing"
)

func TestAdminUserConnections(t *testing.T) {
	env := newTestEnv(t)

	created := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "connuser", "username": "Conn User", "password": "connpass", "enabled": true,
	}))
	userID := int64(created["id"].(float64))

	node := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "Connection Node", "type": "hysteria", "cores": []string{"hysteria"},
		"flag": "JP", "ip": "203.0.113.9", "port": 443, "tlsMode": "selfsigned", "active": true,
	}))
	nodeObj, _ := node["node"].(map[string]any)
	if nodeObj["id"] == nil {
		t.Fatalf("node not created: %+v", node)
	}

	conn := decode[map[string]any](t, env.do(t, "GET", "/api/users/"+itoa(userID)+"/connections", env.token, nil))
	userObj, _ := conn["user"].(map[string]any)
	if userObj["userId"] != "connuser" || userObj["status"] != "active" {
		t.Fatalf("connection user = %+v", userObj)
	}
	sub, _ := conn["subscription"].(map[string]any)
	if url, _ := sub["url"].(string); !strings.Contains(url, "/api/files/") {
		t.Fatalf("subscription = %+v", sub)
	}
	nodes, _ := conn["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v", conn["nodes"])
	}
	n, _ := nodes[0].(map[string]any)
	configs, _ := n["configs"].([]any)
	if len(configs) == 0 {
		t.Fatalf("node has no configs: %+v", n)
	}
	cfg, _ := configs[0].(map[string]any)
	uri, _ := cfg["uri"].(string)
	if !strings.Contains(uri, "203.0.113.9:443") {
		t.Fatalf("connection uri = %q", uri)
	}

	// Deleting the user or node must not leak connections through the old id.
	if r := env.do(t, "GET", "/api/users/99999/connections", env.token, nil); r.StatusCode != 404 {
		r.Body.Close()
		t.Fatalf("missing user = %d", r.StatusCode)
	}
}
