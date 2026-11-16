package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

const apiKeySelect = `SELECT id, name, description, prefix, scopes, node_ids, group_ids, rate_limit,
	request_count, last_used_ip, enabled, created_at, updated_at, last_used_at, expires_at FROM api_keys`

func scanAPIKey(row interface{ Scan(...any) error }) (*model.APIKey, error) {
	var k model.APIKey
	var scopes, nodeIDs, groupIDs, lastIP string
	var enabled int
	var created string
	var updated, last, expires sql.NullString
	err := row.Scan(&k.ID, &k.Name, &k.Description, &k.Prefix, &scopes, &nodeIDs, &groupIDs,
		&k.RateLimit, &k.RequestCount, &lastIP, &enabled, &created, &updated, &last, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.Enabled = enabled == 1
	k.Scopes = splitScopes(scopes)
	k.NodeIDs = splitInt64s(nodeIDs)
	k.GroupIDs = splitInt64s(groupIDs)
	k.LastUsedIP = lastIP
	k.CreatedAt = parseTime(created)
	if updated.Valid {
		k.UpdatedAt = parseTime(updated.String)
	} else {
		k.UpdatedAt = k.CreatedAt
	}
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

func splitInt64s(raw string) []int64 {
	out := []int64{}
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if id, err := strconv.ParseInt(s, 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func JoinInt64s(ids []int64) string {
	seen := map[int64]bool{}
	out := []string{}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, strconv.FormatInt(id, 10))
		}
	}
	return strings.Join(out, ",")
}

// CreateAPIKey stores a new hashed API key with default (unrestricted) resource
// scope. It keeps the original signature so legacy importers keep working.
func (s *Store) CreateAPIKey(ctx context.Context, name, prefix, hash string, scopes []string, rateLimit int, expires *time.Time) (*model.APIKey, error) {
	return s.CreateAPIKeyFull(ctx, name, "", prefix, hash, scopes, nil, nil, rateLimit, expires)
}

// CreateAPIKeyFull stores a new hashed API key including its permission scopes
// and optional node/group resource restriction.
func (s *Store) CreateAPIKeyFull(ctx context.Context, name, description, prefix, hash string, scopes []string, nodeIDs, groupIDs []int64, rateLimit int, expires *time.Time) (*model.APIKey, error) {
	if rateLimit <= 0 {
		rateLimit = 60
	}
	nowTS := ts(now())
	id, err := s.InsertID(ctx,
		`INSERT INTO api_keys (name, description, prefix, token_hash, scopes, node_ids, group_ids, rate_limit,
			request_count, last_used_ip, enabled, created_at, updated_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, '', 1, ?, ?, ?)`,
		name, description, prefix, hash, JoinScopes(scopes), JoinInt64s(nodeIDs), JoinInt64s(groupIDs),
		rateLimit, nowTS, nowTS, tsPtr(expires))
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

// GetAPIKey loads one API key by primary key.
func (s *Store) GetAPIKey(ctx context.Context, id int64) (*model.APIKey, error) {
	row := s.DB.QueryRowContext(ctx, apiKeySelect+` WHERE id = ?`, id)
	return scanAPIKey(row)
}

// UpdateAPIKey replaces the editable fields of a key. The token itself is not
// changed; use RotateAPIKey for that.
func (s *Store) UpdateAPIKey(ctx context.Context, id int64, name, description string, scopes []string, nodeIDs, groupIDs []int64, rateLimit int, expires *time.Time) error {
	if rateLimit <= 0 {
		rateLimit = 60
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE api_keys SET name = ?, description = ?, scopes = ?, node_ids = ?, group_ids = ?,
			rate_limit = ?, updated_at = ?, expires_at = ? WHERE id = ?`,
		name, description, JoinScopes(scopes), JoinInt64s(nodeIDs), JoinInt64s(groupIDs),
		rateLimit, ts(now()), tsPtr(expires), id)
	if err != nil {
		return attr(err, "update api key")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RotateAPIKey replaces the token hash and prefix, revoking the old token.
func (s *Store) RotateAPIKey(ctx context.Context, id int64, prefix, hash string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE api_keys SET prefix = ?, token_hash = ?, updated_at = ? WHERE id = ?`,
		prefix, hash, ts(now()), id)
	if err != nil {
		return attr(err, "rotate api key")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchAPIKey records usage time, caller address and increments the request
// counter so operators can see activity per key.
func (s *Store) TouchAPIKey(ctx context.Context, id int64, ip string) {
	_, _ = s.DB.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ?, last_used_ip = ?, request_count = request_count + 1 WHERE id = ?`,
		ts(now()), ip, id)
}

// SetAPIKeyEnabled toggles a key.
func (s *Store) SetAPIKeyEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE api_keys SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), ts(now()), id)
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
