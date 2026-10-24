package server

import (
	"strings"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestNodeRuntimeModeAndInstallCommands(t *testing.T) {
	env := newTestEnv(t)

	base := map[string]any{
		"name": "Docker Node", "type": "hysteria", "cores": []string{"hysteria"},
		"ip": "203.0.113.20", "port": 443, "tlsMode": "selfsigned", "active": true,
		"runtimeMode": "docker",
	}
	created := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, base))
	nodeObj, _ := created["node"].(map[string]any)
	if nodeObj["runtimeMode"] != "docker" {
		t.Fatalf("node runtimeMode = %+v", nodeObj["runtimeMode"])
	}
	if created["runtimeMode"] != "docker" {
		t.Fatalf("response runtimeMode = %+v", created["runtimeMode"])
	}
	dockerCmd, _ := created["installCommand"].(string)
	if !strings.Contains(dockerCmd, "type: docker") || !strings.Contains(dockerCmd, "/var/run/docker.sock") ||
		!strings.Contains(dockerCmd, "--config /config/node.yaml") || !strings.Contains(dockerCmd, "/etc/mikvace/node.yaml") {
		t.Fatalf("docker install command:\n%s", dockerCmd)
	}
	compose, _ := created["compose"].(string)
	if !strings.Contains(compose, "--config") || !strings.Contains(compose, "/var/run/docker.sock") ||
		strings.Contains(compose, "environment:") {
		t.Fatalf("docker compose:\n%s", compose)
	}

	nodeID := int64(nodeObj["id"].(float64))
	install := decode[map[string]any](t, env.do(t, "GET", "/api/nodes/"+itoa(nodeID)+"/install", env.token, nil))
	if install["runtimeMode"] != "docker" {
		t.Fatalf("install runtimeMode = %+v", install["runtimeMode"])
	}
	if script, _ := install["installScript"].(string); !strings.Contains(script, "--runtime docker") {
		t.Fatalf("install script:\n%s", script)
	}
	if yamlText, _ := install["nodeYaml"].(string); !strings.Contains(yamlText, "type: docker") || !strings.Contains(yamlText, "token_file: /config/node.token") {
		t.Fatalf("node yaml:\n%s", yamlText)
	}
	if cmd, _ := install["processCommand"].(string); !strings.Contains(cmd, "type: process") || strings.Contains(cmd, "docker.sock") {
		t.Fatalf("process command:\n%s", cmd)
	}

	// Switching back to process mode updates the generated command.
	base["runtimeMode"] = "process"
	updated := decode[map[string]any](t, env.do(t, "PUT", "/api/nodes/"+itoa(nodeID), env.token, base))
	updatedNode, _ := updated["node"].(map[string]any)
	if updatedNode["runtimeMode"] != "process" {
		t.Fatalf("updated runtimeMode = %+v", updatedNode["runtimeMode"])
	}
	processCmd, _ := updated["installCommand"].(string)
	if !strings.Contains(processCmd, "type: process") || strings.Contains(processCmd, "docker.sock") {
		t.Fatalf("updated install command:\n%s", processCmd)
	}

	// Invalid values are rejected instead of silently falling back.
	base["runtimeMode"] = "podman"
	bad := env.do(t, "PUT", "/api/nodes/"+itoa(nodeID), env.token, base)
	bad.Body.Close()
	if bad.StatusCode != 400 {
		t.Fatalf("invalid runtimeMode = %d", bad.StatusCode)
	}
}

func TestSSHInstallScriptRuntimeModes(t *testing.T) {
	env := newTestEnv(t)

	dockerScript := env.app.sshInstallScript(&model.Node{
		Token: "mtn_test_token", SSHInstallDocker: true, RuntimeMode: "docker",
	})
	for _, want := range []string{
		"type: docker",
		"host_data_dir: /var/lib/mikvace",
		"/var/run/docker.sock",
		"--config /config/node.yaml",
		"/etc/mikvace/node.token",
	} {
		if !strings.Contains(dockerScript, want) {
			t.Fatalf("docker script missing %q:\n%s", want, dockerScript)
		}
	}

	processScript := env.app.sshInstallScript(&model.Node{Token: "mtn_test_token", RuntimeMode: "process"})
	if !strings.Contains(processScript, "type: process") || strings.Contains(processScript, "/var/run/docker.sock") {
		t.Fatalf("process script:\n%s", processScript)
	}
}
