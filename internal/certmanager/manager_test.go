package certmanager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/acme"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

func TestEABHelpers(t *testing.T) {
	if !RequiresEAB(LiteSSLCADir) || RequiresEAB(DefaultCADir) {
		t.Fatal("RequiresEAB did not identify LiteSSL")
	}
	raw := []byte("0123456789abcdef")
	for _, value := range []string{
		base64.RawURLEncoding.EncodeToString(raw),
		base64.URLEncoding.EncodeToString(raw),
		base64.StdEncoding.EncodeToString(raw),
		hex.EncodeToString(raw),
		strings.ToUpper(hex.EncodeToString(raw)),
	} {
		got, err := decodeEABKey(value)
		if err != nil {
			t.Fatalf("decode %q: %v", value, err)
		}
		if string(got) != string(raw) {
			t.Fatalf("decode %q = %q", value, got)
		}
	}
	if _, err := decodeEABKey("not valid!"); err == nil {
		t.Fatal("invalid EAB key accepted")
	}
}

func TestKeyAuthorizationMatchesACME(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client := &acme.Client{Key: key}
	want, err := client.DNS01ChallengeRecord("token-123")
	if err != nil {
		t.Fatal(err)
	}
	keyAuth, err := keyAuthorization(key, "token-123")
	if err != nil {
		t.Fatal(err)
	}
	// The TXT value is SHA-256(keyAuth) in base64url.
	sum := sha256.Sum256([]byte(keyAuth))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("key authorization mismatch: got %s want %s", got, want)
	}
}

func TestParseCSR(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "node.example.com"},
		DNSNames: []string{"node.example.com"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	csr, err := parseCSR(csrPEM)
	if err != nil {
		t.Fatal(err)
	}
	domains := csrDomains(csr)
	if len(domains) != 1 || domains[0] != "node.example.com" {
		t.Fatalf("domains = %v", domains)
	}
	if _, err := parseCSR("not a csr"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestHandleRequestRejectsForeignDomain(t *testing.T) {
	m := &Manager{jobs: map[int64]*job{}}
	node := &model.Node{ID: 1, Domain: "node.example.com", TLSMode: "managed"}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "evil.example.net"},
		DNSNames: []string{"evil.example.net"},
	}, key)
	csrPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	err := m.HandleRequest(context.Background(), node, Request{CSRPEM: csrPEM, Domains: []string{"evil.example.net"}})
	if err == nil {
		t.Fatal("expected domain validation error")
	}
}

func TestHandleRequestPushesExistingCertificate(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir + "/panel.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB.Exec(`INSERT INTO nodes (id, name, token_hash, created_at, updated_at) VALUES (1, 'test', 'hash', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	var pushed bool
	m := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), make([]byte, 32), func(nodeID int64, msgType string, payload any) bool {
		if nodeID == 1 && msgType == "cert.push" {
			pushed = true
		}
		return true
	})
	certPEM, _ := testCertificate(t, "node.example.com")
	leaf, err := leafFromPEM(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert, _, err := st.SaveCertificate(context.Background(), &model.Certificate{
		Domains: []string{"node.example.com"}, CommonName: "node.example.com", CertPEM: certPEM,
		Fingerprint: certFingerprint(leaf), KeyFinger: publicKeyFingerprint(leaf.PublicKey),
		Issuer: "test", NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter,
		Source: "acme", Status: "valid", AutoRenew: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNodeCertificate(context.Background(), &model.NodeCertificate{
		NodeID: 1, CertID: cert.ID, Revision: 1, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "node.example.com"},
		DNSNames: []string{"node.example.com"},
	}, key)
	csrPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	node := &model.Node{ID: 1, Domain: "node.example.com", TLSMode: "managed"}
	if err := m.HandleRequest(context.Background(), node, Request{CSRPEM: csrPEM, Reason: "apply"}); err != nil {
		t.Fatal(err)
	}
	if !pushed {
		t.Fatal("expected the existing certificate to be pushed")
	}
}

func testCertificate(t *testing.T, host string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}
