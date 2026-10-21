package agent

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CapabilityCSR advertises that this agent can generate a key pair, submit a
// CSR and install a panel-issued certificate.
const CapabilityCSR = "cert.csr.v1"

func (a *Agent) certFiles() (string, string) {
	dir := filepath.Join(a.cfg.DataDir, "certs")
	return filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
}

// managedCertStatus validates the installed managed certificate for host.
func (a *Agent) managedCertStatus(host string) (*x509.Certificate, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("node host is empty")
	}
	certPath, keyPath := a.certFiles()
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	if len(pair.Certificate) == 0 {
		return nil, errors.New("empty certificate")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if time.Now().After(leaf.NotAfter) {
		return nil, errors.New("certificate expired")
	}
	if time.Now().Add(time.Hour).Before(leaf.NotBefore) {
		return nil, errors.New("certificate is not valid yet")
	}
	if err := leaf.VerifyHostname(host); err != nil {
		return nil, err
	}
	return leaf, nil
}

// certFileMeta returns fingerprint/expiry/key fingerprint of the installed
// certificate pair.
func (a *Agent) certFileMeta() (string, int64, string, error) {
	certPath, keyPath := a.certFiles()
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return "", 0, "", err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return "", 0, "", err
	}
	return certFingerprint(leaf), leaf.NotAfter.Unix(), publicKeyFingerprint(leaf.PublicKey), nil
}

// loadCertMeta fills missing certificate metadata from disk at startup.
func (a *Agent) loadCertMeta() {
	if fp, after, keyFP, err := a.certFileMeta(); err == nil {
		a.configMu.Lock()
		a.meta.CertFingerprint = fp
		a.meta.CertNotAfter = after
		a.meta.KeyFingerprint = keyFP
		a.configMu.Unlock()
	}
}

func (a *Agent) setPendingConfig(raw []byte, host string) {
	a.certMu.Lock()
	a.pendingConfig = append([]byte(nil), raw...)
	a.pendingHost = strings.TrimSpace(host)
	a.pendingManaged = true
	a.certMu.Unlock()
}

func (a *Agent) clearPendingConfig() {
	a.certMu.Lock()
	a.pendingConfig = nil
	a.pendingHost = ""
	a.pendingManaged = false
	a.certMu.Unlock()
}

func (a *Agent) pendingSnapshot() ([]byte, string) {
	a.certMu.Lock()
	defer a.certMu.Unlock()
	return append([]byte(nil), a.pendingConfig...), a.pendingHost
}

