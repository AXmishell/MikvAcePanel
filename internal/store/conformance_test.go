package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestRemoteConformance(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		env    string
	}{
		{"postgres", "postgres", "TEST_POSTGRES_DSN"},
		{"mysql", "mysql", "TEST_MYSQL_DSN"},
	}
	for _, tc := range cases {
		tc := tc
		dsn := os.Getenv(tc.env)
		if dsn == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			st, err := OpenConfig(Config{Driver: tc.driver, DSN: dsn})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			ctx := context.Background()
			suffix := fmt.Sprintf("%d", time.Now().UnixNano())
			if err := st.EnsureDefaultSettings(ctx); err != nil {
				t.Fatal(err)
			}
			group, err := st.CreateGroup(ctx, &model.Group{Name: "conf-" + suffix, MaxDevices: 2})
			if err != nil {
				t.Fatal(err)
			}
			user, err := st.CreateUser(ctx, &model.User{
				UserID: "conf-" + suffix, Password: "pw", Enabled: true,
				TrafficLimit: 1000, ExpireAt: timePtr(time.Now().Add(time.Hour)),
			}, []int64{group.ID}, nil)
			if err != nil {
				t.Fatal(err)
			}
			node, err := st.CreateNode(ctx, &model.Node{
				Name: "conf-" + suffix, Type: "hysteria", Active: true, Port: 443,
			}, "hash-"+suffix, "pref", []int64{group.ID})
			if err != nil {
				t.Fatal(err)
			}
			nodes, err := st.NodesForUser(ctx, user.ID)
			if err != nil || len(nodes) != 1 || nodes[0].ID != node.ID {
				t.Fatalf("NodesForUser = %+v err=%v", nodes, err)
			}
			users, err := st.UsersForNode(ctx, node.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, u := range users {
				if u.ID == user.ID {
					found = true
				}
			}
			if !found {
				t.Fatal("user missing from UsersForNode")
			}
			cfg, created, err := st.SaveNodeConfigBundle(ctx, node.ID, "yaml", map[string]string{"hysteria": "yaml"}, "hash-"+suffix)
			if err != nil || !created || cfg.Revision != 1 {
				t.Fatalf("config = %+v created=%v err=%v", cfg, created, err)
			}
			if _, created, err := st.SaveNodeConfigBundle(ctx, node.ID, "yaml", map[string]string{"hysteria": "yaml"}, "hash-"+suffix); err != nil || created {
				t.Fatalf("duplicate config created=%v err=%v", created, err)
			}
			gotCfg, err := st.GetNodeConfig(ctx, node.ID, 0)
			if err != nil || gotCfg.Hash != "hash-"+suffix {
				t.Fatalf("GetNodeConfig = %+v err=%v", gotCfg, err)
			}
			if err := st.MarkConfigApplied(ctx, node.ID, cfg.Revision); err != nil {
				t.Fatal(err)
			}

			metric := &model.NodeMetric{
				NodeID: node.ID, TS: time.Now().UTC(), CPUPct: 12.5, MemUsed: 1, MemTotal: 2,
				Online: 1, TrafficTx: 10, TrafficRx: 20,
			}
			if err := st.InsertMetric(ctx, metric); err != nil {
				t.Fatal(err)
			}
			if metric.ID == 0 {
				t.Fatal("metric id = 0")
			}
			if _, _, err := st.TrafficSince(ctx, time.Now().Add(-time.Hour), node.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := st.TrafficSeries(ctx, time.Now().Add(-time.Hour), node.ID, false); err != nil {
				t.Fatal(err)
			}
			if _, err := st.LatestMetrics(ctx); err != nil {
				t.Fatal(err)
			}

			entry := model.LogEntry{
				TS: time.Now().UTC(), Level: "INFO", Logger: "test", Message: "hello",
				Event: "connect", UserID: user.UserID, ClientIP: "203.0.113.1",
			}
			inserted, err := st.InsertLogBatch(ctx, node.ID, "log-"+suffix, []model.LogEntry{entry})
			if err != nil || !inserted {
				t.Fatalf("log insert=%v err=%v", inserted, err)
			}
			if inserted, err := st.InsertLogBatch(ctx, node.ID, "log-"+suffix, []model.LogEntry{entry}); err != nil || inserted {
				t.Fatalf("duplicate log insert=%v err=%v", inserted, err)
			}
			logs, total, err := st.QueryLogs(ctx, model.LogFilter{NodeID: node.ID, Page: 1, Size: 10})
			if err != nil || total != 1 || len(logs) != 1 {
				t.Fatalf("logs=%d total=%d err=%v", len(logs), total, err)
			}
			if _, err := st.LogTimeline(ctx, model.LogStatsOptions{NodeID: node.ID, From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.LogNodeStats(ctx, model.LogStatsOptions{NodeID: node.ID}); err != nil {
				t.Fatal(err)
			}

			cert, created, err := st.SaveCertificate(ctx, &model.Certificate{
				Domains: []string{"example.test"}, CommonName: "example.test", CertPEM: "pem",
				Fingerprint: "fp-" + suffix, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			})
			if err != nil || !created || cert.ID == 0 {
				t.Fatalf("cert=%+v created=%v err=%v", cert, created, err)
			}
			if err := st.UpsertNodeCertificate(ctx, &model.NodeCertificate{
				NodeID: node.ID, CertID: cert.ID, Revision: 1, KeyFingerprint: "kf",
				Status: "active", AppliedFingerprint: "af",
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertNodeCertificate(ctx, &model.NodeCertificate{
				NodeID: node.ID, CertID: cert.ID, Revision: 2, KeyFingerprint: "kf2",
				Status: "active",
			}); err != nil {
				t.Fatal(err)
			}
			nc, err := st.GetNodeCertificate(ctx, node.ID)
			if err != nil || nc.Revision != 2 || nc.AppliedFingerprint != "af" {
				t.Fatalf("node cert=%+v err=%v", nc, err)
			}
			if err := st.SetNodeCertPending(ctx, node.ID, "kf3"); err != nil {
				t.Fatal(err)
			}
			nc, err = st.GetNodeCertificate(ctx, node.ID)
			if err != nil || nc.Status != "requesting" || nc.KeyFingerprint != "kf3" {
				t.Fatalf("pending node cert=%+v err=%v", nc, err)
			}

			key, err := st.CreateAPIKey(ctx, "conf-"+suffix, "pref", "keyhash-"+suffix, []string{"users:read"}, 10, nil)
			if err != nil || key.ID == 0 {
				t.Fatalf("api key=%+v err=%v", key, err)
			}
			if _, err := st.GetAPIKeyByHash(ctx, "keyhash-"+suffix); err != nil {
				t.Fatal(err)
			}
			if err := st.SetSetting(ctx, "conf_"+suffix, "v"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.GetSettings(ctx); err != nil {
				t.Fatal(err)
			}
			st.AddEvent(ctx, "test", node.ID, "info", "event")
			if _, err := st.ListEvents(ctx, node.ID, 10); err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.CountUsers(ctx); err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.SumNodeTraffic(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UsersOverQuota(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := st.CountLogs(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }
