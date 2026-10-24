package agent

import (
	"context"
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

	"github.com/axmishell/mikvacepanel/internal/hy2config"
	"github.com/axmishell/mikvacepanel/internal/model"
)

func testAuthServer(t *testing.T, panelURL string, online bool) (*localAuthServer, context.CancelFunc) {
	t.Helper()
	cfg := Config{
		PanelURL: panelURL, DataDir: t.TempDir(),
		AuthListen: "127.0.0.1:0", OfflineAuth: true, PanelInsecure: true,
	}
	srv := newLocalAuthServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), func() bool { return online })
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		srv.Close()
	})
	return srv, cancel
}

func postAuth(t *testing.T, srv *localAuthServer, body string) (int, map[string]any, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		"http://"+srv.Addr()+"/api/auth?node=7&key="+srv.Secret(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header.Get("X-MikvAce-Auth")
}

func TestLocalAuthOfflineSnapshot(t *testing.T) {
	srv, _ := testAuthServer(t, "http://127.0.0.1:1", false)
	expire := time.Now().Add(-time.Hour)
	snap := model.UserAuthSnapshot{
		NodeID: 7, GeneratedAt: time.Now().UTC(), GraceMinutes: 15,
		Users: []model.UserAuth{
			{UserID: "alice", Password: "alice-pw", Enabled: true, MaxDevices: 1},
			{UserID: "expired", Password: "pw", Enabled: true, ExpireAt: &expire},
			{UserID: "quota", Password: "pw", Enabled: true, TrafficLimit: 10, TrafficTx: 10},
			{UserID: "disabled", Password: "pw", Enabled: false},
		},
	}
	if err := srv.SetSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	status, out, source := postAuth(t, srv, `{"addr":"1.1.1.1:1000","auth":"alice:alice-pw"}`)
	if status != 200 || out["ok"] != true || source != "offline" {
		t.Fatalf("alice = %d %+v source=%q", status, out, source)
	}
	if out["id"] != "alice" {
		t.Fatalf("alice id = %v", out["id"])
	}

	// Same IP reconnects within the grace period and is still allowed.
	if _, out, _ := postAuth(t, srv, `{"addr":"1.1.1.1:1001","auth":"alice:alice-pw"}`); out["ok"] != true {
		t.Fatalf("alice repeat = %+v", out)
	}
	// A second unique IP exceeds MaxDevices=1.
	if _, out, _ := postAuth(t, srv, `{"addr":"2.2.2.2:1000","auth":"alice:alice-pw"}`); out["ok"] != false {
		t.Fatalf("alice second device = %+v", out)
	}
	for _, tc := range []struct{ name, auth string }{
		{"wrong password", "alice:nope"},
		{"expired", "expired:pw"},
		{"over quota", "quota:pw"},
		{"disabled", "disabled:pw"},
		{"unknown", "nobody:pw"},
		{"malformed", "alice"},
	} {
		if _, out, _ := postAuth(t, srv, `{"addr":"3.3.3.3:1000","auth":"`+tc.auth+`"}`); out["ok"] != false {
			t.Fatalf("%s = %+v", tc.name, out)
		}
	}

	// Snapshot is persisted and can be loaded by a fresh server instance.
	b, err := os.ReadFile(filepath.Join(srv.cfg.DataDir, "state", authSnapshotFile))
	if err != nil || !strings.Contains(string(b), "alice-pw") {
		t.Fatalf("snapshot file = %v %s", err, b)
	}
	if snap2, err := loadAuthSnapshot(srv.cfg.DataDir); err != nil || snap2 == nil || len(snap2.Users) != 4 {
		t.Fatalf("reloaded snapshot = %+v %v", snap2, err)
	}
	if mode := fileMode(t, filepath.Join(srv.cfg.DataDir, "state", authSnapshotFile)); mode.Perm() != 0o600 {
		t.Fatalf("snapshot mode = %o", mode.Perm())
	}
}

func TestLocalAuthRejectsBadKey(t *testing.T) {
	srv, _ := testAuthServer(t, "http://127.0.0.1:1", false)
	resp, err := http.Post("http://"+srv.Addr()+"/api/auth?node=7&key=wrong", "application/json",
		strings.NewReader(`{"auth":"alice:pw"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad key status = %d", resp.StatusCode)
	}
}

func TestLocalAuthProxiesPanelThenFallsBack(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth" || r.URL.Query().Get("node") != "7" {
			t.Errorf("unexpected panel path %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"ok":true,"id":"from-panel"}`))
	}))
	defer panel.Close()

	srv, _ := testAuthServer(t, panel.URL, true)
	if err := srv.SetSnapshot(model.UserAuthSnapshot{
		NodeID: 7, GraceMinutes: 15,
		Users: []model.UserAuth{{UserID: "alice", Password: "alice-pw", Enabled: true}},
	}); err != nil {
		t.Fatal(err)
	}
	_, out, source := postAuth(t, srv, `{"addr":"1.1.1.1:1000","auth":"alice:alice-pw"}`)
	if out["id"] != "from-panel" || source != "panel" {
		t.Fatalf("proxy = %+v source=%q", out, source)
	}

	panel.Close()
	_, out, source = postAuth(t, srv, `{"addr":"1.1.1.1:1000","auth":"alice:alice-pw"}`)
	if out["ok"] != true || source != "offline" {
		t.Fatalf("fallback = %+v source=%q", out, source)
	}
}

