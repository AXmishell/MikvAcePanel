package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store/dialect"
)

// DB wraps *sql.DB and rebinds every statement for the configured dialect.
//
// Keeping the rewrite here means the store methods, the CELERITY importer and
// maintenance code can keep writing portable "?" SQL without knowing which
// backend is active.
type DB struct {
	*sql.DB
	dialect dialect.Dialect
}

// Tx is the transaction counterpart of DB.
type Tx struct {
	*sql.Tx
	dialect dialect.Dialect
}

// Dialect returns the backend descriptor.
func (db *DB) Dialect() dialect.Dialect { return db.dialect }

// Rebind exposes placeholder rewriting for callers that build SQL fragments.
func (db *DB) Rebind(query string) string { return db.dialect.Rebind(query) }

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.DB.ExecContext(ctx, db.dialect.Rebind(query), args...)
}

func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.DB.Exec(db.dialect.Rebind(query), args...)
}

func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.DB.QueryContext(ctx, db.dialect.Rebind(query), args...)
}

func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.DB.Query(db.dialect.Rebind(query), args...)
}

func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.DB.QueryRowContext(ctx, db.dialect.Rebind(query), args...)
}

func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.DB.QueryRow(db.dialect.Rebind(query), args...)
}

func (db *DB) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return db.DB.PrepareContext(ctx, db.dialect.Rebind(query))
}

func (db *DB) Prepare(query string) (*sql.Stmt, error) {
	return db.DB.Prepare(db.dialect.Rebind(query))
}

func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := db.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{Tx: tx, dialect: db.dialect}, nil
}

func (db *DB) Begin() (*Tx, error) {
	return db.BeginTx(context.Background(), nil)
}

// InTx runs fn in a transaction and commits when it returns nil.
func (db *DB) InTx(ctx context.Context, opts *sql.TxOptions, fn func(*Tx) error) error {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// PoolStats exposes connection pool counters for health endpoints.
func (db *DB) PoolStats() sql.DBStats { return db.DB.Stats() }

// Heartbeat pings the database with a short deadline.
func (db *DB) Heartbeat(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return db.DB.PingContext(ctx)
}

func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.Tx.ExecContext(ctx, tx.dialect.Rebind(query), args...)
}

func (tx *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.Tx.Exec(tx.dialect.Rebind(query), args...)
}

func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.QueryContext(ctx, tx.dialect.Rebind(query), args...)
}

func (tx *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.Query(tx.dialect.Rebind(query), args...)
}

func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.Tx.QueryRowContext(ctx, tx.dialect.Rebind(query), args...)
}

func (tx *Tx) QueryRow(query string, args ...any) *sql.Row {
	return tx.Tx.QueryRow(tx.dialect.Rebind(query), args...)
}

func (tx *Tx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return tx.Tx.PrepareContext(ctx, tx.dialect.Rebind(query))
}

func (tx *Tx) Prepare(query string) (*sql.Stmt, error) {
	return tx.Tx.Prepare(tx.dialect.Rebind(query))
}
