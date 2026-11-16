package dialect

import "testing"

func TestFor(t *testing.T) {
	cases := map[string]string{
		"":        SQLite,
		"sqlite":  SQLite,
		"sqlite3": SQLite,
	}
	for in, want := range cases {
		d, err := For(in)
		if err != nil {
			t.Fatalf("For(%q): %v", in, err)
		}
		if d.Name() != want {
			t.Fatalf("For(%q) = %q, want %q", in, d.Name(), want)
		}
	}
	for _, unsupported := range []string{"postgres", "mysql", "oracle"} {
		if _, err := For(unsupported); err == nil {
			t.Fatalf("For(%q) should fail", unsupported)
		}
	}
}

func TestQuote(t *testing.T) {
	if got := (SQLiteDialect{}).Quote(`we"ird`); got != `"we""ird"` {
		t.Fatalf("sqlite quote = %s", got)
	}
}

func TestSQLiteHelpersDoNotDependOnDriverName(t *testing.T) {
	if (SQLiteDialect{}).DriverName() != "sqlite" {
		t.Fatal("unexpected driver name")
	}
	if (SQLiteDialect{}).Name() != SQLite {
		t.Fatal("unexpected dialect name")
	}
}
