package dialect

import (
	"context"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/store/schema"
)

// SQLiteDialect implements the panel's only supported backend. Its SQL already
// uses "?" placeholders, so statements can be passed through unchanged.
type SQLiteDialect struct{}

func (SQLiteDialect) Name() string       { return SQLite }
func (SQLiteDialect) DriverName() string { return "sqlite" }
func (SQLiteDialect) Quote(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (d SQLiteDialect) ColumnDDL(c schema.Column) string {
	name := quoteIdent(c.Name)
	if c.Auto && c.PK {
		return name + " INTEGER PRIMARY KEY AUTOINCREMENT"
	}
	parts := []string{name, columnType(c)}
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
	return createTable(t)
}
func (d SQLiteDialect) CreateIndex(table string, ix schema.Index) string {
	return createIndex(table, ix)
}
func (d SQLiteDialect) AddColumn(table string, c schema.Column) string {
	return addColumn(table, c)
}
func (d SQLiteDialect) InsertIgnore(table string, cols, _ []string) string {
	return insertIgnoreSQL(table, cols)
}
func (d SQLiteDialect) Upsert(table string, cols, conflict, updates []string) string {
	return upsertSQL(table, cols, conflict, updates)
}
func (d SQLiteDialect) HasTable(ctx context.Context, q Querier, table string) (bool, error) {
	return hasTable(ctx, q, table)
}
func (d SQLiteDialect) HasColumn(ctx context.Context, q Querier, table, column string) (bool, error) {
	return hasColumn(ctx, q, table, column)
}
func (d SQLiteDialect) HasIndex(ctx context.Context, q Querier, table, index string) (bool, error) {
	return hasIndex(ctx, q, table, index)
}
