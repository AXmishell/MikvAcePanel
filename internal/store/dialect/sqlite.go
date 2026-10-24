package dialect

import (
	"context"
	"database/sql"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/store/schema"
)

// SQLiteDialect is the default backend. Its SQL already uses "?" placeholders
// and double-quoted identifiers, so most operations are pass-through.
type SQLiteDialect struct{}

func (SQLiteDialect) Name() string       { return SQLite }
func (SQLiteDialect) DriverName() string { return "sqlite" }
func (SQLiteDialect) Rebind(q string) string {
	return q
}
func (SQLiteDialect) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
func (SQLiteDialect) IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
func (SQLiteDialect) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return containsAny(msg, "database is locked", "database table is locked", "sqlite_busy", "sqlite_locked")
}

func (d SQLiteDialect) ColumnDDL(c schema.Column) string {
	name := quoteIdent(d, c.Name)
	if c.Auto && c.PK {
		return name + " INTEGER PRIMARY KEY AUTOINCREMENT"
	}
	parts := []string{name, ColumnType(d, c)}
	if c.NotNull {
		parts = append(parts, "NOT NULL")
	}
	if c.Default != nil {
		parts = append(parts, "DEFAULT "+sqlLiteral(c.Default))
	}
	if c.Unique {
		parts = append(parts, "UNIQUE")
	}
	if c.PK {
		parts = append(parts, "PRIMARY KEY")
	}
	return strings.Join(parts, " ")
}

func (SQLiteDialect) TableSuffix() string { return "" }
func (d SQLiteDialect) CreateTable(t schema.Table) string {
	return createTable(d, t)
}
func (d SQLiteDialect) CreateIndex(table string, ix schema.Index) string {
	return createIndex(d, table, ix)
}
func (d SQLiteDialect) AddColumn(table string, c schema.Column) string {
	return addColumn(d, table, c)
}
func (d SQLiteDialect) InsertIgnore(table string, cols, conflict []string) string {
	return insertIgnoreSQL(d, table, cols, conflict)
}
func (d SQLiteDialect) Upsert(table string, cols, conflict, updates []string) string {
	return upsertSQL(d, table, cols, conflict, updates)
}
func (d SQLiteDialect) HasTable(ctx context.Context, q Querier, table string) (bool, error) {
	return hasTable(ctx, q, d, table)
}
func (d SQLiteDialect) HasColumn(ctx context.Context, q Querier, table, column string) (bool, error) {
	return hasColumn(ctx, q, d, table, column)
}
func (d SQLiteDialect) HasIndex(ctx context.Context, q Querier, table, index string) (bool, error) {
	return hasIndex(ctx, q, d, table, index)
}
func (d SQLiteDialect) AcquireMigrationLock(ctx context.Context, db *sql.DB) (func(), error) {
	return acquireLock(ctx, db, d)
}
