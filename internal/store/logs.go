package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// TimeCount is one point of an aggregated log timeline.
type TimeCount struct {
	Time         time.Time `json:"time"`
	Total        int64     `json:"total"`
	Errors       int64     `json:"errors"`
	AuthFailures int64     `json:"authFailures"`
}

// IPCount aggregates log events per client IP.
type IPCount struct {
	IP       string    `json:"ip"`
	Count    int64     `json:"count"`
	Failures int64     `json:"failures"`
	LastSeen time.Time `json:"lastSeen"`
}

// UserCount aggregates log events and traffic per user id.
type UserCount struct {
	UserID   string    `json:"userId"`
	Count    int64     `json:"count"`
	Tx       int64     `json:"tx"`
	Rx       int64     `json:"rx"`
	LastSeen time.Time `json:"lastSeen"`
}

// EventCount is a named counter.
type EventCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// NodeCount is a per-node counter.
type NodeCount struct {
	NodeID   int64  `json:"nodeId"`
	NodeName string `json:"nodeName"`
	Count    int64  `json:"count"`
}

// NodeEventStat aggregates one classified event per node. FirstSeen and
// LastSeen are the real log timestamps of the oldest and newest occurrence.
type NodeEventStat struct {
	NodeID    int64     `json:"nodeId"`
	NodeName  string    `json:"nodeName"`
	Count     int64     `json:"count"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

// NodeEventMark is one classified event occurrence.
type NodeEventMark struct {
	NodeID   int64
	NodeName string
	Time     time.Time
}

// NodeLogStat combines the main counters for one node.
type NodeLogStat struct {
	NodeID       int64  `json:"nodeId"`
	NodeName     string `json:"nodeName"`
	Total        int64  `json:"total"`
	Errors       int64  `json:"errors"`
	Warnings     int64  `json:"warnings"`
	AuthFailures int64  `json:"authFailures"`
}

// InsertLogBatch stores a batch idempotently. It returns false when the batch
// was already processed.
func (s *Store) InsertLogBatch(ctx context.Context, nodeID int64, batchID string, entries []model.LogEntry) (bool, error) {
	if batchID == "" {
		batchID = RandomToken(16)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	inserted, err := s.InsertIgnoreTx(ctx, tx, "log_batches",
		[]string{"node_id", "batch_id", "received_at"}, []string{"node_id", "batch_id"},
		nodeID, batchID, ts(now()))
	if err != nil {
		return false, err
	}
	if !inserted {
		return false, nil // duplicate
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO hy2_logs (node_id, ts, level, logger, message, raw, event, client_ip, user_id,
		 host, port, bytes_tx, bytes_rx, duration_ms, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return false, err
	}
	defer stmt.Close()
	for _, e := range entries {
		if e.TS.IsZero() {
			e.TS = now()
		}
		if _, err := stmt.ExecContext(ctx, nodeID, ts(e.TS), e.Level, e.Logger, e.Message, e.Raw, e.Event,
			e.ClientIP, e.UserID, e.Host, e.Port, e.BytesTx, e.BytesRx, e.DurationMS, e.Error); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func logWhere(f model.LogFilter) (string, []any) {
	where := []string{"1=1"}
	args := []any{}
	if f.NodeID > 0 {
		where = append(where, "l.node_id = ?")
		args = append(args, f.NodeID)
	}
	if f.Level != "" {
		where = append(where, "l.level = ?")
		args = append(args, strings.ToUpper(f.Level))
	}
	if f.Event != "" {
		where = append(where, "l.event = ?")
		args = append(args, f.Event)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + q + "%"
		where = append(where, "(l.message LIKE ? OR l.raw LIKE ? OR l.client_ip LIKE ? OR l.user_id LIKE ? OR l.error LIKE ?)")
		args = append(args, like, like, like, like, like)
	}
	if !f.From.IsZero() {
		where = append(where, "l.ts >= ?")
		args = append(args, ts(f.From))
	}
	if !f.To.IsZero() {
		where = append(where, "l.ts <= ?")
		args = append(args, ts(f.To))
	}
	return strings.Join(where, " AND "), args
}

const logSelect = `SELECT l.id, l.node_id, COALESCE(n.name,''), l.ts, l.level, l.logger, l.message, l.raw, l.event,
	l.client_ip, l.user_id, l.host, l.port, l.bytes_tx, l.bytes_rx, l.duration_ms, l.error
	FROM hy2_logs l LEFT JOIN nodes n ON n.id = l.node_id`

type logScanner interface {
	Scan(dest ...any) error
}

func scanLogEntry(row logScanner) (model.LogEntry, error) {
	var e model.LogEntry
	var t string
	if err := row.Scan(&e.ID, &e.NodeID, &e.NodeName, &t, &e.Level, &e.Logger, &e.Message, &e.Raw,
		&e.Event, &e.ClientIP, &e.UserID, &e.Host, &e.Port, &e.BytesTx, &e.BytesRx, &e.DurationMS, &e.Error); err != nil {
		return e, err
	}
	e.TS = parseTime(t)
	return e, nil
}

// CountLogsFiltered returns the number of rows matching a log filter.
func (s *Store) CountLogsFiltered(ctx context.Context, f model.LogFilter) (int, error) {
	where, args := logWhere(f)
	var total int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM hy2_logs l WHERE `+where, args...).Scan(&total)
	return total, err
}

