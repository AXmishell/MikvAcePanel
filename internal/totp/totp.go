// Package totp implements RFC 6238 time-based one-time passwords without
// external dependencies.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	Period = 30
	Digits = 6
)

var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns a new 160-bit base32 secret.
func GenerateSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32NoPad.EncodeToString(b), nil
}

// Code returns the TOTP code for the given secret and time.
func Code(secret string, t time.Time) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, uint64(t.Unix()/Period))
}

// Validate accepts a code from the previous, current or next time step.
func Validate(secret, code string, t time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != Digits {
		return false
	}
	for _, offset := range []int64{-1, 0, 1} {
		want, err := Code(secret, t.Add(time.Duration(offset)*Period*time.Second))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func decodeSecret(secret string) ([]byte, error) {
	secret = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	secret = strings.TrimRight(secret, "=")
	return base32NoPad.DecodeString(secret)
}

func hotp(key []byte, counter uint64) (string, error) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%0*d", Digits, value%1_000_000), nil
}

// OTPAuthURL builds a standard otpauth:// URI for authenticator apps.
func OTPAuthURL(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprint(Digits))
	v.Set("period", fmt.Sprint(Period))
	return "otpauth://totp/" + label + "?" + v.Encode()
}

// GenerateRecoveryCodes returns n human-readable one-time recovery codes.
func GenerateRecoveryCodes(n int) ([]string, error) {
	out := make([]string, 0, n)
	seen := map[string]bool{}
	for len(out) < n {
		b := make([]byte, 6)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		raw := base32NoPad.EncodeToString(b)[:10]
		code := raw[:5] + "-" + raw[5:]
		if seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	return out, nil
}

// NormalizeRecovery canonicalises a recovery code for hashing.
func NormalizeRecovery(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}

// HashRecovery returns a keyed hash of a recovery code.
func HashRecovery(key []byte, code string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(NormalizeRecovery(code)))
	return hex.EncodeToString(mac.Sum(nil))
}
