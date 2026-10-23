package server

import (
	"strings"
	"testing"
)

func TestNodeHy2UpdateChangesConfig(t *testing.T) {
	env := newTestEnv(t)

	payload := map[string]any{
		"name": "Tuning Node", "type": "hysteria", "cores": []string{"hysteria"},
		"ip": "203.0.113.30", "port": 443, "tlsMode": "selfsigned", "active": true,
		"hy2": map[string]any{
			"congestion": map[string]any{"type": "bbr", "bbrProfile": "aggressive"},
			"sniff":      map[string]any{"enable": true, "tcpPorts": "80,443"},
		},
	}
	created := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, payload))
	nodeObj := created["node"].(map[string]any)
	nodeID := int64(nodeObj["id"].(float64))
	if created["configChanged"] != true {
		t.Fatalf("create configChanged = %+v", created["configChanged"])
	}
	rev1 := created["configRevision"].(float64)

	cfg := decode[map[string]any](t, env.do(t, "GET", "/api/nodes/"+itoa(nodeID)+"/config", env.token, nil))
	text := cfg["yaml"].(string)
	if !strings.Contains(text, "bbrProfile: aggressive") || !strings.Contains(text, "tcpPorts: 80,443") {
		t.Fatalf("initial config missing hy2 fields:\n%s", text)
	}

	// Changing a tuning field must create a new revision.
	payload["hy2"].(map[string]any)["quic"] = map[string]any{"maxIdleTimeout": "45s"}
	updated := decode[map[string]any](t, env.do(t, "PUT", "/api/nodes/"+itoa(nodeID), env.token, payload))
	if updated["configChanged"] != true {
		t.Fatalf("update configChanged = %+v", updated["configChanged"])
	}
	rev2 := updated["configRevision"].(float64)
	if rev2 <= rev1 {
		t.Fatalf("revision %v did not advance from %v", rev2, rev1)
	}
	cfg = decode[map[string]any](t, env.do(t, "GET", "/api/nodes/"+itoa(nodeID)+"/config", env.token, nil))
	if text = cfg["yaml"].(string); !strings.Contains(text, "maxIdleTimeout: 45s") {
		t.Fatalf("updated config missing quic field:\n%s", text)
	}

	// A no-op save must not churn revisions.
	again := decode[map[string]any](t, env.do(t, "PUT", "/api/nodes/"+itoa(nodeID), env.token, payload))
	if again["configChanged"] != false {
		t.Fatalf("no-op update configChanged = %+v", again["configChanged"])
	}
	if again["configRevision"].(float64) != rev2 {
		t.Fatalf("no-op update revision = %v, want %v", again["configRevision"], rev2)
	}
}
