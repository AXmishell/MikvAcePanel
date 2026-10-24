package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

func newSetupEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := Config{
		Addr: ":0", DataDir: dir, DBDriver: "sqlite", DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin",
		AdminPassword: "generated-placeholder", AdminPasswordGenerated: true,
		ServeWeb: true, SessionTTL: time.Hour,
	}
	app, err := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{srv: ts, store: st, app: app}
}

func TestInstallFlow(t *testing.T) {
	env := newSetupEnv(t)

	status := decode[map[string]any](t, env.do(t, "GET", "/api/install/status", "", nil))
	if status["needsSetup"] != true || status["installed"] != false {
		t.Fatalf("initial status = %+v", status)
	}
	root := env.do(t, "GET", "/", "", nil)
	if root.Request.URL.Path != "/install" {
		t.Fatalf("root served %q instead of /install", root.Request.URL.Path)
	}
	root.Body.Close()
	cfg := decode[map[string]any](t, env.do(t, "GET", "/api/admin/config", "", nil))
	if cfg["installed"] != false {
		t.Fatalf("admin config installed = %v", cfg["installed"])
	}

	weak := decode[map[string]any](t, env.do(t, "POST", "/api/install", "", map[string]any{
		"username": "root", "password": "short",
	}))
	if weak["error"] == nil {
		t.Fatalf("weak password accepted: %+v", weak)
	}
	if n, _ := env.store.CountAdmins(t.Context()); n != 0 {
		t.Fatalf("admin created after weak password: %d", n)
	}

	resp := env.do(t, "POST", "/api/install", "", map[string]any{
		"username": "ops", "password": "a-strong-password-123",
		"panelName": "My Panel", "publicUrl": "https://panel.example.com/",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("install = %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Token == "" {
		t.Fatal("no session token")
	}
	if n, _ := env.store.CountAdmins(t.Context()); n != 1 {
		t.Fatalf("admin count = %d", n)
	}
	settings, _ := env.store.GetSettings(t.Context())
	if settings["panel_name"] != "My Panel" || settings["public_url"] != "https://panel.example.com" {
		t.Fatalf("settings = %+v", settings)
	}
	if settings["install_completed_at"] == "" {
		t.Fatal("install_completed_at not set")
	}

	status = decode[map[string]any](t, env.do(t, "GET", "/api/install/status", "", nil))
	if status["installed"] != true || status["needsSetup"] != false {
		t.Fatalf("status after install = %+v", status)
	}
	hidden := env.do(t, "GET", "/install", "", nil)
	if hidden.StatusCode != http.StatusNotFound {
		t.Fatalf("installed /install = %d, want 404", hidden.StatusCode)
	}
	hidden.Body.Close()

	again := env.do(t, "POST", "/api/install", "", map[string]any{
		"username": "other", "password": "another-strong-pass-123",
	})
	if again.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(again.Body)
		t.Fatalf("second install = %d: %s", again.StatusCode, b)
	}
	again.Body.Close()

	login := env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "ops", "password": "a-strong-password-123",
	})
	if login.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(login.Body)
		t.Fatalf("login after install = %d: %s", login.StatusCode, b)
	}
	login.Body.Close()
}

func TestInstallRejectsInvalidUsername(t *testing.T) {
	env := newSetupEnv(t)
	body := map[string]any{"username": "a b", "password": "a-strong-password-123"}
	resp := env.do(t, "POST", "/api/install", "", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid username = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestExplicitAdminPasswordSkipsInstall(t *testing.T) {
	env := newTestEnv(t) // config carries an explicit ADMIN_PASSWORD
	status := decode[map[string]any](t, env.do(t, "GET", "/api/install/status", "", nil))
	if status["installed"] != true {
		t.Fatalf("status = %+v", status)
	}
	if n, _ := env.store.CountAdmins(t.Context()); n != 1 {
		t.Fatalf("admin count = %d", n)
	}
}
