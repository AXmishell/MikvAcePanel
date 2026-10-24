package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// ApplyStatsBatch applies a per-user traffic delta exactly once per batch id.
func (s *Store) ApplyStatsBatch(ctx context.Context, nodeID int64, batchID string, users map[string]model.UserTraffic) (bool, error) {
	if batchID == "" {
		batchID = RandomToken(16)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	inserted, err := s.InsertIgnoreTx(ctx, tx, "stats_batches",
		[]string{"node_id", "batch_id", "received_at"}, []string{"node_id", "batch_id"},
		nodeID, batchID, ts(now()))
	if err != nil {
		return false, err
	}
	if !inserted {
		return false, nil
	}
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE users SET traffic_tx = traffic_tx + ?, traffic_rx = traffic_rx + ?, last_seen_at = ?, updated_at = ?
		 WHERE user_id = ?`)
	if err != nil {
		return false, err
	}
	defer stmt.Close()
	nowT := ts(now())
	for uid, delta := range users {
		if delta.Tx == 0 && delta.Rx == 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, delta.Tx, delta.Rx, nowT, nowT, uid); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// InsertMetric stores a heartbeat sample.
func (s *Store) InsertMetric(ctx context.Context, m *model.NodeMetric) error {
	if m.TS.IsZero() {
		m.TS = now()
	}
	id, err := s.InsertID(ctx,
		`INSERT INTO node_metrics (node_id, ts, cpu_pct, mem_used, mem_total, disk_used, disk_total,
		 load1, uptime_sec, online, traffic_tx, traffic_rx, hy2_running)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.NodeID, ts(m.TS), m.CPUPct, m.MemUsed, m.MemTotal, m.DiskUsed, m.DiskTotal,
		m.Load1, m.UptimeSec, m.Online, m.TrafficTx, m.TrafficRx, boolInt(m.Hy2Running))
	if err != nil {
		return err
	}
	m.ID = id
	return nil
}

// TrafficPoint is one aggregated traffic bucket.
type TrafficPoint struct {
	Time time.Time `json:"time"`
	Tx   int64     `json:"tx"`
	Rx   int64     `json:"rx"`
}

// TrafficSeries aggregates heartbeat deltas per hour (or per minute for short
// ranges). substr() works identically on the portable timestamp text format
// used by all three backends.
func (s *Store) TrafficSeries(ctx context.Context, since time.Time, nodeID int64, minute bool) ([]TrafficPoint, error) {
	bucket := `substr(ts,1,13)||':00'`
	format := "2006-01-02T15:00"
	if minute {
		bucket = `substr(ts,1,16)`
		format = "2006-01-02T15:04"
	}
	q := `SELECT ` + bucket + `, COALESCE(SUM(traffic_tx),0), COALESCE(SUM(traffic_rx),0)
		FROM node_metrics WHERE ts >= ?`
	args := []any{ts(since)}
	if nodeID > 0 {
		q += ` AND node_id = ?`
		args = append(args, nodeID)
	}
	q += ` GROUP BY 1 ORDER BY 1`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrafficPoint{}
	for rows.Next() {
		var label string
		var p TrafficPoint
		if err := rows.Scan(&label, &p.Tx, &p.Rx); err != nil {
			return nil, err
		}
		p.Time, _ = time.Parse(format, label)
		out = append(out, p)
	}
	return out, rows.Err()
}

// TrafficSince returns the total traffic delta recorded since a time.
func (s *Store) TrafficSince(ctx context.Context, since time.Time, nodeID int64) (tx, rx int64, err error) {
	q := `SELECT COALESCE(SUM(traffic_tx),0), COALESCE(SUM(traffic_rx),0) FROM node_metrics WHERE ts >= ?`
	args := []any{ts(since)}
	if nodeID > 0 {
		q += ` AND node_id = ?`
		args = append(args, nodeID)
	}
	err = s.DB.QueryRowContext(ctx, q, args...).Scan(&tx, &rx)
	return
}

// LatestMetrics returns the latest heartbeat per node.
func (s *Store) LatestMetrics(ctx context.Context) (map[int64]model.NodeMetric, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT m.node_id, m.ts, m.cpu_pct, m.mem_used, m.mem_total, m.disk_used, m.disk_total,
		        m.load1, m.uptime_sec, m.online, m.traffic_tx, m.traffic_rx, m.hy2_running
		 FROM node_metrics m
		 JOIN (SELECT node_id, MAX(ts) AS max_ts FROM node_metrics GROUP BY node_id) x
		   ON x.node_id = m.node_id AND x.max_ts = m.ts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]model.NodeMetric{}
	for rows.Next() {
		var m model.NodeMetric
		var t string
		var running int
		if err := rows.Scan(&m.NodeID, &t, &m.CPUPct, &m.MemUsed, &m.MemTotal, &m.DiskUsed, &m.DiskTotal,
			&m.Load1, &m.UptimeSec, &m.Online, &m.TrafficTx, &m.TrafficRx, &running); err != nil {
			return nil, err
		}
		m.TS = parseTime(t)
		m.Hy2Running = running == 1
		out[m.NodeID] = m
	}
	return out, rows.Err()
}

// ExpireUsers disables accounts whose expiry has passed. It returns the number
// of affected users.
func (s *Store) ExpireUsers(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE users SET enabled = 0, updated_at = ? WHERE enabled = 1 AND expire_at IS NOT NULL
		 AND expire_at != '' AND expire_at <= ?`, ts(now()), ts(now()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneMetrics deletes old heartbeat samples.
func (s *Store) PruneMetrics(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM node_metrics WHERE ts < ?`, ts(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

var _ = sql.ErrNoRows
