package pglogs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

type fakeDirectory struct {
	names map[int64]string
	users map[string]bool
}

func (f fakeDirectory) NodeNames(_ context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	for _, id := range ids {
		if name, ok := f.names[id]; ok {
			out[id] = name
		}
	}
	return out, nil
}

func (f fakeDirectory) ExistingUserIDs(_ context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		if f.users[id] {
			out[id] = true
		}
	}
	return out, nil
}

func testDSN() string {
	return strings.TrimSpace(os.Getenv("MIKVACE_TEST_POSTGRES_DSN"))
}

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("set MIKVACE_TEST_POSTGRES_DSN to run PostgreSQL telemetry tests")
	}
	schema := fmt.Sprintf("mikvace_test_%d", time.Now().UnixNano())
	ctx := context.Background()
	st, err := Open(ctx, Config{
		DSN:    dsn,
		Schema: schema,
		Directory: fakeDirectory{
			names: map[int64]string{1: "tokyo", 2: "osaka"},
			users: map[string]bool{"alice": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quoteIdent(schema)+` CASCADE`)
		_ = st.Close()
	})
	return st, schema
}

func entry(ts time.Time, level, event, user, ip string) model.LogEntry {
	return model.LogEntry{
		TS: ts, Level: level, Logger: "hysteria", Event: event,
		UserID: user, ClientIP: ip, Message: event + " message",
		Raw: "raw " + event, Error: "",
	}
}

