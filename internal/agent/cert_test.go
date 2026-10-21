package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCertPair(t *testing.T, host string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
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
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return
}

func TestManagedCertStatus(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{cfg: Config{DataDir: dir}}
	certPEM, keyPEM := testCertPair(t, "node.example.com")
	if err := a.installCertFiles(string(certPEM), keyPEM, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.managedCertStatus("node.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.managedCertStatus("other.example.com"); err == nil {
		t.Fatal("expected hostname mismatch")
	}
	fp, after, keyFP, err := a.certFileMeta()
	if err != nil {
		t.Fatal(err)
	}
	if fp == "" || keyFP == "" || after <= time.Now().Unix() {
		t.Fatalf("meta = %q %d %q", fp, after, keyFP)
	}
}

func TestInstallCertFilesKeepsKey(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{cfg: Config{DataDir: dir}}
	_, keyPEM := testCertPair(t, "node.example.com")
	_, keyPath := a.certFiles()
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cert2, _ := testCertPair(t, "node.example.com")
	if err := a.installCertFiles(string(cert2), nil, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(keyPEM) {
		t.Fatal("key should not be replaced")
	}
}

func TestBuildCSR(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, fp, err := buildCSR(key, "node.example.com")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		t.Fatal("invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatal(err)
	}
	if len(csr.DNSNames) != 1 || csr.DNSNames[0] != "node.example.com" {
		t.Fatalf("dns names = %v", csr.DNSNames)
	}
	if fp == "" {
		t.Fatal("empty key fingerprint")
	}
}

func TestRequiresManagedCert(t *testing.T) {
	a := &Agent{}
	reality := `{"inbounds":[{"streamSettings":{"security":"reality"}}]}`
	tlsCfg := `{"inbounds":[{"streamSettings":{"security":"tls"}}]}`
	if a.requiresManagedCert("xray", reality, "managed") {
		t.Fatal("reality should not require a managed certificate")
	}
	if a.requiresManagedCert("hysteria", "any", "selfsigned") {
		t.Fatal("selfsigned mode should not require a managed certificate")
	}
	if !a.requiresManagedCert("hysteria", "any", "managed") {
		t.Fatal("hysteria managed mode requires a certificate")
	}
	if !a.requiresManagedCert("xray", tlsCfg, "managed") {
		t.Fatal("xray tls managed mode requires a certificate")
	}
}

func TestNeedsManagedCert(t *testing.T) {
	a := &Agent{}
	reality := `{"inbounds":[{"streamSettings":{"security":"reality"}}]}`
	tlsCfg := `{"inbounds":[{"streamSettings":{"security":"tls"}}]}`
	if !a.needsManagedCert("hysteria", "any") {
		t.Fatal("hysteria always needs a certificate")
	}
	if a.needsManagedCert("xray", reality) {
		t.Fatal("reality does not need a certificate")
	}
	if !a.needsManagedCert("xray", tlsCfg) {
		t.Fatal("tls does need a certificate")
	}
}
