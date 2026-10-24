package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
		`SELECT id, username, password_hash, created_at, last_login_at,
			totp_secret, totp_pending, totp_enabled, totp_recovery, totp_pending_recovery,
			token_version
		 FROM admins WHERE username = ?`, username)
	return scanAdmin(row)
}

// GetAdminByID loads an administrator by numeric id.
func (s *Store) GetAdminByID(ctx context.Context, id int64) (*model.Admin, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at, last_login_at,
			totp_secret, totp_pending, totp_enabled, totp_recovery, totp_pending_recovery,
			token_version
		 FROM admins WHERE id = ?`, id)
	return scanAdmin(row)
}

func scanAdmin(row *sql.Row) (*model.Admin, error) {
	var a model.Admin
	var created string
	var last sql.NullString
	var recovery, pendingRecovery string
	var totpEnabled int
	err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &created, &last,
		&a.TOTPSecret, &a.TOTPPending, &totpEnabled, &recovery, &pendingRecovery,
		&a.TokenVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt = parseTime(created)
	a.LastLoginAt = nullTime(last)
	a.TOTPEnabled = totpEnabled == 1
	_ = json.Unmarshal([]byte(recovery), &a.TOTPRecovery)
	_ = json.Unmarshal([]byte(pendingRecovery), &a.TOTPPendingRecovery)
	return &a, nil
}

// SetAdminTOTPPending stores an encrypted secret and recovery hashes until the
// administrator confirms the first code.
func (s *Store) SetAdminTOTPPending(ctx context.Context, id int64, secretCipher string, recovery []string) error {
	b, _ := json.Marshal(recovery)
	_, err := s.DB.ExecContext(ctx, `UPDATE admins SET totp_pending = ?, totp_pending_recovery = ? WHERE id = ?`,
		secretCipher, string(b), id)
	return err
}

// EnableAdminTOTP promotes the pending secret to the active one.
func (s *Store) EnableAdminTOTP(ctx context.Context, id int64, secretCipher string, recovery []string) error {
	b, _ := json.Marshal(recovery)
	_, err := s.DB.ExecContext(ctx, `UPDATE admins
		SET totp_secret = ?, totp_recovery = ?, totp_enabled = 1,
			totp_pending = '', totp_pending_recovery = '[]'
		WHERE id = ?`, secretCipher, string(b), id)
	return err
}

// DisableAdminTOTP removes all TOTP material.
func (s *Store) DisableAdminTOTP(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE admins
		SET totp_secret = '', totp_recovery = '[]', totp_enabled = 0,
			totp_pending = '', totp_pending_recovery = '[]'
		WHERE id = ?`, id)
	return err
}

// SetAdminTOTPRecovery replaces the recovery-code hashes.
func (s *Store) SetAdminTOTPRecovery(ctx context.Context, id int64, recovery []string) error {
	b, _ := json.Marshal(recovery)
	_, err := s.DB.ExecContext(ctx, `UPDATE admins SET totp_recovery = ? WHERE id = ?`, string(b), id)
	return err
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

// UpdateAdminUsername changes an administrator's login name.
func (s *Store) UpdateAdminUsername(ctx context.Context, id int64, username string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE admins SET username = ? WHERE id = ?`, username, id)
	if err != nil {
		return attr(err, "update admin username")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// BumpAdminTokenVersion invalidates all sessions issued for an administrator
// and returns the new version.
func (s *Store) BumpAdminTokenVersion(ctx context.Context, id int64) (int, error) {
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE admins SET token_version = token_version + 1 WHERE id = ?`, id); err != nil {
		return 0, err
	}
	var version int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT token_version FROM admins WHERE id = ?`, id).Scan(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return version, nil
}

// GetAdminTokenVersion returns the current session version of an administrator.
func (s *Store) GetAdminTokenVersion(ctx context.Context, id int64) (int, error) {
	var version int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT token_version FROM admins WHERE id = ?`, id).Scan(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return version, nil
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
	"homepage_mode":                "builtin",
	"homepage_version":             "",
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
	"portal_enabled":               "true",
	"portal_title":                 "用户中心",
	"portal_note":                  "",
	"portal_allow_password_change": "true",
	"portal_allow_sub_reset":       "false",
	"portal_session_hours":         "72",
	"admin_security_enabled":       "false",
	"admin_security_path":          "",
}

// EnsureDefaultSettings inserts missing settings.
func (s *Store) EnsureDefaultSettings(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range DefaultSettings {
		if _, err := s.InsertIgnoreTx(ctx, tx, "settings",
			[]string{"setting_key", "value"}, []string{"setting_key"}, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetSettings returns all settings as a string map.
func (s *Store) GetSettings(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	rows, err := s.DB.QueryContext(ctx, `SELECT setting_key, value FROM settings`)
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
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE setting_key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting writes a single setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	return s.Upsert(ctx, "settings",
		[]string{"setting_key", "value"}, []string{"setting_key"}, []string{"value"}, key, value)
}

// UpdateSettings writes several settings in one transaction.
func (s *Store) UpdateSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range values {
		if err := s.UpsertTx(ctx, tx, "settings",
			[]string{"setting_key", "value"}, []string{"setting_key"}, []string{"value"}, k, v); err != nil {
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
