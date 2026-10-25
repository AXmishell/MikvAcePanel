package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, context.Background()
}

func TestUserAndGroupLifecycle(t *testing.T) {
	st, ctx := testStore(t)
	if err := st.EnsureDefaultSettings(ctx); err != nil {
		t.Fatal(err)
	}
	g, err := st.CreateGroup(ctx, &model.Group{Name: "EU", Color: "#fff", MaxDevices: 3})
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, &model.User{
		UserID: "alice", Username: "Alice", Password: "pw", Enabled: true, TrafficLimit: 100,
	}, []int64{g.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.SubscriptionToken == "" || len(u.Groups) != 1 {
		t.Fatalf("created user = %+v", u)
	}
	byToken, err := st.GetUserByToken(ctx, u.SubscriptionToken)
	if err != nil || byToken.UserID != "alice" {
		t.Fatalf("token lookup: %v %+v", err, byToken)
	}
	if err := st.AddUserTraffic(ctx, "alice", 70, 40); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetUserByID(ctx, u.ID)
	if got.TrafficUsed() != 110 || !got.TrafficExceeded() {
		t.Fatalf("traffic = %d exceeded=%v", got.TrafficUsed(), got.TrafficExceeded())
	}
	total, active, err := st.CountUsers(ctx)
	if err != nil || total != 1 || active != 1 {
		t.Fatalf("counts = %d/%d %v", total, active, err)
	}
	if err := st.ResetUserTraffic(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetUserByID(ctx, u.ID)
	if got.TrafficUsed() != 0 {
		t.Fatalf("traffic not reset: %d", got.TrafficUsed())
	}
	if err := st.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetUserByID(ctx, u.ID); err != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestNodesForUser(t *testing.T) {
	st, ctx := testStore(t)
	if err := st.EnsureDefaultSettings(ctx); err != nil {
		t.Fatal(err)
	}
	g, _ := st.CreateGroup(ctx, &model.Group{Name: "EU"})
	mkNode := func(name string, groupIDs []int64) *model.Node {
		n, err := st.CreateNode(ctx, &model.Node{
			Name: name, IP: "203.0.113.1", Port: 443, Active: true, StatsPort: 9999,
			TLSMode: "selfsigned", Ranking: 1,
		}, "hash-"+name, "prefix", groupIDs)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	grouped := mkNode("grouped", []int64{g.ID})
	public := mkNode("public", nil)

	u, _ := st.CreateUser(ctx, &model.User{UserID: "u1", Password: "pw", Enabled: true}, []int64{g.ID}, nil)
	nodes, err := st.NodesForUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, n := range nodes {
		ids[n.ID] = true
	}
	if !ids[grouped.ID] || !ids[public.ID] {
		t.Fatalf("user did not receive expected nodes: %v", ids)
	}

	u2, _ := st.CreateUser(ctx, &model.User{UserID: "u2", Password: "pw", Enabled: true}, nil, nil)
	nodes2, _ := st.NodesForUser(ctx, u2.ID)
	if len(nodes2) != 1 || nodes2[0].ID != public.ID {
		t.Fatalf("ungrouped user got %+v", nodes2)
	}

	u3, _ := st.CreateUser(ctx, &model.User{UserID: "u3", Password: "pw", Enabled: true}, nil, []int64{grouped.ID})
	nodes3, _ := st.NodesForUser(ctx, u3.ID)
	if len(nodes3) != 1 || nodes3[0].ID != grouped.ID {
		t.Fatalf("direct assignment got %+v", nodes3)
	}
}

func TestUsersForNodeSkipsExpiredAndOverQuota(t *testing.T) {
	st, ctx := testStore(t)
	node, err := st.CreateNode(ctx, &model.Node{
		Name: "xray", Type: "xray", IP: "203.0.113.1", Port: 443, Active: true,
		StatsPort: 9999, TLSMode: "selfsigned",
	}, "hash-xray", "prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	expiredAt := time.Now().Add(-time.Hour).UTC()
	active, err := st.CreateUser(ctx, &model.User{UserID: "active", Password: "pw", Enabled: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := st.CreateUser(ctx, &model.User{UserID: "expired", Password: "pw", Enabled: true, ExpireAt: &expiredAt}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	over, err := st.CreateUser(ctx, &model.User{
		UserID: "over", Password: "pw", Enabled: true, TrafficLimit: 100, TrafficTx: 60, TrafficRx: 50,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, &model.User{UserID: "disabled", Password: "pw", Enabled: false}, nil, nil); err != nil {
		t.Fatal(err)
	}
	users, err := st.UsersForNode(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, u := range users {
		got[u.UserID] = true
	}
	if !got["active"] || got["expired"] || got["over"] || got["disabled"] {
		t.Fatalf("UsersForNode = %v", got)
	}
	if err := st.ResetUserTraffic(ctx, over.ID); err != nil {
		t.Fatal(err)
	}
	users, err = st.UsersForNode(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	got = map[string]bool{}
	for _, u := range users {
		got[u.UserID] = true
	}
	if !got["over"] || !got[active.UserID] || got[expired.UserID] || got["disabled"] {
		t.Fatalf("after reset UsersForNode = %v", got)
	}
}

func TestLogBatchDeduplication(t *testing.T) {
	st, ctx := testStore(t)
	entry := model.LogEntry{TS: now(), Level: "INFO", Message: "hello", Event: "log"}
	ok, err := st.InsertLogBatch(ctx, 1, "batch-1", []model.LogEntry{entry})
	if err != nil || !ok {
		t.Fatalf("insert = %v %v", ok, err)
	}
	ok, err = st.InsertLogBatch(ctx, 1, "batch-1", []model.LogEntry{entry})
	if err != nil || ok {
		t.Fatalf("duplicate accepted: %v %v", ok, err)
	}
	logs, total, err := st.QueryLogs(ctx, model.LogFilter{Page: 1, Size: 10})
	if err != nil || total != 1 || len(logs) != 1 {
		t.Fatalf("query = %d %d %v", total, len(logs), err)
	}
}

func TestStatsBatchIdempotent(t *testing.T) {
	st, ctx := testStore(t)
	_, _ = st.CreateUser(ctx, &model.User{UserID: "alice", Password: "pw", Enabled: true}, nil, nil)
	users := map[string]model.UserTraffic{"alice": {Tx: 10, Rx: 20}}
	ok, err := st.ApplyStatsBatch(ctx, 1, "s1", users)
	if err != nil || !ok {
		t.Fatal(err)
	}
	ok, _ = st.ApplyStatsBatch(ctx, 1, "s1", users)
	if ok {
		t.Fatal("duplicate stats batch applied")
	}
	u, _ := st.GetUserByUserID(ctx, "alice")
	if u.TrafficTx != 10 || u.TrafficRx != 20 {
		t.Fatalf("traffic = %+v", u)
	}
}

func TestNodeConfigRevision(t *testing.T) {
	st, ctx := testStore(t)
	n, err := st.CreateNode(ctx, &model.Node{Name: "n", IP: "1.2.3.4", Port: 443, Active: true, StatsPort: 9999, TLSMode: "selfsigned"}, "h", "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	c1, created, err := st.SaveNodeConfig(ctx, n.ID, "a: 1\n", "hash1")
	if err != nil || !created || c1.Revision != 1 {
		t.Fatalf("first config = %+v %v %v", c1, created, err)
	}
	c2, created, err := st.SaveNodeConfig(ctx, n.ID, "a: 1\n", "hash1")
	if err != nil || created || c2.Revision != 1 {
		t.Fatalf("same hash = %+v %v %v", c2, created, err)
	}
	c3, created, _ := st.SaveNodeConfig(ctx, n.ID, "a: 2\n", "hash2")
	if !created || c3.Revision != 2 {
		t.Fatalf("second revision = %+v %v", c3, created)
	}
}

func TestPendingRestoreSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(context.Background(), &model.User{UserID: "restored", Password: "pw", Enabled: true}, nil, nil); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "snapshot.db")
	if _, err := st.DB.ExecContext(context.Background(), `VACUUM INTO ?`, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(snapshot, path+".restore"); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	u, err := st2.GetUserByUserID(context.Background(), "restored")
	if err != nil || u.UserID != "restored" {
		t.Fatalf("restored user = %+v %v", u, err)
	}
}

func TestNodeCoresAndConfigBundle(t *testing.T) {
	st, ctx := testStore(t)
	n, err := st.CreateNode(ctx, &model.Node{
		Name: "hybrid", Type: "hysteria", Cores: []string{"hysteria", "xray"},
		IP: "203.0.113.20", Port: 443, Active: true, StatsPort: 9999, TLSMode: "selfsigned",
		Xray: model.XraySettings{Inbounds: []model.XrayInbound{{Protocol: "vless", Tag: "vless-in", Port: 8443}}},
	}, "hash", "prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Cores) != 2 || !n.HasCore("xray") {
		t.Fatalf("cores = %v", n.Cores)
	}
	if len(n.Xray.Inbounds) != 1 || n.Xray.Inbounds[0].Port != 8443 {
		t.Fatalf("inbounds = %+v", n.Xray.Inbounds)
	}
	configs := map[string]string{"hysteria": "a: 1\n", "xray": `{"inbounds":[]}`}
	cfg, created, err := st.SaveNodeConfigBundle(ctx, n.ID, configs["hysteria"], configs, "bundle-hash")
	if err != nil || !created {
		t.Fatalf("save bundle: %v created=%v", err, created)
	}
	got, err := st.GetNodeConfig(ctx, n.ID, cfg.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got.Configs["xray"] != configs["xray"] || got.YAML != configs["hysteria"] {
		t.Fatalf("config roundtrip = %+v", got)
	}
}

func TestNodeHy2SettingsRoundtrip(t *testing.T) {
	st, ctx := testStore(t)
	n, err := st.CreateNode(ctx, &model.Node{
		Name: "hy2", IP: "203.0.113.9", Port: 443, Active: true, StatsPort: 9999, TLSMode: "selfsigned",
		Hy2: model.Hy2Settings{
			QUIC:               &model.Hy2QUIC{MaxIdleTimeout: "30s", MaxIncomingStreams: 512},
			Congestion:         &model.Hy2Congestion{Type: "bbr", BBRProfile: "aggressive"},
			Sniff:              &model.Hy2Sniff{Enable: true, TCPPorts: "80,443"},
			ClientCA:           "-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n",
			ECHPEM:             "-----BEGIN ECH KEYS-----\nAA==\n-----END ECH KEYS-----\n",
			ECHConfig:          "AEz+DQBIAAAg",
			ListenPortRange:    "20000-50000",
			ExtraYAML:          "custom: true\n",
			GeckoMinPacketSize: 512,
		},
	}, "hash", "prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetNode(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hy2.Congestion == nil || got.Hy2.Congestion.BBRProfile != "aggressive" {
		t.Fatalf("congestion = %+v", got.Hy2.Congestion)
	}
	if got.Hy2.ECHConfig != "AEz+DQBIAAAg" || got.Hy2.ECHPEM == "" {
		t.Fatalf("ech not persisted: %+v", got.Hy2)
	}
	if got.Hy2.ClientCA == "" || got.Hy2.ListenPortRange != "20000-50000" {
		t.Fatalf("hy2 fields lost: %+v", got.Hy2)
	}
	got.Hy2.Congestion = &model.Hy2Congestion{Type: "reno"}
	got.Hy2.ExtraYAML = ""
	if _, err := st.UpdateNode(ctx, got, nil); err != nil {
		t.Fatal(err)
	}
	again, err := st.GetNode(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Hy2.Congestion.Type != "reno" || again.Hy2.ExtraYAML != "" {
		t.Fatalf("update not persisted: %+v", again.Hy2)
	}
	if again.Hy2.ECHPEM == "" {
		t.Fatal("update must preserve ECH PEM")
	}
}

func TestUpdateNodeVersionsPartial(t *testing.T) {
	st, ctx := testStore(t)
	n, err := st.CreateNode(ctx, &model.Node{
		Name: "versions", IP: "203.0.113.30", Port: 443, Active: true, StatsPort: 9999, TLSMode: "selfsigned",
	}, "hash", "prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	hy2 := "v2.14.0"
	if err := st.UpdateNodeVersions(ctx, n.ID, NodeVersionUpdate{Hy2Version: &hy2}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetNode(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hy2Version != "v2.14.0" || got.AgentVersion != "" {
		t.Fatalf("partial update = %+v", got)
	}
	agent, runtimeName, xray := "sha-test", "process", ""
	if err := st.UpdateNodeVersions(ctx, n.ID, NodeVersionUpdate{
		AgentVersion: &agent, XrayVersion: &xray, Runtime: &runtimeName,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetNode(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentVersion != "sha-test" || got.XrayVersion != "" || got.Runtime != "process" || got.Hy2Version != "v2.14.0" {
		t.Fatalf("merged update = %+v", got)
	}
}
