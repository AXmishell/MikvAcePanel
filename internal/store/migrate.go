package store

import (
	"context"
	"fmt"

	"github.com/axmishell/mikvacepanel/internal/store/dialect"
	"github.com/axmishell/mikvacepanel/internal/store/schema"
)

// Migrate creates or upgrades the schema. It is idempotent, uses dialect
// introspection instead of engine-specific IF NOT EXISTS clauses, and records
// data migrations in schema_migrations.
func (s *Store) Migrate(ctx context.Context) error {
	d := s.Dialect()
	release, err := d.AcquireMigrationLock(ctx, s.DB.DB)
	if err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	defer release()

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

// migrateLegacyNames upgrades SQLite databases created before the
// MySQL-compatible names were introduced.
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
		var stmt string
		if d.Name() == dialect.MySQL {
			stmt = "RENAME TABLE " + d.Quote("groups") + " TO " + d.Quote("panel_groups")
		} else {
			stmt = "ALTER TABLE " + d.Quote("groups") + " RENAME TO " + d.Quote("panel_groups")
		}
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("rename groups table: %w", err)
		}
	}
	return nil
}

func (s *Store) runDataMigrations(ctx context.Context) error {
	const initialSchemaVersion = 1
	applied, err := s.migrationApplied(ctx, initialSchemaVersion)
	if err != nil {
		return err
	}
	if applied {
		return nil
	}
	if err := s.backfillXrayUUID(ctx); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		initialSchemaVersion, ts(now()))
	if err != nil {
		return fmt.Errorf("record schema version: %w", err)
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
