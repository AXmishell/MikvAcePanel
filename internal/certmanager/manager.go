// Package certmanager issues and distributes TLS certificates for nodes.
//
// The default flow keeps private keys on the node: the agent generates a key
// pair and sends a CSR over the control channel, the panel completes an ACME
// DNS-01 order and pushes back the certificate chain. Uploaded certificates
// are the exception: the panel stores their private key encrypted at rest and
// pushes it to the node.
package certmanager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/axmishell/mikvacepanel/internal/certcrypto"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

const (
	CapabilityCSR = "cert.csr.v1"

	SettingCA        = "cert_acme_ca"
	SettingEmail     = "cert_acme_email"
	SettingProvider  = "cert_dns_provider"
	SettingDNSConfig = "cert_dns_config"
	SettingRenewDays = "cert_renew_days"
	DefaultCADir     = "https://acme-v02.api.letsencrypt.org/directory"
	DefaultRenewDays = 30
	jobTimeout       = 30 * time.Minute
	propagationWait  = 5 * time.Minute
)

// PushFunc delivers a control message to a connected agent.
type PushFunc func(nodeID int64, msgType string, payload any) bool

// Request is the payload of a cert.request message.
type Request struct {
	RequestID      string   `json:"requestId"`
	Reason         string   `json:"reason"`
	Domains        []string `json:"domains"`
	CSRPEM         string   `json:"csrPEM"`
	KeyFingerprint string   `json:"keyFingerprint"`
}

// ManualRecord is one DNS TXT record the administrator must create.
type ManualRecord struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ManualChallenge describes a DNS-01 challenge waiting for the administrator.
type ManualChallenge struct {
	NodeID    int64          `json:"nodeId"`
	Domains   []string       `json:"domains"`
	Records   []ManualRecord `json:"records"`
	CreatedAt time.Time      `json:"createdAt"`

	confirm chan struct{}
	once    sync.Once
}

type job struct {
	nodeID int64
	cancel context.CancelFunc
	manual *ManualChallenge
}

// Manager coordinates certificate issuance and pushing.
type Manager struct {
	st   *store.Store
	log  *slog.Logger
	key  []byte
	push PushFunc

	mu      sync.Mutex
	jobs    map[int64]*job
	txtWait time.Duration
}

// New creates a certificate manager.
func New(st *store.Store, logger *slog.Logger, key []byte, push PushFunc) *Manager {
	return &Manager{
		st: st, log: logger, key: key, push: push,
		jobs: map[int64]*job{}, txtWait: propagationWait,
	}
}

