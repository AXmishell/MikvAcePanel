package dialect

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store/schema"
)

// Querier is implemented by *store.DB and *store.Tx.
type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// ColumnType returns the base SQL type for a column.
func ColumnType(d Dialect, c schema.Column) string {
	switch c.Kind {
	case schema.Int:
		return map[string]string{SQLite: "INTEGER", Postgres: "INTEGER", MySQL: "INT"}[d.Name()]
	case schema.Int64:
		return map[string]string{SQLite: "INTEGER", Postgres: "BIGINT", MySQL: "BIGINT"}[d.Name()]
	case schema.Bool:
		return map[string]string{SQLite: "INTEGER", Postgres: "SMALLINT", MySQL: "TINYINT"}[d.Name()]
	case schema.Float:
		return map[string]string{SQLite: "REAL", Postgres: "DOUBLE PRECISION", MySQL: "DOUBLE"}[d.Name()]
	case schema.String:
		if c.Size > 0 {
			return fmt.Sprintf("VARCHAR(%d)", c.Size)
		}
		if d.Name() == MySQL {
			return "MEDIUMTEXT"
		}
		return "TEXT"
	case schema.Text, schema.JSON:
		if d.Name() == MySQL {
			return "MEDIUMTEXT"
		}
		return "TEXT"
	case schema.Time:
		if d.Name() == MySQL {
			return "VARCHAR(64)"
		}
		return "TEXT"
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

func createTable(d Dialect, t schema.Table) string {
	parts := make([]string, 0, len(t.Columns)+len(t.ForeignKeys)+1)
	for _, c := range t.Columns {
		parts = append(parts, d.ColumnDDL(c))
	}
	if len(t.PrimaryKey) > 0 {
		parts = append(parts, "PRIMARY KEY ("+quotedList(d, t.PrimaryKey)+")")
	}
	for _, fk := range t.ForeignKeys {
		part := "FOREIGN KEY (" + quotedList(d, fk.Columns) + ") REFERENCES " +
			quoteIdent(d, fk.RefTable) + " (" + quotedList(d, fk.RefColumns) + ")"
		if fk.OnDelete != "" {
			part += " ON DELETE " + fk.OnDelete
		}
		parts = append(parts, part)
	}
	return "CREATE TABLE IF NOT EXISTS " + quoteIdent(d, t.Name) + " (\n  " +
		strings.Join(parts, ",\n  ") + "\n)" + d.TableSuffix()
}

func quoteIdent(d Dialect, ident string) string {
	switch d.Name() {
	case MySQL:
		return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
	default:
		return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
	}
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func insertIgnoreSQL(d Dialect, table string, cols, conflict []string) string {
	head := "INSERT INTO " + quoteIdent(d, table) + " (" + quotedList(d, cols) + ") VALUES (" + placeholders(len(cols)) + ")"
	switch d.Name() {
	case SQLite:
		return "INSERT OR IGNORE INTO " + quoteIdent(d, table) + " (" + quotedList(d, cols) + ") VALUES (" + placeholders(len(cols)) + ")"
	case Postgres:
		if len(conflict) == 0 {
			return head + " ON CONFLICT DO NOTHING"
		}
		return head + " ON CONFLICT (" + quotedList(d, conflict) + ") DO NOTHING"
	default: // MySQL
		noop := cols[0]
		if len(conflict) > 0 {
			noop = conflict[0]
		}
		return head + " ON DUPLICATE KEY UPDATE " + quoteIdent(d, noop) + "=" + quoteIdent(d, noop)
	}
}

func upsertSQL(d Dialect, table string, cols, conflict, updates []string) string {
	head := "INSERT INTO " + quoteIdent(d, table) + " (" + quotedList(d, cols) + ") VALUES (" + placeholders(len(cols)) + ")"
	switch d.Name() {
	case SQLite, Postgres:
		sets := make([]string, 0, len(updates))
		for _, c := range updates {
			sets = append(sets, quoteIdent(d, c)+"=excluded."+quoteIdent(d, c))
		}
		return head + " ON CONFLICT (" + quotedList(d, conflict) + ") DO UPDATE SET " + strings.Join(sets, ", ")
	default: // MySQL
		sets := make([]string, 0, len(updates))
		for _, c := range updates {
			sets = append(sets, quoteIdent(d, c)+"=VALUES("+quoteIdent(d, c)+")")
		}
		return head + " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	}
}

func quotedList(d Dialect, cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = quoteIdent(d, c)
	}
	return strings.Join(out, ", ")
}

func hasTable(ctx context.Context, q Querier, d Dialect, table string) (bool, error) {
	var n int
	var err error
	switch d.Name() {
	case SQLite:
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
	case Postgres:
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=?`, table).Scan(&n)
	default:
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?`, table).Scan(&n)
	}
	return n > 0, err
}

func hasColumn(ctx context.Context, q Querier, d Dialect, table, column string) (bool, error) {
	var n int
	var err error
	switch d.Name() {
	case SQLite:
		rows, e := q.QueryContext(ctx, `PRAGMA table_info(`+quoteIdent(d, table)+`)`)
		if e != nil {
			return false, e
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
	case Postgres:
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? AND column_name=?`, table, column).Scan(&n)
	default:
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, column).Scan(&n)
	}
	return n > 0, err
}

func hasIndex(ctx context.Context, q Querier, d Dialect, table, index string) (bool, error) {
	var n int
	var err error
	switch d.Name() {
	case SQLite:
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name=? AND name=?`, table, index).Scan(&n)
	case Postgres:
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE schemaname=current_schema() AND tablename=? AND indexname=?`, table, index).Scan(&n)
	default:
		err = q.QueryRowContext(ctx, `SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?`, table, index).Scan(&n)
	}
	return n > 0, err
}

func createIndex(d Dialect, table string, ix schema.Index) string {
	kind := "INDEX"
	if ix.Unique {
		kind = "UNIQUE INDEX"
	}
	prefix := "CREATE " + kind + " "
	if d.Name() != MySQL {
		prefix += "IF NOT EXISTS "
	}
	return prefix + quoteIdent(d, ix.Name) + " ON " + quoteIdent(d, table) + " (" + quotedList(d, ix.Columns) + ")"
}

func addColumn(d Dialect, table string, c schema.Column) string {
	return "ALTER TABLE " + quoteIdent(d, table) + " ADD COLUMN " + d.ColumnDDL(c)
}

func acquireLock(ctx context.Context, db *sql.DB, d Dialect) (func(), error) {
	switch d.Name() {
	case Postgres:
		conn, err := db.Conn(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext('mikvace-migrate'))`); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = conn.ExecContext(releaseCtx, `SELECT pg_advisory_unlock(hashtext('mikvace-migrate'))`)
			_ = conn.Close()
		}, nil
	case MySQL:
		conn, err := db.Conn(ctx)
		if err != nil {
			return nil, err
		}
		var got sql.NullInt64
		if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK('mikvace-migrate', 30)`).Scan(&got); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if !got.Valid || got.Int64 != 1 {
			_ = conn.Close()
			return nil, fmt.Errorf("could not acquire migration lock")
		}
		return func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = conn.ExecContext(releaseCtx, `SELECT RELEASE_LOCK('mikvace-migrate')`)
			_ = conn.Close()
		}, nil
	default:
		return func() {}, nil
	}
}
