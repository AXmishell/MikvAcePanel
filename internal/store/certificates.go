package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store/dialect"
)

const certificateColumns = `id, domains_json, common_name, cert_pem, key_cipher, key_nonce,
	fingerprint, key_fingerprint, issuer, not_before, not_after, source, status,
	auto_renew, last_error, created_at, updated_at`

func scanCertificate(row interface{ Scan(...any) error }) (*model.Certificate, error) {
	var c model.Certificate
	var domains, notBefore, notAfter, created, updated string
	var autoRenew int
	err := row.Scan(&c.ID, &domains, &c.CommonName, &c.CertPEM, &c.KeyCipher, &c.KeyNonce,
		&c.Fingerprint, &c.KeyFinger, &c.Issuer, &notBefore, &notAfter, &c.Source, &c.Status,
		&autoRenew, &c.LastError, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(domains), &c.Domains)
	c.NotBefore = parseTime(notBefore)
	c.NotAfter = parseTime(notAfter)
	c.CreatedAt = parseTime(created)
	c.UpdatedAt = parseTime(updated)
	c.AutoRenew = autoRenew != 0
	return &c, nil
}

// SaveCertificate inserts a certificate, returning the existing row when the
// fingerprint already exists.
func (s *Store) SaveCertificate(ctx context.Context, c *model.Certificate) (*model.Certificate, bool, error) {
	if c.Fingerprint == "" {
		return nil, false, errors.New("certificate fingerprint is required")
	}
	if existing, err := s.GetCertificateByFingerprint(ctx, c.Fingerprint); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	domains, _ := json.Marshal(c.Domains)
	t := now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = t
	}
	c.UpdatedAt = t
	if c.Status == "" {
		c.Status = "valid"
	}
	id, err := s.InsertID(ctx, `INSERT INTO certificates
		(domains_json, common_name, cert_pem, key_cipher, key_nonce, fingerprint, key_fingerprint,
		 issuer, not_before, not_after, source, status, auto_renew, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(domains), c.CommonName, c.CertPEM, c.KeyCipher, c.KeyNonce, c.Fingerprint,
		c.KeyFinger, c.Issuer, ts(c.NotBefore), ts(c.NotAfter), c.Source, c.Status,
		boolInt(c.AutoRenew), c.LastError, ts(c.CreatedAt), ts(c.UpdatedAt))
	if err != nil {
		return nil, false, err
	}
	c.ID = id
	return c, true, nil
}

// UpdateCertificateStatus updates the lifecycle fields of a certificate.
func (s *Store) UpdateCertificateStatus(ctx context.Context, id int64, status, lastErr string, notAfter time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE certificates
		SET status = ?, last_error = ?, not_after = CASE WHEN ? <> '' THEN ? ELSE not_after END, updated_at = ?
		WHERE id = ?`,
		status, lastErr, ts(notAfter), ts(notAfter), ts(now()), id)
	return err
}

