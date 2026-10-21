package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

const userSelect = `SELECT id, user_id, username, password, subscription_token, xray_uuid, enabled,
	traffic_tx, traffic_rx, traffic_limit, max_devices, expire_at, last_seen_at,
	created_at, updated_at FROM users`

// RandomToken returns a URL-safe random hex token.
// RandomUUID returns a RFC 4122 version 4 UUID.
func RandomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return RandomToken(16)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func RandomToken(bytes int) string {
	if bytes <= 0 {
		bytes = 16
	}
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is fatal for security tokens; fall back to a
		// time based value rather than producing an empty token.
		return fmt.Sprintf("%x%x", time.Now().UnixNano(), b)
	}
	return hex.EncodeToString(b)
}

func scanUser(row interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	var enabled int
	var expire, last sql.NullString
	var created, updated string
	err := row.Scan(&u.ID, &u.UserID, &u.Username, &u.Password, &u.SubscriptionToken, &u.XrayUUID, &enabled,
		&u.TrafficTx, &u.TrafficRx, &u.TrafficLimit, &u.MaxDevices, &expire, &last,
		&created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled == 1
	u.ExpireAt = nullTime(expire)
	u.LastSeenAt = nullTime(last)
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updated)
	u.Groups = []model.Group{}
	u.NodeIDs = []int64{}
	return &u, nil
}

// CreateUser inserts a user and its group / node assignments.
func (s *Store) CreateUser(ctx context.Context, u *model.User, groupIDs, nodeIDs []int64) (*model.User, error) {
	if u.SubscriptionToken == "" {
		u.SubscriptionToken = RandomToken(12)
	}
	if u.UserID == "" {
		u.UserID = RandomToken(8)
	}
	if u.Username == "" {
		u.Username = u.UserID
	}
	if u.XrayUUID == "" {
		u.XrayUUID = RandomUUID()
	}
	nowT := now()
	u.CreatedAt, u.UpdatedAt = nowT, nowT
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (user_id, username, password, subscription_token, xray_uuid, enabled,
		 traffic_tx, traffic_rx, traffic_limit, max_devices, expire_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.UserID, u.Username, u.Password, u.SubscriptionToken, u.XrayUUID, boolInt(u.Enabled),
		u.TrafficTx, u.TrafficRx, u.TrafficLimit, u.MaxDevices, tsPtr(u.ExpireAt), ts(nowT), ts(nowT))
	if err != nil {
		return nil, attr(err, "create user")
	}
	id, _ := res.LastInsertId()
	u.ID = id
	if err := s.SetUserGroups(ctx, id, groupIDs); err != nil {
		return nil, err
	}
	if err := s.SetUserNodes(ctx, id, nodeIDs); err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}

