package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

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
