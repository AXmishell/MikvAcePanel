package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.yaml")
	if err := os.WriteFile(path, []byte(`version: 1
panel:
  name: My Panel
  public_url: "https://panel.example.com/"
server:
  serve_web: false
  frontend_url: "https://app.example.com/"
admin:
  username: ops
session:
  ttl: "12h"
database:
  driver: sqlite
  path: /tmp/test.db
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServeWeb {
		t.Fatal("ServeWeb should be false")
	}
	if cfg.FrontendURL != "https://app.example.com" || cfg.PublicURL != "https://panel.example.com" {
		t.Fatalf("urls = %q / %q", cfg.FrontendURL, cfg.PublicURL)
	}
	if cfg.PanelName != "My Panel" || cfg.AdminUsername != "ops" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.SessionTTL.Hours() != 12 || cfg.DBPath != "/tmp/test.db" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.ConfigPath != path {
		t.Fatalf("config path = %q", cfg.ConfigPath)
	}
}

func TestLoadConfigSecretFiles(t *testing.T) {
	dir := t.TempDir()
	pw := filepath.Join(dir, "admin_password")
	if err := os.WriteFile(pw, []byte("a-strong-password-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	jwt := filepath.Join(dir, "jwt")
	if err := os.WriteFile(jwt, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "panel.yaml")
	if err := os.WriteFile(path, []byte(`version: 1
admin:
  password_file: "`+pw+`"
security:
  jwt_secret_file: "`+jwt+`"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminPassword != "a-strong-password-123" {
		t.Fatalf("admin password = %q", cfg.AdminPassword)
	}
	secret, err := loadJWTSecret(cfg)
	if err != nil || string(secret) != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("jwt = %q %v", secret, err)
	}
}

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nunknown_field: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown field must fail")
	}
}

func TestLoadConfigMissingFileUsesSetupMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.yaml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" || !cfg.ServeWeb || cfg.DBDriver != SetupDriver {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.DBPath == "" {
		t.Fatal("setup mode should still expose the default SQLite path for the wizard")
	}
}

func TestLoadConfigExistingFileWithoutDatabaseDefaultsToSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBDriver != "sqlite" {
		t.Fatalf("driver = %q, want sqlite", cfg.DBDriver)
	}
}

func TestLoadConfigSetupAliases(t *testing.T) {
	for _, driver := range []string{"setup", "none", "unset", "ask"} {
		path := filepath.Join(t.TempDir(), "panel.yaml")
		body := "version: 1\ndatabase:\n  driver: " + driver + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DBDriver != SetupDriver {
			t.Fatalf("driver %q = %q", driver, cfg.DBDriver)
		}
	}
}
