package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newSetupModeEnv builds the no-database first-run app used by
// database.driver: setup. It deliberately never creates a store.
func newSetupModeEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		ConfigPath: filepath.Join(dir, "panel.yaml"),
		Addr:       ":0", DataDir: dir, DBDriver: SetupDriver,
		DBPath:    filepath.Join(dir, "panel.db"),
		PanelName: "MikvAce Panel", ServeWeb: true, SessionTTL: time.Hour,
	}
	app := NewSetup(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{srv: ts, app: app}, dir
}

func TestSetupModeStartsWithoutCreatingDatabase(t *testing.T) {
	env, dir := newSetupModeEnv(t)

	status := decode[map[string]any](t, env.do(t, "GET", "/api/install/status", "", nil))
	if status["setupMode"] != true || status["installed"] != false || status["needsSetup"] != true {
		t.Fatalf("status = %+v", status)
	}
	if status["database"] != SetupDriver {
		t.Fatalf("database = %v", status["database"])
	}
	if _, err := os.Stat(filepath.Join(dir, "panel.db")); !os.IsNotExist(err) {
		t.Fatalf("setup mode created panel.db: %v", err)
	}

	resp := env.do(t, "GET", "/install", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/install = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Admin creation must wait until a database has been selected.
	resp = env.do(t, "POST", "/api/install", "", map[string]any{
		"username": "ops", "password": "a-strong-password-123",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("install before database = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSetupModeSavesDatabaseAndRestarts(t *testing.T) {
	env, dir := newSetupModeEnv(t)
	restarted := make(chan struct{}, 1)
	env.app.cfg.Restart = func() { restarted <- struct{}{} }

	chosen := filepath.Join(dir, "chosen.db")
	resp := env.do(t, "POST", "/api/install/database", "", map[string]any{
		"driver": "sqlite", "path": chosen,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("save = %d: %s", resp.StatusCode, b)
	}
	var saved struct {
		RestartRequired bool `json:"restartRequired"`
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(resp.Body).Decode(&saved)
	if !saved.RestartRequired {
		t.Fatal("setup mode must restart after the first database is saved")
	}

	raw, err := os.ReadFile(env.app.cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "driver: sqlite") || !strings.Contains(string(raw), chosen) {
		t.Fatalf("panel.yaml = \n%s", raw)
	}

	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("database save did not trigger a restart")
	}
}
