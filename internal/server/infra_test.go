package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

func newInfraTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	configPath := filepath.Join(dir, "panel.yaml")
	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		ConfigPath: configPath, Addr: ":0", DataDir: dir, DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin", AdminPassword: "testpass123",
		SessionTTL: time.Hour,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := New(cfg, st, logger)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	_, entry := app.adminSecurityEntry(context.Background())
	env := &testEnv{srv: ts, store: st, app: app, entry: entry}
	env.token = env.login(t)
	return env
}

func TestInfraStatus(t *testing.T) {
	env := newInfraTestEnv(t)
	status := decode[infraStatus](t, env.do(t, "GET", "/api/infra", env.token, nil))
	if status.Telemetry.Driver != "sqlite" || status.Telemetry.ActiveDriver != "sqlite" {
		t.Fatalf("telemetry = %+v", status.Telemetry)
	}
	if !status.Telemetry.OK || status.Telemetry.Fallback {
		t.Fatalf("telemetry health = %+v", status.Telemetry)
	}
	if status.Redis.Enabled || !status.Redis.OK {
		t.Fatalf("redis = %+v", status.Redis)
	}
	if status.Cache.Backend != "memory" {
		t.Fatalf("cache = %+v", status.Cache)
	}
	if len(status.DSNTemplates.Postgres) == 0 || len(status.DSNTemplates.Redis) == 0 {
		t.Fatalf("dsn templates = %+v", status.DSNTemplates)
	}
}

func TestInfraTestAndSave(t *testing.T) {
	env := newInfraTestEnv(t)

	originalPG := testTelemetryConnection
	originalRedis := testRedisConnection
	var pgCalls, redisCalls int
	testTelemetryConnection = func(_ context.Context, dsn, schema string) error {
		pgCalls++
		if !strings.Contains(dsn, "postgres://") || schema != "logs" {
			t.Fatalf("dsn=%q schema=%q", dsn, schema)
		}
		return nil
	}
	testRedisConnection = func(_ context.Context, rawURL string) error {
		redisCalls++
		if rawURL != "redis://:secret@cache:6379/0" {
			t.Fatalf("url=%q", rawURL)
		}
		return nil
	}
	defer func() {
		testTelemetryConnection = originalPG
		testRedisConnection = originalRedis
	}()

	restarted := make(chan struct{}, 1)
	env.app.cfg.Restart = func() { restarted <- struct{}{} }

	payload := map[string]any{
		"telemetry": map[string]any{
			"enabled": true, "dsn": "postgres://panel:pw@db:5432/mikvace?sslmode=disable",
			"schema": "logs", "required": true,
		},
		"redis": map[string]any{
			"enabled": true, "url": "redis://:secret@cache:6379/0",
			"prefix": "panel:", "required": false,
		},
	}
	resp := env.do(t, "POST", "/api/infra/test", env.token, payload)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("test = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	if pgCalls != 1 || redisCalls != 1 {
		t.Fatalf("test calls pg=%d redis=%d", pgCalls, redisCalls)
	}

	saved := decode[map[string]any](t, env.do(t, "PUT", "/api/infra", env.token, payload))
	if saved["ok"] != true || saved["restartRequired"] != true ||
		saved["telemetry"] != "postgres" || saved["redis"] != true {
		t.Fatalf("save = %+v", saved)
	}
	raw, err := os.ReadFile(env.app.cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"driver: postgres", "schema: logs", "dsn_file:", "url_file:", "prefix:", "panel:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("panel.yaml missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "postgres://panel:pw@db") || strings.Contains(text, "secret@cache") {
		t.Fatal("panel.yaml must not contain secrets")
	}
	if _, err := os.Stat(filepath.Join(env.app.cfg.DataDir, "telemetry.pg.url")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.app.cfg.DataDir, "redis.url")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("infra change did not trigger a restart")
	}
}

func TestInfraValidation(t *testing.T) {
	env := newInfraTestEnv(t)
	cases := []map[string]any{
		{"telemetry": map[string]any{"enabled": true}},
		{"redis": map[string]any{"enabled": true, "url": "http://not-redis"}},
		{"telemetry": map[string]any{"enabled": true, "dsn": "x", "schema": "bad-schema"}},
	}
	for _, payload := range cases {
		resp := env.do(t, "POST", "/api/infra/test", env.token, payload)
		if resp.StatusCode != http.StatusBadRequest {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("%+v = %d: %s", payload, resp.StatusCode, b)
		}
		resp.Body.Close()
	}
}

func TestInfraDisableWritesSQLiteConfig(t *testing.T) {
	env := newInfraTestEnv(t)
	env.app.cfg.TelemetryDriver = "postgres"
	env.app.cfg.TelemetryDSN = "postgres://old"
	env.app.cfg.RedisURL = "redis://old"
	env.app.cfg.RedisPrefix = "old:"
	saved := decode[map[string]any](t, env.do(t, "PUT", "/api/infra", env.token, map[string]any{
		"telemetry": map[string]any{"enabled": false},
		"redis":     map[string]any{"enabled": false},
	}))
	if saved["restartRequired"] != true || saved["telemetry"] != "sqlite" || saved["redis"] != false {
		t.Fatalf("save = %+v", saved)
	}
	raw, _ := os.ReadFile(env.app.cfg.ConfigPath)
	text := string(raw)
	if !strings.Contains(text, "driver: sqlite") {
		t.Fatalf("panel.yaml:\n%s", text)
	}
	if strings.Contains(text, "postgres://old") || strings.Contains(text, "redis://old") {
		t.Fatal("disabled backend secret leaked into panel.yaml")
	}
}
