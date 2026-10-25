package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// nodeRuntimeMode normalizes the operator-selected runtime before it is
// written to SQLite. Invalid or missing values fall back to process mode.
func nodeRuntimeMode(n *model.Node) string {
	if n != nil && strings.EqualFold(strings.TrimSpace(n.RuntimeMode), "docker") {
		return "docker"
	}
	return "process"
}

const nodeSelect = `SELECT id, name, type, flag, ip, domain, sni, listen, port, port_range, hop_interval,
	obfs_type, obfs_password, stats_port, stats_secret, xray_json, hy2_json, ssh_port, ssh_user, ssh_password, ssh_key, ssh_host_key, ssh_install_docker,
	tls_mode, acme_email, bandwidth_up, bandwidth_down,
	masquerade_url, acl_json, outbounds_json, max_online, ranking, active, status, token_hash, token, token_prefix,
	agent_version, os, arch, hy2_version, runtime, runtime_mode, last_seen_at, config_revision, applied_revision,
	config_error, traffic_tx, traffic_rx, online_users, created_at, updated_at,
	capabilities_json, cert_fingerprint, cert_not_after, cores_json, xray_version FROM nodes`

func scanNode(row interface{ Scan(...any) error }) (*model.Node, error) {
	var n model.Node
	var acl, outbounds, xrayJSON, hy2JSON string
	var active, sshInstallDocker int
	var last sql.NullString
	var capsJSON, certAfter, coresJSON sql.NullString
	var created, updated string
	err := row.Scan(&n.ID, &n.Name, &n.Type, &n.Flag, &n.IP, &n.Domain, &n.SNI, &n.Listen,
		&n.Port, &n.PortRange, &n.HopInterval, &n.ObfsType, &n.ObfsPassword, &n.StatsPort, &n.StatsSecret,
		&xrayJSON, &hy2JSON, &n.SSHPort, &n.SSHUser, &n.SSHPassword, &n.SSHKey, &n.SSHHostKey, &sshInstallDocker,
		&n.TLSMode, &n.ACMEEmail, &n.BandwidthUp, &n.BandwidthDown, &n.MasqueradeURL, &acl, &outbounds,
		&n.MaxOnline, &n.Ranking, &active, &n.Status, new(string), &n.Token, &n.TokenPrefix, &n.AgentVersion,
		&n.OS, &n.Arch, &n.Hy2Version, &n.Runtime, &n.RuntimeMode, &last, &n.ConfigRevision, &n.AppliedRevision,
		&n.ConfigError, &n.TrafficTx, &n.TrafficRx, &n.OnlineUsers, &created, &updated,
		&capsJSON, &n.CertFingerprint, &certAfter, &coresJSON, &n.XrayVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	n.Active = active == 1
	n.SSHInstallDocker = sshInstallDocker == 1
	n.LastSeenAt = nullTime(last)
	n.CertNotAfter = nullTime(certAfter)
	if capsJSON.Valid && capsJSON.String != "" {
		_ = json.Unmarshal([]byte(capsJSON.String), &n.Capabilities)
	}
	if coresJSON.Valid && coresJSON.String != "" {
		_ = json.Unmarshal([]byte(coresJSON.String), &n.Cores)
	}
	n.CreatedAt = parseTime(created)
	n.UpdatedAt = parseTime(updated)
	if n.RuntimeMode == "" {
		n.RuntimeMode = "process"
	}
	n.ACL = unmarshalStrings(acl)
	n.Outbounds = unmarshalOutbounds(outbounds)
	if xrayJSON != "" && xrayJSON != "{}" {
		_ = json.Unmarshal([]byte(xrayJSON), &n.Xray)
	}
	if hy2JSON != "" && hy2JSON != "{}" {
		_ = json.Unmarshal([]byte(hy2JSON), &n.Hy2)
	}
	n.Groups = []model.Group{}
	if n.Type == "" {
		n.Type = "hysteria"
	}
	return &n, nil
}

// ListNodeGroups returns the groups of a node.
func (s *Store) ListNodeGroups(ctx context.Context, nodeID int64) ([]model.Group, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT g.id, g.name, g.description, g.color, g.subscription_title, g.max_devices, g.created_at, g.updated_at
		 FROM panel_groups g JOIN node_groups ng ON ng.group_id = g.id WHERE ng.node_id = ? ORDER BY g.name`, nodeID)
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

// GetNodeGroupIDs returns group ids assigned to a node.
func (s *Store) GetNodeGroupIDs(ctx context.Context, nodeID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT group_id FROM node_groups WHERE node_id = ? ORDER BY group_id`, nodeID)
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

// SetNodeGroups replaces node group assignments.
func (s *Store) SetNodeGroups(ctx context.Context, nodeID int64, groupIDs []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_groups WHERE node_id = ?`, nodeID); err != nil {
		return err
	}
	for _, gid := range uniqueIDs(groupIDs) {
		if _, err := s.InsertIgnoreTx(ctx, tx, "node_groups",
			[]string{"node_id", "group_id"}, []string{"node_id", "group_id"}, nodeID, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListNodes returns all nodes.
func (s *Store) ListNodes(ctx context.Context) ([]model.Node, error) {
	rows, err := s.DB.QueryContext(ctx, nodeSelect+` ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		groups, err := s.ListNodeGroups(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Groups = groups
	}
	return out, nil
}

// GetNode loads a node by id.
func (s *Store) GetNode(ctx context.Context, id int64) (*model.Node, error) {
	n, err := scanNode(s.DB.QueryRowContext(ctx, nodeSelect+` WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	groups, err := s.ListNodeGroups(ctx, id)
	if err != nil {
		return nil, err
	}
	n.Groups = groups
	return n, nil
}

// GetNodeByTokenHash resolves a node from an agent token hash.
func (s *Store) GetNodeByTokenHash(ctx context.Context, hash string) (*model.Node, error) {
	n, err := scanNode(s.DB.QueryRowContext(ctx, nodeSelect+` WHERE token_hash = ?`, hash))
	if err != nil {
		return nil, err
	}
	return n, nil
}

// CreateNode inserts a node. tokenHash is the SHA-256 hash of the agent token.
func (s *Store) CreateNode(ctx context.Context, n *model.Node, tokenHash, tokenPrefix string, groupIDs []int64) (*model.Node, error) {
	nowT := now()
	n.CreatedAt, n.UpdatedAt = nowT, nowT
	if n.Type == "" {
		n.Type = "hysteria"
	}
	if n.TLSMode == "" {
		n.TLSMode = "selfsigned"
	}
	if n.StatsPort == 0 {
		n.StatsPort = 9999
	}
	if n.Port == 0 {
		n.Port = 443
	}
	id, err := s.InsertID(ctx,
		`INSERT INTO nodes (name, type, flag, ip, domain, sni, listen, port, port_range, hop_interval,
		 obfs_type, obfs_password, stats_port, stats_secret, xray_json, hy2_json, cores_json, ssh_port, ssh_user, ssh_password, ssh_key, ssh_host_key, ssh_install_docker,
		 tls_mode, acme_email, bandwidth_up, bandwidth_down,
		 masquerade_url, acl_json, outbounds_json, max_online, ranking, active, status, token_hash, token, token_prefix,
		 runtime_mode, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Name, n.Type, n.Flag, n.IP, n.Domain, n.SNI, n.Listen, n.Port, n.PortRange, n.HopInterval,
		n.ObfsType, n.ObfsPassword, n.StatsPort, n.StatsSecret, marshalJSON(n.Xray), marshalJSON(n.Hy2), marshalJSON(n.Cores), n.SSHPort, n.SSHUser, n.SSHPassword, n.SSHKey, n.SSHHostKey,
		boolInt(n.SSHInstallDocker), n.TLSMode, n.ACMEEmail, n.BandwidthUp, n.BandwidthDown,
		n.MasqueradeURL, marshalJSON(n.ACL), marshalJSON(n.Outbounds), n.MaxOnline, n.Ranking, boolInt(n.Active),
		"offline", tokenHash, n.Token, tokenPrefix, nodeRuntimeMode(n), ts(nowT), ts(nowT))
	if err != nil {
		return nil, attr(err, "create node")
	}
	if err := s.SetNodeGroups(ctx, id, groupIDs); err != nil {
		return nil, err
	}
	return s.GetNode(ctx, id)
}

// UpdateNode updates a node's editable settings. The agent token is unchanged.
func (s *Store) UpdateNode(ctx context.Context, n *model.Node, groupIDs []int64) (*model.Node, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE nodes SET name = ?, type = ?, flag = ?, ip = ?, domain = ?, sni = ?, listen = ?, port = ?, port_range = ?,
		 hop_interval = ?, obfs_type = ?, obfs_password = ?, stats_port = ?, xray_json = ?, hy2_json = ?, cores_json = ?, ssh_port = ?, ssh_user = ?,
		 ssh_password = ?, ssh_key = ?, ssh_host_key = ?, ssh_install_docker = ?, tls_mode = ?, acme_email = ?,
		 bandwidth_up = ?, bandwidth_down = ?, masquerade_url = ?, acl_json = ?, outbounds_json = ?,
		 max_online = ?, ranking = ?, active = ?, runtime_mode = ?, updated_at = ? WHERE id = ?`,
		n.Name, n.Type, n.Flag, n.IP, n.Domain, n.SNI, n.Listen, n.Port, n.PortRange, n.HopInterval,
		n.ObfsType, n.ObfsPassword, n.StatsPort, marshalJSON(n.Xray), marshalJSON(n.Hy2), marshalJSON(n.Cores), n.SSHPort, n.SSHUser, n.SSHPassword, n.SSHKey, n.SSHHostKey,
		boolInt(n.SSHInstallDocker), n.TLSMode, n.ACMEEmail, n.BandwidthUp, n.BandwidthDown,
		n.MasqueradeURL, marshalJSON(n.ACL), marshalJSON(n.Outbounds), n.MaxOnline, n.Ranking,
		boolInt(n.Active), nodeRuntimeMode(n), ts(now()), n.ID)
	if err != nil {
		return nil, attr(err, "update node")
	}
	cnt, _ := res.RowsAffected()
	if cnt == 0 {
		return nil, ErrNotFound
	}
	if groupIDs != nil {
		if err := s.SetNodeGroups(ctx, n.ID, groupIDs); err != nil {
			return nil, err
		}
	}
	return s.GetNode(ctx, n.ID)
}

// ReorderNodes persists the operator's node order by assigning descending
// ranking weights. Higher weights sort first in subscriptions, so the first id
// in ids receives the highest weight. Existing weights are preserved as the
// scale upper bound when possible, keeping manually tuned values meaningful.
func (s *Store) ReorderNodes(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	var maxRanking float64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ranking), 1) FROM nodes WHERE id IN (`+placeholders+`)`, args...,
	).Scan(&maxRanking); err != nil {
		return err
	}
	base := float64(len(ids))
	if maxRanking > base {
		base = maxRanking
	}
	stamp := ts(now())
	for i, id := range ids {
		res, err := tx.ExecContext(ctx,
			`UPDATE nodes SET ranking = ?, updated_at = ? WHERE id = ?`,
			base-float64(i), stamp, id)
		if err != nil {
			return err
		}
		cnt, _ := res.RowsAffected()
		if cnt == 0 {
			return ErrNotFound
		}
	}
	return tx.Commit()
}

// DeleteNode removes a node.
func (s *Store) DeleteNode(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	cnt, _ := res.RowsAffected()
	if cnt == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetNodeToken replaces the agent token.
func (s *Store) ResetNodeToken(ctx context.Context, id int64, token, hash, prefix string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE nodes SET token = ?, token_hash = ?, token_prefix = ?, updated_at = ? WHERE id = ?`,
		token, hash, prefix, ts(now()), id)
	if err != nil {
		return err
	}
	cnt, _ := res.RowsAffected()
	if cnt == 0 {
		return ErrNotFound
	}
	return nil
}

// SetNodeSSHHostKey pins the SSH host key used for future installations.
func (s *Store) SetNodeSSHHostKey(ctx context.Context, id int64, hostKey string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE nodes SET ssh_host_key = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(hostKey), ts(now()), id)
	if err != nil {
		return err
	}
	cnt, _ := res.RowsAffected()
	if cnt == 0 {
		return ErrNotFound
	}
	return nil
}

// SetNodeStatus updates connectivity state.
func (s *Store) SetNodeStatus(ctx context.Context, id int64, status string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE nodes SET status = ?, updated_at = ? WHERE id = ?`, status, ts(now()), id)
	return err
}

// NodeHello is the information an agent sends when connecting.
type NodeHello struct {
	Version     string
	OS          string
	Arch        string
	Hy2Version  string
	XrayVersion string
	Cores       []string
	Runtime     string
	OnlineUsers int
}

// MarkNodeOnline records a successful agent connection.
func (s *Store) MarkNodeOnline(ctx context.Context, id int64, hello NodeHello) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE nodes SET status = 'online', last_seen_at = ?, agent_version = ?, os = ?, arch = ?,
		 hy2_version = ?, xray_version = ?, runtime = ?, online_users = ?, updated_at = ? WHERE id = ?`,
		ts(now()), hello.Version, hello.OS, hello.Arch, hello.Hy2Version, hello.XrayVersion,
		hello.Runtime, hello.OnlineUsers, ts(now()), id)
	return err
}

// MarkNodeOffline records a disconnected agent.
func (s *Store) MarkNodeOffline(ctx context.Context, id int64, status string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE nodes SET status = ?, online_users = 0, updated_at = ? WHERE id = ?`, status, ts(now()), id)
	return err
}

