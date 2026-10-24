package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfigEnvironmentOnly(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(envPanelURL, "https://panel.example.com/")
	t.Setenv(envNodeToken, "mtn_env")
	t.Setenv(envRuntime, "docker")
	t.Setenv(envHostDataDir, "/srv/mikvace")

	cfg, err := ParseConfig(nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PanelURL != "https://panel.example.com" {
		t.Fatalf("PanelURL = %q", cfg.PanelURL)
	}
	if cfg.Token != "mtn_env" {
		t.Fatalf("Token = %q", cfg.Token)
	}
	if cfg.Runtime != "docker" || cfg.HostDataDir != "/srv/mikvace" {
		t.Fatalf("runtime cfg = %+v", cfg)
	}
	if cfg.ConfigPath != "" {
		t.Fatalf("ConfigPath = %q, want empty for environment-only config", cfg.ConfigPath)
	}
}

func TestParseConfigEnvironmentOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "node.token")
	if err := os.WriteFile(tokenPath, []byte("file-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "node.yaml")
	body := `version: 1
panel:
  url: https://file.example.com
  token_file: "` + tokenPath + `"
agent:
  data_dir: /data
runtime:
  type: process
offline_auth:
  enabled: true
  listen: "127.0.0.1:9998"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv(envPanelURL, "https://env.example.com")
	t.Setenv(envNodeToken, "env-token")
	t.Setenv(envRuntime, "docker")

	cfg, err := ParseConfig([]string{"--config", path}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PanelURL != "https://env.example.com" || cfg.Token != "env-token" || cfg.Runtime != "docker" {
		t.Fatalf("environment must win over YAML: %+v", cfg)
	}

	t.Setenv(envOfflineAuth, "maybe")
	if _, err := ParseConfig([]string{"--config", path}, "test"); err == nil {
		t.Fatal("invalid OFFLINE_AUTH must be rejected")
	}
}
