package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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
	env := newTestEnv(t)
	ctx := context.Background()

	doEntry := func(method, path, token, entry string, body any) *http.Response {
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
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Disabled by default.
	cfg := decode[map[string]any](t, env.do(t, "GET", "/api/admin/config", "", nil))
	if cfg["securityEntryEnabled"] != false {
		t.Fatalf("security entry should default to disabled: %+v", cfg)
	}

	// Invalid paths are rejected before the entrance is enabled.
	for _, bad := range []string{"abc", "has space", "with/slash", "admin"} {
		r := env.do(t, "PUT", "/api/settings", env.token, map[string]any{
			"admin_security_enabled": true, "admin_security_path": bad,
		})
		if r.StatusCode != http.StatusBadRequest {
			r.Body.Close()
			t.Fatalf("path %q status = %d, want 400", bad, r.StatusCode)
		}
		r.Body.Close()
	}
	// Enabling without a path is rejected.
	r := env.do(t, "PUT", "/api/settings", env.token, map[string]any{
		"admin_security_enabled": true, "admin_security_path": "",
	})
	if r.StatusCode != http.StatusBadRequest {
		r.Body.Close()
		t.Fatalf("missing path status = %d, want 400", r.StatusCode)
	}
	r.Body.Close()

	if err := env.store.SetSetting(ctx, "admin_security_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetSetting(ctx, "admin_security_path", "secret-entry"); err != nil {
		t.Fatal(err)
	}
	if cfg := decode[map[string]any](t, env.do(t, "GET", "/api/admin/config", "", nil)); cfg["securityEntryEnabled"] != true {
		t.Fatalf("security entry should be enabled: %+v", cfg)
	}

	// The login endpoint is hidden without the entrance header.
	r = doEntry("POST", "/api/login", "", "", map[string]any{"username": "admin", "password": "testpass123"})
	if r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("login without entry = %d, want 404", r.StatusCode)
	}
	r.Body.Close()

	r = doEntry("POST", "/api/login", "", "wrong-entry", map[string]any{"username": "admin", "password": "testpass123"})
	if r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("login with wrong entry = %d, want 404", r.StatusCode)
	}
	r.Body.Close()

	login := decode[map[string]any](t, doEntry("POST", "/api/login", "", "secret-entry", map[string]any{
		"username": "admin", "password": "testpass123",
	}))
	token, _ := login["token"].(string)
	if token == "" {
		t.Fatalf("login with entry failed: %+v", login)
	}

	// Admin API calls carry the same entrance requirement for JWT sessions.
	r = doEntry("GET", "/api/me", token, "", nil)
	if r.StatusCode != http.StatusNotFound {
		r.Body.Close()
		t.Fatalf("/api/me without entry = %d, want 404", r.StatusCode)
	}
	r.Body.Close()
	r = doEntry("GET", "/api/me", token, "secret-entry", nil)
	if r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("/api/me with entry = %d, want 200", r.StatusCode)
	}
	r.Body.Close()

	// Disabling restores the normal /admin login flow.
	if err := env.store.SetSetting(ctx, "admin_security_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	r = doEntry("POST", "/api/login", "", "", map[string]any{"username": "admin", "password": "testpass123"})
	if r.StatusCode != http.StatusOK {
		r.Body.Close()
		t.Fatalf("login after disabling = %d, want 200", r.StatusCode)
	}
	r.Body.Close()
}
