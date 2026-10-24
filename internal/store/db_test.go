package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store/dialect"
)

func TestOpenConfigSQLiteDefault(t *testing.T) {
	st, err := OpenConfig(Config{Path: filepath.Join(t.TempDir(), "panel.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.Dialect().Name() != dialect.SQLite {
		t.Fatalf("dialect = %s", st.Dialect().Name())
	}
	if got := st.DB.Rebind("SELECT ?"); got != "SELECT ?" {
		t.Fatalf("sqlite rebind = %q", got)
	}
	if err := st.DB.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenConfigRejectsRemoteBackendsForNow(t *testing.T) {
	for _, driver := range []string{dialect.Postgres, dialect.MySQL} {
		if _, err := OpenConfig(Config{Driver: driver, DSN: "unused://"}); err == nil {
			t.Fatalf("%s should not open yet", driver)
		}
	}
}

func TestInsertID(t *testing.T) {
	st, ctx := testStore(t)
	if err := st.EnsureDefaultSettings(ctx); err != nil {
		t.Fatal(err)
	}
	g, err := st.CreateGroup(ctx, &model.Group{Name: "id-test"})
	if err != nil {
		t.Fatal(err)
	}
	if g.ID == 0 {
		t.Fatal("InsertID returned zero")
	}
	u, err := st.CreateUser(ctx, &model.User{UserID: "insert-id", Password: "pw", Enabled: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 {
		t.Fatal("CreateUser id = 0")
	}
	if _, err := st.InsertID(ctx, `INSERT INTO groups (name, created_at, updated_at) VALUES (?, ?, ?)`,
		"id-test-2", DBTime(now()), DBTime(now())); err != nil {
		t.Fatal(err)
	}
}
