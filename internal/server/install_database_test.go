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
}

func TestInstallDatabaseRejectsRemoteDriver(t *testing.T) {
	env := newSetupEnv(t)
	resp := env.do(t, "POST", "/api/install/database/test", "", map[string]any{
		"driver": "postgres",
		"path":   filepath.Join(env.app.cfg.DataDir, "panel.db"),
	})
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("remote driver = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
}

func TestInstallDatabaseChangeRestarts(t *testing.T) {
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

	chosen := filepath.Join(env.app.cfg.DataDir, "chosen.db")
	resp := env.do(t, "POST", "/api/install/database", "", map[string]any{
		"driver": "sqlite", "path": chosen,
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
	if saved.Driver != "sqlite" || !saved.RestartRequired {
		t.Fatalf("save = %+v", saved)
	}

	raw, err := os.ReadFile(env.app.cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "driver: sqlite") || !strings.Contains(string(raw), chosen) {
		t.Fatalf("panel.yaml = \n%s", raw)
	}

	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("database change did not trigger a restart")
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
