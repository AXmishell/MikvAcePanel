package logs

import (
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestParseZapConsole(t *testing.T) {
	raw := "2026-10-21T15:33:51+08:00\tINFO\tclient connected\t{\"addr\": \"203.0.113.9:38499\", \"id\": \"alice\", \"tx\": 1234}"
	e := Parse(raw, time.Time{})
	if e.Event != "connect" {
		t.Fatalf("event = %q, want connect", e.Event)
	}
	if e.UserID != "alice" {
		t.Fatalf("user = %q", e.UserID)
	}
	if e.ClientIP != "203.0.113.9" || e.Port != 38499 {
		t.Fatalf("client = %q:%d", e.ClientIP, e.Port)
	}
	if e.BytesTx != 1234 {
		t.Fatalf("tx = %d", e.BytesTx)
	}
	if e.Level != "INFO" {
		t.Fatalf("level = %q", e.Level)
	}
	if e.TS.IsZero() || e.TS.Year() != 2026 {
		t.Fatalf("timestamp = %v", e.TS)
	}
}

func TestParseWithLoggerField(t *testing.T) {
	raw := "2026-10-21T15:33:52.123Z\tWARN\tserver\tauthentication failed\t{\"addr\": \"198.51.100.7:5555\", \"id\": \"bob\", \"error\": \"bad password\"}"
	e := Parse(raw, time.Time{})
	if e.Event != "auth_fail" || e.Logger != "server" || e.UserID != "bob" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if e.Error != "bad password" {
		t.Fatalf("error field = %q", e.Error)
	}
}

func TestParseLogfmtAndANSI(t *testing.T) {
	raw := "\x1b[34mtime=2026-10-21T15:33:53Z level=error msg=\"server exploded\" addr=10.0.0.1:443\x1b[0m"
	e := Parse(raw, time.Time{})
	if e.Level != "ERROR" || e.Event != "error" {
		t.Fatalf("level/event = %q/%q", e.Level, e.Event)
	}
	if e.ClientIP != "10.0.0.1" {
		t.Fatalf("ip = %q", e.ClientIP)
	}
	if e.Message != "server exploded" {
		t.Fatalf("msg = %q", e.Message)
	}
}

func TestParseStartupAndFallbackTime(t *testing.T) {
	fallback := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	e := Parse("server up and running", fallback)
	if e.Event != "start" {
		t.Fatalf("event = %q", e.Event)
	}
	if !e.TS.Equal(fallback) {
		t.Fatalf("fallback timestamp not used: %v", e.TS)
	}
}

func TestTimelineBucketing(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	points := []struct {
		at int
	}{}
	_ = points
	got := bucketTimeline(nil, from, to)
	if len(got) != 25 {
		t.Fatalf("buckets = %d, want 25", len(got))
	}
	for _, p := range got {
		if !p.Time.Equal(p.Time.Truncate(time.Hour)) {
			t.Fatalf("unaligned bucket %v", p.Time)
		}
	}
}

func TestParseRange(t *testing.T) {
	nowT := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	from, to, err := ParseRange("24h", nowT)
	if err != nil {
		t.Fatal(err)
	}
	if to.Sub(from) != 24*time.Hour {
		t.Fatalf("range = %v", to.Sub(from))
	}
	if _, _, err := ParseRange("nope", nowT); err == nil {
		t.Fatal("expected error")
	}
}

var _ = model.LogEntry{}

func TestParseXrayAccessAndError(t *testing.T) {
	access := "2026/10/21 16:29:05.972187 from 198.51.100.5:48460 accepted tcp:example.com:443 [vless-in >> direct] email: alice"
	e := Parse(access, time.Time{})
	if e.Event != "connect" || e.UserID != "alice" || e.ClientIP != "198.51.100.5" {
		t.Fatalf("access entry = %+v", e)
	}
	if e.Host != "example.com" || e.Port != 443 {
		t.Fatalf("target = %s:%d", e.Host, e.Port)
	}
	api := "2026/10/21 16:29:06.972187 from 127.0.0.1:42724 accepted tcp:127.0.0.1:16101 [API_INBOUND -> API]"
	if e := Parse(api, time.Time{}); e.Event != "stats" {
		t.Fatalf("api event = %q", e.Event)
	}
	errLine := "2026/10/21 16:29:06.972187 [Warning] core: Xray 26.3.27 started"
	e = Parse(errLine, time.Time{})
	if e.Level != "WARN" || e.Event != "start" {
		t.Fatalf("error entry = %+v", e)
	}
}
