package pglogs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// argBuilder numbers PostgreSQL placeholders as $1, $2, ...
type argBuilder struct {
	args []any
}

func (a *argBuilder) add(v any) string {
	a.args = append(a.args, v)
	return fmt.Sprintf("$%d", len(a.args))
}

// logWhere mirrors store.logWhere. Node names are resolved separately because
// nodes live in the SQLite control database.
func logWhere(f model.LogFilter) (string, []any) {
	a := &argBuilder{}
	where := []string{"1=1"}
	if f.NodeID > 0 {
		where = append(where, "l.node_id = "+a.add(f.NodeID))
	} else if f.NodeIDs != nil {
		if len(f.NodeIDs) == 0 {
			where = append(where, "1=0")
		} else if marks, args := intMarks(f.NodeIDs); len(marks) > 0 {
			where = append(where, "l.node_id IN ("+marks+")")
			a.args = append(a.args, args...)
		}
	}
	if f.Level != "" {
		where = append(where, "l.level = "+a.add(strings.ToUpper(f.Level)))
	}
	if f.Event != "" {
		where = append(where, "l.event = "+a.add(f.Event))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + q + "%"
		where = append(where, "(l.message ILIKE "+a.add(like)+" OR l.raw ILIKE "+a.add(like)+
			" OR l.client_ip ILIKE "+a.add(like)+" OR l.user_id ILIKE "+a.add(like)+
			" OR l.error ILIKE "+a.add(like)+")")
	}
	if !f.From.IsZero() {
		where = append(where, "l.ts >= "+a.add(f.From.UTC()))
	}
	if !f.To.IsZero() {
		where = append(where, "l.ts <= "+a.add(f.To.UTC()))
	}
	return strings.Join(where, " AND "), a.args
}

// analysisWhere mirrors store.analysisWhere. It uses the unqualified node_id
// column because aggregate queries do not alias the table.
func analysisWhere(o model.LogStatsOptions) (string, []any) {
	a := &argBuilder{}
	where := []string{"1=1"}
	if o.NodeID > 0 {
		where = append(where, "node_id = "+a.add(o.NodeID))
	} else if o.NodeIDs != nil {
		if len(o.NodeIDs) == 0 {
			where = append(where, "1=0")
		} else if marks, args := intMarks(o.NodeIDs); len(marks) > 0 {
			where = append(where, "node_id IN ("+marks+")")
			a.args = append(a.args, args...)
		}
	}
	if !o.From.IsZero() {
		where = append(where, "ts >= "+a.add(o.From.UTC()))
	}
	if !o.To.IsZero() {
		where = append(where, "ts <= "+a.add(o.To.UTC()))
	}
	return strings.Join(where, " AND "), a.args
}

func intMarks(ids []int64) (string, []any) {
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		marks[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	return strings.Join(marks, ","), args
}

func (s *Store) logSelect() string {
	return `SELECT l.id, l.node_id, l.ts, l.level, l.logger, l.message, l.raw, l.event,
		l.client_ip, l.user_id, l.host, l.port, l.bytes_tx, l.bytes_rx, l.duration_ms, l.error
		FROM ` + s.qualified("hy2_logs") + ` l`
}

func (s *Store) scanLogEntry(row interface{ Scan(...any) error }) (model.LogEntry, error) {
	var e model.LogEntry
	if err := row.Scan(&e.ID, &e.NodeID, &e.TS, &e.Level, &e.Logger, &e.Message, &e.Raw,
		&e.Event, &e.ClientIP, &e.UserID, &e.Host, &e.Port, &e.BytesTx, &e.BytesRx,
		&e.DurationMS, &e.Error); err != nil {
		return e, err
	}
	e.TS = e.TS.UTC()
	return e, nil
}

// resolveNames fills NodeName from the control database.
func (s *Store) resolveNames(ctx context.Context, entries []model.LogEntry) {
	if s.directory == nil || len(entries) == 0 {
		return
	}
	ids := make([]int64, 0, len(entries))
	seen := map[int64]bool{}
	for _, e := range entries {
		if e.NodeID > 0 && !seen[e.NodeID] {
			seen[e.NodeID] = true
			ids = append(ids, e.NodeID)
		}
	}
	names, err := s.directory.NodeNames(ctx, ids)
	if err != nil {
		return
	}
	for i := range entries {
		entries[i].NodeName = names[entries[i].NodeID]
	}
}

func (s *Store) nodeNames(ctx context.Context, ids []int64) map[int64]string {
	if s.directory == nil || len(ids) == 0 {
		return map[int64]string{}
	}
	names, err := s.directory.NodeNames(ctx, ids)
	if err != nil {
		return map[int64]string{}
	}
	return names
}

// CountLogsFiltered implements store.LogStore.
func (s *Store) CountLogsFiltered(ctx context.Context, f model.LogFilter) (int, error) {
	where, args := logWhere(f)
	var total int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+s.qualified("hy2_logs")+` l WHERE `+where, args...).Scan(&total)
	return total, err
}