// HandleRequest validates a CSR from a node and starts issuance in the
// background. It never blocks the WebSocket read loop.
func (m *Manager) HandleRequest(ctx context.Context, node *model.Node, req Request) error {
	if node == nil {
		return errors.New("unknown node")
	}
	if !strings.EqualFold(node.TLSMode, "managed") {
		return errors.New("node TLS mode is not managed")
	}
	csr, err := parseCSR(req.CSRPEM)
	if err != nil {
		return err
	}
	domains := csrDomains(csr)
	if len(domains) == 0 {
		return errors.New("CSR has no domains")
	}
	want := normalizeDomain(node.Domain)
	if want == "" {
		return errors.New("node has no domain for a managed certificate")
	}
	for _, d := range domains {
		if normalizeDomain(d) != want {
			return fmt.Errorf("CSR domain %q does not match node domain %q", d, want)
		}
	}
	keyFP := publicKeyFingerprint(csr.PublicKey)
	if req.KeyFingerprint != "" && req.KeyFingerprint != keyFP {
		return errors.New("CSR public key does not match the reported key fingerprint")
	}

	// If we already hold a certificate outside the renewal window, push it
	// instead of hitting the CA again.
	settings, _ := m.LoadSettings(ctx)
	renewDays := settings.RenewDays
	if renewDays <= 0 {
		renewDays = DefaultRenewDays
	}
	if cert := m.usableNodeCert(ctx, node.ID, renewDays); cert != nil && !strings.EqualFold(req.Reason, "renew") {
		_ = m.pushCertificate(ctx, node.ID, cert, req.RequestID)
		return nil
	}

	m.mu.Lock()
	if _, ok := m.jobs[node.ID]; ok {
		m.mu.Unlock()
		// One issuance per node is enough; the running job will push its
		// certificate to the agent. Remaining silent avoids a retry storm from
		// the agent's periodic certificate check.
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	j := &job{nodeID: node.ID, cancel: cancel}
	m.jobs[node.ID] = j
	m.mu.Unlock()

	_ = m.st.SetNodeCertPending(ctx, node.ID, keyFP)
	go m.runJob(ctx, j, node, domains, csr, req)
	return nil
}

// runJob performs the ACME order and pushes the resulting certificate.
func (m *Manager) runJob(ctx context.Context, j *job, node *model.Node, domains []string, csr *x509.CertificateRequest, req Request) {
	defer func() {
		j.cancel()
		m.mu.Lock()
		delete(m.jobs, j.nodeID)
		m.mu.Unlock()
	}()
	certPEM, err := m.obtain(ctx, j, domains, csr)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			err = errors.New("certificate request cancelled")
		}
		m.log.Warn("certificate issuance failed", "node", node.ID, "domain", node.Domain, "error", err)
		_ = m.st.SetNodeCertStatus(context.Background(), node.ID, "error", err.Error())
		m.sendError(node.ID, req.RequestID, err)
		return
	}
	cert, _, err := m.storeIssued(ctx, domains, certPEM, nil, "acme")
	if err != nil {
		_ = m.st.SetNodeCertStatus(context.Background(), node.ID, "error", err.Error())
		m.sendError(node.ID, req.RequestID, err)
		return
	}
	_ = m.st.SetNodeCertStatus(ctx, node.ID, "issued", "")
	m.log.Info("certificate issued", "node", node.ID, "domain", node.Domain,
		"fingerprint", cert.Fingerprint, "notAfter", cert.NotAfter)
	if !m.pushCertificate(ctx, node.ID, cert, req.RequestID) {
		_ = m.st.SetNodeCertStatus(ctx, node.ID, "pushing", "node offline, will push on reconnect")
	}
}

// PushExisting pushes the stored certificate to a node, if any. It is called
// when an agent connects.
func (m *Manager) PushExisting(ctx context.Context, nodeID int64) bool {
	if m.Busy(nodeID) {
		return false
	}
	n, err := m.st.GetNode(ctx, nodeID)
	if err != nil || !strings.EqualFold(n.TLSMode, "managed") {
		return false
	}
	nc, err := m.st.GetNodeCertificate(ctx, nodeID)
	if err != nil || nc == nil || nc.CertID == 0 {
		return false
	}
	cert, err := m.st.GetCertificate(ctx, nc.CertID)
	if err != nil || cert == nil {
		return false
	}
	if cert.NotAfter.Before(time.Now()) {
		return false
	}
	if leaf, err := leafFromPEM(cert.CertPEM); err == nil && n.Domain != "" {
		if err := leaf.VerifyHostname(n.Domain); err != nil {
			return false
		}
	}
	return m.pushCertificate(ctx, nodeID, cert, "stored")
}

// pushCertificate sends a cert.push message and records the push attempt.
func (m *Manager) pushCertificate(ctx context.Context, nodeID int64, cert *model.Certificate, requestID string) bool {
	if m.push == nil {
		return false
	}
	nc, _ := m.st.GetNodeCertificate(ctx, nodeID)
	rev := int64(1)
	if nc != nil {
		rev = nc.Revision + 1
	}
	keyPEM := ""
	if cert.KeyCipher != "" {
		if b, err := certcrypto.Decrypt(m.key, cert.KeyCipher, cert.KeyNonce); err == nil {
			keyPEM = string(b)
		} else {
			m.log.Warn("decrypt certificate key", "cert", cert.ID, "error", err)
			return false
		}
	}
	payload := map[string]any{
		"requestId":   requestID,
		"revision":    rev,
		"domains":     cert.Domains,
		"notBefore":   cert.NotBefore,
		"notAfter":    cert.NotAfter,
		"fingerprint": cert.Fingerprint,
		"certPEM":     cert.CertPEM,
		"keyPEM":      keyPEM,
	}
	ok := m.push(nodeID, "cert.push", payload)
	if ok {
		_ = m.st.UpsertNodeCertificate(ctx, &model.NodeCertificate{
			NodeID: nodeID, CertID: cert.ID, Revision: rev, KeyFingerprint: cert.KeyFinger,
			Status: "pushing", LastError: "",
		})
	}
	return ok
}

