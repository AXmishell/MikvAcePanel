package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateCreatesMetricsTimestampIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var n int
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_metrics_ts'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("idx_metrics_ts count = %d", n)
	}
}

func TestMigrateLegacyAPIKeyScopes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-keys.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		prefix TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		scopes TEXT NOT NULL DEFAULT '',
		rate_limit INTEGER NOT NULL DEFAULT 60,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		last_used_at TEXT,
		expires_at TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO api_keys (name, prefix, token_hash, scopes, rate_limit, enabled, created_at)
		VALUES ('old', 'ck_old', 'hash-old', 'users:write,nodes:write,stats:read', 60, 1, '2025-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	keys, err := st.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("keys = %d", len(keys))
	}
	got := map[string]bool{}
	for _, sc := range keys[0].Scopes {
		got[sc] = true
	}
	for _, want := range []string{"users:write", "users:delete", "nodes:write", "nodes:operate", "nodes:secrets", "dashboard:read", "logs:read", "groups:read"} {
		if !got[want] {
			t.Errorf("missing migrated scope %s in %v", want, keys[0].Scopes)
		}
	}
}

func TestMigrateLegacySQLiteNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE groups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		description TEXT NOT NULL DEFAULT '',
		color TEXT NOT NULL DEFAULT '#38bdf8',
		subscription_title TEXT NOT NULL DEFAULT '',
		max_devices INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO groups (name, created_at, updated_at) VALUES ('legacy', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO settings (key, value) VALUES ('legacy_setting', 'kept')`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var name string
	if err := st.DB.QueryRowContext(ctx, `SELECT name FROM panel_groups WHERE id = 1`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "legacy" {
		t.Fatalf("group name = %q", name)
	}
	var value string
	if err := st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE setting_key = 'legacy_setting'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "kept" {
		t.Fatalf("setting = %q", value)
	}
	var oldGroups int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='groups'`).Scan(&oldGroups); err != nil {
		t.Fatal(err)
	}
	if oldGroups != 0 {
		t.Fatal("legacy groups table still exists")
	}
	if ok, err := st.migrationApplied(ctx, 1); err != nil || !ok {
		t.Fatalf("schema version 1 applied=%v err=%v", ok, err)
	}
}
