package store

import (
	"context"
	"database/sql"
	"time"
)

// DB wraps *sql.DB with the transaction and health helpers used by the store.
type DB struct {
	*sql.DB
}

// Tx is the transaction counterpart of DB.
type Tx struct {
	*sql.Tx
}

func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := db.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{Tx: tx}, nil
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