// Ack records the result reported by an agent.
func (m *Manager) Ack(ctx context.Context, nodeID int64, fingerprint string, ok bool, errMsg string) {
	if ok {
		_ = m.st.SetNodeCertApplied(ctx, nodeID, fingerprint)
		_ = m.st.UpdateNodeCertInfo(ctx, nodeID, fingerprint, nil)
		m.log.Info("certificate installed", "node", nodeID, "fingerprint", fingerprint)
		return
	}
	_ = m.st.SetNodeCertStatus(ctx, nodeID, "error", errMsg)
	m.log.Warn("certificate rejected by agent", "node", nodeID, "error", errMsg)
}

// sendError tells the agent why issuance failed.
func (m *Manager) sendError(nodeID int64, requestID string, err error) {
	if m.push == nil {
		return
	}
	m.push(nodeID, "cert.error", map[string]any{"requestId": requestID, "error": err.Error()})
}

// Confirm releases a manual DNS-01 challenge.
func (m *Manager) Confirm(nodeID int64) error {
	m.mu.Lock()
	j := m.jobs[nodeID]
	m.mu.Unlock()
	if j == nil || j.manual == nil {
		return errors.New("no pending DNS challenge for this node")
	}
	j.manual.once.Do(func() { close(j.manual.confirm) })
	return nil
}

// Cancel aborts a running issuance.
func (m *Manager) Cancel(nodeID int64) error {
	m.mu.Lock()
	j := m.jobs[nodeID]
	m.mu.Unlock()
	if j == nil {
		return errors.New("no certificate request in progress")
	}
	j.cancel()
	return nil
}

// Pending returns the manual challenge of a node, if any.
func (m *Manager) Pending(nodeID int64) *ManualChallenge {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[nodeID]; j != nil {
		return j.manual
	}
	return nil
}

// Busy reports whether issuance is running for a node.
func (m *Manager) Busy(nodeID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.jobs[nodeID]
	return ok
}

// Close cancels all running jobs.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		j.cancel()
	}
}

