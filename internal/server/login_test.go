package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// captchaAnswer reads the expected answer straight from the in-memory challenge
// store. Tests run without Redis, so this mirrors what a human would read from
// the image.
func (e *testEnv) captchaAnswer(t *testing.T, id string) string {
	t.Helper()
	e.app.captchaMu.Lock()
	defer e.app.captchaMu.Unlock()
	entry, ok := e.app.captchas[id]
	if !ok {
		t.Fatalf("captcha %q not found", id)
	}
	return entry.answer
}

// loginWithCaptcha sends a password login and, when the server asks for a
// captcha, solves the challenge and retries once.
func (e *testEnv) loginWithCaptcha(t *testing.T, username, password string) *http.Response {
	t.Helper()
	resp := e.do(t, "POST", "/api/login", "", map[string]any{"username": username, "password": password})
	if resp.StatusCode != http.StatusUnauthorized {
		return resp
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var payload struct {
		CaptchaRequired bool `json:"captchaRequired"`
		Captcha         struct {
			ID string `json:"id"`
		} `json:"captcha"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || !payload.CaptchaRequired || payload.Captcha.ID == "" {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp
	}
	return e.do(t, "POST", "/api/login", "", map[string]any{
		"username": username, "password": password,
		"captchaId": payload.Captcha.ID, "captchaAnswer": e.captchaAnswer(t, payload.Captcha.ID),
	})
}

func TestLoginCaptchaAfterFailure(t *testing.T) {
	env := newTestEnv(t)

	// The first failure returns a challenge but does not require one for the
	// attempt that just failed.
	first := decode[map[string]any](t, env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "wrong",
	}))
	if first["captchaRequired"] != true {
		t.Fatalf("first failure = %+v", first)
	}
	captcha, _ := first["captcha"].(map[string]any)
	id, _ := captcha["id"].(string)
	image, _ := captcha["image"].(string)
	if id == "" || !strings.HasPrefix(image, "data:image/svg+xml;base64,") {
		t.Fatalf("captcha = %+v", captcha)
	}

	// Later attempts must include a captcha, even with the right password.
	second := decode[map[string]any](t, env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "testpass123",
	}))
	if second["captchaRequired"] != true {
		t.Fatalf("second attempt without captcha = %+v", second)
	}
	secondID, _ := second["captcha"].(map[string]any)["id"].(string)
	if secondID == "" {
		t.Fatalf("missing second challenge: %+v", second)
	}

	// A wrong answer is rejected and yields a fresh challenge.
	third := decode[map[string]any](t, env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "testpass123",
		"captchaId": secondID, "captchaAnswer": "ZZZZZ",
	}))
	if third["captchaRequired"] != true {
		t.Fatalf("wrong captcha = %+v", third)
	}
	thirdID, _ := third["captcha"].(map[string]any)["id"].(string)
	if thirdID == "" {
		t.Fatalf("missing third challenge: %+v", third)
	}

	// The right answer logs in.
	ok := decode[map[string]any](t, env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "testpass123",
		"captchaId": thirdID, "captchaAnswer": env.captchaAnswer(t, thirdID),
	}))
	if ok["token"] == nil {
		t.Fatalf("login with captcha = %+v", ok)
	}

	// A successful login clears the gate: password-only login works again.
	if resp := env.do(t, "POST", "/api/login", "", map[string]any{
		"username": "admin", "password": "testpass123",
	}); resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("login after success = %d: %s", resp.StatusCode, body)
	} else {
		resp.Body.Close()
	}
}

func TestCaptchaIsOneTimeAndExpires(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	challenge, err := env.app.issueLoginCaptcha(ctx)
	if err != nil {
		t.Fatal(err)
	}
	answer := env.captchaAnswer(t, challenge.ID)
	if !env.app.verifyLoginCaptcha(ctx, challenge.ID, strings.ToLower(answer)) {
		t.Fatal("lowercase answer should verify")
	}
	if env.app.verifyLoginCaptcha(ctx, challenge.ID, answer) {
		t.Fatal("a challenge must not be reusable")
	}

	env.app.captchaMu.Lock()
	env.app.captchas["expired"] = captchaEntry{answer: "ABCDE", expires: time.Now().Add(-time.Second)}
	env.app.captchaMu.Unlock()
	if env.app.verifyLoginCaptcha(ctx, "expired", "ABCDE") {
		t.Fatal("expired challenge should fail")
	}
}

func TestLoginRateLimitCannotBeBypassedWithForwardedHeaders(t *testing.T) {
	env := newTestEnv(t)
	attempt := func(xff string) int {
		body, _ := json.Marshal(map[string]any{"username": "admin", "password": "wrong"})
		req, err := http.NewRequest(http.MethodPost, env.srv.URL+"/api/login", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		resp, err := env.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	for i := 0; i < 10; i++ {
		if code := attempt(fmt.Sprintf("203.0.113.%d", i+10)); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", i, code)
		}
	}
	if code := attempt("198.51.100.9"); code != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF bypassed the limiter, got %d", code)
	}
}
