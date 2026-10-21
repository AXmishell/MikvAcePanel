package hy2config

import (
	"strings"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
	"gopkg.in/yaml.v3"
)

func TestRenderSelfSigned(t *testing.T) {
	n := &model.Node{
		ID: 7, Name: "Tokyo", IP: "203.0.113.10", Port: 443, StatsPort: 9999, StatsSecret: "s3cret",
		TLSMode: "selfsigned", ObfsType: "salamander", ObfsPassword: "obfs",
		BandwidthUp: "1 gbps", ACL: []string{"direct(all)"},
	}
	text, hash, err := Render(n, "https://panel.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" || !strings.HasPrefix(text, "listen: :443") {
		t.Fatalf("unexpected output: %s", text)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatal(err)
	}
	auth := cfg["auth"].(map[string]any)["http"].(map[string]any)
	if auth["url"] != "https://panel.example.com/api/auth?node=7" {
		t.Fatalf("auth url = %v", auth["url"])
	}
	stats := cfg["trafficStats"].(map[string]any)
	if stats["secret"] != "s3cret" || stats["listen"] != "127.0.0.1:9999" {
		t.Fatalf("stats = %v", stats)
	}
}

func TestRenderACME(t *testing.T) {
	n := &model.Node{ID: 1, Domain: "node.example.com", Port: 443, TLSMode: "acme", ACMEEmail: "a@b.c", StatsPort: 9999}
	text, _, err := Render(n, "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "tls:") || !strings.Contains(text, "acme:") || !strings.Contains(text, "node.example.com") {
		t.Fatalf("unexpected acme config:\n%s", text)
	}
	// Node-side ACME must not bind port 80 and must persist its cache.
	if !strings.Contains(text, "disableHTTP: true") || !strings.Contains(text, "dir: /data/acme") {
		t.Fatalf("acme config must use TLS-ALPN only and a persistent dir:\n%s", text)
	}
	if _, _, err := Render(&model.Node{TLSMode: "acme"}, "https://panel"); err == nil {
		t.Fatal("expected error for acme without domain")
	}
}

func TestRenderManaged(t *testing.T) {
	n := &model.Node{ID: 2, Domain: "node.example.com", Port: 443, TLSMode: "managed", StatsPort: 9999}
	text, _, err := Render(n, "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "tls:") || !strings.Contains(text, "/data/certs/cert.pem") {
		t.Fatalf("managed config must reference the panel-managed files:\n%s", text)
	}
	if strings.Contains(text, "acme:") {
		t.Fatalf("managed config must not contain acme:\n%s", text)
	}
}

func TestRenderRequiresURL(t *testing.T) {
	if _, _, err := Render(&model.Node{Port: 443}, ""); err == nil {
		t.Fatal("expected error")
	}
}
