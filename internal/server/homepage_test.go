package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

func TestHomepageServesSPAInsteadOfRedirecting(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := Config{
		Addr: ":0", DataDir: dir, DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin", AdminPassword: "testpass123",
		SessionTTL: time.Hour, ServeWeb: true,
	}
	app, err := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)

	// Do not follow redirects: `/` must render the landing page directly.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("root content type = %q", ct)
	}

	// The administrator console and the user portal are SPA routes under
	// their own prefixes.
	for _, path := range []string{"/admin/login", "/admin/dashboard", "/user", "/user/login"} {
		r, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, r.StatusCode)
		}
	}

	// The retired /panel prefix no longer redirects into the console; it is
	// hidden behind the same 404 view as any other unknown route.
	for _, path := range []string{"/panel", "/panel/", "/panel/users"} {
		r, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, r.StatusCode)
		}
		if loc := r.Header.Get("Location"); loc != "" {
			t.Fatalf("%s redirected to %q, want no redirect", path, loc)
		}
	}

	// Enabling the security entrance hides /admin and requires the secret
	// segment for both the SPA shell and the login API.
	if err := st.SetSetting(context.Background(), "admin_security_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(context.Background(), "admin_security_path", "secret-entry"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin", "/admin/login", "/admin/wrong-entry/login"} {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s with security entrance = %d, want 404", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/admin/secret-entry", "/admin/secret-entry/login", "/admin/secret-entry/dashboard"} {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s with valid entrance = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestLegacyPanelPrefixReturnsNotFoundInAPIOnlyMode(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, "GET", "/panel", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/panel = %d, want 404", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		t.Fatalf("/panel redirected to %q, want no redirect", loc)
	}
}
