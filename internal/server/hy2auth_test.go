package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestHy2AuthFailureLogsAreValidatedAndCapped(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	node, err := env.store.CreateNode(ctx, &model.Node{
		Name: "auth-node", Type: "hysteria", IP: "203.0.113.1", Port: 443,
		StatsPort: 9999, TLSMode: "selfsigned", Active: true,
	}, "auth-log-hash", "prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := func(nodeID int64) {
		env.do(t, http.MethodPost, fmt.Sprintf("/api/auth?node=%d", nodeID), "", map[string]any{
			"addr": "198.51.100.9:54321", "auth": "ghost:wrong",
		}).Body.Close()
	}

	// Unknown node ids must not create log rows.
	for i := 0; i < 5; i++ {
		auth(999999)
	}
	if n := countAuthFailLogs(t, env, node.ID); n != 0 {
		t.Fatalf("logs written for an unknown node: %d", n)
	}

	for i := 0; i < authFailureLogLimit+20; i++ {
		auth(node.ID)
	}
	if n := countAuthFailLogs(t, env, node.ID); n == 0 {
		t.Fatal("expected some failure logs")
	}
	var raw string
	if err := env.store.DB.QueryRowContext(ctx,
		`SELECT raw FROM hy2_logs WHERE node_id = ? AND event = 'auth_fail' LIMIT 1`, node.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) > 512 {
		t.Fatalf("auth log row is unexpectedly large: %d bytes", len(raw))
	}
}

func TestAllowAuthFailureLogCapsPerNode(t *testing.T) {
	s := &Server{authLogWindow: map[int64]authLogBucket{}}
	allowed := 0
	for i := 0; i < authFailureLogLimit+50; i++ {
		if s.allowAuthFailureLog(1) {
			allowed++
		}
	}
	if allowed != authFailureLogLimit {
		t.Fatalf("allowed %d logs, want %d", allowed, authFailureLogLimit)
	}
	// The limit is per node and must not block other nodes.
	if !s.allowAuthFailureLog(2) {
		t.Fatal("second node was affected by another node's limit")
	}
}

func countAuthFailLogs(t *testing.T, env *testEnv, nodeID int64) int {
	t.Helper()
	var n int
	if err := env.store.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM hy2_logs WHERE node_id = ? AND event = 'auth_fail'`, nodeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
