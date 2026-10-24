package dialect

import (
	"errors"
	"testing"
)

type stateErr struct {
	state string
	msg   string
}

func (e *stateErr) Error() string    { return e.msg }
func (e *stateErr) SQLState() string { return e.state }

func TestFor(t *testing.T) {
	cases := map[string]string{
		"":           SQLite,
		"sqlite":     SQLite,
		"sqlite3":    SQLite,
		"postgres":   Postgres,
		"postgresql": Postgres,
		"pg":         Postgres,
		"mysql":      MySQL,
		"mariadb":    MySQL,
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
	if _, err := For("oracle"); err == nil {
		t.Fatal("For(oracle) should fail")
	}
}

func TestForDSN(t *testing.T) {
	cases := map[string]string{
		"postgres://user:pass@host/db": Postgres,
		"postgresql://host/db":         Postgres,
		"mysql://user@host/db":         MySQL,
		"sqlite:///data/panel.db":      SQLite,
		"file:panel.db":                SQLite,
		"/data/panel.db":               "",
	}
	for in, want := range cases {
		if got := ForDSN(in); got != want {
			t.Fatalf("ForDSN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPostgresRebind(t *testing.T) {
	d := PostgresDialect{}
	cases := []struct{ in, want string }{
		{"SELECT * FROM users WHERE id = ?", "SELECT * FROM users WHERE id = $1"},
		{"SELECT * FROM users WHERE a = ? AND b = ?", "SELECT * FROM users WHERE a = $1 AND b = $2"},
		{"SELECT '?', ?", "SELECT '?', $1"},
		{"SELECT 'it''s ?', ?", "SELECT 'it''s ?', $1"},
		{`SELECT "?" AS q, ?`, `SELECT "?" AS q, $1`},
		{"SELECT $$?$$, ?", "SELECT $$?$$, $1"},
		{"SELECT $tag$?$tag$, ?", "SELECT $tag$?$tag$, $1"},
		{"SELECT 1 -- is ?\nFROM t WHERE x = ?", "SELECT 1 -- is ?\nFROM t WHERE x = $1"},
		{"SELECT 1 /* ? */ FROM t WHERE x = ?", "SELECT 1 /* ? */ FROM t WHERE x = $1"},
		{"SELECT 1 /* a /* ? */ b */ FROM t WHERE x = ?", "SELECT 1 /* a /* ? */ b */ FROM t WHERE x = $1"},
		{"SELECT `?`, ?", "SELECT `?`, $1"},
	}
	for _, c := range cases {
		if got := d.Rebind(c.in); got != c.want {
			t.Fatalf("Rebind(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestRebindIdentity(t *testing.T) {
	q := "SELECT * FROM users WHERE id = ?"
	for _, d := range []Dialect{SQLiteDialect{}, MySQLDialect{}} {
		if got := d.Rebind(q); got != q {
			t.Fatalf("%s rebind changed %q to %q", d.Name(), q, got)
		}
	}
}

func TestQuote(t *testing.T) {
	if got := (PostgresDialect{}).Quote(`we"ird`); got != `"we""ird"` {
		t.Fatalf("postgres quote = %s", got)
	}
	if got := (MySQLDialect{}).Quote("we`ird"); got != "`we``ird`" {
		t.Fatalf("mysql quote = %s", got)
	}
}

func TestErrorClassification(t *testing.T) {
	d := PostgresDialect{}
	if !d.IsUniqueViolation(&stateErr{state: "23505", msg: "duplicate"}) {
		t.Fatal("23505 should be a unique violation")
	}
	if !d.IsRetryable(&stateErr{state: "40001", msg: "serialization failure"}) {
		t.Fatal("40001 should be retryable")
	}
	if d.IsUniqueViolation(errors.New("syntax error")) {
		t.Fatal("syntax error is not a unique violation")
	}

	if !(SQLiteDialect{}).IsUniqueViolation(errors.New("constraint failed: UNIQUE constraint failed: users.user_id")) {
		t.Fatal("sqlite unique constraint not detected")
	}
	if !(MySQLDialect{}).IsUniqueViolation(errors.New("Error 1062: Duplicate entry 'x' for key 'users.user_id'")) {
		t.Fatal("mysql duplicate entry not detected")
	}
	if !(MySQLDialect{}).IsRetryable(errors.New("Error 1213: Deadlock found")) {
		t.Fatal("mysql deadlock should be retryable")
	}
	if (MySQLDialect{}).IsUniqueViolation(nil) {
		t.Fatal("nil must not be a conflict")
	}
}
