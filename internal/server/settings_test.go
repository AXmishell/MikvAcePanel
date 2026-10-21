package server

import (
	"context"
	"encoding/json"
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