// QueryLogs returns a page of parsed logs and the total count.
func (s *Store) QueryLogs(ctx context.Context, f model.LogFilter) ([]model.LogEntry, int, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Size < 1 || f.Size > 500 {
		f.Size = 50
	}
	where, args := logWhere(f)
	total, err := s.CountLogsFiltered(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	q := logSelect + ` WHERE ` + where + ` ORDER BY l.ts DESC, l.id DESC LIMIT ? OFFSET ?`
	args = append(args, f.Size, (f.Page-1)*f.Size)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.LogEntry{}
	for rows.Next() {
		e, err := scanLogEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// StreamLogs walks matching log rows newest first without loading them all
// into memory. It stops after limit rows or when fn returns an error.
func (s *Store) StreamLogs(ctx context.Context, f model.LogFilter, limit int, fn func(model.LogEntry) error) (int, error) {
	if limit <= 0 || limit > 200000 {
		limit = 100000
	}
	where, args := logWhere(f)
	q := logSelect + ` WHERE ` + where + ` ORDER BY l.ts DESC, l.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		e, err := scanLogEntry(rows)
		if err != nil {
			return n, err
		}
		if err := fn(e); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func analysisWhere(o model.LogStatsOptions) (string, []any) {
	where := []string{"1=1"}
	args := []any{}
	if o.NodeID > 0 {
		where = append(where, "node_id = ?")
		args = append(args, o.NodeID)
	}
	if !o.From.IsZero() {
		where = append(where, "ts >= ?")
		args = append(args, ts(o.From))
	}
	if !o.To.IsZero() {
		where = append(where, "ts <= ?")
		args = append(args, ts(o.To))
	}
	return strings.Join(where, " AND "), args
}

// LogCounts returns the headline counters for a time range.
func (s *Store) LogCounts(ctx context.Context, o model.LogStatsOptions) (total, errors, warnings, authOK, authFail int64, err error) {
	where, args := analysisWhere(o)
	err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN level IN ('ERROR','FATAL') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN level = 'WARN' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN event IN ('auth_success','connect') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN event = 'auth_fail' THEN 1 ELSE 0 END),0)
		FROM hy2_logs WHERE `+where, args...).Scan(&total, &errors, &warnings, &authOK, &authFail)
	return
}

// LogLevelCounts groups logs by level.
func (s *Store) LogLevelCounts(ctx context.Context, o model.LogStatsOptions) ([]EventCount, error) {
	where, args := analysisWhere(o)
	rows, err := s.DB.QueryContext(ctx,
		`SELECT level, COUNT(*) FROM hy2_logs WHERE `+where+` GROUP BY level ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventCount{}
	for rows.Next() {
		var e EventCount
		if err := rows.Scan(&e.Name, &e.Count); err != nil {
			return nil, err
		}
		if e.Name == "" {
			e.Name = "UNKNOWN"
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LogEventCounts groups logs by classified event.
func (s *Store) LogEventCounts(ctx context.Context, o model.LogStatsOptions) ([]EventCount, error) {
	where, args := analysisWhere(o)
	rows, err := s.DB.QueryContext(ctx,
		`SELECT event, COUNT(*) FROM hy2_logs WHERE `+where+` GROUP BY event ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventCount{}
	for rows.Next() {
		var e EventCount
		if err := rows.Scan(&e.Name, &e.Count); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LogTimeline returns minute buckets. The caller may further aggregate.
func (s *Store) LogTimeline(ctx context.Context, o model.LogStatsOptions) ([]TimeCount, error) {
	where, args := analysisWhere(o)
	rows, err := s.DB.QueryContext(ctx,
		`SELECT substr(ts,1,16) AS minute, COUNT(*),
		 COALESCE(SUM(CASE WHEN level IN ('ERROR','FATAL') THEN 1 ELSE 0 END),0),
		 COALESCE(SUM(CASE WHEN event = 'auth_fail' THEN 1 ELSE 0 END),0)
		 FROM hy2_logs WHERE `+where+` GROUP BY minute ORDER BY minute`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TimeCount{}
	for rows.Next() {
		var minute string
		var t TimeCount
		if err := rows.Scan(&minute, &t.Total, &t.Errors, &t.AuthFailures); err != nil {
			return nil, err
		}
		t.Time, _ = time.Parse("2006-01-02T15:04", minute)
		out = append(out, t)
	}
	return out, rows.Err()
}

// LogTopIPs returns the noisiest client addresses in a time range.
func (s *Store) LogTopIPs(ctx context.Context, o model.LogStatsOptions, limit int) ([]IPCount, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	where, args := analysisWhere(o)
	rows, err := s.DB.QueryContext(ctx,
		`SELECT client_ip, COUNT(*), COALESCE(SUM(CASE WHEN event='auth_fail' THEN 1 ELSE 0 END),0), MAX(ts)
		 FROM hy2_logs WHERE `+where+` AND client_ip != '' GROUP BY client_ip ORDER BY COUNT(*) DESC LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IPCount{}
	for rows.Next() {
		var c IPCount
		var last string
		if err := rows.Scan(&c.IP, &c.Count, &c.Failures, &last); err != nil {
			return nil, err
		}
		c.LastSeen = parseTime(last)
		out = append(out, c)
	}
	return out, rows.Err()
}

// LogTopUsers returns the most active user ids in a time range.
func (s *Store) LogTopUsers(ctx context.Context, o model.LogStatsOptions, limit int) ([]UserCount, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	where, args := analysisWhere(o)
	rows, err := s.DB.QueryContext(ctx,
		`SELECT user_id, COUNT(*), COALESCE(SUM(bytes_tx),0), COALESCE(SUM(bytes_rx),0), MAX(ts)
		 FROM hy2_logs WHERE `+where+` AND user_id != '' GROUP BY user_id ORDER BY COUNT(*) DESC LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserCount{}
	for rows.Next() {
		var c UserCount
		var last string
		if err := rows.Scan(&c.UserID, &c.Count, &c.Tx, &c.Rx, &last); err != nil {
			return nil, err
		}
		c.LastSeen = parseTime(last)
		out = append(out, c)
	}
	return out, rows.Err()
}

// AuthFailuresByIP aggregates failed authentications per IP above a threshold.
func (s *Store) AuthFailuresByIP(ctx context.Context, o model.LogStatsOptions, min int64) ([]IPCount, error) {
	where, args := analysisWhere(o)
	q := `SELECT client_ip, COUNT(*), MAX(ts) FROM hy2_logs
		WHERE ` + where + ` AND event='auth_fail' AND client_ip != ''
		GROUP BY client_ip HAVING COUNT(*) >= ? ORDER BY COUNT(*) DESC LIMIT 50`
	args = append(args, min)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IPCount{}
	for rows.Next() {
		var c IPCount
		var last string
		if err := rows.Scan(&c.IP, &c.Count, &last); err != nil {
			return nil, err
		}
		c.Failures = c.Count
		c.LastSeen = parseTime(last)
		out = append(out, c)
	}
	return out, rows.Err()
}

// AuthFailuresByNode aggregates failed authentications per node.
func (s *Store) AuthFailuresByNode(ctx context.Context, o model.LogStatsOptions, min int64) ([]NodeCount, error) {
	where, args := analysisWhere(o)
	q := `SELECT l.node_id, COALESCE(MAX(n.name),''), COUNT(*) FROM hy2_logs l
		LEFT JOIN nodes n ON n.id = l.node_id
		WHERE ` + strings.ReplaceAll(where, "node_id", "l.node_id") + ` AND l.event='auth_fail'
		GROUP BY l.node_id HAVING COUNT(*) >= ? ORDER BY COUNT(*) DESC`
	args = append(args, min)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeCount{}
	for rows.Next() {
		var c NodeCount
		if err := rows.Scan(&c.NodeID, &c.NodeName, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UnknownAuthUsers returns failed auth attempts for ids that do not exist.
func (s *Store) UnknownAuthUsers(ctx context.Context, o model.LogStatsOptions, limit int) ([]UserCount, error) {
	if limit <= 0 {
		limit = 20
	}
	where, args := analysisWhere(o)
	q := `SELECT l.user_id, COUNT(*), MAX(l.ts) FROM hy2_logs l
		WHERE ` + strings.ReplaceAll(where, "node_id", "l.node_id") + ` AND l.event='auth_fail' AND l.user_id != ''
		AND NOT EXISTS (SELECT 1 FROM users u WHERE u.user_id = l.user_id)
		GROUP BY l.user_id ORDER BY COUNT(*) DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserCount{}
	for rows.Next() {
		var c UserCount
		var last string
		if err := rows.Scan(&c.UserID, &c.Count, &last); err != nil {
			return nil, err
		}
		c.LastSeen = parseTime(last)
		out = append(out, c)
	}
	return out, rows.Err()
}

// LogNodeStats returns headline counters grouped by node.
func (s *Store) LogNodeStats(ctx context.Context, o model.LogStatsOptions) ([]NodeLogStat, error) {
	where, args := analysisWhere(o)
	q := `SELECT l.node_id, COALESCE(MAX(n.name),''), COUNT(*),
		COALESCE(SUM(CASE WHEN l.level IN ('ERROR','FATAL') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN l.level = 'WARN' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN l.event = 'auth_fail' THEN 1 ELSE 0 END),0)
		FROM hy2_logs l LEFT JOIN nodes n ON n.id = l.node_id
		WHERE ` + strings.ReplaceAll(where, "node_id", "l.node_id") + `
		GROUP BY l.node_id ORDER BY COUNT(*) DESC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeLogStat{}
	for rows.Next() {
		var c NodeLogStat
		if err := rows.Scan(&c.NodeID, &c.NodeName, &c.Total, &c.Errors, &c.Warnings, &c.AuthFailures); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NodeEventStats counts a classified event per node and keeps the oldest and
// newest occurrence so findings can report real timestamps.
func (s *Store) NodeEventStats(ctx context.Context, event string, o model.LogStatsOptions) ([]NodeEventStat, error) {
	where, args := analysisWhere(o)
	q := `SELECT l.node_id, COALESCE(MAX(n.name),''), COUNT(*), MIN(l.ts), MAX(l.ts) FROM hy2_logs l
		LEFT JOIN nodes n ON n.id = l.node_id
		WHERE ` + strings.ReplaceAll(where, "node_id", "l.node_id") + ` AND l.event = ?
		GROUP BY l.node_id ORDER BY COUNT(*) DESC`
	args = append(args, event)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeEventStat{}
	for rows.Next() {
		var c NodeEventStat
		var first, last string
		if err := rows.Scan(&c.NodeID, &c.NodeName, &c.Count, &first, &last); err != nil {
			return nil, err
		}
		c.FirstSeen, c.LastSeen = parseTime(first), parseTime(last)
		out = append(out, c)
	}
	return out, rows.Err()
}

// NodeEventMarks returns individual occurrences of a classified event, newest
// first. The result is capped so a pathological restart loop cannot exhaust
// memory; callers that need exact totals should use NodeEventStats.
func (s *Store) NodeEventMarks(ctx context.Context, event string, o model.LogStatsOptions) ([]NodeEventMark, error) {
	const maxMarks = 100000
	where, args := analysisWhere(o)
	q := `SELECT l.node_id, COALESCE(n.name,''), l.ts FROM hy2_logs l
		LEFT JOIN nodes n ON n.id = l.node_id
		WHERE ` + strings.ReplaceAll(where, "node_id", "l.node_id") + ` AND l.event = ?
		ORDER BY l.ts DESC LIMIT ?`
	args = append(args, event, maxMarks)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeEventMark{}
	for rows.Next() {
		var m NodeEventMark
		var at string
		if err := rows.Scan(&m.NodeID, &m.NodeName, &at); err != nil {
			return nil, err
		}
		m.Time = parseTime(at)
		out = append(out, m)
	}
	return out, rows.Err()
}

// RecentLogs returns the newest logs, optionally for one node.
func (s *Store) RecentLogs(ctx context.Context, nodeID int64, limit int) ([]model.LogEntry, error) {
	logs, _, err := s.QueryLogs(ctx, model.LogFilter{NodeID: nodeID, Page: 1, Size: limit})
	return logs, err
}

// CountLogs returns the number of stored log rows.
func (s *Store) CountLogs(ctx context.Context) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM hy2_logs`).Scan(&n)
	return n, err
}

// LogRetentionCutoff returns the timestamp before which logs can be deleted.
func LogRetentionCutoff(days int) time.Time {
	return now().AddDate(0, 0, -days)
}

// DeleteLogsBefore removes old logs.
func (s *Store) DeleteLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM hy2_logs WHERE ts < ?`, ts(before))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// TableCount is a small helper used by tests and diagnostics.
func (s *Store) TableCount(ctx context.Context, table string) (int64, error) {
	var n int64
	allowed := map[string]bool{"users": true, "nodes": true, "hy2_logs": true, "panel_groups": true}
	if !allowed[table] {
		return 0, fmt.Errorf("unsupported table")
	}
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n)
	return n, err
}

// Ensure sql is referenced when build tags trim methods.
var _ = sql.ErrNoRows