// QueryLogs implements store.LogStore.
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
	args = append(args, f.Size, (f.Page-1)*f.Size)
	q := s.logSelect() + ` WHERE ` + where +
		fmt.Sprintf(` ORDER BY l.ts DESC, l.id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.LogEntry{}
	for rows.Next() {
		e, err := s.scanLogEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	s.resolveNames(ctx, out)
	return out, total, nil
}

// StreamLogs implements store.LogStore.
func (s *Store) StreamLogs(ctx context.Context, f model.LogFilter, limit int, fn func(model.LogEntry) error) (int, error) {
	if limit <= 0 || limit > 200000 {
		limit = 100000
	}
	where, args := logWhere(f)
	args = append(args, limit)
	q := s.logSelect() + ` WHERE ` + where +
		fmt.Sprintf(` ORDER BY l.ts DESC, l.id DESC LIMIT $%d`, len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	batch := make([]model.LogEntry, 0, 256)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		s.resolveNames(ctx, batch)
		for _, e := range batch {
			if err := fn(e); err != nil {
				return err
			}
			n++
		}
		batch = batch[:0]
		return nil
	}
	for rows.Next() {
		e, err := s.scanLogEntry(rows)
		if err != nil {
			return n, err
		}
		batch = append(batch, e)
		if len(batch) >= cap(batch) {
			if err := flush(); err != nil {
				return n, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	if err := flush(); err != nil {
		return n, err
	}
	return n, nil
}

// LogCounts implements store.LogStore.
func (s *Store) LogCounts(ctx context.Context, o model.LogStatsOptions) (total, errors, warnings, authOK, authFail int64, err error) {
	where, args := analysisWhere(o)
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN level IN ('ERROR','FATAL') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN level = 'WARN' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN event IN ('auth_success','connect') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN event = 'auth_fail' THEN 1 ELSE 0 END),0)
		FROM `+s.qualified("hy2_logs")+` WHERE `+where, args...).
		Scan(&total, &errors, &warnings, &authOK, &authFail)
	return
}

