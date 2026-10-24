package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

const apiKeySelect = `SELECT id, name, prefix, scopes, rate_limit, enabled, created_at, last_used_at, expires_at FROM api_keys`

func scanAPIKey(row interface{ Scan(...any) error }) (*model.APIKey, error) {
	var k model.APIKey
	var scopes string
	var enabled int
	var created string
	var last, expires sql.NullString
	err := row.Scan(&k.ID, &k.Name, &k.Prefix, &scopes, &k.RateLimit, &enabled, &created, &last, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.Enabled = enabled == 1
	k.Scopes = splitScopes(scopes)
	k.CreatedAt = parseTime(created)
	k.LastUsedAt = nullTime(last)
	k.ExpiresAt = nullTime(expires)
	return &k, nil
}

func splitScopes(raw string) []string {
	out := []string{}
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func JoinScopes(scopes []string) string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}

// CreateAPIKey stores a new hashed API key.
func (s *Store) CreateAPIKey(ctx context.Context, name, prefix, hash string, scopes []string, rateLimit int, expires *time.Time) (*model.APIKey, error) {
	if rateLimit <= 0 {
		rateLimit = 60
	}
	id, err := s.InsertID(ctx,
		`INSERT INTO api_keys (name, prefix, token_hash, scopes, rate_limit, enabled, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, 1, ?, ?)`,
		name, prefix, hash, JoinScopes(scopes), rateLimit, ts(now()), tsPtr(expires))
	if err != nil {
		return nil, attr(err, "create api key")
	}
	row := s.DB.QueryRowContext(ctx, apiKeySelect+` WHERE id = ?`, id)
	return scanAPIKey(row)
}

// ListAPIKeys returns all API keys without their secrets.
func (s *Store) ListAPIKeys(ctx context.Context) ([]model.APIKey, error) {
	rows, err := s.DB.QueryContext(ctx, apiKeySelect+` ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// GetAPIKeyByHash resolves an API key from its hash.
func (s *Store) GetAPIKeyByHash(ctx context.Context, hash string) (*model.APIKey, error) {
	row := s.DB.QueryRowContext(ctx, apiKeySelect+` WHERE token_hash = ?`, hash)
	return scanAPIKey(row)
}

// TouchAPIKey records the last usage time.
func (s *Store) TouchAPIKey(ctx context.Context, id int64) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, ts(now()), id)
}

// SetAPIKeyEnabled toggles a key.
func (s *Store) SetAPIKeyEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE api_keys SET enabled = ? WHERE id = ?`, boolInt(enabled), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAPIKey removes a key.
func (s *Store) DeleteAPIKey(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetUserTraffic zeroes the traffic counters of a user.
func (s *Store) ResetUserTraffic(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET traffic_tx = 0, traffic_rx = 0, updated_at = ? WHERE id = ?`, ts(now()), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
