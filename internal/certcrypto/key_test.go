package certcrypto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plain := []byte("-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----")
	cipherText, nonce, err := Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(key, cipherText, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: %q", got)
	}
	if _, err := Decrypt(bytes.Repeat([]byte{8}, 32), cipherText, nonce); err == nil {
		t.Fatal("expected authentication failure with a different key")
	}
}

func TestEncryptString(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	token, err := EncryptString(key, `{"apiToken":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptString(key, token)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"apiToken":"x"}` {
		t.Fatalf("got %q", got)
	}
	if _, err := DecryptString(key, "broken"); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadKeyCreatesFile(t *testing.T) {
	dir := t.TempDir()
	key, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d", len(key))
	}
	info, err := os.Stat(filepath.Join(dir, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v", info.Mode().Perm())
	}
	again, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, again) {
		t.Fatal("key was not reused")
	}
}