// MarkStaleNodesOffline marks nodes whose last heartbeat is older than cutoff.
func (s *Store) MarkStaleNodesOffline(ctx context.Context, cutoff time.Time) ([]model.Node, error) {
	rows, err := s.DB.QueryContext(ctx, nodeSelect+` WHERE status = 'online' AND (last_seen_at IS NULL OR last_seen_at < ?)`,
		ts(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stale := []model.Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		stale = append(stale, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, n := range stale {
		_ = s.MarkNodeOffline(ctx, n.ID, "offline")
	}
	return stale, nil
}

// UpdateNodeHeartbeat applies a heartbeat to the node row. The traffic values
// are deltas since the previous sample, because agent counters reset when an
// agent restarts while the panel's node counters are cumulative.
func (s *Store) UpdateNodeHeartbeat(ctx context.Context, id int64, online int, dtx, drx int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE nodes SET status = CASE WHEN config_error != '' THEN 'error' ELSE 'online' END,
		 last_seen_at = ?, online_users = ?, traffic_tx = traffic_tx + ?,
		 traffic_rx = traffic_rx + ?, updated_at = ? WHERE id = ?`,
		ts(now()), online, dtx, drx, ts(now()), id)
	return err
}

// NodeVersionUpdate carries version fields reported by an agent heartbeat.
// Nil fields were not reported by that agent version and must be left alone.
type NodeVersionUpdate struct {
	AgentVersion *string
	Hy2Version   *string
	XrayVersion  *string
	Runtime      *string
}

// UpdateNodeVersions refreshes only the version fields present in a heartbeat.
// Older agents do not send these fields, so a partial update is essential.
func (s *Store) UpdateNodeVersions(ctx context.Context, id int64, v NodeVersionUpdate) error {
	sets := []string{}
	args := []any{}
	add := func(column string, value *string) {
		if value == nil {
			return
		}
		sets = append(sets, column+" = ?")
		args = append(args, strings.TrimSpace(*value))
	}
	add("agent_version", v.AgentVersion)
	add("hy2_version", v.Hy2Version)
	add("xray_version", v.XrayVersion)
	add("runtime", v.Runtime)
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, ts(now()), id)
	_, err := s.DB.ExecContext(ctx, `UPDATE nodes SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

// SetNodeAppliedRevision records the result of a config push.
func (s *Store) SetNodeAppliedRevision(ctx context.Context, id, revision int64, errMsg string) error {
	status := "online"
	if errMsg != "" {
		status = "error"
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE nodes SET applied_revision = ?, config_error = ?, status = ?, last_seen_at = ?, updated_at = ? WHERE id = ?`,
		revision, errMsg, status, ts(now()), ts(now()), id)
	return err
}

// SetNodeConfigError stores an agent reported configuration error.
func (s *Store) SetNodeConfigError(ctx context.Context, id int64, errMsg string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE nodes SET config_error = ?, status = CASE WHEN ? != '' THEN 'error' ELSE status END, updated_at = ? WHERE id = ?`,
		errMsg, errMsg, ts(now()), id)
	return err
}

// GetNodesByIDs returns active or inactive nodes matching ids.
func (s *Store) GetNodesByIDs(ctx context.Context, ids []int64) ([]model.Node, error) {
	out := []model.Node{}
	for _, id := range uniqueIDs(ids) {
		n, err := s.GetNode(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, nil
}

// NodesForUser implements the CELERITY subscription targeting rules:
//   - direct node assignments win when present;
//   - otherwise nodes in the user's groups plus ungrouped ("public") nodes;
//   - users without groups receive only ungrouped nodes.
func (s *Store) NodesForUser(ctx context.Context, userID int64) ([]model.Node, error) {
	direct, err := s.GetUserNodeIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT g.id FROM user_groups ug JOIN panel_groups g ON g.id = ug.group_id WHERE ug.user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	groupIDs := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		groupIDs = append(groupIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(direct) > 0 {
		nodes, err := s.GetNodesByIDs(ctx, direct)
		if err != nil {
			return nil, err
		}
		out := nodes[:0]
		for _, n := range nodes {
			if n.Active {
				out = append(out, n)
			}
		}
		return out, nil
	}

	placeholders := ""
	args := []any{}
	if len(groupIDs) > 0 {
		for _, id := range groupIDs {
			if placeholders != "" {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, id)
		}
		placeholders = fmt.Sprintf(` OR n.id IN (
			SELECT node_id FROM node_groups WHERE group_id IN (%s))`, placeholders)
	}
	q := nodeSelect + ` n WHERE n.active = 1 AND (
		NOT EXISTS (SELECT 1 FROM node_groups ng WHERE ng.node_id = n.id)` + placeholders + `)
		ORDER BY n.ranking DESC, n.id ASC`
	// The prefix aliasing above is only valid when nodeSelect has no alias; use
	// a subquery form instead to keep column names unambiguous.
	q = `SELECT * FROM (` + strings.Replace(nodeSelect, "FROM nodes", "FROM nodes", 1) + `) n WHERE n.active = 1 AND (
		NOT EXISTS (SELECT 1 FROM node_groups ng WHERE ng.node_id = n.id)` + placeholders + `)
		ORDER BY n.ranking DESC, n.id ASC`
	rows2, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	out := []model.Node{}
	for rows2.Next() {
		n, err := scanNode(rows2)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows2.Err()
}

// ListNodeConfigs returns configuration revisions for a node (newest first).
func (s *Store) ListNodeConfigs(ctx context.Context, nodeID int64, limit int) ([]model.NodeConfig, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, node_id, revision, yaml, hash, created_at, applied_at, last_error
		 FROM node_configs WHERE node_id = ? ORDER BY revision DESC LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.NodeConfig{}
	for rows.Next() {
		var c model.NodeConfig
		var created string
		var applied sql.NullString
		if err := rows.Scan(&c.ID, &c.NodeID, &c.Revision, &c.YAML, &c.Hash, &created, &applied, &c.LastError); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTime(created)
		c.AppliedAt = nullTime(applied)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveNodeConfig stores a new single-core config revision. When the hash
// matches the latest revision, the existing revision is returned with
// created=false.
func (s *Store) SaveNodeConfig(ctx context.Context, nodeID int64, yaml, hash string) (*model.NodeConfig, bool, error) {
	return s.SaveNodeConfigBundle(ctx, nodeID, yaml, nil, hash)
}

// SaveNodeConfigBundle stores a revision containing one config per core.
func (s *Store) SaveNodeConfigBundle(ctx context.Context, nodeID int64, yaml string, configs map[string]string, hash string) (*model.NodeConfig, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	var lastHash string
	var lastRev int64
	err = tx.QueryRowContext(ctx,
		`SELECT hash, revision FROM node_configs WHERE node_id = ? ORDER BY revision DESC LIMIT 1`, nodeID).
		Scan(&lastHash, &lastRev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if lastHash == hash && lastRev > 0 {
		var c model.NodeConfig
		var created string
		var applied, configsJSON sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT id, node_id, revision, yaml, hash, created_at, applied_at, last_error, configs_json FROM node_configs
			 WHERE node_id = ? AND revision = ?`, nodeID, lastRev).
			Scan(&c.ID, &c.NodeID, &c.Revision, &c.YAML, &c.Hash, &created, &applied, &c.LastError, &configsJSON)
		if err != nil {
			return nil, false, err
		}
		c.CreatedAt = parseTime(created)
		c.AppliedAt = nullTime(applied)
		if configsJSON.Valid && configsJSON.String != "" {
			_ = json.Unmarshal([]byte(configsJSON.String), &c.Configs)
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return &c, false, nil
	}
	rev := lastRev + 1
	nowT := now()
	id, err := s.InsertIDTx(ctx, tx,
		`INSERT INTO node_configs (node_id, revision, yaml, hash, configs_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		nodeID, rev, yaml, hash, marshalJSON(configs), ts(nowT))
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET config_revision = ?, config_error = '', updated_at = ? WHERE id = ?`,
		rev, ts(nowT), nodeID); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &model.NodeConfig{ID: id, NodeID: nodeID, Revision: rev, YAML: yaml, Configs: configs, Hash: hash, CreatedAt: nowT}, true, nil
}

// GetNodeConfig returns a specific revision, or the latest when revision <= 0.
func (s *Store) GetNodeConfig(ctx context.Context, nodeID, revision int64) (*model.NodeConfig, error) {
	q := `SELECT id, node_id, revision, yaml, hash, created_at, applied_at, last_error, configs_json FROM node_configs WHERE node_id = ?`
	args := []any{nodeID}
	if revision > 0 {
		q += ` AND revision = ?`
		args = append(args, revision)
	}
	q += ` ORDER BY revision DESC LIMIT 1`
	var c model.NodeConfig
	var created string
	var applied, configsJSON sql.NullString
	err := s.DB.QueryRowContext(ctx, q, args...).
		Scan(&c.ID, &c.NodeID, &c.Revision, &c.YAML, &c.Hash, &created, &applied, &c.LastError, &configsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt = parseTime(created)
	c.AppliedAt = nullTime(applied)
	if configsJSON.Valid && configsJSON.String != "" {
		_ = json.Unmarshal([]byte(configsJSON.String), &c.Configs)
	}
	return &c, nil
}

// MarkConfigApplied records a successful config application.
func (s *Store) MarkConfigApplied(ctx context.Context, nodeID, revision int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE node_configs SET applied_at = ?, last_error = '' WHERE node_id = ? AND revision = ?`,
		ts(now()), nodeID, revision)
	return err
}

// CountNodes returns total and active node counts.
func (s *Store) CountNodes(ctx context.Context) (total, active int, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(active),0) FROM nodes`).Scan(&total, &active)
	return
}

// SumNodeTraffic returns the cumulative traffic counters of all nodes.
func (s *Store) SumNodeTraffic(ctx context.Context) (tx, rx int64, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(traffic_tx),0), COALESCE(SUM(traffic_rx),0) FROM nodes`).Scan(&tx, &rx)
	return
}

// NodeTokenInfo returns the stored token and its display prefix.
func (s *Store) NodeTokenInfo(ctx context.Context, id int64) (token, prefix string, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT token, token_prefix FROM nodes WHERE id = ?`, id).Scan(&token, &prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return
}
