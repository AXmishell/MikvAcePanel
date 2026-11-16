package dialect

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/store/schema"
)

// Querier is implemented by *store.DB and *store.Tx.
type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// columnType returns the SQLite storage type for a column.
func columnType(c schema.Column) string {
	switch c.Kind {
	case schema.Int, schema.Int64, schema.Bool:
		return "INTEGER"
	case schema.Float:
		return "REAL"
	case schema.String:
		if c.Size > 0 {
			return fmt.Sprintf("VARCHAR(%d)", c.Size)
		}
	}
	return "TEXT"
}

func sqlLiteral(v any) string {
	switch x := v.(type) {
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	case bool:
		if x {
			return "1"
		}
		return "0"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

func createTable(t schema.Table) string {
	parts := make([]string, 0, len(t.Columns)+len(t.ForeignKeys)+1)
	for _, c := range t.Columns {
		parts = append(parts, (SQLiteDialect{}).ColumnDDL(c))
	}
	if len(t.PrimaryKey) > 0 {
		parts = append(parts, "PRIMARY KEY ("+quotedList(t.PrimaryKey)+")")
	}
	for _, fk := range t.ForeignKeys {
		part := "FOREIGN KEY (" + quotedList(fk.Columns) + ") REFERENCES " +
			quoteIdent(fk.RefTable) + " (" + quotedList(fk.RefColumns) + ")"
		if fk.OnDelete != "" {
			part += " ON DELETE " + fk.OnDelete
		}
		parts = append(parts, part)
	}
	return "CREATE TABLE IF NOT EXISTS " + quoteIdent(t.Name) + " (\n  " +
		strings.Join(parts, ",\n  ") + "\n)"
}

func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func insertIgnoreSQL(table string, cols []string) string {
	return "INSERT OR IGNORE INTO " + quoteIdent(table) + " (" + quotedList(cols) + ") VALUES (" + placeholders(len(cols)) + ")"
}

func upsertSQL(table string, cols, conflict, updates []string) string {
	head := "INSERT INTO " + quoteIdent(table) + " (" + quotedList(cols) + ") VALUES (" + placeholders(len(cols)) + ")"
	sets := make([]string, 0, len(updates))
	for _, c := range updates {
		sets = append(sets, quoteIdent(c)+"=excluded."+quoteIdent(c))
	}
	return head + " ON CONFLICT (" + quotedList(conflict) + ") DO UPDATE SET " + strings.Join(sets, ", ")
}

func quotedList(cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = quoteIdent(c)
	}
	return strings.Join(out, ", ")
}

func hasTable(ctx context.Context, q Querier, table string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
	return n > 0, err
}

func hasColumn(ctx context.Context, q Querier, table, column string) (bool, error) {
	rows, err := q.QueryContext(ctx, `PRAGMA table_info(`+quoteIdent(table)+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func hasIndex(ctx context.Context, q Querier, table, index string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name=? AND name=?`, table, index).Scan(&n)
	return n > 0, err
}

func createIndex(table string, ix schema.Index) string {
	kind := "INDEX"
	if ix.Unique {
		kind = "UNIQUE INDEX"
	}
	return "CREATE " + kind + " IF NOT EXISTS " + quoteIdent(ix.Name) + " ON " + quoteIdent(table) + " (" + quotedList(ix.Columns) + ")"
}

func addColumn(table string, c schema.Column) string {
	return "ALTER TABLE " + quoteIdent(table) + " ADD COLUMN " + (SQLiteDialect{}).ColumnDDL(c)
}