// LogLevelCounts implements store.LogStore.
func (s *Store) LogLevelCounts(ctx context.Context, o model.LogStatsOptions) ([]store.EventCount, error) {
	where, args := analysisWhere(o)
	rows, err := s.db.QueryContext(ctx,
		`SELECT level, COUNT(*) FROM `+s.qualified("hy2_logs")+` WHERE `+where+
			` GROUP BY level ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.EventCount{}
	for rows.Next() {
		var e store.EventCount
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

// LogEventCounts implements store.LogStore.
func (s *Store) LogEventCounts(ctx context.Context, o model.LogStatsOptions) ([]store.EventCount, error) {
	where, args := analysisWhere(o)
	rows, err := s.db.QueryContext(ctx,
		`SELECT event, COUNT(*) FROM `+s.qualified("hy2_logs")+` WHERE `+where+
			` GROUP BY event ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.EventCount{}
	for rows.Next() {
		var e store.EventCount
		if err := rows.Scan(&e.Name, &e.Count); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LogTimeline implements store.LogStore.
func (s *Store) LogTimeline(ctx context.Context, o model.LogStatsOptions) ([]store.TimeCount, error) {
	where, args := analysisWhere(o)
	rows, err := s.db.QueryContext(ctx,
		`SELECT date_trunc('minute', ts AT TIME ZONE 'UTC') AS minute, COUNT(*),
		 COALESCE(SUM(CASE WHEN level IN ('ERROR','FATAL') THEN 1 ELSE 0 END),0),
		 COALESCE(SUM(CASE WHEN event = 'auth_fail' THEN 1 ELSE 0 END),0)
		 FROM `+s.qualified("hy2_logs")+` WHERE `+where+
			` GROUP BY minute ORDER BY minute`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.TimeCount{}
	for rows.Next() {
		var t store.TimeCount
		if err := rows.Scan(&t.Time, &t.Total, &t.Errors, &t.AuthFailures); err != nil {
			return nil, err
		}
		t.Time = t.Time.UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

// LogTopIPs implements store.LogStore.
func (s *Store) LogTopIPs(ctx context.Context, o model.LogStatsOptions, limit int) ([]store.IPCount, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	where, args := analysisWhere(o)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT client_ip, COUNT(*), COALESCE(SUM(CASE WHEN event='auth_fail' THEN 1 ELSE 0 END),0), MAX(ts)
		 FROM `+s.qualified("hy2_logs")+` WHERE `+where+` AND client_ip != ''
		 GROUP BY client_ip ORDER BY COUNT(*) DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.IPCount{}
	for rows.Next() {
		var c store.IPCount
		if err := rows.Scan(&c.IP, &c.Count, &c.Failures, &c.LastSeen); err != nil {
			return nil, err
		}
		c.LastSeen = c.LastSeen.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// LogTopUsers implements store.LogStore.
func (s *Store) LogTopUsers(ctx context.Context, o model.LogStatsOptions, limit int) ([]store.UserCount, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	where, args := analysisWhere(o)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id, COUNT(*), COALESCE(SUM(bytes_tx),0), COALESCE(SUM(bytes_rx),0), MAX(ts)
		 FROM `+s.qualified("hy2_logs")+` WHERE `+where+` AND user_id != ''
		 GROUP BY user_id ORDER BY COUNT(*) DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.UserCount{}
	for rows.Next() {
		var c store.UserCount
		if err := rows.Scan(&c.UserID, &c.Count, &c.Tx, &c.Rx, &c.LastSeen); err != nil {
			return nil, err
		}
		c.LastSeen = c.LastSeen.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// AuthFailuresByIP implements store.LogStore.
func (s *Store) AuthFailuresByIP(ctx context.Context, o model.LogStatsOptions, min int64) ([]store.IPCount, error) {
	where, args := analysisWhere(o)
	args = append(args, min)
	q := `SELECT client_ip, COUNT(*), MAX(ts) FROM ` + s.qualified("hy2_logs") + `
		WHERE ` + where + ` AND event='auth_fail' AND client_ip != ''
		GROUP BY client_ip HAVING COUNT(*) >= $` + fmt.Sprint(len(args)) +
		` ORDER BY COUNT(*) DESC LIMIT 50`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.IPCount{}
	for rows.Next() {
		var c store.IPCount
		if err := rows.Scan(&c.IP, &c.Count, &c.LastSeen); err != nil {
			return nil, err
		}
		c.Failures = c.Count
		c.LastSeen = c.LastSeen.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// AuthFailuresByNode implements store.LogStore.
func (s *Store) AuthFailuresByNode(ctx context.Context, o model.LogStatsOptions, min int64) ([]store.NodeCount, error) {
	where, args := analysisWhere(o)
	args = append(args, min)
	q := `SELECT node_id, COUNT(*) FROM ` + s.qualified("hy2_logs") + `
		WHERE ` + where + ` AND event='auth_fail'
		GROUP BY node_id HAVING COUNT(*) >= $` + fmt.Sprint(len(args)) +
		` ORDER BY COUNT(*) DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.NodeCount{}
	names := map[int64]string{}
	ids := []int64{}
	for rows.Next() {
		var c store.NodeCount
		if err := rows.Scan(&c.NodeID, &c.Count); err != nil {
			return nil, err
		}
		ids = append(ids, c.NodeID)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names = s.nodeNames(ctx, ids)
	for i := range out {
		out[i].NodeName = names[out[i].NodeID]
	}
	return out, nil
}

// UnknownAuthUsers implements store.LogStore. Candidate user ids are checked
// against the SQLite user table in chunks, so this remains correct for panels
// with many accounts.
func (s *Store) UnknownAuthUsers(ctx context.Context, o model.LogStatsOptions, limit int) ([]store.UserCount, error) {
	if limit <= 0 {
		limit = 20
	}
	if s.directory == nil {
		return []store.UserCount{}, nil
	}
	where, args := analysisWhere(o)
	args = append(args, unknownCandidateLimit)
	q := `SELECT user_id, COUNT(*), MAX(ts) FROM ` + s.qualified("hy2_logs") + `
		WHERE ` + where + ` AND event='auth_fail' AND user_id != ''
		GROUP BY user_id ORDER BY COUNT(*) DESC LIMIT $` + fmt.Sprint(len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []store.UserCount{}
	for rows.Next() {
		var c store.UserCount
		if err := rows.Scan(&c.UserID, &c.Count, &c.LastSeen); err != nil {
			return nil, err
		}
		c.LastSeen = c.LastSeen.UTC()
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for start := 0; start < len(candidates); start += 500 {
		end := start + 500
		if end > len(candidates) {
			end = len(candidates)
		}
		ids := make([]string, 0, end-start)
		for _, c := range candidates[start:end] {
			ids = append(ids, c.UserID)
		}
		found, err := s.directory.ExistingUserIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		for id := range found {
			existing[id] = true
		}
	}
	out := make([]store.UserCount, 0, limit)
	for _, c := range candidates {
		if existing[c.UserID] {
			continue
		}
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out, nil
}

// LogNodeStats implements store.LogStore.
func (s *Store) LogNodeStats(ctx context.Context, o model.LogStatsOptions) ([]store.NodeLogStat, error) {
	where, args := analysisWhere(o)
	q := `SELECT node_id, COUNT(*),
		COALESCE(SUM(CASE WHEN level IN ('ERROR','FATAL') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN level = 'WARN' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN event = 'auth_fail' THEN 1 ELSE 0 END),0)
		FROM ` + s.qualified("hy2_logs") + ` WHERE ` + where +
		` GROUP BY node_id ORDER BY COUNT(*) DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.NodeLogStat{}
	ids := []int64{}
	for rows.Next() {
		var c store.NodeLogStat
		if err := rows.Scan(&c.NodeID, &c.Total, &c.Errors, &c.Warnings, &c.AuthFailures); err != nil {
			return nil, err
		}
		ids = append(ids, c.NodeID)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names := s.nodeNames(ctx, ids)
	for i := range out {
		out[i].NodeName = names[out[i].NodeID]
	}
	return out, nil
}

// NodeEventStats implements store.LogStore.
func (s *Store) NodeEventStats(ctx context.Context, event string, o model.LogStatsOptions) ([]store.NodeEventStat, error) {
	where, args := analysisWhere(o)
	args = append(args, event)
	q := `SELECT node_id, COUNT(*), MIN(ts), MAX(ts) FROM ` + s.qualified("hy2_logs") + `
		WHERE ` + where + ` AND event = $` + fmt.Sprint(len(args)) + `
		GROUP BY node_id ORDER BY COUNT(*) DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.NodeEventStat{}
	ids := []int64{}
	for rows.Next() {
		var c store.NodeEventStat
		if err := rows.Scan(&c.NodeID, &c.Count, &c.FirstSeen, &c.LastSeen); err != nil {
			return nil, err
		}
		c.FirstSeen = c.FirstSeen.UTC()
		c.LastSeen = c.LastSeen.UTC()
		ids = append(ids, c.NodeID)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names := s.nodeNames(ctx, ids)
	for i := range out {
		out[i].NodeName = names[out[i].NodeID]
	}
	return out, nil
}

// NodeEventMarks implements store.LogStore.
func (s *Store) NodeEventMarks(ctx context.Context, event string, o model.LogStatsOptions) ([]store.NodeEventMark, error) {
	const maxMarks = 100000
	where, args := analysisWhere(o)
	args = append(args, event, maxMarks)
	q := `SELECT node_id, ts FROM ` + s.qualified("hy2_logs") + `
		WHERE ` + where + ` AND event = $` + fmt.Sprint(len(args)-1) + `
		ORDER BY ts DESC LIMIT $` + fmt.Sprint(len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.NodeEventMark{}
	ids := []int64{}
	for rows.Next() {
		var m store.NodeEventMark
		if err := rows.Scan(&m.NodeID, &m.Time); err != nil {
			return nil, err
		}
		m.Time = m.Time.UTC()
		ids = append(ids, m.NodeID)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names := s.nodeNames(ctx, ids)
	for i := range out {
		out[i].NodeName = names[out[i].NodeID]
	}
	return out, nil
}

// RecentLogs implements store.LogStore.
func (s *Store) RecentLogs(ctx context.Context, nodeID int64, limit int) ([]model.LogEntry, error) {
	logs, _, err := s.QueryLogs(ctx, model.LogFilter{NodeID: nodeID, Page: 1, Size: limit})
	return logs, err
}

// CountLogs implements store.LogStore.
func (s *Store) CountLogs(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+s.qualified("hy2_logs")).Scan(&n)
	return n, err
}

// DeleteLogsBefore implements store.LogStore. Fully expired partitions are
// dropped first so clearing history does not leave dead tuples behind.
func (s *Store) DeleteLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	dropped, err := s.dropExpiredPartitions(ctx, before, true)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM `+s.qualified("hy2_logs")+` WHERE ts < $1`, before.UTC())
	if err != nil {
		return dropped, err
	}
	deleted, _ := res.RowsAffected()
	return dropped + deleted, nil
}
