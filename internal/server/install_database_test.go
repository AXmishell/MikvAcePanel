package server

import (
	"context"
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

func TestInstallDatabaseOptionalPostgresAndRedis(t *testing.T) {
	env := newSetupEnv(t)

	originalDBTest := testDatabaseConnection
	originalPrepare := prepareDatabaseConnection
	originalPG := testTelemetryConnection
	originalRedis := testRedisConnection
	var pgCalls, redisCalls int
	testDatabaseConnection = func(store.Config) error { return nil }
	prepareDatabaseConnection = func(store.Config) error { return nil }
	testTelemetryConnection = func(_ context.Context, dsn, schema string) error {
		pgCalls++
		if !strings.HasPrefix(dsn, "postgres://") || schema != "logs" {
			t.Fatalf("telemetry test dsn=%q schema=%q", dsn, schema)
		}
		return nil
	}
	testRedisConnection = func(_ context.Context, rawURL string) error {
		redisCalls++
		if rawURL != "redis://:secret@cache:6379/2" {
			t.Fatalf("redis test url=%q", rawURL)
		}
		return nil
	}
	defer func() {
		testDatabaseConnection = originalDBTest
		prepareDatabaseConnection = originalPrepare
		testTelemetryConnection = originalPG
		testRedisConnection = originalRedis
	}()

	payload := map[string]any{
		"driver": "sqlite",
		"path":   filepath.Join(env.app.cfg.DataDir, "panel.db"),
		"telemetry": map[string]any{
			"enabled": true, "dsn": "postgres://panel:pw@db:5432/mikvace?sslmode=disable",
			"schema": "logs", "required": true,
		},
		"redis": map[string]any{
			"enabled": true, "url": "redis://:secret@cache:6379/2",
			"prefix": "panel:", "required": false,
		},
	}
	testResp := env.do(t, "POST", "/api/install/database/test", "", payload)
	if testResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(testResp.Body)
		t.Fatalf("test optional backends = %d: %s", testResp.StatusCode, b)
	}
	testResp.Body.Close()
	if pgCalls != 1 || redisCalls != 1 {
		t.Fatalf("test calls pg=%d redis=%d", pgCalls, redisCalls)
	}

	saved := decode[map[string]any](t, env.do(t, "POST", "/api/install/database", "", payload))
	if saved["ok"] != true || saved["restartRequired"] != true ||
		saved["telemetry"] != "postgres" || saved["redis"] != true {
		t.Fatalf("save = %+v", saved)
	}
	raw, err := os.ReadFile(env.app.cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"driver: postgres", "schema: logs", "dsn_file:", "url_file:",
		"prefix:", "panel:", "required: true",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("panel.yaml missing %q:\n%s", want, text)
		}
	}
	pgSecret, err := os.ReadFile(filepath.Join(env.app.cfg.DataDir, "telemetry.pg.url"))
	if err != nil || !strings.Contains(string(pgSecret), "postgres://panel:pw@db") {
		t.Fatalf("telemetry secret = %q err=%v", pgSecret, err)
	}
	redisSecret, err := os.ReadFile(filepath.Join(env.app.cfg.DataDir, "redis.url"))
	if err != nil || string(redisSecret) != "redis://:secret@cache:6379/2" {
		t.Fatalf("redis secret = %q err=%v", redisSecret, err)
	}
	for _, name := range []string{"telemetry.pg.url", "redis.url"} {
		info, err := os.Stat(filepath.Join(env.app.cfg.DataDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s mode = %v", name, info.Mode().Perm())
		}
	}
	if strings.Contains(text, "postgres://panel:pw@db") || strings.Contains(text, "secret@cache") {
		t.Fatal("panel.yaml must not contain DSN or Redis credentials")
	}
}

func TestInstallDatabaseOptionalValidation(t *testing.T) {
	env := newSetupEnv(t)
	cases := []map[string]any{
		{"driver": "sqlite", "telemetry": map[string]any{"enabled": true}},
		{"driver": "sqlite", "redis": map[string]any{"enabled": true, "url": "http://not-a-redis"}},
		{"driver": "sqlite", "telemetry": map[string]any{"enabled": true, "dsn": "x", "schema": "bad-schema"}},
	}
	for _, payload := range cases {
		resp := env.do(t, "POST", "/api/install/database/test", "", payload)
		if resp.StatusCode != http.StatusBadRequest {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("%+v = %d: %s", payload, resp.StatusCode, b)
		}
		resp.Body.Close()
	}
}

func TestInstallStatusReportsOptionalBackends(t *testing.T) {
	env := newSetupEnv(t)
	env.app.cfg.TelemetryDriver = "postgres"
	env.app.cfg.TelemetryDSN = "postgres://secret"
	env.app.cfg.TelemetrySchema = "mikvace"
	env.app.cfg.TelemetryRequired = true
	env.app.cfg.RedisURL = "redis://secret"
	env.app.cfg.RedisPrefix = "panel:"
	env.app.cfg.RedisRequired = true
	status := decode[map[string]any](t, env.do(t, "GET", "/api/install/status", "", nil))
	telemetry, _ := status["telemetryConfig"].(map[string]any)
	if telemetry["driver"] != "postgres" || telemetry["schema"] != "mikvace" ||
		telemetry["configured"] != true || telemetry["required"] != true {
		t.Fatalf("telemetryConfig = %+v", telemetry)
	}
	if _, leaked := telemetry["dsn"]; leaked {
		t.Fatalf("status leaked DSN: %+v", telemetry)
	}
	redis, _ := status["redisConfig"].(map[string]any)
	if redis["enabled"] != true || redis["prefix"] != "panel:" || redis["required"] != true {
		t.Fatalf("redisConfig = %+v", redis)
	}
	if _, leaked := redis["url"]; leaked {
		t.Fatalf("status leaked Redis URL: %+v", redis)
	}
}