// GetUserByID loads a user by primary key.
func (s *Store) GetUserByID(ctx context.Context, id int64) (*model.User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx, userSelect+` WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := s.hydrateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByUserID loads a user by the Hysteria auth identifier.
func (s *Store) GetUserByUserID(ctx context.Context, userID string) (*model.User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx, userSelect+` WHERE user_id = ?`, userID))
	if err != nil {
		return nil, err
	}
	if err := s.hydrateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// GetUserByToken loads a user by subscription token.
func (s *Store) GetUserByToken(ctx context.Context, token string) (*model.User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx, userSelect+` WHERE subscription_token = ?`, token))
	if err != nil {
		return nil, err
	}
	if err := s.hydrateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) hydrateUser(ctx context.Context, u *model.User) error {
	groups, err := s.GetUserGroups(ctx, u.ID)
	if err != nil {
		return err
	}
	u.Groups = groups
	ids, err := s.GetUserNodeIDs(ctx, u.ID)
	if err != nil {
		return err
	}
	u.NodeIDs = ids
	return nil
}

// GetUserGroups returns groups assigned to a user.
func (s *Store) GetUserGroups(ctx context.Context, userID int64) ([]model.Group, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT g.id, g.name, g.description, g.color, g.subscription_title, g.max_devices, g.created_at, g.updated_at
		 FROM groups g JOIN user_groups ug ON ug.group_id = g.id
		 WHERE ug.user_id = ? ORDER BY g.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// GetUserNodeIDs returns direct node assignments for a user.
func (s *Store) GetUserNodeIDs(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT node_id FROM user_nodes WHERE user_id = ? ORDER BY node_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetUserGroups replaces the group assignments of a user.
func (s *Store) SetUserGroups(ctx context.Context, userID int64, groupIDs []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_groups WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, gid := range uniqueIDs(groupIDs) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_groups (user_id, group_id) VALUES (?, ?)`, userID, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetUserNodes replaces the direct node assignments of a user.
func (s *Store) SetUserNodes(ctx context.Context, userID int64, nodeIDs []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_nodes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, nid := range uniqueIDs(nodeIDs) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_nodes (user_id, node_id) VALUES (?, ?)`, userID, nid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// UpdateUser updates all mutable user fields. An empty Password keeps the
// current password.
func (s *Store) UpdateUser(ctx context.Context, u *model.User, groupIDs, nodeIDs []int64) (*model.User, error) {
	nowT := now()
	password := u.Password
	if password == "" {
		var old string
		err := s.DB.QueryRowContext(ctx, `SELECT password FROM users WHERE id = ?`, u.ID).Scan(&old)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		password = old
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE users SET user_id = ?, username = ?, password = ?, xray_uuid = ?, enabled = ?, traffic_limit = ?,
		 max_devices = ?, expire_at = ?, updated_at = ? WHERE id = ?`,
		u.UserID, u.Username, password, u.XrayUUID, boolInt(u.Enabled), u.TrafficLimit,
		u.MaxDevices, tsPtr(u.ExpireAt), ts(nowT), u.ID)
	if err != nil {
		return nil, attr(err, "update user")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	if groupIDs != nil {
		if err := s.SetUserGroups(ctx, u.ID, groupIDs); err != nil {
			return nil, err
		}
	}
	if nodeIDs != nil {
		if err := s.SetUserNodes(ctx, u.ID, nodeIDs); err != nil {
			return nil, err
		}
	}
	return s.GetUserByID(ctx, u.ID)
}

// DeleteUser removes a user.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserEnabled toggles a user account.
func (s *Store) SetUserEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), ts(now()), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetUserSubscriptionToken generates a new subscription token.
func (s *Store) ResetUserSubscriptionToken(ctx context.Context, id int64) (string, error) {
	token := RandomToken(12)
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET subscription_token = ?, updated_at = ? WHERE id = ?`, token, ts(now()), id)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

// ResetUserPassword sets a new auth password.
func (s *Store) ResetUserPassword(ctx context.Context, id int64, password string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET password = ?, updated_at = ? WHERE id = ?`, password, ts(now()), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddUserTraffic applies a per-user traffic delta.
func (s *Store) AddUserTraffic(ctx context.Context, userID string, tx, rx int64) error {
	if tx == 0 && rx == 0 {
		return nil
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET traffic_tx = traffic_tx + ?, traffic_rx = traffic_rx + ?, last_seen_at = ?, updated_at = ?
		 WHERE user_id = ?`, tx, rx, ts(now()), ts(now()), userID)
	return err
}

// TouchUserSeen updates the last-seen timestamp of an active user.
func (s *Store) TouchUserSeen(ctx context.Context, userID string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE user_id = ?`, ts(now()), userID)
}

// ListUsers returns a filtered, paginated user list.
func (s *Store) ListUsers(ctx context.Context, query, status string, page, size int) ([]model.User, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	where := []string{"1=1"}
	args := []any{}
	if q := strings.TrimSpace(query); q != "" {
		like := "%" + q + "%"
		where = append(where, "(user_id LIKE ? OR username LIKE ? OR subscription_token LIKE ?)")
		args = append(args, like, like, like)
	}
	switch status {
	case "active":
		where = append(where, "enabled = 1 AND (expire_at IS NULL OR expire_at = '' OR expire_at > ?)")
		args = append(args, ts(now()))
	case "disabled":
		where = append(where, "enabled = 0")
	case "expired":
		where = append(where, "expire_at IS NOT NULL AND expire_at != '' AND expire_at <= ?")
		args = append(args, ts(now()))
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := userSelect + ` WHERE ` + cond + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	users := []model.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range users {
		if err := s.hydrateUser(ctx, &users[i]); err != nil {
			return nil, 0, err
		}
	}
	return users, total, nil
}

// CountUsers returns total and enabled account counts.
func (s *Store) CountUsers(ctx context.Context) (total, active int, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(enabled), 0) FROM users`).Scan(&total, &active)
	return
}

// CountExpiredUsers returns the number of expired accounts.
func (s *Store) CountExpiredUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE expire_at IS NOT NULL AND expire_at != '' AND expire_at <= ?`, ts(now())).Scan(&n)
	return n, err
}

// SumUserTraffic returns the sum of all user traffic counters.
func (s *Store) SumUserTraffic(ctx context.Context) (tx, rx int64, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(traffic_tx),0), COALESCE(SUM(traffic_rx),0) FROM users`).Scan(&tx, &rx)
	return
}

// UsersOverQuota returns enabled users at or above their traffic limit.
func (s *Store) UsersOverQuota(ctx context.Context) ([]model.User, error) {
	rows, err := s.DB.QueryContext(ctx, userSelect+` WHERE enabled = 1 AND traffic_limit > 0 AND (traffic_tx + traffic_rx) >= traffic_limit`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UsersForNode returns enabled users that are allowed to use a node according
// to the same direct/group/public targeting rules as subscriptions. Expired
// and over-quota accounts are excluded so Xray nodes (which have no external
// auth hook) stop serving them as soon as the config is reconciled.
func (s *Store) UsersForNode(ctx context.Context, nodeID int64) ([]model.User, error) {
	q := userSelect + ` WHERE enabled = 1 AND xray_uuid != ''
		AND (expire_at IS NULL OR expire_at = '' OR expire_at > ?)
		AND (traffic_limit = 0 OR (traffic_tx + traffic_rx) < traffic_limit) AND (
		EXISTS (SELECT 1 FROM user_nodes un WHERE un.user_id = users.id AND un.node_id = ?)
		OR (
			NOT EXISTS (SELECT 1 FROM user_nodes un WHERE un.user_id = users.id)
			AND (
				NOT EXISTS (SELECT 1 FROM node_groups ng WHERE ng.node_id = ?)
				OR EXISTS (
					SELECT 1 FROM user_groups ug
					JOIN node_groups ng ON ng.group_id = ug.group_id
					WHERE ug.user_id = users.id AND ng.node_id = ?
				)
			)
		)
	) ORDER BY users.id`
	rows, err := s.DB.QueryContext(ctx, q, ts(now()), nodeID, nodeID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}
