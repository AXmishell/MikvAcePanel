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
	for _, want := range []string{
		"-e PANEL_URL='http://panel.test'",
		"-e HY2_RUNTIME='docker'",
		"-e HOST_DATA_DIR='/var/lib/mikvace'",
		"/var/lib/mikvace:/data",
		"/var/run/docker.sock",
	} {
		if !strings.Contains(dockerCmd, want) {
			t.Fatalf("docker install command missing %q:\n%s", want, dockerCmd)
		}
	}
	if strings.Contains(dockerCmd, "node.yaml") || strings.Contains(dockerCmd, "node.token") {
		t.Fatalf("docker install command still references config files:\n%s", dockerCmd)
	}
	compose, _ := created["compose"].(string)
	for _, want := range []string{
		"environment:",
		`PANEL_URL: "http://panel.test"`,
		"NODE_TOKEN:",
		`HY2_RUNTIME: "docker"`,
		`HOST_DATA_DIR: "/var/lib/mikvace"`,
		"/var/lib/mikvace:/data",
		"/var/run/docker.sock",
		"max-size:",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("docker compose missing %q:\n%s", want, compose)
		}
	}
	if strings.Contains(compose, "node.yaml") || strings.Contains(compose, "node.token") {
		t.Fatalf("docker compose still references config files:\n%s", compose)
	}

	nodeID := int64(nodeObj["id"].(float64))
	install := decode[map[string]any](t, env.do(t, "GET", "/api/nodes/"+itoa(nodeID)+"/install", env.token, nil))
	if install["runtimeMode"] != "docker" {
		t.Fatalf("install runtimeMode = %+v", install["runtimeMode"])
	}
	if _, ok := install["nodeYaml"]; ok {
		t.Fatal("install response must not contain nodeYaml")
	}
	if script, _ := install["installScript"].(string); !strings.Contains(script, "--runtime docker") {
		t.Fatalf("install script:\n%s", script)
	}
	if cmd, _ := install["processCommand"].(string); !strings.Contains(cmd, "-e HY2_RUNTIME='process'") || strings.Contains(cmd, "docker.sock") {
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
	if !strings.Contains(processCmd, "-e HY2_RUNTIME='process'") || strings.Contains(processCmd, "docker.sock") {
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
		"-e PANEL_URL='http://panel.test'",
		"-e NODE_TOKEN='mtn_test_token'",
		"-e HY2_RUNTIME='docker'",
		"-e HOST_DATA_DIR='/var/lib/mikvace'",
		"/var/lib/mikvace:/data",
		"/var/run/docker.sock",
	} {
		if !strings.Contains(dockerScript, want) {
			t.Fatalf("docker script missing %q:\n%s", want, dockerScript)
		}
	}
	if strings.Contains(dockerScript, "node.yaml") || strings.Contains(dockerScript, "node.token") {
		t.Fatalf("docker script still writes config files:\n%s", dockerScript)
	}

	processScript := env.app.sshInstallScript(&model.Node{Token: "mtn_test_token", RuntimeMode: "process"})
	if !strings.Contains(processScript, "-e HY2_RUNTIME='process'") || strings.Contains(processScript, "/var/run/docker.sock") {
		t.Fatalf("process script:\n%s", processScript)
	}
}
