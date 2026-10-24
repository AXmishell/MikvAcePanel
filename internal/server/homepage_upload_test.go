package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

func newHomepageEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := Config{
		ConfigPath: filepath.Join(dir, "panel.yaml"),
		Addr:       ":0", DataDir: dir, DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin", AdminPassword: "testpass123",
		SessionTTL: time.Hour, ServeWeb: true,
	}
	app, err := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	env := &testEnv{srv: ts, store: st, app: app}
	env.token = env.login(t)
	return env
}

func (e *testEnv) uploadHomepage(t *testing.T, filename string, data []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", e.srv.URL+"/api/homepage/upload", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHomepageUploadHTMLAndServeSandboxed(t *testing.T) {
	env := newHomepageEnv(t)

	page := []byte(`<!doctype html><html><body><h1>Custom</h1><script>localStorage.setItem("x","1")</script></body></html>`)
	resp := env.uploadHomepage(t, "landing.html", page)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload = %d: %s", resp.StatusCode, b)
	}
	var uploaded struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&uploaded)
	resp.Body.Close()
	if uploaded.Version == "" {
		t.Fatal("no version returned")
	}

	// Upload alone must not replace the built-in homepage.
	root := env.do(t, "GET", "/", "", nil)
	body, _ := io.ReadAll(root.Body)
	root.Body.Close()
	if strings.Contains(string(body), "Custom") {
		t.Fatal("uploaded page served before activation")
	}

	act := env.do(t, "POST", "/api/homepage/activate", env.token, map[string]any{"version": uploaded.Version})
	if act.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(act.Body)
		t.Fatalf("activate = %d: %s", act.StatusCode, b)
	}
	act.Body.Close()

	root = env.do(t, "GET", "/", "", nil)
	body, _ = io.ReadAll(root.Body)
	root.Body.Close()
	if !strings.Contains(string(body), "/homepage/index.html") || !strings.Contains(string(body), "sandbox=") {
		t.Fatalf("root wrapper = %s", body)
	}

	pageResp := env.do(t, "GET", "/homepage/index.html", "", nil)
	pageBody, _ := io.ReadAll(pageResp.Body)
	pageResp.Body.Close()
	if pageResp.StatusCode != http.StatusOK || !strings.Contains(string(pageBody), "Custom") {
		t.Fatalf("page = %d: %s", pageResp.StatusCode, pageBody)
	}
	if csp := pageResp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("CSP = %q", csp)
	}
	if xfo := pageResp.Header.Get("X-Frame-Options"); xfo != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q", xfo)
	}
	if acao := pageResp.Header.Get("Access-Control-Allow-Origin"); acao != "*" {
		t.Fatalf("ACAO = %q", acao)
	}
}

func TestHomepageUploadZipAndPreview(t *testing.T) {
	env := newHomepageEnv(t)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"site/index.html":       `<html><body><div id="app">ZIP</div><script src="./assets/app.js"></script></body></html>`,
		"site/assets/app.js":    `document.body.dataset.ok = '1'`,
		"site/assets/style.css": `body{color:red}`,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	resp := env.uploadHomepage(t, "site.zip", buf.Bytes())
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload = %d: %s", resp.StatusCode, b)
	}
	var uploaded struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&uploaded)
	resp.Body.Close()

	act := env.do(t, "POST", "/api/homepage/activate", env.token, map[string]any{"version": uploaded.Version})
	if act.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(act.Body)
		t.Fatalf("activate = %d: %s", act.StatusCode, b)
	}
	act.Body.Close()

	for path, want := range map[string]string{
		"/homepage/index.html":       "ZIP",
		"/homepage/assets/app.js":    "dataset.ok",
		"/homepage/assets/style.css": "color:red",
	} {
		r := env.do(t, "GET", path, "", nil)
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusOK || !strings.Contains(string(b), want) {
			t.Fatalf("%s = %d: %s", path, r.StatusCode, b)
		}
	}

	preview := decode[map[string]any](t, env.do(t, "POST", "/api/homepage/preview", env.token, map[string]any{"version": uploaded.Version}))
	url, _ := preview["url"].(string)
	if !strings.HasPrefix(url, "/preview/homepage/") {
		t.Fatalf("preview = %+v", preview)
	}
	r := env.do(t, "GET", url, "", nil)
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || !strings.Contains(string(b), "ZIP") {
		t.Fatalf("preview page = %d: %s", r.StatusCode, b)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("preview cache = %q", cc)
	}
}

func TestHomepagePreviewRejectsMalformedJSON(t *testing.T) {
	env := newHomepageEnv(t)

	up := env.uploadHomepage(t, "index.html", []byte("<html>preview</html>"))
	var uploaded struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(up.Body).Decode(&uploaded)
	up.Body.Close()
	if resp := env.do(t, "POST", "/api/homepage/activate", env.token, map[string]any{"version": uploaded.Version}); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	req, _ := http.NewRequest("POST", env.srv.URL+"/api/homepage/preview", strings.NewReader("{"))
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("malformed preview = %d: %s", resp.StatusCode, b)
	}
}

func TestHomepageRejectsZipSlip(t *testing.T) {
	env := newHomepageEnv(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../evil.html")
	_, _ = w.Write([]byte("<html>evil</html>"))
	_ = zw.Close()

	resp := env.uploadHomepage(t, "evil.zip", buf.Bytes())
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("zip slip = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
}

func TestHomepageIncludedInBackupAndRestore(t *testing.T) {
	env := newHomepageEnv(t)
	up := env.uploadHomepage(t, "index.html", []byte("<html>backup-me</html>"))
	var uploaded struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(up.Body).Decode(&uploaded)
	up.Body.Close()
	if resp := env.do(t, "POST", "/api/homepage/activate", env.token, map[string]any{"version": uploaded.Version}); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	info, err := env.app.CreateBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(env.app.backupDir(), info.Name)

	dest := t.TempDir()
	if err := extractHomepageFromArchive(tarPath, dest); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPendingHomepageRestore(dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "homepage", "versions", uploaded.Version, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "backup-me") {
		t.Fatalf("restored homepage = %q", b)
	}
}

func TestHomepageModeResetAndAuth(t *testing.T) {
	env := newHomepageEnv(t)

	// Unauth upload must fail before touching the filesystem.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "x.html")
	_, _ = fw.Write([]byte("<html></html>"))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", env.srv.URL+"/api/homepage/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upload = %d", resp.StatusCode)
	}

	up := env.uploadHomepage(t, "index.html", []byte("<html>reset</html>"))
	var uploaded struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(up.Body).Decode(&uploaded)
	up.Body.Close()
	if resp := env.do(t, "POST", "/api/homepage/activate", env.token, map[string]any{"version": uploaded.Version}); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d", resp.StatusCode)
	}
	resp.Body.Close()

	reset := env.do(t, "DELETE", "/api/homepage", env.token, nil)
	if reset.StatusCode != http.StatusOK {
		t.Fatalf("reset = %d", reset.StatusCode)
	}
	reset.Body.Close()

	root := env.do(t, "GET", "/", "", nil)
	b, _ := io.ReadAll(root.Body)
	root.Body.Close()
	if strings.Contains(string(b), "reset") {
		t.Fatal("built-in mode still served the uploaded page")
	}
}
