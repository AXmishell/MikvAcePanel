package model

import "time"

// Certificate is an X.509 certificate managed by the panel.
//
// CertPEM always contains the leaf and any intermediate certificates. KeyPEM
// is only populated when the panel holds the private key (uploaded
// certificates or shared wildcard certificates); certificates issued from a
// node CSR never carry a private key.
type Certificate struct {
	ID          int64     `json:"id"`
	Domains     []string  `json:"domains"`
	CommonName  string    `json:"commonName"`
	CertPEM     string    `json:"-"`
	KeyPEM      string    `json:"-"`
	KeyCipher   string    `json:"-"`
	KeyNonce    string    `json:"-"`
	Fingerprint string    `json:"fingerprint"`
	KeyFinger   string    `json:"keyFingerprint"`
	Issuer      string    `json:"issuer"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
	Source      string    `json:"source"` // acme | upload
	Status      string    `json:"status"` // valid | expiring | expired | revoked
	AutoRenew   bool      `json:"autoRenew"`
	LastError   string    `json:"lastError"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// NodeCertificate links a node to the certificate it should serve.
type NodeCertificate struct {
	NodeID             int64      `json:"nodeId"`
	CertID             int64      `json:"certId"`
	Revision           int64      `json:"revision"`
	KeyFingerprint     string     `json:"keyFingerprint"`
	AppliedFingerprint string     `json:"appliedFingerprint"`
	Status             string     `json:"status"` // pending|requesting|issued|pushing|active|error
	LastError          string     `json:"lastError"`
	RequestedAt        *time.Time `json:"requestedAt,omitempty"`
	AppliedAt          *time.Time `json:"appliedAt,omitempty"`
}

// ACMEAccount stores a persisted ACME account key for one CA/email pair.
type ACMEAccount struct {
	ID         int64     `json:"id"`
	CAURL      string    `json:"caUrl"`
	Email      string    `json:"email"`
	KeyCipher  string    `json:"-"`
	KeyNonce   string    `json:"-"`
	AccountURL string    `json:"accountUrl"`
	CreatedAt  time.Time `json:"createdAt"`
}
