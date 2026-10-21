package celerity

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/axmishell/mikvacepanel/internal/store"
)

func writeCollection(t *testing.T, tw *tar.Writer, name string, docs []bson.M) {
	t.Helper()
	var raw bytes.Buffer
	for _, doc := range docs {
		b, err := bson.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		raw.Write(b)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	hdr := &tar.Header{
		Name: "celerity-backup-test/hysteria/" + name + ".bson.gz",
		Mode: 0o600, Size: int64(gz.Len()), ModTime: time.Now(), Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(gz.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestBuildDatabaseFromCelerityBackup(t *testing.T) {
	groupID := primitive.NewObjectID()
	hyNodeID := primitive.NewObjectID()
	xrayNodeID := primitive.NewObjectID()
	userID := primitive.NewObjectID()
	now := time.Now().UTC().Truncate(time.Second)

	archive := filepath.Join(t.TempDir(), "celerity-backup.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	writeCollection(t, tw, "servergroups", []bson.M{{
		"_id": groupID, "name": "Main", "description": "primary", "color": "#123456",
		"maxDevices": float64(3), "subscriptionTitle": "Main Sub",
	}})
	writeCollection(t, tw, "hynodes", []bson.M{
		{
			"_id": hyNodeID, "type": "hysteria", "name": "Hy One", "flag": "JP", "ip": "1.2.3.4",
			"port": float64(8443), "portRange": "20000-30000", "subscriptionVariants": "both",
			"statsPort": float64(9999), "statsSecret": "secret", "groups": []primitive.ObjectID{groupID},
			"active": true, "rankingCoefficient": float64(2), "traffic": bson.M{"tx": float64(11), "rx": float64(22)},
			"obfs":   bson.M{"type": "salamander", "password": "obfs"},
			"domain": "hy.example.com", "acme": bson.M{"email": "ops@example.com"},
			"masquerade": bson.M{"type": "proxy", "proxy": bson.M{"url": "https://example.com"}},
			"bandwidth":  bson.M{"up": "100 mbps", "down": "200 mbps"},
			"aclRules":   []string{"reject(all, udp/443)"},
			"outbounds":  []bson.M{{"name": "direct", "type": "direct"}},
			"ssh":        bson.M{"port": 2222, "username": "root"},
			"createdAt":  now,
		},
		{
			"_id": xrayNodeID, "type": "xray", "name": "Xray One", "ip": "5.6.7.8",
			"port": float64(443), "groups": []primitive.ObjectID{groupID}, "active": true,
			"xray": bson.M{
				"transport": "ws", "security": "tls", "wsPath": "/vless", "wsHost": "cdn.example.com",
				"alpn": []string{"h2"}, "inboundTag": "vless-in",
			},
		},
		{"_id": primitive.NewObjectID(), "type": "xray", "name": "XHTTP", "ip": "9.9.9.9",
			"xray": bson.M{"transport": "xhttp", "security": "tls"}},
		{"_id": primitive.NewObjectID(), "type": "virtual", "name": "Virtual", "ip": nil},
	})
	writeCollection(t, tw, "hyusers", []bson.M{
		{
			"_id": userID, "userId": "alice", "username": "Alice", "password": "secret123",
			"subscriptionToken": "subtoken", "xrayUuid": "11111111-1111-1111-1111-111111111111",
			"enabled": true, "groups": []primitive.ObjectID{groupID},
			"nodes":   []primitive.ObjectID{xrayNodeID},
			"traffic": bson.M{"tx": float64(100), "rx": float64(200)}, "trafficLimit": float64(1000),
			"maxDevices": float64(2), "expireAt": now.Add(24 * time.Hour), "createdAt": now,
		},
		{"_id": primitive.NewObjectID(), "userId": "probe", "password": "x", "isProbe": true},
	})
	writeCollection(t, tw, "admins", []bson.M{{
		"_id": primitive.NewObjectID(), "username": "celerity-admin",
		"passwordHash": "$2b$12$abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQ",
		"createdAt":    now,
	}})
	keyHash := hashToken("ck_0123456789abcdef0123456789abcdef0123456789abcdef")
	writeCollection(t, tw, "apikeys", []bson.M{{
		"_id": primitive.NewObjectID(), "name": "automation", "keyHash": keyHash,
		"keyPrefix": "ck_01234567", "scopes": []string{"users:read", "mcp:enabled"},
		"rateLimit": float64(30), "active": true, "createdAt": now,
	}})
	writeCollection(t, tw, "settings", []bson.M{{
		"_id": "settings", "deviceGracePeriod": 20,
		"backup": bson.M{"enabled": true, "intervalHours": 12, "keepLast": 5},
		"subscription": bson.M{
			"supportUrl": "https://t.me/support", "webPageUrl": "https://example.com",
			"happProviderId": "prov", "logoUrl": "https://example.com/logo.png",
			"pageTitle": "Cells", "pageNote": "Welcome", "updateInterval": 6,
			"buttons": []bson.M{{"label": "Telegram", "url": "https://t.me/x"}},
			"happ":    bson.M{"announce": "hello"},
		},
	}})

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ok, err := IsBackup(archive)
	if err != nil || !ok {
		t.Fatalf("IsBackup = %v, %v", ok, err)
	}

	out := filepath.Join(t.TempDir(), "panel.db")
	sum, err := BuildDatabase(context.Background(), archive, out, Options{
		FallbackAdminUsername: "admin", FallbackAdminPasswordHash: "fallback-hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Users != 1 || sum.Nodes != 2 || sum.Groups != 1 || sum.Admins != 2 || sum.APIKeys != 1 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	if sum.SkippedNodes != 2 || sum.SkippedUsers != 1 {
		t.Fatalf("unexpected skipped counts: %+v", sum)
	}

	st, err := store.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	users, _, err := st.ListUsers(ctx, "", "", 1, 100)
	if err != nil || len(users) != 1 {
		t.Fatalf("users = %d, %v", len(users), err)
	}
	u, err := st.GetUserByUserID(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !u.Enabled || u.TrafficTx != 100 || u.TrafficRx != 200 || u.TrafficLimit != 1000 || u.MaxDevices != 2 {
		t.Fatalf("unexpected user: %+v", u)
	}
	if len(u.Groups) != 1 || u.Groups[0].Name != "Main" {
		t.Fatalf("unexpected user groups: %+v", u.Groups)
	}
	if len(u.NodeIDs) != 1 {
		t.Fatalf("unexpected user nodes: %+v", u.NodeIDs)
	}

	nodes, err := st.ListNodes(ctx)
	if err != nil || len(nodes) != 2 {
		t.Fatalf("nodes = %d, %v", len(nodes), err)
	}
	var hy, xray bool
	for _, n := range nodes {
		if n.Name == "Hy One" {
			hy = true
			if n.Type != "hysteria" || n.TLSMode != "acme" || n.PortRange != "20000-30000" ||
				n.ObfsType != "salamander" || n.MasqueradeURL != "https://example.com" ||
				n.Ranking != 2 || n.SSHPort != 2222 || n.TrafficTx != 11 {
				t.Fatalf("unexpected hysteria node: %+v", n)
			}
		}
		if n.Name == "Xray One" {
			xray = true
			if n.Type != "xray" || n.Xray.Transport != "ws" || n.Xray.WSPath != "/vless" {
				t.Fatalf("unexpected xray node: %+v", n)
			}
		}
	}
	if !hy || !xray {
		t.Fatalf("missing imported nodes: %+v", nodes)
	}

	settings, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings["page_title"] != "Cells" || settings["support_url"] != "https://t.me/support" ||
		settings["subscription_update_interval"] != "6" || settings["backup_interval_hours"] != "12" ||
		settings["device_grace_minutes"] != "20" {
		t.Fatalf("unexpected settings: %+v", settings)
	}
	key, err := st.GetAPIKeyByHash(ctx, keyHash)
	if err != nil {
		t.Fatalf("api key not imported: %v", err)
	}
	if !key.Enabled || key.RateLimit != 30 || len(key.Scopes) != 1 || key.Scopes[0] != "users:read" {
		t.Fatalf("unexpected api key: %+v", key)
	}
	if _, err := st.GetAdminByUsername(ctx, "celerity-admin"); err != nil {
		t.Fatalf("celerity admin not imported: %v", err)
	}
	if _, err := st.GetAdminByUsername(ctx, "admin"); err != nil {
		t.Fatalf("fallback admin not imported: %v", err)
	}
}