func (a *Agent) certHost() string {
	raw, pendingHost := a.pendingSnapshot()
	_ = raw
	if pendingHost != "" {
		return pendingHost
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.meta.Host != "" {
		return a.meta.Host
	}
	return a.meta.Domain
}

// needsManagedCert reports whether the pushed config actually uses the TLS
// certificate. Xray nodes using REALITY or no TLS do not need one.
func (a *Agent) needsManagedCert(protocol, configText string) bool {
	if protocol != "xray" {
		return true
	}
	var cfg struct {
		Inbounds []struct {
			StreamSettings struct {
				Security string `json:"security"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(configText), &cfg); err != nil {
		return true
	}
	for _, in := range cfg.Inbounds {
		if strings.EqualFold(in.StreamSettings.Security, "tls") {
			return true
		}
	}
	return false
}

// requiresManagedCert reports whether a certificate must exist before the
// configuration can run. Xray REALITY/plaintext nodes never use the file.
func (a *Agent) requiresManagedCert(protocol, configText, tlsMode string) bool {
	return strings.EqualFold(strings.TrimSpace(tlsMode), "managed") && a.needsManagedCert(protocol, configText)
}

// maybeRequestCert asks the panel for a certificate when the installed one is
// missing or about to expire.
func (a *Agent) maybeRequestCert(_ context.Context, reason string) {
	a.configMu.Lock()
	mode := a.meta.TLSMode
	host := a.meta.Host
	protocol := a.meta.Protocol
	configText := a.currentConfig
	if host == "" {
		host = a.meta.Domain
	}
	a.configMu.Unlock()
	if configText != "" && !a.requiresManagedCert(protocol, configText, mode) {
		return
	}
	a.certMu.Lock()
	pendingManaged, pendingHost := a.pendingManaged, a.pendingHost
	a.certMu.Unlock()
	if pendingManaged && pendingHost != "" {
		mode, host = "managed", pendingHost
	}
	if !strings.EqualFold(mode, "managed") || host == "" {
		return
	}
	leaf, err := a.managedCertStatus(host)
	if err == nil && time.Until(leaf.NotAfter) > 30*24*time.Hour {
		return
	}
	a.requestCertificate(reason, false)
}

// requestCertificate builds a CSR with the local key and sends it to the panel.
func (a *Agent) requestCertificate(reason string, force bool) {
	host := a.certHost()
	if host == "" {
		a.log.Warn("certificate request skipped: node host is empty")
		return
	}
	if net.ParseIP(host) != nil {
		a.log.Warn("certificate request skipped: managed certificates require a domain", "host", host)
		return
	}
	a.certMu.Lock()
	if !force && time.Since(a.certRequestedAt) < 2*time.Minute {
		a.certMu.Unlock()
		return
	}
	a.certRequestedAt = time.Now()
	a.certMu.Unlock()

	signer, err := a.loadOrCreateCertKey()
	if err != nil {
		a.log.Error("prepare certificate key", "error", err)
		return
	}
	csrPEM, keyFP, err := buildCSR(signer, host)
	if err != nil {
		a.log.Error("build CSR", "error", err)
		return
	}
	a.log.Info("requesting certificate from panel", "host", host, "reason", reason)
	a.write("cert.request", map[string]any{
		"requestId":      randomID(),
		"reason":         reason,
		"domains":        []string{host},
		"csrPEM":         csrPEM,
		"keyFingerprint": keyFP,
	})
}

// loadOrCreateCertKey reuses the installed key so renewals keep the same key.
// A fresh key is only created when none exists or the file is unreadable.
func (a *Agent) loadOrCreateCertKey() (crypto.Signer, error) {
	_, keyPath := a.certFiles()
	if b, err := os.ReadFile(keyPath); err == nil {
		if signer, err := parsePrivateKey(b); err == nil {
			return signer, nil
		}
		_ = os.Rename(keyPath, keyPath+".bak")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func parsePrivateKey(b []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("invalid private key")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if s, ok := k.(crypto.Signer); ok {
			return s, nil
		}
		return nil, errors.New("private key is not a signer")
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func buildCSR(signer crypto.Signer, host string) (string, string, error) {
	tmpl := &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: host},
		DNSNames: []string{host},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.DNSNames = nil
		tmpl.IPAddresses = []net.IP{ip}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, signer)
	if err != nil {
		return "", "", err
	}
	csrPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	pub, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(pub)
	return csrPEM, hex.EncodeToString(sum[:]), nil
}

type certPushPayload struct {
	RequestID   string    `json:"requestId"`
	Revision    int64     `json:"revision"`
	Domains     []string  `json:"domains"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
	Fingerprint string    `json:"fingerprint"`
	CertPEM     string    `json:"certPEM"`
	KeyPEM      string    `json:"keyPEM"`
}

func (a *Agent) handleCertPush(raw json.RawMessage) {
	var p certPushPayload
	if err := json.Unmarshal(raw, &p); err != nil || strings.TrimSpace(p.CertPEM) == "" {
		a.write("cert.ack", map[string]any{"requestId": p.RequestID, "ok": false, "error": "invalid cert.push payload"})
		return
	}
	_, keyPath := a.certFiles()
	keyPEM := []byte(p.KeyPEM)
	if len(keyPEM) == 0 {
		b, err := os.ReadFile(keyPath)
		if err != nil {
			a.certAck(p, false, "local private key is missing: "+err.Error())
			return
		}
		keyPEM = b
	}
	pair, err := tls.X509KeyPair([]byte(p.CertPEM), keyPEM)
	if err != nil {
		a.certAck(p, false, "certificate/key mismatch: "+err.Error())
		return
	}
	if len(pair.Certificate) == 0 {
		a.certAck(p, false, "empty certificate chain")
		return
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		a.certAck(p, false, err.Error())
		return
	}
	host := a.certHost()
	if host == "" && len(p.Domains) > 0 {
		host = p.Domains[0]
	}
	if host == "" {
		a.certAck(p, false, "node host is empty")
		return
	}
	if err := leaf.VerifyHostname(host); err != nil {
		a.certAck(p, false, "certificate does not cover "+host+": "+err.Error())
		return
	}
	if time.Now().After(leaf.NotAfter) {
		a.certAck(p, false, "certificate is already expired")
		return
	}
	fp := certFingerprint(leaf)
	a.configMu.Lock()
	already := a.meta.CertFingerprint == fp
	a.configMu.Unlock()
	if already {
		if _, err := a.managedCertStatus(host); err == nil {
			a.certAck(p, true, "")
			pending, _ := a.pendingSnapshot()
			if len(pending) > 0 {
				a.handleConfig(pending)
			}
			return
		}
	}
	if err := a.installCertFiles(p.CertPEM, keyPEM, len(p.KeyPEM) > 0); err != nil {
		a.certAck(p, false, err.Error())
		return
	}
	keyFP := publicKeyFingerprint(leaf.PublicKey)
	a.configMu.Lock()
	a.meta.CertFingerprint = fp
	a.meta.CertNotAfter = leaf.NotAfter.Unix()
	a.meta.KeyFingerprint = keyFP
	a.configMu.Unlock()
	a.saveLocalState()
	p.Fingerprint = fp
	a.certAck(p, true, "")
	a.log.Info("certificate installed", "fingerprint", fp, "notAfter", leaf.NotAfter)

	pending, _ := a.pendingSnapshot()
	if len(pending) > 0 {
		a.handleConfig(pending)
		return
	}
	rt, _ := a.runtimeState()
	a.configMu.Lock()
	hasConfig := a.currentConfig != "" && rt != nil
	a.configMu.Unlock()
	if hasConfig && rt.Running() {
		if err := rt.Restart(); err != nil {
			a.log.Error("restart after certificate install", "error", err)
		}
	}
}

func (a *Agent) certAck(p certPushPayload, ok bool, errMsg string) {
	payload := map[string]any{
		"requestId": p.RequestID, "revision": p.Revision, "ok": ok,
		"fingerprint": p.Fingerprint,
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	a.write("cert.ack", payload)
}

// installCertFiles writes the new pair, keeping a backup for rollback.
func (a *Agent) installCertFiles(certPEM string, keyPEM []byte, replaceKey bool) error {
	certPath, keyPath := a.certFiles()
	dir := filepath.Dir(certPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = copyFile(certPath, filepath.Join(dir, "previous-cert.pem"))
	_ = copyFile(keyPath, filepath.Join(dir, "previous-key.pem"))
	if replaceKey {
		if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
			return err
		}
	}
	return writeFileAtomic(certPath, []byte(certPEM), 0o644)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, b, 0o600)
}

func certFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func publicKeyFingerprint(pub any) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// certRetryLoop periodically retries certificate requests while a managed
// certificate is missing or close to expiry. Failures and panel restarts are
// therefore self-healing without operator action.
func (a *Agent) certRetryLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.maybeRequestCert(ctx, "retry")
		}
	}
}
