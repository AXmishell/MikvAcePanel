package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func TestSettingsNeverReturnDNSConfig(t *testing.T) {
	env := newTestEnv(t)
	if err := env.store.SetSetting(context.Background(), "cert_dns_config", "SECRET-CIPHER"); err != nil {
		t.Fatal(err)
	}
	check := func(resp *http.Response) {
		t.Helper()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out struct {
			Settings map[string]string `json:"settings"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if _, ok := out.Settings["cert_dns_config"]; ok {
			t.Fatal("cert_dns_config leaked in settings response")
		}
		if out.Settings["cert_dns_config_set"] != "true" {
			t.Fatalf("cert_dns_config_set = %q", out.Settings["cert_dns_config_set"])
		}
	}
	check(env.do(t, http.MethodGet, "/api/settings", env.token, nil))
	check(env.do(t, http.MethodPut, "/api/settings", env.token, map[string]any{"panel_name": "MikvAce"}))
}

func TestSettingsNeverReturnEABHMAC(t *testing.T) {
	env := newTestEnv(t)
	if err := env.store.SetSetting(context.Background(), "cert_acme_eab_hmac", "SECRET-CIPHER"); err != nil {
		t.Fatal(err)
	}
	check := func(resp *http.Response) {
		t.Helper()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out struct {
			Settings map[string]string `json:"settings"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if _, ok := out.Settings["cert_acme_eab_hmac"]; ok {
			t.Fatal("cert_acme_eab_hmac leaked in settings response")
		}
		if out.Settings["cert_acme_eab_hmac_set"] != "true" {
			t.Fatalf("cert_acme_eab_hmac_set = %q", out.Settings["cert_acme_eab_hmac_set"])
		}
	}
	check(env.do(t, http.MethodGet, "/api/settings", env.token, nil))
	check(env.do(t, http.MethodPut, "/api/settings", env.token, map[string]any{"panel_name": "MikvAce"}))
}

func TestAdminSecurityEntrance(t *testing.T) {
	env := newWebTestEnv(t)
	ctx := context.Background()

	doEntry := func(client *http.Client, method, path, token, entry string, body any) *http.Response {
		t.Helper()
		var rdr io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, env.srv.URL+path, rdr)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if entry != "" {
			req.Header.Set("X-Admin-Entry", entry)
		}
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// The entrance is mandatory from the first boot.
	if env.entry == "" {
		t.Fatal("boot did not generate an administrator entrance")
	}
	cfg := decode[map[string]any](t, env.do(t, "GET", "/api/admin/config", "", nil))
	if cfg["securityEntryEnabled"] != true {
		t.Fatalf("security entry must be enabled: %+v", cfg)
	}
	if cfg["gateActive"] != false {
		t.Fatalf("gate should be inactive before visiting the entrance: %+v", cfg)
	}
	probe := env.do(t, "GET", "/api/admin/config", "", nil)
	if got := probe.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
		probe.Body.Close()
		t.Fatalf("admin config Cache-Control = %q, want no-store", got)
	}
	probe.Body.Close()

	// Invalid and empty paths are rejected; the entrance cannot be disabled.
	for _, bad := range []string{"abc", "has space", "with/slash", "admin", ""} {
		r := env.do(t, "PUT", "/api/settings", env.token, map[string]any{
			"admin_security_path": bad,
		})
		if r.StatusCode != http.StatusBadRequest {
			r.Body.Close()
			t.Fatalf("path %q status = %d, want 400", bad, r.StatusCode)
		}
		r.Body.Close()
	}
	r := env.do(t, "PUT", "/api/settings", env.token, map[string]any{
		"admin_security_enabled": false,
	})
	if r.StatusCode != http.StatusBadRequest {
		r.Body.Close()
		t.Fatalf("disabling the entrance = %d, want 400", r.StatusCode)
	}
	r.Body.Close()

	// Rotating the path applies immediately.
	r = env.do(t, "PUT", "/api/settings", env.token, map[string]any{
		"admin_security_path": "secret-entry",
	})
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		t.Fatalf("rotate entrance = %d: %s", r.StatusCode, b)
	}
	if setCookie := r.Header.Get("Set-Cookie"); !strings.Contains(setCookie, adminEntryCookieName+"=") ||
		!strings.Contains(setCookie, "HttpOnly") {
		r.Body.Close()
		t.Fatalf("rotate Set-Cookie = %q, want a rotated HttpOnly gate cookie", setCookie)
	}
	r.Body.Close()
	env.entry = "secret-entry"

	// The login endpoint and every admin API are hidden without the entrance.
	r = doEntry(nil, "POST", "/api/login", "", "", map[string]any{"username": "admin", "password": "testpass123"})
	if r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("login without entry = %d, want 404", r.StatusCode)
	}
	r.Body.Close()

	r = doEntry(nil, "GET", "/api/me", "", "", nil)
	if r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("anonymous /api/me without entry = %d, want 404", r.StatusCode)
	}
	r.Body.Close()

	r = doEntry(nil, "POST", "/api/login", "", "wrong-entry", map[string]any{"username": "admin", "password": "testpass123"})
	if r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("login with wrong entry = %d, want 404", r.StatusCode)
	}
	r.Body.Close()

	login := decode[map[string]any](t, doEntry(nil, "POST", "/api/login", "", "secret-entry", map[string]any{
		"username": "admin", "password": "testpass123",
	}))
	token, _ := login["token"].(string)
	if token == "" {
		t.Fatalf("login with entry failed: %+v", login)
	}

	// Authenticated requests still need the entrance header or gate cookie.
	r = doEntry(nil, "GET", "/api/me", token, "", nil)
	if r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("/api/me without entry = %d, want 404", r.StatusCode)
	}
	r.Body.Close()
	r = doEntry(nil, "GET", "/api/me", token, "secret-entry", nil)
	if r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("/api/me with entry = %d, want 200", r.StatusCode)
	}
	r.Body.Close()

	// Visiting the entrance issues the HttpOnly gate cookie. The cookie alone
	// is enough for the SPA and API in the same browser.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	gated := &http.Client{Jar: jar}
	first := doEntry(gated, "GET", "/admin/secret-entry", "", "", nil)
	if first.StatusCode != http.StatusOK {
		first.Body.Close()
		t.Fatalf("entrance = %d, want 200", first.StatusCode)
	}
	first.Body.Close()
	if setCookie := first.Header.Get("Set-Cookie"); !strings.Contains(setCookie, adminEntryCookieName+"=") || !strings.Contains(setCookie, "HttpOnly") {
		t.Fatalf("entrance Set-Cookie = %q, want HttpOnly gate cookie", setCookie)
	}
	gateCookie := false
	for _, c := range jar.Cookies(mustParseURL(env.srv.URL)) {
		if c.Name == adminEntryCookieName && c.Value != "" {
			gateCookie = true
			break
		}
	}
	if !gateCookie {
		t.Fatal("entrance did not issue the admin gate cookie")
	}
	r = doEntry(gated, "GET", "/api/me", token, "", nil)
	if r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("/api/me with gate cookie = %d, want 200", r.StatusCode)
	}
	r.Body.Close()
	r = doEntry(gated, "GET", "/admin", "", "", nil)
	if r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("/admin with gate cookie = %d, want 200", r.StatusCode)
	}
	r.Body.Close()
	if cfg := decode[map[string]any](t, doEntry(gated, "GET", "/api/admin/config", "", "", nil)); cfg["gateActive"] != true {
		t.Fatalf("gateActive with cookie = %+v", cfg)
	}

	// Rotating the entrance invalidates the old gate cookie.
	if err := env.store.SetSetting(ctx, "admin_security_path", "rotated-entry"); err != nil {
		t.Fatal(err)
	}
	r = doEntry(gated, "GET", "/api/me", token, "", nil)
	if r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("/api/me with stale gate cookie = %d, want 404", r.StatusCode)
	}
	r.Body.Close()
	if err := env.store.SetSetting(ctx, "admin_security_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	// The legacy disabled flag cannot reopen the console.
	r = doEntry(nil, "GET", "/api/me", token, "rotated-entry", nil)
	if r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("/api/me after legacy disable = %d, want 200", r.StatusCode)
	}
	r.Body.Close()

	// A misconfigured empty path fails closed.
	if err := env.store.SetSetting(ctx, "admin_security_path", ""); err != nil {
		t.Fatal(err)
	}
	r = doEntry(nil, "GET", "/api/me", token, "rotated-entry", nil)
	if r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("/api/me with empty entrance path = %d, want 404", r.StatusCode)
	}
	r.Body.Close()
}
