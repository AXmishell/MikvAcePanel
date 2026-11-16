package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

// TestBootstrapMigratesLegacyAdminSecurityEntry covers the regression that
// left /admin public: an installation with the legacy disabled flag and an
// empty entrance must be upgraded to a generated, mandatory entrance.
func TestBootstrapMigratesLegacyAdminSecurityEntry(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.EnsureDefaultSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "admin_security_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "admin_security_path", ""); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Addr: ":0", DataDir: dir, DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin", AdminPassword: "testpass123",
		SessionTTL: time.Hour, ServeWeb: true,
	}
	app, err := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	enabled, entrance := app.adminSecurityEntry(ctx)
	if !enabled || entrance == "" {
		t.Fatalf("admin security entrance = (%v, %q), want enabled with a path", enabled, entrance)
	}
	if !validAdminSecurityPath(entrance) {
		t.Fatalf("generated entrance %q is invalid", entrance)
	}
	if v, _ := st.GetSetting(ctx, "admin_security_enabled"); v != "true" {
		t.Fatalf("admin_security_enabled = %q, want true", v)
	}

	// The recovery file is the supported way back in after losing the URL.
	raw, err := os.ReadFile(filepath.Join(dir, adminEntryFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "/admin/"+entrance) {
		t.Fatalf("%s does not contain the entrance:\n%s", adminEntryFileName, raw)
	}

	// Direct /admin is hidden, the entrance is served, and the resulting
	// gate cookie unlocks the plain console.
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/admin = %d, want 404", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/"+entrance, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("entrance = %d, want 200", resp.StatusCode)
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 || cookies[0].Name != adminEntryCookieName || !cookies[0].HttpOnly {
		t.Fatalf("entrance cookies = %+v", cookies)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/admin", nil)
	req.AddCookie(cookies[0])
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin with gate cookie = %d, want 200", resp.StatusCode)
	}
}

func TestAdminEntryPathNormalization(t *testing.T) {
	cases := []struct {
		path    string
		segment string
		admin   bool
	}{
		{"/admin", "", true},
		{"/admin/", "", true},
		{"/ADMIN", "", true},
		{"//admin", "", true},
		{"/admin;/", "", true},
		{"/admin;foo", "", true},
		{"/admin/secret", "secret", true},
		{"/admin/secret/login", "secret", true},
		{"/admin/../other", "", false},
		{"/administrator", "", false},
		{"/adminx", "", false},
		{"/other/admin", "", false},
	}
	for _, tc := range cases {
		segment, admin := adminUIPathSegment(tc.path)
		if admin != tc.admin || (admin && segment != tc.segment) {
			t.Fatalf("adminUIPathSegment(%q) = (%q, %v), want (%q, %v)",
				tc.path, segment, admin, tc.segment, tc.admin)
		}
	}
}

func TestAdminEntranceRedactedFromLogs(t *testing.T) {
	cases := map[string]string{
		"/admin":                    "/admin",
		"/admin/secret-entry":       "/admin/<entrance>",
		"/Admin/secret-entry/login": "/Admin/<entrance>/login",
		"//admin/secret-entry":      "/admin/<entrance>",
		"/admin;/secret-entry":      "/admin/<entrance>",
		"/admin/../other":           "/admin/../other",
		"/api/users":                "/api/users",
	}
	for input, want := range cases {
		if got := redactAdminEntrancePath(input); got != want {
			t.Fatalf("redactAdminEntrancePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAdminGateCookieBoundToEntrance(t *testing.T) {
	s := &Server{secret: []byte("test-secret")}
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: adminEntryCookieName, Value: s.adminGateValue("first-entry")})
	if !s.adminGateCookieValid(req, "first-entry") {
		t.Fatal("gate cookie should be valid for its entrance")
	}
	if s.adminGateCookieValid(req, "rotated-entry") {
		t.Fatal("gate cookie must not survive an entrance rotation")
	}
}
