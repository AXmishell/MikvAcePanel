package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// EnsureAdmin creates the bootstrap administrator when no admin exists.
func (s *Store) EnsureAdmin(ctx context.Context, username, passwordHash string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO admins (username, password_hash, created_at) VALUES (?, ?, ?)`,
		username, passwordHash, ts(now()))
	return err == nil, err
}

// GetAdminByUsername loads an administrator by login name.
func (s *Store) GetAdminByUsername(ctx context.Context, username string) (*model.Admin, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at, last_login_at FROM admins WHERE username = ?`, username)
	return scanAdmin(row)
}

// GetAdminByID loads an administrator by numeric id.
func (s *Store) GetAdminByID(ctx context.Context, id int64) (*model.Admin, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at, last_login_at FROM admins WHERE id = ?`, id)
	return scanAdmin(row)
}

func scanAdmin(row *sql.Row) (*model.Admin, error) {
	var a model.Admin
	var created string
	var last sql.NullString
	err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &created, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt = parseTime(created)
	a.LastLoginAt = nullTime(last)
	return &a, nil
}

// TouchAdminLogin records a successful login timestamp.
func (s *Store) TouchAdminLogin(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE admins SET last_login_at = ? WHERE id = ?`, ts(now()), id)
	return err
}

// UpdateAdminPassword changes the password hash of an administrator.
func (s *Store) UpdateAdminPassword(ctx context.Context, id int64, hash string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE admins SET password_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAdmins returns the number of administrator accounts.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

// DefaultSettings are written on first boot. Existing values are never
// overwritten by an upgrade.
var DefaultSettings = map[string]string{
	"panel_name":                   "MikvAce Panel",
	"public_url":                   "",
	"logo_url":                     "",
	"page_title":                   "连接信息",
	"page_note":                    "你的个人订阅",
	"support_url":                  "",
	"web_page_url":                 "",
	"subscription_title":           "MikvAce",
	"subscription_update_interval": "12",
	"subscription_buttons":         "[]",
	"device_grace_minutes":         "15",
	"log_retention_days":           "14",
	"log_ingest_enabled":           "true",
	"analysis_window_hours":        "24",
	"alert_auth_fail_threshold":    "10",
	"alert_error_burst_threshold":  "20",
	"bootstrap_warning":            "",
	"happ_provider_id":             "",
	"happ_routing_mode":            "off",
	"happ_routing_profile":         "",
	"happ_announce":                "",
	"backup_enabled":               "true",
	"backup_interval_hours":        "24",
	"backup_keep":                  "7",
}

// EnsureDefaultSettings inserts missing settings.
func (s *Store) EnsureDefaultSettings(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for k, v := range DefaultSettings {
		if _, err := stmt.ExecContext(ctx, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetSettings returns all settings as a string map.
func (s *Store) GetSettings(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// GetSetting reads a single setting.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting writes a single setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// UpdateSettings writes several settings in one transaction.
func (s *Store) UpdateSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range values {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddEvent records a panel event.
func (s *Store) AddEvent(ctx context.Context, typ string, nodeID int64, severity, message string) {
	_, _ = s.DB.ExecContext(ctx,
		`INSERT INTO events (type, node_id, message, severity, created_at) VALUES (?, ?, ?, ?, ?)`,
		typ, nodeID, message, severity, ts(now()))
}

// ListEvents returns the newest events, optionally filtered by node.
func (s *Store) ListEvents(ctx context.Context, nodeID int64, limit int) ([]model.Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	q := `SELECT e.id, e.type, e.node_id, COALESCE(n.name, ''), e.message, e.severity, e.created_at
	      FROM events e LEFT JOIN nodes n ON n.id = e.node_id`
	args := []any{}
	if nodeID > 0 {
		q += ` WHERE e.node_id = ?`
		args = append(args, nodeID)
	}
	q += ` ORDER BY e.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var e model.Event
		var created string
		if err := rows.Scan(&e.ID, &e.Type, &e.NodeID, &e.NodeName, &e.Message, &e.Severity, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Prune deletes old logs and metrics according to the retention policy.
func (s *Store) Prune(ctx context.Context, logDays, metricDays int) error {
	if logDays > 0 {
		cutoff := ts(now().AddDate(0, 0, -logDays))
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM hy2_logs WHERE ts < ?`, cutoff); err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM log_batches WHERE received_at < ?`, cutoff); err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM events WHERE created_at < ?`, cutoff); err != nil {
			return err
		}
		statsCutoff := ts(now().AddDate(0, 0, -maxInt(logDays, 7)))
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM stats_batches WHERE received_at < ?`, statsCutoff); err != nil {
			return err
		}
	}
	if metricDays > 0 {
		cutoff := ts(now().AddDate(0, 0, -metricDays))
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM node_metrics WHERE ts < ?`, cutoff); err != nil {
			return err
		}
	}
	return nil
}

// DBTime exposes the canonical timestamp format to other packages (tests).
func DBTime(t time.Time) string { return ts(t) }
