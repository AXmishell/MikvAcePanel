// Package pglogs implements the PostgreSQL telemetry backend used when
// telemetry.driver is postgres.
//
// Only parsed proxy logs and their idempotency receipts live here. Users,
// nodes, groups, certificates and traffic counters stay in the SQLite control
// database, so this package never opens a transaction that spans both stores.
package pglogs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

const (
	// DefaultSchema is used when telemetry.schema is empty.
	DefaultSchema = "public"
	// retentionFloor keeps a single malformed timestamp from creating a
	// partition far outside the configured retention window.
	maxLogAge = 366 * 24 * time.Hour
	// maxFutureSkew tolerates a slightly fast node clock.
	maxFutureSkew = 24 * time.Hour
	// insertChunk bounds the number of rows per multi-row INSERT. Fifteen
	// columns per row keeps the statement well below PostgreSQL's parameter
	// limit.
	insertChunk = 500
	// unknownCandidateLimit bounds the user ids fetched before checking which
	// ones exist in the control database.
	unknownCandidateLimit = 10000
)

// Config configures the PostgreSQL telemetry store.
type Config struct {
	DSN             string
	Schema          string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	// Directory resolves node names and user ids from the control database.
	// It is optional for tools that only write logs.
	Directory store.LogDirectory
}

// Store is a PostgreSQL log backend.
type Store struct {
	db        *sql.DB
	schema    string
	directory store.LogDirectory

	partitionMu sync.Mutex
	partitions  map[string]bool
}

var schemaPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NormalizeSchema validates a telemetry schema name and applies the default.
// It is exported so the first-run wizard can validate input before saving.
func NormalizeSchema(name string) (string, error) {
	schema := strings.TrimSpace(name)
	if schema == "" {
		schema = DefaultSchema
	}
	if len(schema) > 63 || !schemaPattern.MatchString(schema) {
		return "", fmt.Errorf("invalid telemetry schema %q", name)
	}
	return schema, nil
}

// TestConnection opens the telemetry schema and closes it again. It is used by
// the install wizard to verify DSN, permissions and DDL access before saving.
func TestConnection(ctx context.Context, dsn, schema string) error {
	st, err := Open(ctx, Config{DSN: dsn, Schema: schema, MaxOpenConns: 1, MaxIdleConns: 1})
	if err != nil {
		return err
	}
	return st.Close()
}

