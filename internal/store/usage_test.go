package store

import (
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestPruneRemovesExpiredTelemetry(t *testing.T) {
	st, ctx := testStore(t)
	if err := st.EnsureDefaultSettings(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := st.CreateNode(ctx, &model.Node{
		Name: "n", IP: "203.0.113.1", Port: 443, Active: true, StatsPort: 9999, TLSMode: "selfsigned",
	}, "h", "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	old := now().AddDate(0, 0, -40)
	recent := now().AddDate(0, 0, -1)
	for _, item := range []struct {
		ts  time.Time
		msg string
	}{{old, "old"}, {recent, "recent"}} {
		if err := st.InsertMetric(ctx, &model.NodeMetric{NodeID: n.ID, TS: item.ts, CPUPct: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.InsertLogBatch(ctx, n.ID, item.msg, []model.LogEntry{
			{TS: item.ts, Level: "INFO", Message: item.msg, Event: "log"},
		}); err != nil {
			t.Fatal(err)
		}
		if item.msg == "old" {
			if _, err := st.DB.ExecContext(ctx,
				`UPDATE log_batches SET received_at = ? WHERE node_id = ? AND batch_id = ?`,
				ts(old), n.ID, item.msg); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := st.DB.ExecContext(ctx,
			`INSERT INTO events (type, node_id, message, severity, created_at) VALUES (?, ?, ?, ?, ?)`,
			"test."+item.msg, n.ID, item.msg, "info", ts(item.ts)); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.ExecContext(ctx,
			`INSERT INTO stats_batches (node_id, batch_id, received_at) VALUES (?, ?, ?)`,
			n.ID, item.msg, ts(item.ts)); err != nil {
			t.Fatal(err)
		}
	}

	res, err := st.Prune(ctx, 14, 30)
	if err != nil {
		t.Fatal(err)
	}
	if res.Logs != 1 || res.LogBatches != 1 || res.Events != 1 || res.StatsBatches != 1 || res.NodeMetrics != 1 {
		t.Fatalf("prune result = %+v", res)
	}
	if res.Total() != 5 {
		t.Fatalf("total = %d", res.Total())
	}
	usage, err := st.DataUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Logs != 1 || usage.LogBatches != 1 || usage.Events != 1 || usage.StatsBatches != 1 || usage.NodeMetrics != 1 {
		t.Fatalf("usage after prune = %+v", usage)
	}
	if err := st.Compact(ctx); err != nil {
		t.Fatal(err)
	}
}
