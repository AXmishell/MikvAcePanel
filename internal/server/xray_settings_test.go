package server

import (
	"net/http"
	"testing"
)

func TestNodeXrayGlobalSettings(t *testing.T) {
	env := newTestEnv(t)
	payload := map[string]any{
		"name": "Xray Settings", "type": "xray", "cores": []string{"xray"},
		"ip": "203.0.113.30", "port": 443, "tlsMode": "selfsigned", "active": true,
		"xray": map[string]any{
			"apiPort":       61000,
			"logLevel":      "debug",
			"routeStrategy": "AsIs",
			"sniffing": map[string]any{
				"enabled": false, "routeOnly": true, "destOverride": []string{"tls"},
			},
			"inbounds": []map[string]any{{
				"protocol": "vless", "tag": "vless-in", "port": 443,
				"transport": "tcp", "security": "reality",
			}},
		},
	}
	created := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, payload))
	nodeObj, _ := created["node"].(map[string]any)
	if nodeObj == nil {
		t.Fatalf("create response = %+v", created)
	}
	xray := nodeObj["xray"].(map[string]any)
	if xray["logLevel"] != "debug" || xray["routeStrategy"] != "AsIs" {
		t.Fatalf("xray settings = %+v", xray)
	}
	sniffing, _ := xray["sniffing"].(map[string]any)
	if sniffing["enabled"] != false {
		t.Fatalf("sniffing = %+v", sniffing)
	}

	nodeID := int64(nodeObj["id"].(float64))
	got := decode[map[string]any](t, env.do(t, "GET", "/api/nodes/"+itoa(nodeID), env.token, nil))
	gotXray := got["xray"].(map[string]any)
	if gotXray["logLevel"] != "debug" || gotXray["routeStrategy"] != "AsIs" {
		t.Fatalf("stored xray settings = %+v", gotXray)
	}

	payload["xray"].(map[string]any)["logLevel"] = "verbose"
	if resp := env.do(t, "POST", "/api/nodes", env.token, payload); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid log level status = %d", resp.StatusCode)
	}
}
