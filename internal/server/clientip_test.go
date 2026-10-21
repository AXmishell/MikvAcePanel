package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustBoundary(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.5:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := s.clientIP(req); got != "198.51.100.5" {
		t.Fatalf("untrusted peer should ignore XFF, got %q", got)
	}

	s.trustedProxies = parseTrustedProxies([]string{"10.0.0.0/8"})
	req.RemoteAddr = "10.1.2.3:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.9.8.7")
	if got := s.clientIP(req); got != "1.2.3.4" {
		t.Fatalf("trusted proxy chain = %q", got)
	}

	req.Header.Del("X-Forwarded-For")
	req.Header.Set("X-Real-IP", "5.6.7.8")
	if got := s.clientIP(req); got != "5.6.7.8" {
		t.Fatalf("X-Real-IP fallback = %q", got)
	}

	req.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := s.clientIP(req); got != "10.1.2.3" {
		t.Fatalf("malformed chain should fall back to peer, got %q", got)
	}
}
