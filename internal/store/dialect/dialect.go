// Package dialect contains the SQLite-specific SQL helpers used by the store.
//
// The panel stores all data in SQLite. Keeping the helpers in a small package
// lets the schema generator, migrations and DML builders share one quoting and
// DDL implementation without scattering SQLite details across the store.
package dialect

import (
	"context"
	"fmt"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/store/schema"
)

// SQLite is the only supported database driver.
const SQLite = "sqlite"

// Dialect describes the SQLite backend.
type Dialect interface {
	// Name is the canonical backend name.
	Name() string
	// DriverName is the database/sql driver name passed to sql.Open.
	DriverName() string
	// Quote quotes an identifier for safe interpolation by DDL builders.
	Quote(identifier string) string

	// DDL generation.
	ColumnDDL(c schema.Column) string
	TableSuffix() string
	CreateTable(t schema.Table) string
	CreateIndex(table string, ix schema.Index) string
	AddColumn(table string, c schema.Column) string

	// DML fragments.
	InsertIgnore(table string, cols, conflict []string) string
	Upsert(table string, cols, conflict, updates []string) string

	// Schema introspection.
	HasTable(ctx context.Context, q Querier, table string) (bool, error)
	HasColumn(ctx context.Context, q Querier, table, column string) (bool, error)
	HasIndex(ctx context.Context, q Querier, table, index string) (bool, error)
}

// For resolves a backend name. An empty name selects SQLite.
func For(name string) (Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", SQLite, "sqlite3":
		return SQLiteDialect{}, nil
	}
	return nil, fmt.Errorf("unsupported database driver %q (only sqlite is supported)", name)
}
