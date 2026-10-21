package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/totp"
)

func TestTwoFactorLoginFlow(t *testing.T) {
	env := newTestEnv(t)

	setup := decode[map[string]any](t, env.do(t, "POST", "/api/2fa/setup", env.token, nil))
	secret, _ := setup["secret"].(string)
	if secret == "" || setup["qr"] == nil {
		t.Fatalf("setup = %+v", setup)
	}
	codes, _ := setup["recoveryCodes"].([]any)
	if len(codes) != 10 {
		t.Fatalf("recovery codes = %d", len(codes))
	}
	code, err := totp.Code(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp := env.do(t, "POST", "/api/2fa/enable", env.token, map[string]any{"code": code})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable status = %d", resp.StatusCode)
	}

	// Password login now returns a challenge instead of a session token.
	login := decode[map[string]any](t, env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "testpass123",
	}))
	if login["twoFactorRequired"] != true {
		t.Fatalf("login = %+v", login)
	}
	challenge, _ := login["challenge"].(string)
	if challenge == "" {
		t.Fatal("missing challenge")
	}
	// A challenge must not be usable as a session.
	if resp := env.do(t, "GET", "/api/2fa", challenge, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("challenge used as session: %d", resp.StatusCode)
	}
	// Wrong code is rejected.
	if resp := env.do(t, "POST", "/api/login/2fa", "", map[string]any{"challenge": challenge, "code": "abcdef"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code status = %d", resp.StatusCode)
	}
	code, _ = totp.Code(secret, time.Now())
	ok := decode[map[string]any](t, env.do(t, "POST", "/api/login/2fa", "", map[string]any{"challenge": challenge, "code": code}))
	token, _ := ok["token"].(string)
	if token == "" {
		t.Fatalf("2fa login = %+v", ok)
	}
	for _, path := range []string{"/api/me", "/api/nodes"} {
		if resp := env.do(t, "GET", path, token, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, resp.StatusCode)
		}
	}
	// The same challenge cannot be replayed.
	code, _ = totp.Code(secret, time.Now())
	if resp := env.do(t, "POST", "/api/login/2fa", "", map[string]any{"challenge": challenge, "code": code}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed challenge status = %d", resp.StatusCode)
	}

	// Recovery code login, then the code is consumed.
	recovery := codes[0].(string)
	login = decode[map[string]any](t, env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "testpass123",
	}))
	challenge, _ = login["challenge"].(string)
	if challenge == "" {
		t.Fatal("missing recovery challenge")
	}
	ok = decode[map[string]any](t, env.do(t, "POST", "/api/login/2fa", "", map[string]any{"challenge": challenge, "code": recovery}))
	if ok["token"] == nil {
		t.Fatalf("recovery login = %+v", ok)
	}
	login = decode[map[string]any](t, env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "testpass123",
	}))
	challenge, _ = login["challenge"].(string)
	if resp := env.do(t, "POST", "/api/login/2fa", "", map[string]any{"challenge": challenge, "code": recovery}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused recovery code status = %d", resp.StatusCode)
	}
}
