package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestReconcileAccessRemovesExpiredAndOverQuotaXrayUsers(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	node, err := env.store.CreateNode(ctx, &model.Node{
		Name: "xray-node", Type: "xray", IP: "203.0.113.20", Port: 443,
		StatsPort: 9999, TLSMode: "selfsigned", Active: true,
		Xray: model.XraySettings{Transport: "tcp", Security: "none"},
	}, "xray-node-hash", "prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	active, err := env.store.CreateUser(ctx, &model.User{
		UserID: "active", Password: "pw", Enabled: true, XrayUUID: "11111111-1111-4111-8111-111111111111",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-time.Hour).UTC()
	expired, err := env.store.CreateUser(ctx, &model.User{
		UserID: "expired", Password: "pw", Enabled: true, ExpireAt: &expiredAt,
		XrayUUID: "22222222-2222-4222-8222-222222222222",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	over, err := env.store.CreateUser(ctx, &model.User{
		UserID: "over", Password: "pw", Enabled: true, TrafficLimit: 10, TrafficTx: 11,
		XrayUUID: "33333333-3333-4333-8333-333333333333",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := env.app.ReconcileAccess(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err := env.store.GetNodeConfig(ctx, node.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.YAML, active.XrayUUID) {
		t.Fatal("active user missing from xray config")
	}
	if strings.Contains(cfg.YAML, expired.XrayUUID) || strings.Contains(cfg.YAML, over.XrayUUID) {
		t.Fatal("expired/over-quota user still present in xray config")
	}
	got, _ := env.store.GetUserByID(ctx, expired.ID)
	if got.Enabled {
		t.Fatal("expired user was not disabled")
	}

	// Resetting traffic must put the user back on the next reconciliation.
	if err := env.store.ResetUserTraffic(ctx, over.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.app.ReconcileAccess(ctx); err != nil {
		t.Fatal(err)
	}
	cfg, err = env.store.GetNodeConfig(ctx, node.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.YAML, over.XrayUUID) {
		t.Fatal("user was not restored after traffic reset")
	}
}
