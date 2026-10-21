package totp

import (
	"testing"
	"time"
)

func TestCodeRFC6238(t *testing.T) {
	// RFC 6238 uses the ASCII secret "12345678901234567890".
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, tc := range cases {
		got, err := Code(secret, time.Unix(tc.unix, 0).UTC())
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("code at %d = %s, want %s", tc.unix, got, tc.want)
		}
	}
}

func TestValidateWindow(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0).UTC()
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if !Validate(secret, code, now) {
		t.Fatal("current code rejected")
	}
	if !Validate(secret, code, now.Add(30*time.Second)) {
		t.Fatal("previous-step code rejected")
	}
	if Validate(secret, "000000", now) {
		t.Fatal("wrong code accepted")
	}
	if Validate(secret, "12345", now) {
		t.Fatal("short code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("got %d codes", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' {
			t.Fatalf("bad format %q", c)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q", c)
		}
		seen[c] = true
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	if HashRecovery(key, codes[0]) != HashRecovery(key, NormalizeRecovery(codes[0])) {
		t.Fatal("normalization mismatch")
	}
	if HashRecovery(key, codes[0]) == HashRecovery(key, codes[1]) {
		t.Fatal("different codes hashed identically")
	}
}

func TestOTPAuthURL(t *testing.T) {
	u := OTPAuthURL("MikvAce", "admin", "JBSWY3DPEHPK3PXP")
	for _, want := range []string{"otpauth://totp/", "secret=JBSWY3DPEHPK3PXP", "issuer=MikvAce", "digits=6", "period=30"} {
		if !contains(u, want) {
			t.Fatalf("URL %q missing %q", u, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
