package logs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

func newTestAnalyzer(t *testing.T) (*Analyzer, *store.Store, context.Context) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st), st, context.Background()
}

func insertEvents(t *testing.T, st *store.Store, nodeID int64, event string, times ...time.Time) {
	t.Helper()
	entries := make([]model.LogEntry, 0, len(times))
	for _, at := range times {
		entries = append(entries, model.LogEntry{
			TS: at, Level: "INFO", Event: event, Message: event, Raw: event,
		})
	}
	if _, err := st.InsertLogBatch(context.Background(), nodeID, store.RandomToken(12), entries); err != nil {
		t.Fatal(err)
	}
}

func findFinding(findings []model.Finding, id string) *model.Finding {
	for i := range findings {
		if findings[i].ID == id {
			return &findings[i]
		}
	}
	return nil
}

func TestRestartLoopFlagsBurst(t *testing.T) {
	a, st, ctx := newTestAnalyzer(t)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	insertEvents(t, st, 7, "start", base, base.Add(time.Minute), base.Add(2*time.Minute))

	summary, err := a.Analyze(ctx, model.LogStatsOptions{
		From: base.Add(-time.Hour), To: base.Add(3 * time.Hour),
	}, 0, Thresholds{})
	if err != nil {
		t.Fatal(err)
	}
	f := findFinding(summary.Findings, "restart-loop-7")
	if f == nil {
		t.Fatalf("restart-loop finding missing: %+v", summary.Findings)
	}
	if f.Count != 3 {
		t.Fatalf("count = %d, want 3", f.Count)
	}
	if f.CountLabel != "窗口内启动 3 次" {
		t.Fatalf("count label = %q", f.CountLabel)
	}
	if !f.FirstSeen.Equal(base) || !f.LastSeen.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("timestamps = %s .. %s", f.FirstSeen, f.LastSeen)
	}
}

func TestRestartLoopIgnoresSpreadOutStarts(t *testing.T) {
	a, st, ctx := newTestAnalyzer(t)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	insertEvents(t, st, 8, "start", base, base.Add(3*time.Hour), base.Add(6*time.Hour))

	summary, err := a.Analyze(ctx, model.LogStatsOptions{
		From: base.Add(-time.Hour), To: base.Add(7 * time.Hour),
	}, 0, Thresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if f := findFinding(summary.Findings, "restart-loop-8"); f != nil {
		t.Fatalf("spread out starts should not be a restart loop: %+v", f)
	}
}

func TestRestartLoopFlagsSustainedRate(t *testing.T) {
	a, st, ctx := newTestAnalyzer(t)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	// One restart per hour for six hours: no tight burst, but clearly a loop.
	times := make([]time.Time, 0, 6)
	for i := 0; i < 6; i++ {
		times = append(times, base.Add(time.Duration(i)*time.Hour))
	}
	insertEvents(t, st, 9, "start", times...)

	summary, err := a.Analyze(ctx, model.LogStatsOptions{
		From: base.Add(-30 * time.Minute), To: base.Add(5*time.Hour + 30*time.Minute),
	}, 0, Thresholds{})
	if err != nil {
		t.Fatal(err)
	}
	f := findFinding(summary.Findings, "restart-loop-9")
	if f == nil {
		t.Fatalf("sustained restarts should be flagged: %+v", summary.Findings)
	}
	if !containsDetail(f.Details, "窗口内平均") {
		t.Fatalf("details = %v", f.Details)
	}
}

func TestRestartLoopIgnoresIntentionalRestarts(t *testing.T) {
	a, st, ctx := newTestAnalyzer(t)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		insertEvents(t, st, 10, "shutdown", at.Add(-30*time.Second))
		insertEvents(t, st, 10, "start", at)
	}

	summary, err := a.Analyze(ctx, model.LogStatsOptions{
		From: base.Add(-time.Hour), To: base.Add(time.Hour),
	}, 0, Thresholds{})
	if err != nil {
		t.Fatal(err)
	}
	if f := findFinding(summary.Findings, "restart-loop-10"); f != nil {
		t.Fatalf("intentional restarts should be excluded: %+v", f)
	}
}

func TestRestartLoopOnlyExcusesOneStartPerShutdown(t *testing.T) {
	a, st, ctx := newTestAnalyzer(t)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	insertEvents(t, st, 11, "shutdown", base.Add(-30*time.Second))
	insertEvents(t, st, 11, "start", base, base.Add(time.Minute), base.Add(2*time.Minute), base.Add(3*time.Minute))

	summary, err := a.Analyze(ctx, model.LogStatsOptions{
		From: base.Add(-time.Hour), To: base.Add(time.Hour),
	}, 0, Thresholds{})
	if err != nil {
		t.Fatal(err)
	}
	f := findFinding(summary.Findings, "restart-loop-11")
	if f == nil {
		t.Fatalf("crashes after an intentional restart should still be flagged: %+v", summary.Findings)
	}
	if f.Count != 3 {
		t.Fatalf("count = %d, want 3 unplanned starts", f.Count)
	}
	if !containsDetail(f.Details, "已排除 1 次主动重启") {
		t.Fatalf("details = %v", f.Details)
	}
}

func TestErrorBurstUsesRealTimestamps(t *testing.T) {
	a, st, ctx := newTestAnalyzer(t)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		insertEvents(t, st, 12, "error", base.Add(time.Duration(i)*time.Minute))
	}

	summary, err := a.Analyze(ctx, model.LogStatsOptions{
		From: base.Add(-time.Hour), To: base.Add(time.Hour),
	}, 0, Thresholds{ErrorBurstCount: 3})
	if err != nil {
		t.Fatal(err)
	}
	f := findFinding(summary.Findings, "error-burst-12")
	if f == nil {
		t.Fatalf("error burst finding missing: %+v", summary.Findings)
	}
	if !f.FirstSeen.Equal(base) || !f.LastSeen.Equal(base.Add(4*time.Minute)) {
		t.Fatalf("timestamps = %s .. %s", f.FirstSeen, f.LastSeen)
	}
}

func TestUnplannedStartsRequiresRecentShutdown(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	crashes, planned := unplannedStarts(
		[]time.Time{base.Add(3 * time.Minute)},
		[]time.Time{base},
	)
	if planned != 0 || len(crashes) != 1 {
		t.Fatalf("old shutdown should not excuse a start: crashes=%v planned=%d", crashes, planned)
	}
	crashes, planned = unplannedStarts(
		[]time.Time{base.Add(30 * time.Second)},
		[]time.Time{base},
	)
	if planned != 1 || len(crashes) != 0 {
		t.Fatalf("recent shutdown should excuse a start: crashes=%v planned=%d", crashes, planned)
	}
}

func TestMaxStartBurst(t *testing.T) {
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	starts := []time.Time{
		base,
		base.Add(5 * time.Minute),
		base.Add(10 * time.Minute), // exactly on the boundary, still counted
		base.Add(11 * time.Minute),
	}
	if got := maxStartBurst(starts, 10*time.Minute); got != 3 {
		t.Fatalf("maxStartBurst = %d, want 3", got)
	}
	if got := maxStartBurst(nil, 10*time.Minute); got != 0 {
		t.Fatalf("empty maxStartBurst = %d", got)
	}
}

func containsDetail(details []string, want string) bool {
	for _, d := range details {
		if strings.Contains(d, want) {
			return true
		}
	}
	return false
}