// Upload validates and stores an administrator-uploaded certificate.
func (m *Manager) Upload(ctx context.Context, node *model.Node, certPEM, keyPEM string) (*model.Certificate, error) {
	if node == nil || !strings.EqualFold(node.TLSMode, "managed") {
		return nil, errors.New("node TLS mode is not managed")
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("certificate and key do not match: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if node.Domain == "" {
		return nil, errors.New("node has no domain")
	}
	if err := leaf.VerifyHostname(node.Domain); err != nil {
		return nil, fmt.Errorf("certificate does not cover %s: %w", node.Domain, err)
	}
	if time.Now().After(leaf.NotAfter) {
		return nil, errors.New("certificate is expired")
	}
	keyCipher, keyNonce, err := certcrypto.Encrypt(m.key, []byte(keyPEM))
	if err != nil {
		return nil, err
	}
	cert := &model.Certificate{
		Domains: leaf.DNSNames, CommonName: leaf.Subject.CommonName,
		CertPEM: normalizePEMChain(certPEM), KeyCipher: keyCipher, KeyNonce: keyNonce,
		Fingerprint: certFingerprint(leaf), KeyFinger: publicKeyFingerprint(leaf.PublicKey),
		Issuer: leaf.Issuer.CommonName, NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter,
		Source: "upload", Status: statusFor(leaf, 30), AutoRenew: false,
	}
	if len(cert.Domains) == 0 {
		cert.Domains = []string{node.Domain}
	}
	saved, _, err := m.st.SaveCertificate(ctx, cert)
	if err != nil {
		return nil, err
	}
	if saved.KeyCipher == "" {
		// The same certificate may have been issued from a node CSR earlier;
		// attach the uploaded key so it can be pushed to other nodes.
		_ = m.st.SetCertificateKey(ctx, saved.ID, keyCipher, keyNonce, saved.KeyFinger)
		saved.KeyCipher, saved.KeyNonce = keyCipher, keyNonce
	}
	nc, _ := m.st.GetNodeCertificate(ctx, node.ID)
	rev := int64(1)
	if nc != nil {
		rev = nc.Revision + 1
	}
	_ = m.st.UpsertNodeCertificate(ctx, &model.NodeCertificate{
		NodeID: node.ID, CertID: saved.ID, Revision: rev, KeyFingerprint: saved.KeyFinger,
		Status: "issued",
	})
	_ = m.st.SetNodeCertStatus(ctx, node.ID, "issued", "")
	m.pushCertificate(ctx, node.ID, saved, "upload")
	return saved, nil
}

// storedNodeCert returns the certificate mapped to a node.
func (m *Manager) storedNodeCert(ctx context.Context, nodeID int64) *model.Certificate {
	nc, err := m.st.GetNodeCertificate(ctx, nodeID)
	if err != nil || nc == nil || nc.CertID == 0 {
		return nil
	}
	cert, err := m.st.GetCertificate(ctx, nc.CertID)
	if err != nil {
		return nil
	}
	return cert
}

func (m *Manager) usableNodeCert(ctx context.Context, nodeID int64, renewDays int) *model.Certificate {
	cert := m.storedNodeCert(ctx, nodeID)
	if cert == nil {
		return nil
	}
	if cert.NotAfter.Before(time.Now().Add(time.Duration(renewDays) * 24 * time.Hour)) {
		return nil
	}
	return cert
}

func (m *Manager) storeIssued(ctx context.Context, domains []string, certPEM string, keyPEM []byte, source string) (*model.Certificate, []byte, error) {
	leaf, err := leafFromPEM(certPEM)
	if err != nil {
		return nil, nil, err
	}
	cert := &model.Certificate{
		Domains: domains, CommonName: leaf.Subject.CommonName, CertPEM: normalizePEMChain(certPEM),
		Fingerprint: certFingerprint(leaf), KeyFinger: publicKeyFingerprint(leaf.PublicKey),
		Issuer: leaf.Issuer.CommonName, NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter,
		Source: source, Status: statusFor(leaf, 30), AutoRenew: true,
	}
	if len(cert.Domains) == 0 {
		cert.Domains = leaf.DNSNames
	}
	if len(keyPEM) > 0 {
		ct, nonce, err := certcrypto.Encrypt(m.key, keyPEM)
		if err != nil {
			return nil, nil, err
		}
		cert.KeyCipher, cert.KeyNonce = ct, nonce
	}
	saved, _, err := m.st.SaveCertificate(ctx, cert)
	return saved, keyPEM, err
}

func parseCSR(csrPEM string) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(csrPEM)))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("invalid CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature: %w", err)
	}
	return csr, nil
}

func csrDomains(csr *x509.CertificateRequest) []string {
	out := []string{}
	for _, d := range csr.DNSNames {
		out = append(out, normalizeDomain(d))
	}
	if len(out) == 0 && csr.Subject.CommonName != "" {
		out = append(out, normalizeDomain(csr.Subject.CommonName))
	}
	return out
}

func normalizeDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

func publicKeyFingerprint(pub any) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func certFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func leafFromPEM(certPEM string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, errors.New("invalid certificate PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

func normalizePEMChain(certPEM string) string {
	certPEM = strings.TrimSpace(certPEM)
	if certPEM == "" {
		return ""
	}
	if !strings.HasSuffix(certPEM, "\n") {
		certPEM += "\n"
	}
	return certPEM
}

func statusFor(cert *x509.Certificate, days int) string {
	now := time.Now()
	if now.After(cert.NotAfter) {
		return "expired"
	}
	if now.Add(time.Duration(days) * 24 * time.Hour).After(cert.NotAfter) {
		return "expiring"
	}
	return "valid"
}

// newAccountKey creates a fresh ACME account key.
func newAccountKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}
