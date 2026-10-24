package store

import "context"

// InsertIgnore inserts a row and silently ignores duplicate keys.
func (s *Store) InsertIgnore(ctx context.Context, table string, cols, conflict []string, args ...any) (bool, error) {
	return s.insertIgnore(ctx, s.DB, table, cols, conflict, args...)
}

// InsertIgnoreTx is InsertIgnore inside a transaction.
func (s *Store) InsertIgnoreTx(ctx context.Context, tx *Tx, table string, cols, conflict []string, args ...any) (bool, error) {
	return s.insertIgnore(ctx, tx, table, cols, conflict, args...)
}

func (s *Store) insertIgnore(ctx context.Context, q execer, table string, cols, conflict []string, args ...any) (bool, error) {
	res, err := q.ExecContext(ctx, s.Dialect().InsertIgnore(table, cols, conflict), args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Upsert inserts a row or updates the listed columns on conflict.
func (s *Store) Upsert(ctx context.Context, table string, cols, conflict, updates []string, args ...any) error {
	return s.upsert(ctx, s.DB, table, cols, conflict, updates, args...)
}

// UpsertTx is Upsert inside a transaction.
func (s *Store) UpsertTx(ctx context.Context, tx *Tx, table string, cols, conflict, updates []string, args ...any) error {
	return s.upsert(ctx, tx, table, cols, conflict, updates, args...)
}

func (s *Store) upsert(ctx context.Context, q execer, table string, cols, conflict, updates []string, args ...any) error {
	_, err := q.ExecContext(ctx, s.Dialect().Upsert(table, cols, conflict, updates), args...)
	return err
}
