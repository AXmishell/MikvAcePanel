// Package store implements the panel's SQL persistence layer.
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

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store/dialect"
)

// Store wraps the panel database.
type Store struct {
	DB   *DB
	Path string
}

// Config selects and configures a database backend.
//
// SQLite is the default. PostgreSQL and MySQL use DSN and the same schema
// metadata; each backend gets its own DDL, placeholder rebinding and migration
// introspection.
type Config struct {
	Driver string
	// DSN is required for PostgreSQL and MySQL.
	DSN string
	// Path is the SQLite file location.
	Path            string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

var (
	// ErrNotFound is returned when a requested row does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned for unique constraint violations.
	ErrConflict = errors.New("conflict")
)

// Open opens (and migrates) the SQLite database at path.
func Open(path string) (*Store, error) {
	return OpenConfig(Config{Driver: dialect.SQLite, Path: path})
}

// OpenConfig opens the configured backend.
func OpenConfig(cfg Config) (*Store, error) {
	d, err := dialect.For(cfg.Driver)
	if err != nil {
		return nil, err
	}
	var raw *sql.DB
	var path string
	switch d.Name() {
	case dialect.SQLite:
		path = strings.TrimSpace(cfg.Path)
		if path == "" {
			path = "data/panel.db"
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
		if err := applyPendingRestore(path); err != nil {
			return nil, fmt.Errorf("apply pending restore: %w", err)
		}
		raw, err = sql.Open(d.DriverName(), sqliteDSN(path))
		if err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
		applyPool(raw, cfg, 8, 4, time.Hour)
	case dialect.Postgres:
		if strings.TrimSpace(cfg.DSN) == "" {
			return nil, fmt.Errorf("postgres DSN is required (set database.dsn_file)")
		}
		raw, err = sql.Open(d.DriverName(), strings.TrimSpace(cfg.DSN))
		if err != nil {
			return nil, fmt.Errorf("open postgres: %w", err)
		}
		applyPool(raw, cfg, 20, 10, 30*time.Minute)
		if raw.Stats().MaxOpenConnections == 1 {
			// Migration holds a dedicated connection for the advisory lock.
			raw.SetMaxOpenConns(2)
		}
	case dialect.MySQL:
		if strings.TrimSpace(cfg.DSN) == "" {
			return nil, fmt.Errorf("mysql DSN is required (set database.dsn_file)")
		}
		raw, err = sql.Open(d.DriverName(), strings.TrimSpace(cfg.DSN))
		if err != nil {
			return nil, fmt.Errorf("open mysql: %w", err)
		}
		applyPool(raw, cfg, 10, 5, 30*time.Minute)
		if raw.Stats().MaxOpenConnections == 1 {
			raw.SetMaxOpenConns(2)
		}
	default:
		return nil, fmt.Errorf("unsupported database driver %q", d.Name())
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := raw.PingContext(pingCtx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("connect %s: %w", d.Name(), err)
	}
	s := &Store{DB: &DB{DB: raw, dialect: d}, Path: path}
	if err := s.Migrate(context.Background()); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return s, nil
}

// sqliteDSN builds the connection string shared by OpenConfig and
// TestConnection so a connection test exercises the same pragmas as runtime.
func sqliteDSN(path string) string {
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(8000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
}

// TestConnection verifies that the configured backend is reachable without
// creating or migrating tables. /install uses it before writing a new
// database configuration so a typo cannot replace a working setup.
func TestConnection(cfg Config) error {
	d, err := dialect.For(cfg.Driver)
	if err != nil {
		return err
	}
	driverName := d.DriverName()
	dsn := strings.TrimSpace(cfg.DSN)
	if d.Name() == dialect.SQLite {
		path := strings.TrimSpace(cfg.Path)
		if path == "" {
			path = "data/panel.db"
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}
		dsn = sqliteDSN(path)
	} else if dsn == "" {
		return fmt.Errorf("%s DSN is required", d.Name())
	}
	raw, err := sql.Open(driverName, dsn)
	if err != nil {
		return fmt.Errorf("open %s: %w", d.Name(), err)
	}
	defer raw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := raw.PingContext(ctx); err != nil {
		return fmt.Errorf("connect %s: %w", d.Name(), err)
	}
	return nil
}

// Prepare opens the configured backend, runs migrations and closes the
// temporary connection. /install calls it before writing a new configuration
// so a backend that fails DDL cannot take the panel down after a restart.
func Prepare(cfg Config) error {
	st, err := OpenConfig(cfg)
	if err != nil {
		return err
	}
	return st.Close()
}

// applyPool applies explicit pool settings, falling back to backend defaults.
func applyPool(db *sql.DB, cfg Config, defOpen, defIdle int, defLife time.Duration) {
	open, idle, life := cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime
	if open <= 0 {
		open = defOpen
	}
	if idle <= 0 {
		idle = defIdle
	}
	if life <= 0 {
		life = defLife
	}
	if idle > open {
		idle = open
	}
	db.SetMaxOpenConns(open)
	db.SetMaxIdleConns(idle)
	db.SetConnMaxLifetime(life)
}

// Dialect reports the active backend.
func (s *Store) Dialect() dialect.Dialect { return s.DB.Dialect() }

// execer is implemented by *DB and *Tx.
type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// InsertID runs a single-row INSERT and returns its generated id.
//
// PostgreSQL does not support database/sql's LastInsertId, so the statement is
// extended with RETURNING id. SQLite and MySQL keep using LastInsertId.
func (s *Store) InsertID(ctx context.Context, query string, args ...any) (int64, error) {
	return s.insertID(ctx, s.DB, query, args...)
}

// InsertIDTx is InsertID inside an existing transaction.
func (s *Store) InsertIDTx(ctx context.Context, tx *Tx, query string, args ...any) (int64, error) {
	return s.insertID(ctx, tx, query, args...)
}

func (s *Store) insertID(ctx context.Context, q execer, query string, args ...any) (int64, error) {
	if s.Dialect().Name() == dialect.Postgres {
		stmt := strings.TrimRight(strings.TrimSpace(query), ";") + " RETURNING id"
		var id int64
		if err := q.QueryRowContext(ctx, stmt, args...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
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
	required := []string{"admins", "users", "nodes", "settings"}
	for _, table := range required {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("backup is missing table %q", table)
		}
	}
	// Old backups use "groups"; new backups use "panel_groups".
	var groups int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('panel_groups','groups')`).Scan(&groups); err != nil {
		return err
	}
	if groups == 0 {
		return fmt.Errorf("backup is missing table %q", "panel_groups")
	}
	return nil
}
