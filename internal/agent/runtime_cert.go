package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func EnsureSelfSignedCert(dir, host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "localhost"
	}
	certPath := filepath.Join(dir, "certs", "cert.pem")
	keyPath := filepath.Join(dir, "certs", "key.pem")
	if ok, _ := certificateValid(certPath, host); ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"MikvAce Panel"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip, net.ParseIP("127.0.0.1")}
	} else {
		tmpl.DNSNames = []string{host}
		if host != "localhost" {
			tmpl.DNSNames = append(tmpl.DNSNames, "localhost")
		}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDer, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})
	if err := os.WriteFile(certPath, certOut, 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, keyOut, 0o600)
}

func certificateValid(path, host string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return false, fmt.Errorf("invalid certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}
	if time.Now().After(cert.NotAfter.Add(-7 * 24 * time.Hour)) {
		return false, fmt.Errorf("certificate expiring")
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, got := range cert.IPAddresses {
			if got.Equal(ip) {
				return true, nil
			}
		}
		return false, nil
	}
	for _, name := range cert.DNSNames {
		if strings.EqualFold(name, host) {
			return true, nil
		}
	}
	return false, nil
}

// PrepareConfig rewrites the canonical /data paths when Hysteria runs as a
// local process instead of a sibling container.
