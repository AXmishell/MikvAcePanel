package certmanager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// TestPebbleIssuance exercises the full DNS-01 flow against Pebble. It is
// skipped unless ACME_E2E=1; scripts/cert-e2e.sh starts Pebble and sets the
// environment. Pebble runs with PEBBLE_VA_ALWAYS_VALID=1, so confirming the
// manual challenge is enough even though no TXT record is published.
func TestPebbleIssuance(t *testing.T) {
	if os.Getenv("ACME_E2E") != "1" {
		t.Skip("set ACME_E2E=1 and run scripts/cert-e2e.sh to enable the Pebble integration test")
	}
	directory := os.Getenv("ACME_E2E_DIRECTORY")
	if directory == "" {
		directory = "https://localhost:14000/dir"
	}
	const domain = "node.example.com"

	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`INSERT INTO nodes
		(id, name, domain, tls_mode, token_hash, created_at, updated_at)
		VALUES (1, 'pebble', ?, 'managed', 'hash', ?, ?)`, domain, now, now); err != nil {
		t.Fatal(err)
	}

	push := make(chan map[string]any, 8)
	m := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), make([]byte, 32), func(nodeID int64, msgType string, payload any) bool {
		body, _ := payload.(map[string]any)
		if body == nil {
			body = map[string]any{}
		}
		body["__type"] = msgType
		push <- body
		return true
	})
	defer m.Close()
	// Pebble runs with PEBBLE_VA_ALWAYS_VALID=1 and the test does not publish
	// TXT records, so skip the public-resolver propagation check. The staging
	// checklist in docs/certificates-testing.md covers real DNS propagation.
	m.txtWait = 0
	if err := m.SaveSettings(context.Background(), Settings{
		CA: directory, Email: "cert-e2e@example.com", Provider: "manual", RenewDays: 30,
	}, nil); err != nil {
		t.Fatal(err)
	}

	node := &model.Node{ID: 1, Domain: domain, TLSMode: "managed"}
	csrPEM, err := testCSR(domain)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.HandleRequest(context.Background(), node, Request{CSRPEM: csrPEM, Domains: []string{domain}}); err != nil {
		t.Fatal(err)
	}

	// The manual provider must expose the TXT record and wait for confirmation.
	deadline := time.Now().Add(60 * time.Second)
	var challenge *ManualChallenge
	for time.Now().Before(deadline) {
		challenge = m.Pending(1)
		if challenge != nil {
			break
		}
		if !m.Busy(1) {
			nc, _ := st.GetNodeCertificate(context.Background(), 1)
			if nc != nil && nc.LastError != "" {
				t.Fatalf("issuance failed before the challenge: %s", nc.LastError)
			}
			t.Fatal("issuance job ended before creating a manual challenge")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if challenge == nil {
		t.Fatal("timed out waiting for the manual DNS challenge")
	}
	if len(challenge.Records) == 0 || challenge.Records[0].Value == "" {
		t.Fatalf("challenge records = %+v", challenge.Records)
	}
	if err := m.Confirm(1); err != nil {
		t.Fatal(err)
	}

	msg := waitForPush(t, push, 90*time.Second)
	if msg["__type"] == "cert.error" {
		t.Fatalf("certificate error: %v", msg["error"])
	}
	if msg["__type"] != "cert.push" {
		t.Fatalf("push message = %+v", msg)
	}
	certPEM, _ := msg["certPEM"].(string)
	fingerprint, _ := msg["fingerprint"].(string)
	if certPEM == "" || fingerprint == "" {
		t.Fatalf("push payload = %+v", msg)
	}
	leaf := parseLeaf(t, certPEM)
	if err := leaf.VerifyHostname(domain); err != nil {
		t.Fatalf("certificate does not cover %s: %v", domain, err)
	}
	if leaf.Issuer.CommonName == "" {
		t.Fatal("certificate has no issuer")
	}
	m.Ack(context.Background(), 1, fingerprint, true, "")
	nc, err := st.GetNodeCertificate(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status != "active" || nc.AppliedFingerprint != fingerprint {
		t.Fatalf("node certificate = %+v", nc)
	}

	// A valid cached certificate is pushed without opening another ACME order.
	if err := m.HandleRequest(context.Background(), node, Request{CSRPEM: csrPEM, Domains: []string{domain}, Reason: "apply"}); err != nil {
		t.Fatal(err)
	}
	cached := waitForPush(t, push, 10*time.Second)
	if cached["__type"] != "cert.push" || cached["fingerprint"] != fingerprint {
		t.Fatalf("cached push = %+v", cached)
	}
}

func waitForPush(t *testing.T, ch <-chan map[string]any, timeout time.Duration) map[string]any {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a certificate push")
		return nil
	}
}

func testCSR(domain string) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domain},
		DNSNames: []string{domain},
	}, key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), nil
}

func parseLeaf(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("certificate PEM is empty")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}
