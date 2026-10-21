package agent

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHysteriaVersion(t *testing.T) {
	out := "ascii art\nVersion:\tv2.13.0\nBuildDate:\t2026-10-05\n"
	if got := parseHysteriaVersion([]byte(out)); got != "v2.13.0" {
		t.Fatalf("version = %q", got)
	}
	if got := parseHysteriaVersion([]byte("v2.9.1\n")); got != "v2.9.1" {
		t.Fatalf("version = %q", got)
	}
}

func TestEnsureSelfSignedCert(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureSelfSignedCert(dir, "node.example.com"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "certs", "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatal("no pem")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, name := range cert.DNSNames {
		if name == "node.example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dns names = %v", cert.DNSNames)
	}
	// Re-running should keep the existing certificate.
	before := cert.SerialNumber.String()
	if err := EnsureSelfSignedCert(dir, "node.example.com"); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(filepath.Join(dir, "certs", "cert.pem"))
	block2, _ := pem.Decode(b2)
	cert2, _ := x509.ParseCertificate(block2.Bytes)
	if cert2.SerialNumber.String() != before {
		t.Fatal("certificate was replaced unnecessarily")
	}
	// A new host must force regeneration.
	if err := EnsureSelfSignedCert(dir, "other.example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareConfig(t *testing.T) {
	cfg := Config{Runtime: "process", DataDir: "/var/lib/mikvace"}
	got := PrepareConfig(cfg, "tls:\n  cert: /data/certs/cert.pem\n")
	if !strings.Contains(got, "/var/lib/mikvace/certs/cert.pem") {
		t.Fatalf("rewrite = %s", got)
	}
	cfg.Runtime = "docker"
	if got := PrepareConfig(cfg, "cert: /data/certs/cert.pem"); !strings.Contains(got, "/data/certs") {
		t.Fatalf("docker rewrite = %s", got)
	}
}

func TestStatsPoller(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/traffic":
			if r.URL.Query().Get("clear") != "1" {
				t.Error("clear=1 missing")
			}
			_, _ = w.Write([]byte(`{"alice":{"tx":100,"rx":200},"bob":{"tx":1,"rx":2}}`))
		case "/online":
			_, _ = w.Write([]byte(`{"alice":1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := newStatsPoller("secret", "")
	p.Configure("trafficStats:\n  listen: 127.0.0.1:9999\n  secret: secret\n")
	p.baseURL = srv.URL
	p.secret = "secret"
	batch := p.Poll(context.Background())
	if batch == nil {
		t.Fatal("nil batch")
	}
	if batch.Users["alice"].Tx != 100 || batch.Online["alice"] != 1 {
		t.Fatalf("batch = %+v", batch)
	}
	if gotAuth != "secret" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	tx, rx := p.Totals()
	if tx != 101 || rx != 202 {
		t.Fatalf("totals = %d/%d", tx, rx)
	}
	if p.OnlineCount() != 1 {
		t.Fatalf("online = %d", p.OnlineCount())
	}
	// A successful but empty poll still has to be returned so the panel can
	// clear stale online state.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/traffic") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv2.Close()
	p2 := newStatsPoller("", "")
	p2.baseURL = srv2.URL
	if b := p2.Poll(context.Background()); b == nil || len(b.Online) != 0 {
		t.Fatalf("empty batch = %+v", b)
	}
}
