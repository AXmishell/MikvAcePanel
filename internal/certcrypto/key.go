// Package certcrypto protects panel-held secrets (private keys, DNS provider
// credentials and ACME account keys) at rest.
package certcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const keyFile = "cert.key"

// LoadKey returns the panel certificate encryption key. CERT_ENC_KEY may be a
// hex or base64 encoded 32 byte key; otherwise a key file is created under the
// data directory with mode 0600.
func LoadKey(dataDir string) ([]byte, error) {
	if raw := strings.TrimSpace(os.Getenv("CERT_ENC_KEY")); raw != "" {
		if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
			return b, nil
		}
		if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
			return b, nil
		}
		if b, err := base64.RawURLEncoding.DecodeString(raw); err == nil && len(b) == 32 {
			return b, nil
		}
		return nil, fmt.Errorf("CERT_ENC_KEY must be 32 bytes encoded as hex or base64")
	}
	path := filepath.Join(dataDir, keyFile)
	// A database restore may stage a matching encryption key. Swap it in
	// before loading, otherwise encrypted secrets in the restored database
	// would be unreadable.
	if _, err := os.Stat(path + ".restore"); err == nil {
		_ = os.Rename(path+".restore", path)
	}
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b, nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// Encrypt seals plaintext with AES-256-GCM. Both returned values are base64.
func Encrypt(key, plaintext []byte) (cipherText, nonce string, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	n := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return "", "", err
	}
	sealed := gcm.Seal(nil, n, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), base64.StdEncoding.EncodeToString(n), nil
}

// Decrypt opens a value produced by Encrypt.
func Decrypt(key []byte, cipherText, nonce string) ([]byte, error) {
	ct, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		return nil, err
	}
	n, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(n) != gcm.NonceSize() {
		return nil, fmt.Errorf("invalid nonce")
	}
	return gcm.Open(nil, n, ct, nil)
}

// EncryptString is a convenience wrapper returning a single opaque token.
func EncryptString(key []byte, plain string) (string, error) {
	ct, nonce, err := Encrypt(key, []byte(plain))
	if err != nil {
		return "", err
	}
	return ct + "." + nonce, nil
}

// DecryptString reverses EncryptString.
func DecryptString(key []byte, token string) (string, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid encrypted value")
	}
	b, err := Decrypt(key, parts[0], parts[1])
	if err != nil {
		return "", err
	}
	return string(b), nil
}
