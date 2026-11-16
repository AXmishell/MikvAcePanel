package store

import (
	"context"
	"os"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// LogStore is the append-only telemetry backend. The SQLite Store implements
// it directly, and internal/store/pglogs provides a PostgreSQL implementation
// selected with telemetry.driver: postgres.
//
// Only log data crosses this boundary. Users, nodes, settings and traffic
// counters stay in the control database so they keep SQLite transactions and
// the single-file backup.
type LogStore interface {
	// Driver reports the backend name for health and diagnostics.
	Driver() string
	// Ping verifies that the backend is reachable.
	Ping(ctx context.Context) error

	// InsertLogBatch stores one batch exactly once. It returns false when the
	// batch id was already processed.
	InsertLogBatch(ctx context.Context, nodeID int64, batchID string, entries []model.LogEntry) (bool, error)

	CountLogsFiltered(ctx context.Context, f model.LogFilter) (int, error)
	QueryLogs(ctx context.Context, f model.LogFilter) ([]model.LogEntry, int, error)
	StreamLogs(ctx context.Context, f model.LogFilter, limit int, fn func(model.LogEntry) error) (int, error)
	LogCounts(ctx context.Context, o model.LogStatsOptions) (total, errors, warnings, authOK, authFail int64, err error)
	LogLevelCounts(ctx context.Context, o model.LogStatsOptions) ([]EventCount, error)
	LogEventCounts(ctx context.Context, o model.LogStatsOptions) ([]EventCount, error)
	LogTimeline(ctx context.Context, o model.LogStatsOptions) ([]TimeCount, error)
	LogTopIPs(ctx context.Context, o model.LogStatsOptions, limit int) ([]IPCount, error)
	LogTopUsers(ctx context.Context, o model.LogStatsOptions, limit int) ([]UserCount, error)
	AuthFailuresByIP(ctx context.Context, o model.LogStatsOptions, min int64) ([]IPCount, error)
	AuthFailuresByNode(ctx context.Context, o model.LogStatsOptions, min int64) ([]NodeCount, error)
	UnknownAuthUsers(ctx context.Context, o model.LogStatsOptions, limit int) ([]UserCount, error)
	LogNodeStats(ctx context.Context, o model.LogStatsOptions) ([]NodeLogStat, error)
	NodeEventStats(ctx context.Context, event string, o model.LogStatsOptions) ([]NodeEventStat, error)
	NodeEventMarks(ctx context.Context, event string, o model.LogStatsOptions) ([]NodeEventMark, error)
	RecentLogs(ctx context.Context, nodeID int64, limit int) ([]model.LogEntry, error)
	CountLogs(ctx context.Context) (int64, error)
	DeleteLogsBefore(ctx context.Context, before time.Time) (int64, error)

	// PruneLogs deletes log rows and batch receipts older than before.
	PruneLogs(ctx context.Context, before time.Time) (logs, batches int64, err error)
	// LogUsage reports the row counts that grow with log ingestion.
	LogUsage(ctx context.Context) (logs, batches int64, err error)
	// TelemetryStats reports storage usage for the maintenance page.
	TelemetryStats(ctx context.Context) (TelemetryStats, error)
}

// LogDirectory resolves control-plane identifiers for a telemetry backend
// that cannot join against the SQLite tables.
type LogDirectory interface {
	// NodeNames maps node ids to their current names. Missing ids are omitted.
	NodeNames(ctx context.Context, ids []int64) (map[int64]string, error)
	// ExistingUserIDs returns the subset of user ids that exist.
	ExistingUserIDs(ctx context.Context, ids []string) (map[string]bool, error)
}

// TelemetryStats is a backend-neutral storage summary.
type TelemetryStats struct {
	Driver     string `json:"driver"`
	Bytes      int64  `json:"bytes"`
	Rows       int64  `json:"rows"`
	Partitions int    `json:"partitions"`
	// Detail carries backend-specific information such as the schema name.
	Detail string `json:"detail,omitempty"`
}

// Driver implements LogStore.
func (s *Store) Driver() string { return s.dialect.Name() }

// PruneLogs implements LogStore using the SQLite log tables.
func (s *Store) PruneLogs(ctx context.Context, before time.Time) (int64, int64, error) {
	cutoff := ts(before)
	logs, err := s.deleteBefore(ctx, "hy2_logs", "ts", cutoff)
	if err != nil {
		return logs, 0, err
	}
	batches, err := s.deleteBefore(ctx, "log_batches", "received_at", cutoff)
	return logs, batches, err
}

// LogUsage implements LogStore.
func (s *Store) LogUsage(ctx context.Context) (int64, int64, error) {
	var logs, batches int64
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM hy2_logs`).Scan(&logs); err != nil {
		return 0, 0, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM log_batches`).Scan(&batches); err != nil {
		return logs, 0, err
	}
	return logs, batches, nil
}

// ControlDataUsage counts the telemetry rows that stay in the SQLite control
// database. Log rows are counted through the LogStore instead, which may be
// PostgreSQL.
func (s *Store) ControlDataUsage(ctx context.Context) (DataUsage, error) {
	var u DataUsage
	counts := []struct {
		dst *int64
		sql string
	}{
		{&u.Events, `SELECT COUNT(*) FROM events`},
		{&u.StatsBatches, `SELECT COUNT(*) FROM stats_batches`},
		{&u.NodeMetrics, `SELECT COUNT(*) FROM node_metrics`},
	}
	for _, c := range counts {
		if err := s.DB.QueryRowContext(ctx, c.sql).Scan(c.dst); err != nil {
			return u, err
		}
	}
	return u, nil
}

// PruneControl removes stale control-plane telemetry (events, traffic batch
// receipts and heartbeat samples). Log rows are pruned through the LogStore,
// which may live in PostgreSQL.
func (s *Store) PruneControl(ctx context.Context, logDays, metricDays int) (PruneResult, error) {
	var res PruneResult
	var err error
	if logDays > 0 {
		cutoff := ts(now().AddDate(0, 0, -logDays))
		if res.Events, err = s.deleteBefore(ctx, "events", "created_at", cutoff); err != nil {
			return res, err
		}
		statsCutoff := ts(now().AddDate(0, 0, -maxInt(logDays, 7)))
		if res.StatsBatches, err = s.deleteBefore(ctx, "stats_batches", "received_at", statsCutoff); err != nil {
			return res, err
		}
	}
	if metricDays > 0 {
		cutoff := ts(now().AddDate(0, 0, -metricDays))
		if res.NodeMetrics, err = s.deleteBefore(ctx, "node_metrics", "ts", cutoff); err != nil {
			return res, err
		}
	}
	return res, nil
}

// TelemetryStats implements LogStore for the embedded SQLite backend.
func (s *Store) TelemetryStats(ctx context.Context) (TelemetryStats, error) {
	logs, batches, err := s.LogUsage(ctx)
	if err != nil {
		return TelemetryStats{}, err
	}
	var bytes int64
	if info, err := os.Stat(s.Path); err == nil {
		bytes = info.Size()
	}
	return TelemetryStats{
		Driver: s.Driver(),
		Bytes:  bytes,
		Rows:   logs + batches,
	}, nil
}

// NodeNames implements LogDirectory.
func (s *Store) NodeNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	marks, args := intMarks(ids)
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name FROM nodes WHERE id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ExistingUserIDs implements LogDirectory.
func (s *Store) ExistingUserIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT user_id FROM users WHERE user_id IN (`+joinStrings(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Compile-time check.
var _ LogStore = (*Store)(nil)
var _ LogDirectory = (*Store)(nil)
