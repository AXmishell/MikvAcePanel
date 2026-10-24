package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

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

func TestRemoteBackends(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		env    string
	}{
		{"postgres", "postgres", "TEST_POSTGRES_DSN"},
		{"mysql", "mysql", "TEST_MYSQL_DSN"},
	}
	for _, tc := range cases {
		tc := tc
		dsn := os.Getenv(tc.env)
		if dsn == "" {
			t.Logf("skip %s: set %s to run", tc.name, tc.env)
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := OpenConfig(Config{Driver: tc.driver, DSN: dsn})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.EnsureDefaultSettings(ctx); err != nil {
				t.Fatal(err)
			}
			suffix := time.Now().UTC().Format("20060102150405.000000000")
			g, err := st.CreateGroup(ctx, &model.Group{Name: "remote-" + suffix, MaxDevices: 2})
			if err != nil {
				t.Fatal(err)
			}
			u, err := st.CreateUser(ctx, &model.User{
				UserID: "remote-" + suffix, Password: "pw", Enabled: true,
			}, []int64{g.ID}, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := st.GetUserByID(ctx, u.ID)
			if err != nil || got.Password != "pw" || len(got.Groups) != 1 {
				t.Fatalf("user = %+v err=%v", got, err)
			}
			if err := st.SetSetting(ctx, "remote_test", suffix); err != nil {
				t.Fatal(err)
			}
			if v, _ := st.GetSetting(ctx, "remote_test"); v != suffix {
				t.Fatalf("setting = %q", v)
			}
			batch := "batch-" + suffix
			ok, err := st.ApplyStatsBatch(ctx, 1, batch, map[string]model.UserTraffic{
				u.UserID: {Tx: 10, Rx: 5},
			})
			if err != nil || !ok {
				t.Fatalf("first batch ok=%v err=%v", ok, err)
			}
			ok, err = st.ApplyStatsBatch(ctx, 1, batch, map[string]model.UserTraffic{
				u.UserID: {Tx: 10, Rx: 5},
			})
			if err != nil || ok {
				t.Fatalf("duplicate batch ok=%v err=%v", ok, err)
			}
			got, _ = st.GetUserByID(ctx, u.ID)
			if got.TrafficUsed() != 15 {
				t.Fatalf("traffic = %d", got.TrafficUsed())
			}
			// A second Open must be a no-op migration.
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st2, err := OpenConfig(Config{Driver: tc.driver, DSN: dsn})
			if err != nil {
				t.Fatal(err)
			}
			defer st2.Close()
			if _, err := st2.GetUserByID(ctx, u.ID); err != nil {
				t.Fatalf("user after reopen: %v", err)
			}
		})
	}
}
