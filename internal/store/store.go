// Package store implements the panel's SQLite persistence layer.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// Store wraps the SQLite database.
type Store struct {
	DB   *sql.DB
	Path string
}

var (
	// ErrNotFound is returned when a requested row does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned for unique constraint violations.
	ErrConflict = errors.New("conflict")
)

// Open opens (and migrates) the SQLite database at path.
func Open(path string) (*Store, error) {
	if path == "" {
		path = "data/panel.db"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := applyPendingRestore(path); err != nil {
		return nil, fmt.Errorf("apply pending restore: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(8000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)
	s := &Store{DB: db, Path: path}
	if err := s.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// applyPendingRestore swaps in a database uploaded through the restore API.
// The swap happens before SQLite opens the file so no connection can observe a
// half-replaced database.
func applyPendingRestore(path string) error {
	pending := path + ".restore"
	if _, err := os.Stat(pending); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	timestamp := time.Now().UTC().Format("20060102-150405")
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, path+".pre-restore-"+timestamp); err != nil {
			return err
		}
	}
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	_ = os.Remove(pending + "-wal")
	_ = os.Remove(pending + "-shm")
	return os.Rename(pending, path)
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// Migrate creates the schema. It is idempotent and intentionally uses plain
// CREATE IF NOT EXISTS statements so existing deployments can be upgraded in
// place without a migration framework.
func (s *Store) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS admins (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_login_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			color TEXT NOT NULL DEFAULT '#38bdf8',
			subscription_title TEXT NOT NULL DEFAULT '',
			max_devices INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL UNIQUE,
			username TEXT NOT NULL DEFAULT '',
			password TEXT NOT NULL,
			subscription_token TEXT NOT NULL UNIQUE,
			xray_uuid TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 0,
			traffic_tx INTEGER NOT NULL DEFAULT 0,
			traffic_rx INTEGER NOT NULL DEFAULT 0,
			traffic_limit INTEGER NOT NULL DEFAULT 0,
			max_devices INTEGER NOT NULL DEFAULT 0,
			expire_at TEXT,
			last_seen_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_groups (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			PRIMARY KEY (user_id, group_id)
		)`,
		`CREATE TABLE IF NOT EXISTS user_nodes (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			PRIMARY KEY (user_id, node_id)
		)`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'hysteria',
			flag TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			domain TEXT NOT NULL DEFAULT '',
			sni TEXT NOT NULL DEFAULT '',
			listen TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 443,
			port_range TEXT NOT NULL DEFAULT '',
			hop_interval TEXT NOT NULL DEFAULT '',
			obfs_type TEXT NOT NULL DEFAULT '',
			obfs_password TEXT NOT NULL DEFAULT '',
			stats_port INTEGER NOT NULL DEFAULT 9999,
			stats_secret TEXT NOT NULL DEFAULT '',
			xray_json TEXT NOT NULL DEFAULT '{}',
			ssh_port INTEGER NOT NULL DEFAULT 22,
			ssh_user TEXT NOT NULL DEFAULT '',
			ssh_password TEXT NOT NULL DEFAULT '',
			ssh_key TEXT NOT NULL DEFAULT '',
			ssh_install_docker INTEGER NOT NULL DEFAULT 0,
			tls_mode TEXT NOT NULL DEFAULT 'selfsigned',
			acme_email TEXT NOT NULL DEFAULT '',
			bandwidth_up TEXT NOT NULL DEFAULT '',
			bandwidth_down TEXT NOT NULL DEFAULT '',
			masquerade_url TEXT NOT NULL DEFAULT '',
			acl_json TEXT NOT NULL DEFAULT '[]',
			outbounds_json TEXT NOT NULL DEFAULT '[]',
			max_online INTEGER NOT NULL DEFAULT 0,
			ranking REAL NOT NULL DEFAULT 1,
			active INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'offline',
			token_hash TEXT NOT NULL UNIQUE,
			token TEXT NOT NULL DEFAULT '',
			token_prefix TEXT NOT NULL DEFAULT '',
			agent_version TEXT NOT NULL DEFAULT '',
			os TEXT NOT NULL DEFAULT '',
			arch TEXT NOT NULL DEFAULT '',
			hy2_version TEXT NOT NULL DEFAULT '',
			runtime TEXT NOT NULL DEFAULT '',
			last_seen_at TEXT,
			config_revision INTEGER NOT NULL DEFAULT 0,
			applied_revision INTEGER NOT NULL DEFAULT 0,
			config_error TEXT NOT NULL DEFAULT '',
			traffic_tx INTEGER NOT NULL DEFAULT 0,
			traffic_rx INTEGER NOT NULL DEFAULT 0,
			online_users INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_groups (
			node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
			PRIMARY KEY (node_id, group_id)
		)`,
		`CREATE TABLE IF NOT EXISTS node_configs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			revision INTEGER NOT NULL,
			yaml TEXT NOT NULL,
			hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			applied_at TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			UNIQUE(node_id, revision)
		)`,
		`CREATE TABLE IF NOT EXISTS certificates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domains_json TEXT NOT NULL,
			common_name TEXT NOT NULL DEFAULT '',
			cert_pem TEXT NOT NULL,
			key_cipher TEXT NOT NULL DEFAULT '',
			key_nonce TEXT NOT NULL DEFAULT '',
			fingerprint TEXT NOT NULL UNIQUE,
			key_fingerprint TEXT NOT NULL DEFAULT '',
			issuer TEXT NOT NULL DEFAULT '',
			not_before TEXT NOT NULL,
			not_after TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'acme',
			status TEXT NOT NULL DEFAULT 'valid',
			auto_renew INTEGER NOT NULL DEFAULT 1,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_certificates (
			node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
			cert_id INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 0,
			key_fingerprint TEXT NOT NULL DEFAULT '',
			applied_fingerprint TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			last_error TEXT NOT NULL DEFAULT '',
			requested_at TEXT,
			applied_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS acme_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ca_url TEXT NOT NULL,
			email TEXT NOT NULL DEFAULT '',
			key_cipher TEXT NOT NULL,
			key_nonce TEXT NOT NULL,
			account_url TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			UNIQUE(ca_url, email)
		)`,
		`CREATE TABLE IF NOT EXISTS node_metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			ts TEXT NOT NULL,
			cpu_pct REAL NOT NULL DEFAULT 0,
			mem_used INTEGER NOT NULL DEFAULT 0,
			mem_total INTEGER NOT NULL DEFAULT 0,
			disk_used INTEGER NOT NULL DEFAULT 0,
			disk_total INTEGER NOT NULL DEFAULT 0,
			load1 REAL NOT NULL DEFAULT 0,
			uptime_sec INTEGER NOT NULL DEFAULT 0,
			online INTEGER NOT NULL DEFAULT 0,
			traffic_tx INTEGER NOT NULL DEFAULT 0,
			traffic_rx INTEGER NOT NULL DEFAULT 0,
			hy2_running INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			node_id INTEGER NOT NULL DEFAULT 0,
			message TEXT NOT NULL,
			severity TEXT NOT NULL DEFAULT 'info',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS hy2_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id INTEGER NOT NULL,
			ts TEXT NOT NULL,
			level TEXT NOT NULL DEFAULT '',
			logger TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			raw TEXT NOT NULL DEFAULT '',
			event TEXT NOT NULL DEFAULT '',
			client_ip TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 0,
			bytes_tx INTEGER NOT NULL DEFAULT 0,
			bytes_rx INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS log_batches (
			node_id INTEGER NOT NULL,
			batch_id TEXT NOT NULL,
			received_at TEXT NOT NULL,
			PRIMARY KEY (node_id, batch_id)
		)`,
		`CREATE TABLE IF NOT EXISTS stats_batches (
			node_id INTEGER NOT NULL,
			batch_id TEXT NOT NULL,
			received_at TEXT NOT NULL,
			PRIMARY KEY (node_id, batch_id)
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			scopes TEXT NOT NULL DEFAULT '',
			rate_limit INTEGER NOT NULL DEFAULT 60,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			last_used_at TEXT,
			expires_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_users_token ON users(subscription_token)`,
		`CREATE INDEX IF NOT EXISTS idx_users_user_id ON users(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_ts ON hy2_logs(ts)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_node_ts ON hy2_logs(node_id, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_event_ts ON hy2_logs(event, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_ip_ts ON hy2_logs(client_ip, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_user_ts ON hy2_logs(user_id, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_node_ts ON node_metrics(node_id, ts)`,
		`CREATE INDEX IF NOT EXISTS idx_events_created ON events(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_node_configs_node ON node_configs(node_id, revision)`,
		`CREATE INDEX IF NOT EXISTS idx_certificates_fp ON certificates(fingerprint)`,
		`CREATE INDEX IF NOT EXISTS idx_certificates_not_after ON certificates(not_after)`,
		`CREATE INDEX IF NOT EXISTS idx_node_certificates_cert ON node_certificates(cert_id)`,
	}
	for _, stmt := range stmts {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Additive columns for deployments upgrading from an earlier schema.
	columns := []struct{ table, column, ddl string }{
		{"api_keys", "rate_limit", "INTEGER NOT NULL DEFAULT 60"},
		{"users", "xray_uuid", "TEXT NOT NULL DEFAULT ''"},
		{"nodes", "xray_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"nodes", "ssh_port", "INTEGER NOT NULL DEFAULT 22"},
		{"nodes", "ssh_user", "TEXT NOT NULL DEFAULT ''"},
		{"nodes", "ssh_password", "TEXT NOT NULL DEFAULT ''"},
		{"nodes", "ssh_key", "TEXT NOT NULL DEFAULT ''"},
		{"nodes", "ssh_install_docker", "INTEGER NOT NULL DEFAULT 0"},
		{"nodes", "capabilities_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"nodes", "cert_fingerprint", "TEXT NOT NULL DEFAULT ''"},
		{"nodes", "cert_not_after", "TEXT"},
		{"admins", "totp_secret", "TEXT NOT NULL DEFAULT ''"},
		{"admins", "totp_pending", "TEXT NOT NULL DEFAULT ''"},
		{"admins", "totp_enabled", "INTEGER NOT NULL DEFAULT 0"},
		{"admins", "totp_recovery", "TEXT NOT NULL DEFAULT '[]'"},
		{"admins", "totp_pending_recovery", "TEXT NOT NULL DEFAULT '[]'"},
	}
	for _, c := range columns {
		if err := s.ensureColumn(ctx, c.table, c.column, c.ddl); err != nil {
			return err
		}
	}
	// Backfill UUIDs for users created before Xray support was added.
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM users WHERE xray_uuid = '' OR xray_uuid IS NULL`)
	if err == nil {
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			_, _ = s.DB.ExecContext(ctx, `UPDATE users SET xray_uuid = ? WHERE id = ?`, RandomUUID(), id)
		}
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, ddl string) error {
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = s.DB.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+ddl)
	return err
}

// ---- time helpers -------------------------------------------------------

func now() time.Time { return time.Now().UTC() }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func tsPtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return ts(*t)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func nullTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t := parseTime(ns.String)
	if t.IsZero() {
		return nil
	}
	return &t
}

func attr(err error, msg string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		return fmt.Errorf("%s: %w", msg, ErrConflict)
	}
	return fmt.Errorf("%s: %w", msg, err)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalStrings(raw string) []string {
	var out []string
	if raw == "" {
		return []string{}
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func unmarshalOutbounds(raw string) []model.Outbound {
	var out []model.Outbound
	if raw == "" {
		return []model.Outbound{}
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []model.Outbound{}
	}
	return out
}

// Ping verifies the database connection.
func (s *Store) Ping(ctx context.Context) error { return s.DB.PingContext(ctx) }

// ValidateDatabase opens a SQLite file and checks that the core panel tables
// exist. It is used before accepting an uploaded backup.
func ValidateDatabase(path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1&_pragma=busy_timeout(2000)")
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return err
	}
	required := []string{"admins", "users", "nodes", "groups", "settings"}
	for _, table := range required {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("backup is missing table %q", table)
		}
	}
	return nil
}