func TestInsertQueryAndIdempotency(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	entries := []model.LogEntry{
		entry(now.Add(-2*time.Minute), "INFO", "connect", "alice", "10.0.0.1"),
		entry(now.Add(-time.Minute), "ERROR", "error", "alice", "10.0.0.1"),
		entry(now, "WARN", "auth_fail", "ghost", "10.0.0.2"),
	}
	inserted, err := st.InsertLogBatch(ctx, 1, "batch-1", entries)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("first insert must be applied")
	}
	inserted, err = st.InsertLogBatch(ctx, 1, "batch-1", entries)
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("duplicate batch must not be applied")
	}
	if n, err := st.CountLogs(ctx); err != nil || n != 3 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	logs, total, err := st.QueryLogs(ctx, model.LogFilter{Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(logs) != 3 {
		t.Fatalf("total=%d len=%d", total, len(logs))
	}
	if logs[0].NodeName != "tokyo" {
		t.Fatalf("node name = %q", logs[0].NodeName)
	}
	if logs[0].ID <= logs[1].ID {
		t.Fatalf("expected newest first, got ids %d %d", logs[0].ID, logs[1].ID)
	}
	if got, _, err := st.QueryLogs(ctx, model.LogFilter{Level: "ERROR", Page: 1, Size: 10}); err != nil || len(got) != 1 {
		t.Fatalf("level filter: len=%d err=%v", len(got), err)
	}
	if got, _, err := st.QueryLogs(ctx, model.LogFilter{Query: "10.0.0.2", Page: 1, Size: 10}); err != nil || len(got) != 1 {
		t.Fatalf("search: len=%d err=%v", len(got), err)
	}
	if got, _, err := st.QueryLogs(ctx, model.LogFilter{NodeIDs: []int64{2}, Page: 1, Size: 10}); err != nil || len(got) != 0 {
		t.Fatalf("node scope: len=%d err=%v", len(got), err)
	}
}

func TestAggregations(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	entries := []model.LogEntry{
		entry(now.Add(-3*time.Minute), "ERROR", "auth_fail", "ghost", "10.0.0.2"),
		entry(now.Add(-2*time.Minute), "WARN", "auth_fail", "ghost", "10.0.0.2"),
		entry(now.Add(-time.Minute), "INFO", "connect", "alice", "10.0.0.1"),
		entry(now, "INFO", "auth_success", "alice", "10.0.0.1"),
	}
	if _, err := st.InsertLogBatch(ctx, 1, "agg-1", entries); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertLogBatch(ctx, 2, "agg-2", []model.LogEntry{
		entry(now, "ERROR", "error", "", "10.0.0.3"),
	}); err != nil {
		t.Fatal(err)
	}
	opts := model.LogStatsOptions{From: now.Add(-time.Hour), To: now.Add(time.Minute)}
	total, errs, warns, authOK, authFail, err := st.LogCounts(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || errs != 2 || warns != 1 || authOK != 2 || authFail != 2 {
		t.Fatalf("counts total=%d errs=%d warns=%d ok=%d fail=%d", total, errs, warns, authOK, authFail)
	}
	levels, err := st.LogLevelCounts(ctx, opts)
	if err != nil || len(levels) == 0 {
		t.Fatalf("levels=%v err=%v", levels, err)
	}
	timeline, err := st.LogTimeline(ctx, opts)
	if err != nil || len(timeline) == 0 {
		t.Fatalf("timeline=%v err=%v", timeline, err)
	}
	ips, err := st.LogTopIPs(ctx, opts, 10)
	if err != nil || len(ips) != 3 {
		t.Fatalf("ips=%v err=%v", ips, err)
	}
	var topFails int64
	for _, ip := range ips {
		if ip.IP == "10.0.0.2" {
			topFails = ip.Failures
		}
	}
	if topFails != 2 {
		t.Fatalf("expected two failures for 10.0.0.2, ips=%v", ips)
	}
	fails, err := st.AuthFailuresByIP(ctx, opts, 2)
	if err != nil || len(fails) != 1 || fails[0].IP != "10.0.0.2" {
		t.Fatalf("fails=%v err=%v", fails, err)
	}
	bynode, err := st.AuthFailuresByNode(ctx, opts, 2)
	if err != nil || len(bynode) != 1 || bynode[0].NodeName != "tokyo" {
		t.Fatalf("bynode=%v err=%v", bynode, err)
	}
	unknown, err := st.UnknownAuthUsers(ctx, opts, 10)
	if err != nil || len(unknown) != 1 || unknown[0].UserID != "ghost" || unknown[0].Count != 2 {
		t.Fatalf("unknown=%v err=%v", unknown, err)
	}
	nodeStats, err := st.LogNodeStats(ctx, opts)
	if err != nil || len(nodeStats) != 2 {
		t.Fatalf("nodeStats=%v err=%v", nodeStats, err)
	}
	events, err := st.NodeEventStats(ctx, "auth_fail", opts)
	if err != nil || len(events) != 1 || events[0].NodeName != "tokyo" {
		t.Fatalf("events=%v err=%v", events, err)
	}
	marks, err := st.NodeEventMarks(ctx, "auth_fail", opts)
	if err != nil || len(marks) != 2 {
		t.Fatalf("marks=%v err=%v", marks, err)
	}
	if marks[0].Time.Before(marks[1].Time) {
		t.Fatal("marks must be newest first")
	}
}

func TestPruneDropsExpiredPartitions(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -30)
	entries := []model.LogEntry{
		entry(old, "INFO", "connect", "alice", "10.0.0.1"),
		entry(now, "INFO", "connect", "alice", "10.0.0.1"),
	}
	if _, err := st.InsertLogBatch(ctx, 1, "prune-1", entries); err != nil {
		t.Fatal(err)
	}
	logs, batches, err := st.PruneLogs(ctx, now.AddDate(0, 0, -14))
	if err != nil {
		t.Fatal(err)
	}
	_ = logs
	_ = batches
	if n, err := st.CountLogs(ctx); err != nil || n != 1 {
		t.Fatalf("count after prune = %d err=%v", n, err)
	}
	stats, err := st.TelemetryStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Driver != "postgres" || stats.Partitions < 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestInsertClampsBadTimestamps(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	future := time.Now().UTC().AddDate(10, 0, 0)
	if _, err := st.InsertLogBatch(ctx, 1, "clamp-1", []model.LogEntry{
		entry(future, "INFO", "connect", "alice", "10.0.0.1"),
	}); err != nil {
		t.Fatal(err)
	}
	logs, _, err := st.QueryLogs(ctx, model.LogFilter{Page: 1, Size: 10})
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs=%d err=%v", len(logs), err)
	}
	if time.Since(logs[0].TS) > time.Minute {
		t.Fatalf("timestamp was not clamped: %v", logs[0].TS)
	}
}

func TestPruneLogsInterface(t *testing.T) {
	st, _ := openTestStore(t)
	var _ store.LogStore = st
}

func TestOpenValidatesConfigBeforeConnecting(t *testing.T) {
	if _, err := Open(context.Background(), Config{DSN: ""}); err == nil {
		t.Fatal("empty DSN must be rejected")
	}
	if _, err := Open(context.Background(), Config{DSN: "postgres://example", Schema: "bad-schema"}); err == nil {
		t.Fatal("invalid schema must be rejected")
	}
}

func TestPartitionDay(t *testing.T) {
	day, ok := partitionDay("hy2_logs_20261116")
	if !ok || day.Format("20060102") != "20261116" {
		t.Fatalf("day=%v ok=%v", day, ok)
	}
	if _, ok := partitionDay("hy2_logs_other"); ok {
		t.Fatal("unexpected partition match")
	}
	if _, ok := partitionDay("log_batches_20261116"); ok {
		t.Fatal("unexpected partition match")
	}
}
