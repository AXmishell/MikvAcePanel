package store

import (
	"context"
	"database/sql"
	"errors"
)

// NodeExists checks whether a node id exists without hydrating its relations.
func (s *Store) NodeExists(ctx context.Context, id int64) (bool, error) {
	var one int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
