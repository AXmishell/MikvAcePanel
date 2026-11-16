package store

import (
	"context"
	"fmt"

	"github.com/axmishell/mikvacepanel/internal/store/schema"
)

// Migrate creates or upgrades the schema. It is idempotent, inspects
// sqlite_master/PRAGMA instead of blindly replaying DDL, and records data
// migrations in schema_migrations.
func (s *Store) Migrate(ctx context.Context) error {
	d := s.Dialect()
	if err := s.migrateLegacyNames(ctx); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, d.CreateTable(schema.MigrationTable)); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	for _, table := range schema.Tables {
		exists, err := d.HasTable(ctx, s.DB, table.Name)
		if err != nil {
			return fmt.Errorf("inspect table %s: %w", table.Name, err)
		}
		if !exists {
			if _, err := s.DB.ExecContext(ctx, d.CreateTable(table)); err != nil {
				return fmt.Errorf("create table %s: %w", table.Name, err)
			}
		} else {
			for _, col := range table.Columns {
				ok, err := d.HasColumn(ctx, s.DB, table.Name, col.Name)
				if err != nil {
					return fmt.Errorf("inspect %s.%s: %w", table.Name, col.Name, err)
				}
				if ok {
					continue
				}
				if _, err := s.DB.ExecContext(ctx, d.AddColumn(table.Name, col)); err != nil {
					return fmt.Errorf("add %s.%s: %w", table.Name, col.Name, err)
				}
			}
		}
		for _, ix := range table.Indexes {
			ok, err := d.HasIndex(ctx, s.DB, table.Name, ix.Name)
			if err != nil {
				return fmt.Errorf("inspect index %s: %w", ix.Name, err)
			}
			if ok {
				continue
			}
			if _, err := s.DB.ExecContext(ctx, d.CreateIndex(table.Name, ix)); err != nil {
				return fmt.Errorf("create index %s: %w", ix.Name, err)
			}
		}
	}
	return s.runDataMigrations(ctx)
}

// migrateLegacyNames upgrades databases created by older panel releases.
func (s *Store) migrateLegacyNames(ctx context.Context) error {
	d := s.Dialect()
	settings, err := d.HasTable(ctx, s.DB, "settings")
	if err != nil {
		return err
	}
	if settings {
		hasOld, err := d.HasColumn(ctx, s.DB, "settings", "key")
		if err != nil {
			return err
		}
		hasNew, err := d.HasColumn(ctx, s.DB, "settings", "setting_key")
		if err != nil {
			return err
		}
		if hasOld && !hasNew {
			stmt := "ALTER TABLE " + d.Quote("settings") + " RENAME COLUMN " + d.Quote("key") + " TO " + d.Quote("setting_key")
			if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("rename settings.key: %w", err)
			}
		}
	}
	oldGroups, err := d.HasTable(ctx, s.DB, "groups")
	if err != nil {
		return err
	}
	newGroups, err := d.HasTable(ctx, s.DB, "panel_groups")
	if err != nil {
		return err
	}
	if oldGroups && !newGroups {
		stmt := "ALTER TABLE " + d.Quote("groups") + " RENAME TO " + d.Quote("panel_groups")
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("rename groups table: %w", err)
		}
	}
	return nil
}

func (s *Store) runDataMigrations(ctx context.Context) error {
	steps := []struct {
		version int64
		fn      func(context.Context) error
	}{
		{1, s.backfillXrayUUID},
		{2, s.expandLegacyAPIKeyScopes},
	}
	for _, step := range steps {
		applied, err := s.migrationApplied(ctx, step.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := step.fn(ctx); err != nil {
			return fmt.Errorf("apply schema migration %d: %w", step.version, err)
		}
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			step.version, ts(now())); err != nil {
			return fmt.Errorf("record schema version %d: %w", step.version, err)
		}
	}
	return nil
}

// expandLegacyAPIKeyScopes rewrites the original coarse write scopes into the
// fine-grained permissions they used to imply. Read scopes keep working via
// legacyScopeAliases, but expanding them keeps the stored data predictable.
func (s *Store) expandLegacyAPIKeyScopes(ctx context.Context) error {
	expansions := map[string][]string{
		"users:write": {"users:write", "users:delete"},
		"nodes:write": {"nodes:write", "nodes:operate", "nodes:secrets"},
		"stats:read":  {"dashboard:read", "logs:read", "groups:read"},
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, scopes FROM api_keys`)
	if err != nil {
		return err
	}
	type update struct {
		id     int64
		scopes string
	}
	updates := []update{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		scopes := splitScopes(raw)
		out := make([]string, 0, len(scopes)+3)
		for _, sc := range scopes {
			if expanded, ok := expansions[sc]; ok {
				out = append(out, expanded...)
			} else {
				out = append(out, sc)
			}
		}
		next := JoinScopes(out)
		if next != JoinScopes(scopes) {
			updates = append(updates, update{id: id, scopes: next})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, u := range updates {
		if _, err := s.DB.ExecContext(ctx, `UPDATE api_keys SET scopes = ? WHERE id = ?`, u.scopes, u.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrationApplied(ctx context.Context, version int64) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n); err != nil {
		return false, fmt.Errorf("read schema version: %w", err)
	}
	return n > 0, nil
}

// backfillXrayUUID gives legacy users a stable UUID used by Xray configs and
// subscriptions. It runs once (migration version 1).
func (s *Store) backfillXrayUUID(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM users WHERE xray_uuid = '' OR xray_uuid IS NULL`)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := s.DB.ExecContext(ctx, `UPDATE users SET xray_uuid = ? WHERE id = ?`, RandomUUID(), id); err != nil {
			return err
		}
	}
	return nil
}
