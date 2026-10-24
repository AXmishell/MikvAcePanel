package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/certmanager"
)

func makeTestCert(t *testing.T, domain string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
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
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}

func TestCertSettingsLiteSSLRequiresEAB(t *testing.T) {
	env := newTestEnv(t)
	liteSSL := certmanager.LiteSSLCADir

	// LiteSSL rejects accounts without EAB, so the settings endpoint must not
	// let the operator save an incomplete configuration.
	resp := env.do(t, "PUT", "/api/cert/settings", env.token, map[string]any{
		"ca": liteSSL, "email": "admin@example.com", "provider": "manual", "renewDays": 30,
	})
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("LiteSSL without EAB = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	hmac := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	resp = env.do(t, "PUT", "/api/cert/settings", env.token, map[string]any{
		"ca": liteSSL, "email": "admin@example.com", "provider": "manual", "renewDays": 30,
		"eabKid": "eab-kid-1", "eabHmac": hmac,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("LiteSSL with EAB = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	settings := decode[map[string]any](t, env.do(t, "GET", "/api/cert/settings", env.token, nil))
	if settings["ca"] != liteSSL || settings["eabKid"] != "eab-kid-1" || settings["eabConfigured"] != true {
		t.Fatalf("settings = %+v", settings)
	}
	if _, ok := settings["eabHmac"]; ok {
		t.Fatalf("settings must not return the EAB HMAC: %+v", settings)
	}
	stored, err := env.store.GetSetting(t.Context(), certmanager.SettingEABHMAC)
	if err != nil {
		t.Fatal(err)
	}
	if stored == "" || stored == hmac {
		t.Fatalf("EAB HMAC was not encrypted: %q", stored)
	}
	loaded, err := env.app.certs.LoadSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EABKID != "eab-kid-1" || loaded.EABHMAC != hmac || !loaded.EABConfigured {
		t.Fatalf("loaded EAB = %+v", loaded)
	}

	// Moving back to Let's Encrypt allows clearing the binding.
	resp = env.do(t, "PUT", "/api/cert/settings", env.token, map[string]any{
		"ca": certmanager.DefaultCADir, "email": "admin@example.com", "provider": "manual",
		"renewDays": 30, "clearEab": true,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("clear EAB = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	settings = decode[map[string]any](t, env.do(t, "GET", "/api/cert/settings", env.token, nil))
	if settings["eabConfigured"] == true {
		t.Fatalf("EAB still configured: %+v", settings)
	}
}

func TestCertSettingsSwitchingCADropsEAB(t *testing.T) {
	env := newTestEnv(t)
	liteSSL := certmanager.LiteSSLCADir
	hmac := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))

	resp := env.do(t, "PUT", "/api/cert/settings", env.token, map[string]any{
		"ca": liteSSL, "email": "admin@example.com", "provider": "manual", "renewDays": 30,
		"eabKid": "eab-kid-1", "eabHmac": hmac,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("save LiteSSL EAB = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	// Switching to a CA that does not use EAB must not leave the binding behind,
	// otherwise it would be sent to the new directory on the next issuance.
	resp = env.do(t, "PUT", "/api/cert/settings", env.token, map[string]any{
		"ca": certmanager.DefaultCADir, "email": "admin@example.com", "provider": "manual", "renewDays": 30,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("switch CA = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	settings := decode[map[string]any](t, env.do(t, "GET", "/api/cert/settings", env.token, nil))
	if settings["eabConfigured"] == true || settings["eabKid"] != "" {
		t.Fatalf("EAB was not cleared after switching CA: %+v", settings)
	}
	stored, err := env.store.GetSetting(t.Context(), certmanager.SettingEABHMAC)
	if err != nil {
		t.Fatal(err)
	}
	if stored != "" {
		t.Fatalf("EAB HMAC still stored after switching CA: %q", stored)
	}
}

func TestNodeCertificateUpload(t *testing.T) {
	env := newTestEnv(t)
	created := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "Cert Node", "domain": "node.example.com", "port": 443,
		"tlsMode": "managed", "active": true,
	}))
	nodeID := int64(created["node"].(map[string]any)["id"].(float64))

	// Defaults are readable without leaking credentials.
	settings := decode[map[string]any](t, env.do(t, "GET", "/api/cert/settings", env.token, nil))
	if settings["provider"] != "manual" {
		t.Fatalf("settings = %+v", settings)
	}
	// DNS settings are stored and encrypted.
	resp := env.do(t, "PUT", "/api/cert/settings", env.token, map[string]any{
		"ca":    "https://acme-staging-v02.api.letsencrypt.org/directory",
		"email": "admin@example.com", "provider": "manual", "renewDays": 30,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update settings status = %d", resp.StatusCode)
	}

	certPEM, keyPEM := makeTestCert(t, "node.example.com")
	resp = env.do(t, "POST", "/api/nodes/"+itoa(nodeID)+"/cert/upload", env.token, map[string]any{
		"certPEM": certPEM, "keyPEM": keyPEM,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}
	got := decode[map[string]any](t, env.do(t, "GET", "/api/nodes/"+itoa(nodeID)+"/cert", env.token, nil))
	if got["status"] != "issued" {
		t.Fatalf("cert status = %+v", got)
	}
	cert := got["certificate"].(map[string]any)
	if cert["issuer"] == "" || cert["notAfter"] == nil {
		t.Fatalf("certificate summary = %+v", cert)
	}

	// A certificate for a different host must be rejected.
	otherCert, otherKey := makeTestCert(t, "other.example.com")
	resp = env.do(t, "POST", "/api/nodes/"+itoa(nodeID)+"/cert/upload", env.token, map[string]any{
		"certPEM": otherCert, "keyPEM": otherKey,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for foreign certificate, got %d", resp.StatusCode)
	}
}
