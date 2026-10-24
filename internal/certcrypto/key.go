// Package certcrypto protects panel-held secrets (private keys, DNS provider
// credentials and ACME account keys) at rest.
package certcrypto

import (
	"bytes"
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

// LoadKey returns the panel certificate encryption key. keyPath may point to a
// 32-byte raw key, or a hex/base64 encoded key. When empty, DATA_DIR/cert.key
// is created with mode 0600.
func LoadKey(dataDir string, keyPath ...string) ([]byte, error) {
	if len(keyPath) > 0 && strings.TrimSpace(keyPath[0]) != "" {
		b, err := os.ReadFile(strings.TrimSpace(keyPath[0]))
		if err != nil {
			return nil, fmt.Errorf("security.cert_key_file: %w", err)
		}
		if key, ok := decodeKey(b); ok {
			return key, nil
		}
		return nil, fmt.Errorf("security.cert_key_file %q must contain 32 raw bytes or a hex/base64 encoded key", keyPath[0])
	}
	path := filepath.Join(dataDir, keyFile)
	// A database restore may stage a matching encryption key. Swap it in
	// before loading, otherwise encrypted secrets in the restored database
	// would be unreadable.
	if _, err := os.Stat(path + ".restore"); err == nil {
		_ = os.Rename(path+".restore", path)
	}
	if b, err := os.ReadFile(path); err == nil {
		if key, ok := decodeKey(b); ok {
			return key, nil
		}
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

// decodeKey accepts a raw 32-byte key or a hex/base64 encoded representation.
func decodeKey(raw []byte) ([]byte, bool) {
	if len(raw) == 32 {
		return raw, true
	}
	b := bytes.TrimSpace(raw)
	if len(b) == 32 {
		return b, true
	}
	if decoded, err := hex.DecodeString(string(b)); err == nil && len(decoded) == 32 {
		return decoded, true
	}
	if decoded, err := base64.StdEncoding.DecodeString(string(b)); err == nil && len(decoded) == 32 {
		return decoded, true
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(string(b)); err == nil && len(decoded) == 32 {
		return decoded, true
	}
	return nil, false
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
