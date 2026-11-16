package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
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

func TestServerReadsLogsFromPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("MIKVACE_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set MIKVACE_TEST_POSTGRES_DSN to run the PostgreSQL server test")
	}
	dir := t.TempDir()
	control, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	ctx := context.Background()
	node, err := control.CreateNode(ctx, &model.Node{
		Name: "pg-node", Type: "hysteria", IP: "203.0.113.9", Port: 443,
		StatsPort: 9999, TLSMode: "selfsigned", Active: true,
	}, "pg-node-hash", "prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.CreateUser(ctx, &model.User{
		UserID: "alice", Password: "pw", Enabled: true,
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	schema := fmt.Sprintf("server_test_%d", time.Now().UnixNano())
	pg, err := pglogs.Open(ctx, pglogs.Config{DSN: dsn, Schema: schema, Directory: control})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pg.Close()
		admin, err := sql.Open("pgx", dsn)
		if err == nil {
			_, _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
			_ = admin.Close()
		}
	})
	now := time.Now().UTC()
	entries := []model.LogEntry{
		{TS: now.Add(-2 * time.Minute), Level: "INFO", Event: "connect", UserID: "alice", ClientIP: "10.1.1.1", Message: "connected"},
	}
	for i := 0; i < 5; i++ {
		entries = append(entries, model.LogEntry{
			TS: now.Add(-time.Duration(i+1) * time.Minute), Level: "WARN", Event: "auth_fail",
			UserID: "ghost", ClientIP: "10.1.1.2", Message: "auth failed",
		})
	}
	if _, err := pg.InsertLogBatch(ctx, node.ID, "srv-1", entries); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Addr: ":0", DataDir: dir, DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin", AdminPassword: "testpass123",
		SessionTTL: time.Hour, TelemetryDriver: "postgres", TelemetrySchema: schema,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := NewWithLogs(cfg, control, pg, logger)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	_, entry := app.adminSecurityEntry(context.Background())
	env := &testEnv{srv: ts, store: control, app: app, entry: entry}
	env.token = env.login(t)

	resp := env.do(t, "GET", "/api/logs?pageSize=10", env.token, nil)
	defer resp.Body.Close()
	var list struct {
		Items []model.LogEntry `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 6 || len(list.Items) != 6 {
		t.Fatalf("logs total=%d items=%d", list.Total, len(list.Items))
	}
	if list.Items[0].NodeName != "pg-node" {
		t.Fatalf("node name = %q (cross-store lookup failed)", list.Items[0].NodeName)
	}

	resp = env.do(t, "GET", "/api/logs/summary?range=24h", env.token, nil)
	defer resp.Body.Close()
	var summary struct {
		Total        int64            `json:"total"`
		AuthFailures int64            `json:"authFailures"`
		Findings     []model.Finding  `json:"findings"`
		Nodes        []map[string]any `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.Total != 6 || summary.AuthFailures != 5 {
		t.Fatalf("summary total=%d failures=%d", summary.Total, summary.AuthFailures)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0]["nodeName"] != "pg-node" {
		t.Fatalf("summary nodes = %+v", summary.Nodes)
	}
	foundUnknown := false
	for _, f := range summary.Findings {
		if f.ID == "unknown-users" {
			foundUnknown = true
		}
	}
	if !foundUnknown {
		t.Fatalf("expected unknown-users finding, got %+v", summary.Findings)
	}

	resp = env.do(t, "GET", "/api/maintenance/usage", env.token, nil)
	defer resp.Body.Close()
	var usage dataUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		t.Fatal(err)
	}
	if usage.TelemetryDriver != "postgres" || usage.Logs != 6 || usage.TelemetryBytes <= 0 || !usage.TelemetryOK {
		t.Fatalf("usage = %+v", usage)
	}
}
