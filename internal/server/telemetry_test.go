package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// fakeLogStore implements only the methods the maintenance and health paths
// use. The embedded interface keeps the remaining LogStore methods compiling.
type fakeLogStore struct {
	store.LogStore
	driver  string
	logs    int64
	batches int64
	pruned  int64
	dir     store.LogDirectory
}

func (f *fakeLogStore) Driver() string             { return f.driver }
func (f *fakeLogStore) Ping(context.Context) error { return nil }
func (f *fakeLogStore) LogUsage(context.Context) (int64, int64, error) {
	return f.logs, f.batches, nil
}
func (f *fakeLogStore) TelemetryStats(context.Context) (store.TelemetryStats, error) {
	return store.TelemetryStats{Driver: f.driver, Bytes: 4096, Rows: f.logs + f.batches, Detail: "logs"}, nil
}
func (f *fakeLogStore) PruneLogs(_ context.Context, _ time.Time) (int64, int64, error) {
	f.pruned++
	return 3, 1, nil
}
func (f *fakeLogStore) InsertLogBatch(context.Context, int64, string, []model.LogEntry) (bool, error) {
	return true, nil
}

func newTelemetryTestEnv(t *testing.T) (*testEnv, *fakeLogStore) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logs := &fakeLogStore{driver: "postgres", logs: 42, batches: 7, dir: st}
	cfg := Config{
		Addr: ":0", DataDir: dir, DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin", AdminPassword: "testpass123",
		SessionTTL: time.Hour, TelemetryDriver: "postgres", TelemetryRequired: true,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := NewWithLogs(cfg, st, logs, logger)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	_, entry := app.adminSecurityEntry(context.Background())
	env := &testEnv{srv: ts, store: st, app: app, entry: entry}
	env.token = env.login(t)
	return env, logs
}

func TestHealthReportsExternalTelemetry(t *testing.T) {
	env, _ := newTelemetryTestEnv(t)
	resp := env.do(t, "GET", "/api/health", "", nil)
	defer resp.Body.Close()
	var body struct {
		OK        bool `json:"ok"`
		Telemetry struct {
			Driver   string `json:"driver"`
			OK       bool   `json:"ok"`
			Required bool   `json:"required"`
		} `json:"telemetry"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Telemetry.Driver != "postgres" || !body.Telemetry.OK || !body.Telemetry.Required {
		t.Fatalf("health = %+v", body)
	}
}

func TestUsageAndPruneUseTelemetryBackend(t *testing.T) {
	env, logs := newTelemetryTestEnv(t)
	resp := env.do(t, "GET", "/api/maintenance/usage", env.token, nil)
	defer resp.Body.Close()
	var usage dataUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		t.Fatal(err)
	}
	if usage.Logs != 42 || usage.LogBatches != 7 || usage.TelemetryDriver != "postgres" ||
		usage.TelemetryBytes != 4096 || !usage.TelemetryOK {
		t.Fatalf("usage = %+v", usage)
	}
	res, err := env.app.PruneData(context.Background(), 14, 30)
	if err != nil {
		t.Fatal(err)
	}
	if logs.pruned != 1 {
		t.Fatalf("telemetry prune calls = %d", logs.pruned)
	}
	if res.Logs != 3 || res.LogBatches != 1 {
		t.Fatalf("prune result = %+v", res)
	}
}

func TestHealthReportsPostgresFallback(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := Config{
		Addr: ":0", DataDir: dir, DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin", AdminPassword: "testpass123",
		SessionTTL: time.Hour, TelemetryDriver: "postgres",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := NewWithLogs(cfg, st, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	_, entry := app.adminSecurityEntry(context.Background())
	env := &testEnv{srv: ts, store: st, app: app, entry: entry}
	resp := env.do(t, "GET", "/api/health", "", nil)
	defer resp.Body.Close()
	var body struct {
		OK        bool `json:"ok"`
		Telemetry struct {
			Driver     string `json:"driver"`
			Configured string `json:"configured"`
			Fallback   bool   `json:"fallback"`
			OK         bool   `json:"ok"`
		} `json:"telemetry"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Telemetry.Driver != "sqlite" || body.Telemetry.Configured != "postgres" ||
		!body.Telemetry.Fallback || body.Telemetry.OK {
		t.Fatalf("health = %+v", body)
	}
}
