package dialect

import (
	"context"
	"database/sql"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/store/schema"
)

// MySQLDialect targets MySQL 8.0. Placeholders stay "?"; identifiers use
// backticks because several MySQL keywords collide with the panel schema.
type MySQLDialect struct{}

func (MySQLDialect) Name() string       { return MySQL }
func (MySQLDialect) DriverName() string { return "mysql" }
func (MySQLDialect) Rebind(q string) string {
	return q
}
func (MySQLDialect) Quote(ident string) string {
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}
func (MySQLDialect) IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "error 1062") || strings.Contains(msg, "duplicate entry")
}
func (MySQLDialect) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if sqlStateOf(err) == "40001" {
		return true
	}
	msg := strings.ToLower(err.Error())
	return containsAny(msg, "error 1213", "error 1205", "error 1040", "error 2006", "error 2013",
		"deadlock found", "lock wait timeout", "connection refused", "connection reset")
}

func (d MySQLDialect) ColumnDDL(c schema.Column) string {
	name := quoteIdent(d, c.Name)
	if c.Auto && c.PK {
		return name + " BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY"
	}
	typ := ColumnType(d, c)
	parts := []string{name, typ}
	if c.NotNull {
		parts = append(parts, "NOT NULL")
	}
	if c.Default != nil {
		lit := sqlLiteral(c.Default)
		if strings.HasPrefix(typ, "TEXT") || strings.HasPrefix(typ, "MEDIUMTEXT") {
			lit = "(" + lit + ")"
		}
		parts = append(parts, "DEFAULT "+lit)
	}
	if c.Unique {
		parts = append(parts, "UNIQUE")
	}
	if c.PK {
		parts = append(parts, "PRIMARY KEY")
	}
	return strings.Join(parts, " ")
}

func (MySQLDialect) TableSuffix() string {
	return " ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin"
}
func (d MySQLDialect) CreateTable(t schema.Table) string {
	return createTable(d, t)
}
func (d MySQLDialect) CreateIndex(table string, ix schema.Index) string {
	return createIndex(d, table, ix)
}
func (d MySQLDialect) AddColumn(table string, c schema.Column) string {
	return addColumn(d, table, c)
}
func (d MySQLDialect) InsertIgnore(table string, cols, conflict []string) string {
	return insertIgnoreSQL(d, table, cols, conflict)
}
func (d MySQLDialect) Upsert(table string, cols, conflict, updates []string) string {
	return upsertSQL(d, table, cols, conflict, updates)
}
func (d MySQLDialect) HasTable(ctx context.Context, q Querier, table string) (bool, error) {
	return hasTable(ctx, q, d, table)
}
func (d MySQLDialect) HasColumn(ctx context.Context, q Querier, table, column string) (bool, error) {
	return hasColumn(ctx, q, d, table, column)
}
func (d MySQLDialect) HasIndex(ctx context.Context, q Querier, table, index string) (bool, error) {
	return hasIndex(ctx, q, d, table, index)
}
func (d MySQLDialect) AcquireMigrationLock(ctx context.Context, db *sql.DB) (func(), error) {
	return acquireLock(ctx, db, d)
}
