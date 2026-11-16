package server

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

// usagePayload is the JSON shape of GET /api/maintenance/usage.
type usagePayload struct {
	DatabaseBytes       int64 `json:"databaseBytes"`
	WALBytes            int64 `json:"walBytes"`
	LiveBytes           int64 `json:"liveBytes"`
	FreeBytes           int64 `json:"freeBytes"`
	FreelistPages       int64 `json:"freelistPages"`
	PageSize            int64 `json:"pageSize"`
	AutoVacuum          int   `json:"autoVacuum"`
	Logs                int64 `json:"logs"`
	LogBatches          int64 `json:"logBatches"`
	StatsBatches        int64 `json:"statsBatches"`
	NodeMetrics         int64 `json:"nodeMetrics"`
	LogRetentionDays    int   `json:"logRetentionDays"`
	MetricRetentionDays int   `json:"metricRetentionDays"`
}

func footprint(t *testing.T, path string) int64 {
	t.Helper()
	var total int64
	for _, p := range []string{path, path + "-wal"} {
		info, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		total += info.Size()
	}
	return total
}

// seedLogs inserts n log rows with 1 KiB messages so the page cache has
// something worth reclaiming after they are deleted.
func seedLogs(t *testing.T, st *store.Store, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO hy2_logs (node_id, ts, level, message, event) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	body := strings.Repeat("x", 1024)
	for i := 0; i < n; i++ {
		if _, err := stmt.ExecContext(ctx, 1, store.DBTime(time.Now()), "INFO",
			fmt.Sprintf("%04d-%s", i, body), "log"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// seedOldStatsBatches inserts n expired traffic-batch receipts.
func seedOldStatsBatches(t *testing.T, st *store.Store, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO stats_batches (node_id, batch_id, received_at) VALUES (?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	old := store.DBTime(time.Now().Add(-10 * 24 * time.Hour))
	for i := 0; i < n; i++ {
		if _, err := stmt.ExecContext(ctx, 1, fmt.Sprintf("old-%08d", i), old); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceUsageExposesFreePages(t *testing.T) {
	env := newTestEnv(t)
	seedLogs(t, env.store, 50)

	usage := decode[usagePayload](t, env.do(t, "GET", "/api/maintenance/usage", env.token, nil))
	if usage.PageSize <= 0 {
		t.Fatalf("page size = %d", usage.PageSize)
	}
	if usage.DatabaseBytes <= 0 {
		t.Fatalf("database bytes = %d", usage.DatabaseBytes)
	}
	if usage.LiveBytes <= 0 {
		t.Fatalf("live bytes = %d", usage.LiveBytes)
	}
	// Page counters describe the logical database, which spans the main file and
	// the WAL until the next checkpoint.
	if usage.LiveBytes+usage.FreeBytes > usage.DatabaseBytes+usage.WALBytes {
		t.Fatalf("live %d + free %d > database %d + wal %d",
			usage.LiveBytes, usage.FreeBytes, usage.DatabaseBytes, usage.WALBytes)
	}
	if usage.AutoVacuum != 2 {
		t.Fatalf("auto_vacuum = %d, want incremental", usage.AutoVacuum)
	}
	if usage.Logs != 50 {
		t.Fatalf("logs = %d, want 50", usage.Logs)
	}
}

func TestDeleteLogsReclaimsSpace(t *testing.T) {
	env := newTestEnv(t)
	seedLogs(t, env.store, 6000)

	before := footprint(t, env.store.Path)
	beforeUsage := decode[usagePayload](t, env.do(t, "GET", "/api/maintenance/usage", env.token, nil))

	path := "/api/logs?before=" + url.QueryEscape(time.Now().Add(time.Minute).UTC().Format(time.RFC3339))
	resp := env.do(t, "DELETE", path, env.token, nil)
	out := decode[map[string]any](t, resp)
	if deleted, _ := out["deleted"].(float64); deleted != 6000 {
		t.Fatalf("deleted = %v, want 6000", out["deleted"])
	}
	if _, ok := out["freeBytes"]; !ok {
		t.Fatalf("response does not report free bytes: %v", out)
	}
	if msg, _ := out["maintenanceError"].(string); msg != "" {
		t.Fatalf("maintenance error: %s", msg)
	}

	afterUsage := decode[usagePayload](t, env.do(t, "GET", "/api/maintenance/usage", env.token, nil))
	if afterUsage.Logs != 0 {
		t.Fatalf("logs after delete = %d", afterUsage.Logs)
	}
	if afterUsage.FreeBytes >= beforeUsage.FreeBytes+4096 {
		t.Fatalf("free bytes %d -> %d, want the delete to be reclaimed",
			beforeUsage.FreeBytes, afterUsage.FreeBytes)
	}
	if after := footprint(t, env.store.Path); after >= before {
		t.Fatalf("database footprint %d -> %d, want a decrease", before, after)
	}
}

func TestCleanupDataVacuumShrinksFile(t *testing.T) {
	env := newTestEnv(t)
	seedOldStatsBatches(t, env.store, 30000)
	before := footprint(t, env.store.Path)

	res := decode[map[string]any](t, env.do(t, "POST", "/api/maintenance/cleanup", env.token, map[string]any{
		"vacuum": true, "logRetentionDays": 1, "metricRetentionDays": 1,
	}))
	if res["vacuumed"] != true {
		t.Fatalf("vacuumed = %v (error %v)", res["vacuumed"], res["vacuumError"])
	}
	if free, _ := res["freeBytes"].(float64); free != 0 {
		t.Fatalf("free bytes after vacuum = %v, want 0", res["freeBytes"])
	}
	if after := footprint(t, env.store.Path); after >= before {
		t.Fatalf("database footprint %d -> %d, want a decrease", before, after)
	}
}

func TestCleanupDataReclaimsWithoutVacuum(t *testing.T) {
	env := newTestEnv(t)
	seedOldStatsBatches(t, env.store, 30000)
	before := footprint(t, env.store.Path)

	res := decode[map[string]any](t, env.do(t, "POST", "/api/maintenance/cleanup", env.token, map[string]any{
		"vacuum": false, "logRetentionDays": 1, "metricRetentionDays": 1,
	}))
	if msg, _ := res["vacuumError"].(string); msg != "" {
		t.Fatalf("maintenance error: %s", msg)
	}
	if reclaimed, _ := res["reclaimedPages"].(float64); reclaimed <= 0 {
		t.Fatalf("reclaimed pages = %v, want an incremental pass", res["reclaimedPages"])
	}
	if after := footprint(t, env.store.Path); after >= before {
		t.Fatalf("database footprint %d -> %d, want a decrease", before, after)
	}
}
