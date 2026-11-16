package dialect

import (
	"strings"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/store/schema"
)

func TestDDLForSchemas(t *testing.T) {
	d := SQLiteDialect{}
	for _, table := range schema.Tables {
		ddl := d.CreateTable(table)
		if !strings.Contains(ddl, "CREATE TABLE IF NOT EXISTS") {
			t.Fatalf("%s: %s", table.Name, ddl)
		}
		if !strings.Contains(ddl, table.Name) {
			t.Fatalf("%s: table name missing", table.Name)
		}
	}
}

func TestColumnDDL(t *testing.T) {
	id := schema.Column{Name: "id", Kind: schema.Int64, PK: true, Auto: true}
	text := schema.Column{Name: "name", Kind: schema.String, Size: 32, NotNull: true, Default: "x"}

	sq := SQLiteDialect{}
	if got := sq.ColumnDDL(id); !strings.Contains(got, "INTEGER PRIMARY KEY AUTOINCREMENT") {
		t.Fatalf("sqlite id: %s", got)
	}
	if got := sq.ColumnDDL(text); got != `"name" VARCHAR(32) NOT NULL DEFAULT 'x'` {
		t.Fatalf("sqlite text: %s", got)
	}
}

func TestDMLFragments(t *testing.T) {
	cols := []string{"setting_key", "value"}
	if got := (SQLiteDialect{}).InsertIgnore("settings", cols, []string{"setting_key"}); !strings.Contains(got, "INSERT OR IGNORE") {
		t.Fatalf("sqlite ignore: %s", got)
	}
	if got := (SQLiteDialect{}).Upsert("settings", cols, []string{"setting_key"}, []string{"value"}); !strings.Contains(got, "excluded") {
		t.Fatalf("sqlite upsert: %s", got)
	}
	if got := (SQLiteDialect{}).CreateIndex("nodes", schema.Index{Name: "i", Columns: []string{"name"}}); !strings.Contains(got, "IF NOT EXISTS") {
		t.Fatalf("sqlite index: %s", got)
	}
}