func TestRewriteAuthURL(t *testing.T) {
	in := "auth:\n  type: http\n  http:\n    url: https://panel.example.com/api/auth?node=7\n"
	got := rewriteAuthURL(in, "http://127.0.0.1:9998", "abc")
	if !strings.Contains(got, `url: "http://127.0.0.1:9998/api/auth?node=7&key=abc"`) {
		t.Fatalf("rewrite = %s", got)
	}
	if strings.Contains(got, "panel.example.com") {
		t.Fatalf("panel URL left behind: %s", got)
	}
	quoted := "auth:\n  http:\n    url: \"https://panel.example.com/api/auth?node=12\"\n"
	if got := rewriteAuthURL(quoted, "http://[::1]:9000", "k"); !strings.Contains(got, `node=12&key=k`) {
		t.Fatalf("quoted rewrite = %s", got)
	}
	// Unrelated YAML is untouched.
	other := "masquerade:\n  proxyUrl: https://example.com/\n"
	if got := rewriteAuthURL(other, "http://127.0.0.1:1", "k"); got != other {
		t.Fatalf("unrelated rewrite = %s", got)
	}
}

func TestRewriteGeneratedHysteriaConfig(t *testing.T) {
	n := &model.Node{ID: 7, Name: "n", Port: 443, StatsPort: 9999, TLSMode: "selfsigned"}
	text, _, err := hy2config.Render(n, "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	got := rewriteAuthURL(text, "http://127.0.0.1:9998", "secret")
	if !strings.Contains(got, `http://127.0.0.1:9998/api/auth?node=7&key=secret`) {
		t.Fatalf("generated config not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "listen:") || !strings.Contains(got, "tls:") {
		t.Fatalf("rewrite damaged config:\n%s", got)
	}
}

func TestParseConfigRejectsDirectoryPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseConfig([]string{"--config", path}, "test"); err == nil {
		t.Fatal("a directory config path must be rejected")
	}
}

func TestParseConfigYAML(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "node.token")
	if err := os.WriteFile(tokenPath, []byte("token-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "node.yaml")
	write := func(listen string) {
		body := `version: 1
panel:
  url: https://panel.example.com/
  token_file: "` + tokenPath + `"
  insecure: true
agent:
  data_dir: /data
  host_data_dir: /var/lib/mikvace
  log_level: debug
  heartbeat: 12s
  stats_interval: 20s
runtime:
  type: process
offline_auth:
  enabled: true
  listen: "` + listen + `"
`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("127.0.0.1:9998")
	cfg, err := ParseConfig([]string{"--config", path}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PanelURL != "https://panel.example.com" || cfg.Token != "token-123" || !cfg.PanelInsecure {
		t.Fatalf("panel cfg = %+v", cfg)
	}
	if !cfg.OfflineAuth || cfg.AuthListen != "127.0.0.1:9998" {
		t.Fatalf("offline cfg = %+v", cfg)
	}
	if cfg.Heartbeat != 12*time.Second || cfg.StatsInterval != 20*time.Second || cfg.LogLevel != "debug" {
		t.Fatalf("agent cfg = %+v", cfg)
	}
	write("0.0.0.0:9998")
	if _, err := ParseConfig([]string{"--config", path}, "test"); err == nil {
		t.Fatal("non-loopback offline_auth.listen must be rejected")
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode()
}
