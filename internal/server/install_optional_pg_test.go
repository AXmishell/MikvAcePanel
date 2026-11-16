package server

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestInstallOptionalBackendsReal exercises the wizard against real
// PostgreSQL and Redis instances. It is skipped unless both environment
// variables are set, so the default test suite stays dependency-free.
func TestInstallOptionalBackendsReal(t *testing.T) {
	pgDSN := strings.TrimSpace(os.Getenv("MIKVACE_TEST_POSTGRES_DSN"))
	redisURL := strings.TrimSpace(os.Getenv("MIKVACE_TEST_REDIS_URL"))
	if pgDSN == "" || redisURL == "" {
		t.Skip("set MIKVACE_TEST_POSTGRES_DSN and MIKVACE_TEST_REDIS_URL to run the real installer test")
	}
	env := newSetupEnv(t)
	schema := fmt.Sprintf("install_test_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		admin, err := sql.Open("pgx", pgDSN)
		if err == nil {
			_, _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
			_ = admin.Close()
		}
	})
	payload := map[string]any{
		"driver": "sqlite",
		"path":   filepath.Join(env.app.cfg.DataDir, "panel.db"),
		"telemetry": map[string]any{
			"enabled": true, "dsn": pgDSN, "schema": schema, "required": false,
		},
		"redis": map[string]any{
			"enabled": true, "url": redisURL, "prefix": "install-test:", "required": false,
		},
	}
	resp := env.do(t, "POST", "/api/install/database/test", "", payload)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("test optional backends = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	saved := decode[map[string]any](t, env.do(t, "POST", "/api/install/database", "", payload))
	if saved["telemetry"] != "postgres" || saved["redis"] != true {
		t.Fatalf("save = %+v", saved)
	}
	if _, err := os.Stat(filepath.Join(env.app.cfg.DataDir, "telemetry.pg.url")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.app.cfg.DataDir, "redis.url")); err != nil {
		t.Fatal(err)
	}
}
