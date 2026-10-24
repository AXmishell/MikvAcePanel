package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

func TestInstallDatabaseSQLite(t *testing.T) {
	env := newSetupEnv(t)

	status := decode[map[string]any](t, env.do(t, "GET", "/api/install/status", "", nil))
	database, _ := status["databaseConfig"].(map[string]any)
	if database["driver"] != "sqlite" {
		t.Fatalf("databaseConfig = %+v", database)
	}
	startedAt, _ := status["startedAt"].(string)
	if startedAt == "" {
		t.Fatal("status is missing startedAt")
	}

	testResp := env.do(t, "POST", "/api/install/database/test", "", map[string]any{
		"driver": "sqlite", "path": filepath.Join(env.app.cfg.DataDir, "panel.db"),
	})
	if testResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(testResp.Body)
		t.Fatalf("test connection = %d: %s", testResp.StatusCode, b)
	}
	testResp.Body.Close()

	saved := decode[map[string]any](t, env.do(t, "POST", "/api/install/database", "", map[string]any{
		"driver": "sqlite", "path": filepath.Join(env.app.cfg.DataDir, "panel.db"),
	}))
	if saved["ok"] != true || saved["restartRequired"] != false {
		t.Fatalf("save = %+v", saved)
	}

	raw, err := os.ReadFile(env.app.cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "driver: sqlite") {
		t.Fatalf("config missing driver:\n%s", raw)
	}
	if !strings.Contains(string(raw), "dsn_file: \"\"") {
		t.Fatalf("config should clear dsn_file:\n%s", raw)
	}
}

func TestInstallDatabaseRejectsMismatchedDSN(t *testing.T) {
	env := newSetupEnv(t)
	resp := env.do(t, "POST", "/api/install/database/test", "", map[string]any{
		"driver": "postgres",
		"dsn":    "mysql://user:pass@db:3306/mikvace",
	})
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("mismatched dsn = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
}

func TestInstallDatabaseRemoteWritesSecretAndRestarts(t *testing.T) {
	env := newSetupEnv(t)

	originalTest := testDatabaseConnection
	originalPrepare := prepareDatabaseConnection
	testDatabaseConnection = func(store.Config) error { return nil }
	prepareDatabaseConnection = func(store.Config) error { return nil }
	defer func() {
		testDatabaseConnection = originalTest
		prepareDatabaseConnection = originalPrepare
	}()

	restarted := make(chan struct{}, 1)
	env.app.cfg.Restart = func() { restarted <- struct{}{} }

	dsn := "postgres://user:supersecret@db:5432/mikvace?sslmode=disable"
	resp := env.do(t, "POST", "/api/install/database", "", map[string]any{
		"driver": "postgres", "dsn": dsn,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("save database = %d: %s", resp.StatusCode, b)
	}
	var saved struct {
		Driver          string `json:"driver"`
		RestartRequired bool   `json:"restartRequired"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&saved)
	resp.Body.Close()
	if saved.Driver != "postgres" || !saved.RestartRequired {
		t.Fatalf("save = %+v", saved)
	}

	secretPath := filepath.Join(env.app.cfg.DataDir, "database.dsn")
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read secret: %v", err)
	}
	if strings.TrimSpace(string(secret)) != dsn {
		t.Fatalf("secret = %q", secret)
	}
	st, err := os.Stat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode = %v", st.Mode().Perm())
	}

	raw, err := os.ReadFile(env.app.cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "supersecret") {
		t.Fatalf("panel.yaml must not contain the DSN:\n%s", raw)
	}
	if !strings.Contains(string(raw), "driver: postgres") || !strings.Contains(string(raw), "database.dsn") {
		t.Fatalf("panel.yaml = \n%s", raw)
	}

	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("database change did not trigger a restart")
	}
}

func TestInstallDatabaseStatusNeverLeaksDSN(t *testing.T) {
	env := newSetupEnv(t)
	env.app.cfg.DBDSN = "postgres://user:topsecret@db:5432/mikvace"
	env.app.cfg.DBDSNFile = "/run/secrets/database_url"

	resp := env.do(t, "GET", "/api/install/status", "", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "topsecret") {
		t.Fatalf("status leaked the DSN: %s", body)
	}
	if !strings.Contains(string(body), "\"dsnConfigured\":true") {
		t.Fatalf("status did not report the configured DSN: %s", body)
	}
}

func TestInstallDatabaseBlockedAfterInstall(t *testing.T) {
	env := newSetupEnv(t)
	resp := env.do(t, "POST", "/api/install", "", map[string]any{
		"username": "ops", "password": "a-strong-password-123",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("install = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	resp = env.do(t, "POST", "/api/install/database", "", map[string]any{
		"driver": "sqlite", "path": filepath.Join(env.app.cfg.DataDir, "other.db"),
	})
	if resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("database change after install = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
}
