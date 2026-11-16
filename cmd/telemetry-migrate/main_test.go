package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/store/pglogs"
)

func TestMigrateLogsFromSQLite(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("MIKVACE_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set MIKVACE_TEST_POSTGRES_DSN to run the telemetry migration test")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.db")
	control, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		entries := []model.LogEntry{{
			TS: now.Add(-time.Duration(i) * time.Minute), Level: "INFO", Event: "connect",
			UserID: "alice", ClientIP: "10.0.0.1", Message: fmt.Sprintf("row %d", i),
		}}
		if _, err := control.InsertLogBatch(context.Background(), int64(i%2+1),
			fmt.Sprintf("agent-batch-%d", i), entries); err != nil {
			t.Fatal(err)
		}
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}

	sqlite, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()

	schema := fmt.Sprintf("migrate_test_%d", now.UnixNano())
	pg, err := pglogs.Open(context.Background(), pglogs.Config{DSN: dsn, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	defer func() {
		admin, err := sql.Open("pgx", dsn)
		if err == nil {
			_, _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
			_ = admin.Close()
		}
	}()

	if n, err := countLogs(context.Background(), sqlite, 0); err != nil || n != 5 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	rows, batches, err := migrateLogs(context.Background(), sqlite, pg, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 5 || batches == 0 {
		t.Fatalf("migrated rows=%d batches=%d", rows, batches)
	}
	if n, err := pg.CountLogs(context.Background()); err != nil || n != 5 {
		t.Fatalf("pg count=%d err=%v", n, err)
	}
	// A second run must be idempotent.
	if _, _, err := migrateLogs(context.Background(), sqlite, pg, 0, 2); err != nil {
		t.Fatal(err)
	}
	if n, err := pg.CountLogs(context.Background()); err != nil || n != 5 {
		t.Fatalf("after rerun pg count=%d err=%v", n, err)
	}
	receipts, err := migrateReceipts(context.Background(), sqlite, pg)
	if err != nil || receipts != 5 {
		t.Fatalf("receipts=%d err=%v", receipts, err)
	}
}
