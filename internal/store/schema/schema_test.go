package schema

import "testing"

func TestTablesAreConsistent(t *testing.T) {
	byName := map[string]Table{}
	for _, table := range Tables {
		if table.Name == "" {
			t.Fatal("table with empty name")
		}
		if _, ok := byName[table.Name]; ok {
			t.Fatalf("duplicate table %q", table.Name)
		}
		byName[table.Name] = table
		cols := map[string]bool{}
		for _, c := range table.Columns {
			if c.Name == "" {
				t.Fatalf("%s: empty column", table.Name)
			}
			if cols[c.Name] {
				t.Fatalf("%s: duplicate column %q", table.Name, c.Name)
			}
			cols[c.Name] = true
		}
		for _, pk := range table.PrimaryKey {
			if !cols[pk] {
				t.Fatalf("%s: primary key column %q missing", table.Name, pk)
			}
		}
		for _, ix := range table.Indexes {
			if ix.Name == "" || len(ix.Columns) == 0 {
				t.Fatalf("%s: invalid index %+v", table.Name, ix)
			}
			for _, c := range ix.Columns {
				if !cols[c] {
					t.Fatalf("%s: index %s references missing column %q", table.Name, ix.Name, c)
				}
			}
		}
	}
	for _, table := range Tables {
		for _, fk := range table.ForeignKeys {
			ref, ok := byName[fk.RefTable]
			if !ok {
				t.Fatalf("%s: foreign key references missing table %q", table.Name, fk.RefTable)
			}
			refCols := map[string]bool{}
			for _, c := range ref.Columns {
				refCols[c.Name] = true
			}
			for _, c := range fk.RefColumns {
				if !refCols[c] {
					t.Fatalf("%s: foreign key references missing column %s.%s", table.Name, fk.RefTable, c)
				}
			}
			if len(fk.Columns) != len(fk.RefColumns) {
				t.Fatalf("%s: foreign key column count mismatch", table.Name)
			}
		}
	}
	if _, ok := byName["panel_groups"]; !ok {
		t.Fatal("panel_groups missing")
	}
	if _, ok := byName["settings"]; !ok {
		t.Fatal("settings missing")
	}
}