// Open connects to PostgreSQL, applies the telemetry schema and verifies that
// the configured schema is usable.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, errors.New("telemetry DSN is empty")
	}
	schema, err := NormalizeSchema(cfg.Schema)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	applyPool(db, cfg)
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	s := &Store{db: db, schema: schema, directory: cfg.Directory, partitions: map[string]bool{}}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensurePartitions(ctx, []time.Time{time.Now().UTC()}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func applyPool(db *sql.DB, cfg Config) {
	open, idle, life := cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime
	if open <= 0 {
		open = 12
	}
	if idle <= 0 {
		idle = 4
	}
	if life <= 0 {
		life = time.Hour
	}
	if idle > open {
		idle = open
	}
	db.SetMaxOpenConns(open)
	db.SetMaxIdleConns(idle)
	db.SetConnMaxLifetime(life)
}

// Driver implements store.LogStore.
func (s *Store) Driver() string { return "postgres" }

// Ping implements store.LogStore.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

// Stats returns the connection pool counters.
func (s *Store) Stats() sql.DBStats { return s.db.Stats() }

func (s *Store) qualified(table string) string {
	return quoteIdent(s.schema) + "." + quoteIdent(table)
}

func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{}
	// public already exists on every PostgreSQL database. Skipping CREATE
	// SCHEMA lets roles that only have USAGE/CREATE on public run the panel
	// without a database-level CREATE privilege.
	if s.schema != DefaultSchema {
		stmts = append(stmts, `CREATE SCHEMA IF NOT EXISTS `+quoteIdent(s.schema))
	}
	stmts = append(stmts, `CREATE TABLE IF NOT EXISTS `+s.qualified("telemetry_migrations")+` (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("prepare telemetry schema: %w", err)
		}
	}
	var version int
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0) FROM `+s.qualified("telemetry_migrations")).Scan(&version)
	if err != nil {
		return fmt.Errorf("read telemetry schema version: %w", err)
	}
	if version < 1 {
		if err := s.migrateV1(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrateV1(ctx context.Context) error {
	logs := s.qualified("hy2_logs")
	batches := s.qualified("log_batches")
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ` + logs + ` (
			id BIGINT GENERATED BY DEFAULT AS IDENTITY,
			node_id BIGINT NOT NULL,
			ts TIMESTAMPTZ NOT NULL,
			level TEXT NOT NULL DEFAULT '',
			logger TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			raw TEXT NOT NULL DEFAULT '',
			event TEXT NOT NULL DEFAULT '',
			client_ip TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 0,
			bytes_tx BIGINT NOT NULL DEFAULT 0,
			bytes_rx BIGINT NOT NULL DEFAULT 0,
			duration_ms BIGINT NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (id, ts)
		) PARTITION BY RANGE (ts)`,
		`CREATE TABLE IF NOT EXISTS ` + batches + ` (
			node_id BIGINT NOT NULL,
			batch_id TEXT NOT NULL,
			received_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (node_id, batch_id)
		)`,
		`CREATE INDEX IF NOT EXISTS hy2_logs_ts_idx ON ` + logs + ` (ts)`,
		`CREATE INDEX IF NOT EXISTS hy2_logs_node_ts_idx ON ` + logs + ` (node_id, ts)`,
		`CREATE INDEX IF NOT EXISTS hy2_logs_event_ts_idx ON ` + logs + ` (event, ts)`,
		`CREATE INDEX IF NOT EXISTS hy2_logs_ip_ts_idx ON ` + logs + ` (client_ip, ts)`,
		`CREATE INDEX IF NOT EXISTS hy2_logs_user_ts_idx ON ` + logs + ` (user_id, ts)`,
		`CREATE INDEX IF NOT EXISTS log_batches_received_idx ON ` + batches + ` (received_at)`,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply telemetry schema v1: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+s.qualified("telemetry_migrations")+` (version) VALUES (1)
		 ON CONFLICT (version) DO NOTHING`); err != nil {
		return err
	}
	return tx.Commit()
}

// ensurePartitions creates the daily partition for every UTC day in times.
// Timestamps outside the retention window are clamped to today so a bad node
// clock cannot create an unbounded number of partitions.
func (s *Store) ensurePartitions(ctx context.Context, times []time.Time) error {
	if len(times) == 0 {
		return nil
	}
	nowT := time.Now().UTC()
	clamped := make([]time.Time, len(times))
	for i, t := range times {
		clamped[i] = normalizeTS(t, nowT)
	}
	return s.ensurePartitionsRaw(ctx, clamped)
}

// ensurePartitionsRaw creates partitions for the exact days in times. It is
// used by the migration tool, which must preserve historical timestamps.
func (s *Store) ensurePartitionsRaw(ctx context.Context, times []time.Time) error {
	if len(times) == 0 {
		return nil
	}
	seen := map[string]bool{}
	days := make([]time.Time, 0, len(times))
	for _, t := range times {
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		key := day.Format("20060102")
		if seen[key] {
			continue
		}
		seen[key] = true
		days = append(days, day)
	}
	s.partitionMu.Lock()
	defer s.partitionMu.Unlock()
	for _, day := range days {
		key := day.Format("20060102")
		if s.partitions[key] {
			continue
		}
		next := day.AddDate(0, 0, 1)
		name := "hy2_logs_" + key
		stmt := `CREATE TABLE IF NOT EXISTS ` + s.qualified(name) + ` PARTITION OF ` +
			s.qualified("hy2_logs") + ` FOR VALUES FROM ('` +
			day.Format("2006-01-02 00:00:00+00") + `') TO ('` +
			next.Format("2006-01-02 00:00:00+00") + `')`
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create log partition %s: %w", name, err)
		}
		s.partitions[key] = true
	}
	return nil
}

// forgetPartitions invalidates cached partition existence for the days in
// times. It is used when an insert reports a missing partition.
func (s *Store) forgetPartitions(times []time.Time) {
	s.partitionMu.Lock()
	defer s.partitionMu.Unlock()
	for _, t := range times {
		s.partitions[time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Format("20060102")] = false
	}
}

func normalizeTS(t, nowT time.Time) time.Time {
	if t.IsZero() || t.Before(nowT.Add(-maxLogAge)) || t.After(nowT.Add(maxFutureSkew)) {
		return nowT
	}
	return t.UTC()
}

// InsertLogBatch implements store.LogStore. It stores the batch marker and
// its rows in one transaction; a duplicate batch id is a no-op.
func (s *Store) InsertLogBatch(ctx context.Context, nodeID int64, batchID string, entries []model.LogEntry) (bool, error) {
	if batchID == "" {
		batchID = store.RandomToken(16)
	}
	nowT := time.Now().UTC()
	times := make([]time.Time, 0, len(entries))
	for _, e := range entries {
		times = append(times, normalizeTS(e.TS, nowT))
	}
	if err := s.ensurePartitionsRaw(ctx, times); err != nil {
		return false, err
	}
	inserted, err := s.insertBatch(ctx, nodeID, batchID, entries, times, nowT)
	if err != nil && isMissingPartition(err) {
		// A maintenance pass may have dropped the partition between the
		// preflight and the insert, so drop the cached existence flag too.
		s.forgetPartitions(times)
		if ensureErr := s.ensurePartitionsRaw(ctx, times); ensureErr == nil {
			inserted, err = s.insertBatch(ctx, nodeID, batchID, entries, times, nowT)
		}
	}
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// ImportLogBatch writes historical rows without clamping their timestamps.
// It is used by cmd/telemetry-migrate, which copies an existing SQLite log
// table into PostgreSQL before the panel switches backends.
func (s *Store) ImportLogBatch(ctx context.Context, nodeID int64, batchID string, entries []model.LogEntry) (bool, error) {
	if batchID == "" {
		batchID = store.RandomToken(16)
	}
	times := make([]time.Time, len(entries))
	for i, e := range entries {
		if e.TS.IsZero() {
			e.TS = time.Now().UTC()
		}
		times[i] = e.TS.UTC()
	}
	if err := s.ensurePartitionsRaw(ctx, times); err != nil {
		return false, err
	}
	return s.insertBatch(ctx, nodeID, batchID, entries, times, time.Now().UTC())
}

// BatchReceipt is one idempotency marker copied from the SQLite log_batches
// table during migration.
type BatchReceipt struct {
	NodeID     int64
	BatchID    string
	ReceivedAt time.Time
}

// ImportBatchReceipts preserves Agent batch idempotency after a migration.
func (s *Store) ImportBatchReceipts(ctx context.Context, receipts []BatchReceipt) error {
	if len(receipts) == 0 {
		return nil
	}
	for start := 0; start < len(receipts); start += insertChunk {
		end := start + insertChunk
		if end > len(receipts) {
			end = len(receipts)
		}
		chunk := receipts[start:end]
		var b strings.Builder
		b.WriteString(`INSERT INTO ` + s.qualified("log_batches") + ` (node_id, batch_id, received_at) VALUES `)
		args := make([]any, 0, len(chunk)*3)
		for i, r := range chunk {
			if i > 0 {
				b.WriteString(",")
			}
			base := len(args)
			b.WriteString(fmt.Sprintf("($%d,$%d,$%d)", base+1, base+2, base+3))
			args = append(args, r.NodeID, r.BatchID, r.ReceivedAt.UTC())
		}
		b.WriteString(` ON CONFLICT (node_id, batch_id) DO NOTHING`)
		if _, err := s.db.ExecContext(ctx, b.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

func isMissingPartition(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no partition of relation") && strings.Contains(msg, "found for row")
}

func (s *Store) insertBatch(ctx context.Context, nodeID int64, batchID string, entries []model.LogEntry, times []time.Time, nowT time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO `+s.qualified("log_batches")+` (node_id, batch_id, received_at)
		 VALUES ($1,$2,$3) ON CONFLICT (node_id, batch_id) DO NOTHING`,
		nodeID, batchID, nowT)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil // already applied
	}
	for start := 0; start < len(entries); start += insertChunk {
		end := start + insertChunk
		if end > len(entries) {
			end = len(entries)
		}
		if err := s.insertRows(ctx, tx, nodeID, entries[start:end], times[start:end]); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) insertRows(ctx context.Context, tx *sql.Tx, nodeID int64, entries []model.LogEntry, times []time.Time) error {
	if len(entries) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString(`INSERT INTO `)
	b.WriteString(s.qualified("hy2_logs"))
	b.WriteString(` (node_id, ts, level, logger, message, raw, event, client_ip, user_id,
		host, port, bytes_tx, bytes_rx, duration_ms, error) VALUES `)
	args := make([]any, 0, len(entries)*15)
	n := 0
	for i := range entries {
		if i > 0 {
			b.WriteString(",")
		}
		base := n
		b.WriteString("(")
		for col := 0; col < 15; col++ {
			if col > 0 {
				b.WriteString(",")
			}
			args = append(args, nil)
			n++
			b.WriteString("$" + strconv.Itoa(base+col+1))
		}
		b.WriteString(")")
	}
	// Fill the argument slots. Keeping the placeholder builder separate from
	// the value copy makes the column order explicit at the call site.
	slot := 0
	put := func(v any) { args[slot] = v; slot++ }
	for i, e := range entries {
		put(nodeID)
		put(times[i])
		put(e.Level)
		put(e.Logger)
		put(e.Message)
		put(e.Raw)
		put(e.Event)
		put(e.ClientIP)
		put(e.UserID)
		put(e.Host)
		put(e.Port)
		put(e.BytesTx)
		put(e.BytesRx)
		put(e.DurationMS)
		put(e.Error)
	}
	_, err := tx.ExecContext(ctx, b.String(), args...)
	return err
}

// PruneLogs implements store.LogStore. Fully expired daily partitions are
// dropped (fast and bloat-free); the remaining partial day is removed with a
// bounded DELETE.
func (s *Store) PruneLogs(ctx context.Context, before time.Time) (int64, int64, error) {
	dropped, err := s.dropExpiredPartitions(ctx, before, false)
	if err != nil {
		return dropped, 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM `+s.qualified("hy2_logs")+` WHERE ts < $1`, before.UTC())
	if err != nil {
		return dropped, 0, err
	}
	deleted, _ := res.RowsAffected()
	res, err = s.db.ExecContext(ctx,
		`DELETE FROM `+s.qualified("log_batches")+` WHERE received_at < $1`, before.UTC())
	if err != nil {
		return dropped + deleted, 0, err
	}
	batches, _ := res.RowsAffected()
	return dropped + deleted, batches, nil
}

func (s *Store) dropExpiredPartitions(ctx context.Context, before time.Time, exact bool) (int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.relname, GREATEST(c.reltuples, 0)::bigint
		 FROM pg_inherits i
		 JOIN pg_class c ON c.oid = i.inhrelid
		 JOIN pg_class p ON p.oid = i.inhparent
		 JOIN pg_namespace n ON n.oid = p.relnamespace
		 WHERE n.nspname = $1 AND p.relname = 'hy2_logs'`, s.schema)
	if err != nil {
		return 0, err
	}
	type partition struct {
		name string
		rows int64
		day  time.Time
	}
	var expired []partition
	for rows.Next() {
		var p partition
		if err := rows.Scan(&p.name, &p.rows); err != nil {
			rows.Close()
			return 0, err
		}
		day, ok := partitionDay(p.name)
		if !ok {
			continue
		}
		if !day.AddDate(0, 0, 1).After(before.UTC()) {
			p.day = day
			expired = append(expired, p)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	var dropped int64
	for _, p := range expired {
		if exact {
			var n int64
			if err := s.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM `+s.qualified(p.name)).Scan(&n); err == nil {
				p.rows = n
			}
		}
		if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS `+s.qualified(p.name)); err != nil {
			return dropped, err
		}
		s.partitionMu.Lock()
		delete(s.partitions, p.day.Format("20060102"))
		s.partitionMu.Unlock()
		dropped += p.rows
	}
	return dropped, nil
}

func partitionDay(name string) (time.Time, bool) {
	const prefix = "hy2_logs_"
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, false
	}
	raw := strings.TrimPrefix(name, prefix)
	if len(raw) != 8 {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation("20060102", raw, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}

// LogUsage implements store.LogStore.
func (s *Store) LogUsage(ctx context.Context) (int64, int64, error) {
	var logs, batches int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(GREATEST(c.reltuples, 0)), 0)::bigint
		 FROM pg_inherits i
		 JOIN pg_class c ON c.oid = i.inhrelid
		 JOIN pg_class p ON p.oid = i.inhparent
		 JOIN pg_namespace n ON n.oid = p.relnamespace
		 WHERE n.nspname = $1 AND p.relname = 'hy2_logs'`, s.schema).Scan(&logs)
	if err != nil {
		return 0, 0, err
	}
	if logs == 0 {
		// reltuples is only a planner estimate and is often 0 until the first
		// autovacuum/ANALYZE. Fall back to a real count so a freshly migrated
		// database does not report "0 rows" in the UI.
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+s.qualified("hy2_logs")).Scan(&logs); err != nil {
			return 0, 0, err
		}
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+s.qualified("log_batches")).Scan(&batches); err != nil {
		return logs, 0, err
	}
	return logs, batches, nil
}

// TelemetryStats implements store.LogStore.
func (s *Store) TelemetryStats(ctx context.Context) (store.TelemetryStats, error) {
	var bytes, rows int64
	err := s.db.QueryRowContext(ctx,
		`SELECT pg_total_relation_size($1::regclass)::bigint`,
		s.qualified("hy2_logs")).Scan(&bytes)
	if err != nil {
		return store.TelemetryStats{}, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(GREATEST(c.reltuples, 0)), 0)::bigint
		 FROM pg_inherits i
		 JOIN pg_class c ON c.oid = i.inhrelid
		 JOIN pg_class p ON p.oid = i.inhparent
		 JOIN pg_namespace n ON n.oid = p.relnamespace
		 WHERE n.nspname = $1 AND p.relname = 'hy2_logs'`, s.schema).Scan(&rows); err != nil {
		return store.TelemetryStats{}, err
	}
	if rows == 0 {
		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+s.qualified("hy2_logs")).Scan(&rows)
	}
	var partitions int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		 FROM pg_inherits i
		 JOIN pg_class p ON p.oid = i.inhparent
		 JOIN pg_namespace n ON n.oid = p.relnamespace
		 WHERE n.nspname = $1 AND p.relname = 'hy2_logs'`, s.schema).Scan(&partitions); err != nil {
		return store.TelemetryStats{}, err
	}
	var batchesBytes int64
	_ = s.db.QueryRowContext(ctx,
		`SELECT pg_total_relation_size($1::regclass)::bigint`,
		s.qualified("log_batches")).Scan(&batchesBytes)
	return store.TelemetryStats{
		Driver:     s.Driver(),
		Bytes:      bytes + batchesBytes,
		Rows:       rows,
		Partitions: partitions,
		Detail:     s.schema,
	}, nil
}

// Compile-time check.
var _ store.LogStore = (*Store)(nil)
