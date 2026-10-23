package server

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestExportLogsFormats(t *testing.T) {
	env := newTestEnv(t)
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	ok, err := env.store.InsertLogBatch(context.Background(), 0, "export-batch", []model.LogEntry{
		{TS: base, Level: "INFO", Event: "connect", UserID: "alice", ClientIP: "198.51.100.7", Message: `hello "world"`, Raw: "raw line"},
		{TS: base.Add(time.Second), Level: "ERROR", Event: "auth_fail", UserID: "bob", ClientIP: "198.51.100.8", Message: "bad\nline"},
	})
	if err != nil || !ok {
		t.Fatalf("insert logs: ok=%v err=%v", ok, err)
	}

	for _, tc := range []struct{ format, want string }{
		{"log", `user=alice ip=198.51.100.7`},
		{"csv", `"hello ""world"""`},
		{"jsonl", `"event":"connect"`},
	} {
		resp := env.do(t, "GET", "/api/logs/export?format="+tc.format, env.token, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("%s status = %d", tc.format, resp.StatusCode)
		}
		if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, tc.format) {
			t.Fatalf("%s content-disposition = %q", tc.format, cd)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), tc.want) {
			t.Fatalf("%s output missing %q:\n%s", tc.format, tc.want, body)
		}
		if tc.format == "log" && !strings.Contains(string(body), `msg=bad line`) {
			t.Fatalf("log output must flatten newlines:\n%s", body)
		}
	}

	// Filters are honoured by the export endpoint.
	resp := env.do(t, "GET", "/api/logs/export?format=log&level=ERROR", env.token, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "user=alice") || !strings.Contains(string(body), "user=bob") {
		t.Fatalf("level filter not applied:\n%s", body)
	}

	bad := env.do(t, "GET", "/api/logs/export?format=xml", env.token, nil)
	bad.Body.Close()
	if bad.StatusCode != 400 {
		t.Fatalf("invalid format status = %d", bad.StatusCode)
	}
}