// SetCertificateKey stores an encrypted private key for a certificate that
// was previously issued from a CSR (and therefore had no key on the panel).
func (s *Store) SetCertificateKey(ctx context.Context, id int64, keyCipher, keyNonce, keyFingerprint string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE certificates
		SET key_cipher = ?, key_nonce = ?, key_fingerprint = ?, updated_at = ? WHERE id = ?`,
		keyCipher, keyNonce, keyFingerprint, ts(now()), id)
	return err
}

// GetCertificate loads a certificate by id.
func (s *Store) GetCertificate(ctx context.Context, id int64) (*model.Certificate, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+certificateColumns+` FROM certificates WHERE id = ?`, id)
	return scanCertificate(row)
}

// GetCertificateByFingerprint loads a certificate by its SHA-256 fingerprint.
func (s *Store) GetCertificateByFingerprint(ctx context.Context, fp string) (*model.Certificate, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+certificateColumns+` FROM certificates WHERE fingerprint = ?`, fp)
	return scanCertificate(row)
}

// ListCertificates returns every stored certificate.
func (s *Store) ListCertificates(ctx context.Context) ([]model.Certificate, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+certificateColumns+` FROM certificates ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Certificate
	for rows.Next() {
		c, err := scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetAcmeAccount returns the persisted account for a CA/email pair.
func (s *Store) GetAcmeAccount(ctx context.Context, caURL, email string) (*model.ACMEAccount, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, ca_url, email, key_cipher, key_nonce, account_url, created_at
		FROM acme_accounts WHERE ca_url = ? AND email = ?`, caURL, email)
	var a model.ACMEAccount
	var created string
	err := row.Scan(&a.ID, &a.CAURL, &a.Email, &a.KeyCipher, &a.KeyNonce, &a.AccountURL, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt = parseTime(created)
	return &a, nil
}

// SaveAcmeAccount stores a new ACME account key.
func (s *Store) SaveAcmeAccount(ctx context.Context, a *model.ACMEAccount) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now()
	}
	id, err := s.InsertID(ctx, `INSERT INTO acme_accounts
		(ca_url, email, key_cipher, key_nonce, account_url, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.CAURL, a.Email, a.KeyCipher, a.KeyNonce, a.AccountURL, ts(a.CreatedAt))
	if err != nil {
		return err
	}
	a.ID = id
	return nil
}

// UpdateAcmeAccountURL records the account URL returned by the CA.
func (s *Store) UpdateAcmeAccountURL(ctx context.Context, id int64, url string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE acme_accounts SET account_url = ? WHERE id = ?`, url, id)
	return err
}

// GetNodeCertificate returns the certificate mapping of a node.
func (s *Store) GetNodeCertificate(ctx context.Context, nodeID int64) (*model.NodeCertificate, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT node_id, cert_id, revision, key_fingerprint,
		applied_fingerprint, status, last_error, requested_at, applied_at
		FROM node_certificates WHERE node_id = ?`, nodeID)
	nc, err := scanNodeCertificate(row)
	if err != nil {
		return nil, err
	}
	return nc, nil
}

func scanNodeCertificate(row interface{ Scan(...any) error }) (*model.NodeCertificate, error) {
	var nc model.NodeCertificate
	var requested, applied sql.NullString
	err := row.Scan(&nc.NodeID, &nc.CertID, &nc.Revision, &nc.KeyFingerprint,
		&nc.AppliedFingerprint, &nc.Status, &nc.LastError, &requested, &applied)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	nc.RequestedAt = nullTime(requested)
	nc.AppliedAt = nullTime(applied)
	return &nc, nil
}

// UpsertNodeCertificate writes the mapping, preserving applied fields when 0.
func (s *Store) UpsertNodeCertificate(ctx context.Context, nc *model.NodeCertificate) error {
	if nc.Status == "" {
		nc.Status = "pending"
	}
	args := []any{nc.NodeID, nc.CertID, nc.Revision, nc.KeyFingerprint, nc.AppliedFingerprint,
		nc.Status, nc.LastError, tsPtr(nc.RequestedAt), tsPtr(nc.AppliedAt)}
	var query string
	if s.Dialect().Name() == dialect.MySQL {
		query = `INSERT INTO node_certificates
			(node_id, cert_id, revision, key_fingerprint, applied_fingerprint, status, last_error, requested_at, applied_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				cert_id = VALUES(cert_id),
				revision = VALUES(revision),
				key_fingerprint = VALUES(key_fingerprint),
				applied_fingerprint = IF(VALUES(applied_fingerprint) <> '', VALUES(applied_fingerprint), applied_fingerprint),
				status = VALUES(status),
				last_error = VALUES(last_error),
				requested_at = COALESCE(VALUES(requested_at), requested_at),
				applied_at = COALESCE(VALUES(applied_at), applied_at)`
	} else {
		query = `INSERT INTO node_certificates
			(node_id, cert_id, revision, key_fingerprint, applied_fingerprint, status, last_error, requested_at, applied_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(node_id) DO UPDATE SET
				cert_id = excluded.cert_id,
				revision = excluded.revision,
				key_fingerprint = excluded.key_fingerprint,
				applied_fingerprint = CASE WHEN excluded.applied_fingerprint <> '' THEN excluded.applied_fingerprint ELSE node_certificates.applied_fingerprint END,
				status = excluded.status,
				last_error = excluded.last_error,
				requested_at = COALESCE(excluded.requested_at, node_certificates.requested_at),
				applied_at = COALESCE(excluded.applied_at, node_certificates.applied_at)`
	}
	_, err := s.DB.ExecContext(ctx, query, args...)
	return err
}

// SetNodeCertStatus updates just the status/error of a node certificate.
func (s *Store) SetNodeCertStatus(ctx context.Context, nodeID int64, status, errMsg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE node_certificates SET status = ?, last_error = ? WHERE node_id = ?`,
		status, errMsg, nodeID)
	return err
}

// SetNodeCertApplied marks a certificate as installed on the node.
func (s *Store) SetNodeCertApplied(ctx context.Context, nodeID int64, fingerprint string) error {
	t := ts(now())
	_, err := s.DB.ExecContext(ctx, `UPDATE node_certificates
		SET applied_fingerprint = ?, status = 'active', last_error = '', applied_at = ?
		WHERE node_id = ?`, fingerprint, t, nodeID)
	return err
}

// SetNodeCertPending marks a mapping as requesting a fresh certificate.
func (s *Store) SetNodeCertPending(ctx context.Context, nodeID int64, keyFingerprint string) error {
	t := ts(now())
	var query string
	if s.Dialect().Name() == dialect.MySQL {
		query = `INSERT INTO node_certificates
			(node_id, cert_id, revision, key_fingerprint, status, requested_at)
			VALUES (?, 0, 0, ?, 'requesting', ?)
			ON DUPLICATE KEY UPDATE
				key_fingerprint = VALUES(key_fingerprint),
				status = 'requesting',
				last_error = '',
				requested_at = VALUES(requested_at)`
	} else {
		query = `INSERT INTO node_certificates
			(node_id, cert_id, revision, key_fingerprint, status, requested_at)
			VALUES (?, 0, 0, ?, 'requesting', ?)
			ON CONFLICT(node_id) DO UPDATE SET
				key_fingerprint = excluded.key_fingerprint,
				status = 'requesting',
				last_error = '',
				requested_at = excluded.requested_at`
	}
	_, err := s.DB.ExecContext(ctx, query, nodeID, keyFingerprint, t)
	return err
}

// ListNodeCertificates returns certificate mappings, optionally filtered by
// expiry.
func (s *Store) ListNodeCertificates(ctx context.Context, expiringBefore *time.Time) ([]model.NodeCertificate, error) {
	query := `SELECT nc.node_id, nc.cert_id, nc.revision, nc.key_fingerprint,
		nc.applied_fingerprint, nc.status, nc.last_error, nc.requested_at, nc.applied_at
		FROM node_certificates nc`
	args := []any{}
	if expiringBefore != nil {
		query += ` JOIN certificates c ON c.id = nc.cert_id WHERE c.not_after <> '' AND c.not_after < ?`
		args = append(args, ts(*expiringBefore))
	}
	query += ` ORDER BY nc.node_id`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NodeCertificate
	for rows.Next() {
		nc, err := scanNodeCertificate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *nc)
	}
	return out, rows.Err()
}

// UpdateNodeCertInfo stores the certificate metadata reported by an agent.
func (s *Store) UpdateNodeCertInfo(ctx context.Context, nodeID int64, fingerprint string, notAfter *time.Time) error {
	args := []any{fingerprint, ts(now()), nodeID}
	query := `UPDATE nodes SET cert_fingerprint = ?, updated_at = ? WHERE id = ?`
	if notAfter != nil {
		query = `UPDATE nodes SET cert_fingerprint = ?, cert_not_after = ?, updated_at = ? WHERE id = ?`
		args = []any{fingerprint, ts(*notAfter), ts(now()), nodeID}
	}
	_, err := s.DB.ExecContext(ctx, query, args...)
	return err
}

// UpdateNodeCapabilities stores the capability list reported by an agent.
func (s *Store) UpdateNodeCapabilities(ctx context.Context, nodeID int64, caps []string) error {
	b, _ := json.Marshal(caps)
	_, err := s.DB.ExecContext(ctx, `UPDATE nodes SET capabilities_json = ?, updated_at = ? WHERE id = ?`,
		string(b), ts(now()), nodeID)
	return err
}
