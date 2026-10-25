package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/crypto/ssh"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

type testEnv struct {
	srv   *httptest.Server
	store *store.Store
	app   *Server
	token string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := Config{
		Addr: ":0", DataDir: dir, DBPath: filepath.Join(dir, "panel.db"),
		PublicURL: "http://panel.test", AdminUsername: "admin", AdminPassword: "testpass123",
		SessionTTL: time.Hour,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := New(cfg, st, logger)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(app.Handler())
	t.Cleanup(ts.Close)
	env := &testEnv{srv: ts, store: st, app: app}
	env.token = env.login(t)
	return env
}

func (e *testEnv) login(t *testing.T) string {
	t.Helper()
	resp := e.do(t, "POST", "/api/login", "", map[string]any{"username": "admin", "password": "testpass123"})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Token
}

func (e *testEnv) do(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPanelEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	if resp := env.do(t, "GET", "/api/users", "", nil); resp.StatusCode != 401 {
		t.Fatalf("unauthorized users = %d", resp.StatusCode)
	}
	group := decode[model.Group](t, env.do(t, "POST", "/api/groups", env.token, map[string]any{"name": "EU"}))
	if group.ID == 0 {
		t.Fatal("group not created")
	}
	nodeResp := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "Tokyo", "flag": "JP", "ip": "203.0.113.10", "port": 443,
		"portRange": "20000-50000", "hopInterval": "30s", "tlsMode": "selfsigned",
		"active": true, "groupIds": []int64{group.ID}, "acl": []string{"direct(all)"},
	}))
	nodeObj, _ := nodeResp["node"].(map[string]any)
	nodeID := int64(nodeObj["id"].(float64))
	if nodeID == 0 {
		t.Fatalf("node not created: %+v", nodeResp)
	}
	created := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "alice", "username": "Alice", "password": "secretpass", "enabled": true,
		"trafficLimitGb": 5, "maxDevices": 2, "groupIds": []int64{group.ID},
	}))
	subToken, _ := created["subscriptionToken"].(string)
	if subToken == "" {
		t.Fatal("no subscription token")
	}

	// Clash subscription + headers.
	req, _ := http.NewRequest("GET", env.srv.URL+"/api/files/"+subToken, nil)
	req.Header.Set("User-Agent", "ClashMeta/1.19.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("subscription %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "type: hysteria2") || !strings.Contains(string(body), "ports: 20000-50000") {
		t.Fatalf("bad clash body:\n%s", body)
	}
	if resp.Header.Get("Profile-Title") == "" || !strings.Contains(resp.Header.Get("Subscription-Userinfo"), "total=5368709120") {
		t.Fatalf("headers = %v", resp.Header)
	}

	// Global subscription endpoint mode filters the generated links without
	// changing the node. The node above keeps both TLS and Hopping enabled.
	setHy2Mode := func(mode string) {
		resp := env.do(t, "PUT", "/api/settings", env.token, map[string]any{"subscription_hy2_endpoints": mode})
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("set hy2 mode %q = %d: %s", mode, resp.StatusCode, b)
		}
		resp.Body.Close()
	}
	uriSub := func() string {
		req, _ := http.NewRequest("GET", env.srv.URL+"/api/files/"+subToken+"?format=uri", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	setHy2Mode("tls")
	sub := uriSub()
	if strings.Count(sub, "hysteria2://") != 1 || strings.Contains(sub, "mport=") {
		t.Fatalf("TLS-only subscription = %s", sub)
	}
	setHy2Mode("hopping")
	sub = uriSub()
	if strings.Count(sub, "hysteria2://") != 1 || !strings.Contains(sub, "mport=20000-50000") {
		t.Fatalf("Hopping-only subscription = %s", sub)
	}
	setHy2Mode("both")
	sub = uriSub()
	if strings.Count(sub, "hysteria2://") != 2 {
		t.Fatalf("both subscription = %s", sub)
	}
	if bad := env.do(t, "PUT", "/api/settings", env.token, map[string]any{"subscription_hy2_endpoints": "invalid"}); bad.StatusCode != 400 {
		bad.Body.Close()
		t.Fatalf("invalid hy2 mode = %d", bad.StatusCode)
	} else {
		bad.Body.Close()
	}

	// sing-box requested explicitly.
	if got := env.do(t, "GET", "/api/files/"+subToken+"?format=singbox", "", nil); got.StatusCode != 200 {
		t.Fatalf("singbox = %d", got.StatusCode)
	} else {
		got.Body.Close()
	}
	// Info endpoint.
	info := decode[map[string]any](t, env.do(t, "GET", "/api/files/info/"+subToken, "", nil))
	if int(info["servers"].(float64)) != 1 {
		t.Fatalf("info = %+v", info)
	}

	// Hysteria auth.
	ok := decode[map[string]any](t, env.do(t, "POST", "/api/auth?node="+itoa(nodeID), "", map[string]any{
		"addr": "198.51.100.1:1111", "auth": "alice:secretpass",
	}))
	if ok["ok"] != true {
		t.Fatalf("auth ok = %+v", ok)
	}
	bad := decode[map[string]any](t, env.do(t, "POST", "/api/auth?node="+itoa(nodeID), "", map[string]any{
		"addr": "198.51.100.1:1111", "auth": "alice:wrong",
	}))
	if bad["ok"] != false {
		t.Fatalf("auth bad = %+v", bad)
	}

	// Log analysis sees the panel-side rejected auth.
	summary := decode[map[string]any](t, env.do(t, "GET", "/api/logs/summary?range=24h", env.token, nil))
	if int(summary["authFailures"].(float64)) < 1 {
		t.Fatalf("summary = %+v", summary)
	}
	// Dashboard should return nodes and stats.
	dash := decode[map[string]any](t, env.do(t, "GET", "/api/dashboard", env.token, nil))
	if dash["stats"] == nil || len(dash["nodes"].([]any)) != 1 {
		t.Fatalf("dashboard = %+v", dash)
	}
}

func TestAgentWebsocket(t *testing.T) {
	env := newTestEnv(t)
	created := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "Agent Node", "ip": "127.0.0.1", "port": 443, "tlsMode": "selfsigned", "active": true,
	}))
	nodeID := int64(created["node"].(map[string]any)["id"].(float64))
	n, err := env.store.GetNode(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	user := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "bob", "password": "secretpass", "enabled": true,
	}))
	_ = user

	wsURL := "ws" + strings.TrimPrefix(env.srv.URL, "http") + "/api/node/ws"
	header := http.Header{"Authorization": []string{"Bearer " + n.Token}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	_ = conn.WriteJSON(map[string]any{"type": "hello", "payload": map[string]any{
		"version": "test", "os": "linux", "arch": "amd64", "hy2Version": "v2.13.0", "runtime": "process",
		"capabilities": []string{"cert.csr.v1"},
	}})

	// The panel pushes a config after hello.
	var gotConfig bool
	for i := 0; i < 3; i++ {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		if msg["type"] == "config" {
			gotConfig = true
			break
		}
	}
	if !gotConfig {
		t.Fatal("no config push received")
	}
	_ = conn.WriteJSON(map[string]any{"type": "heartbeat", "payload": map[string]any{
		"cpuPct": 1.5, "memUsed": 100, "memTotal": 200, "online": 1,
		"trafficTx": 1000, "trafficRx": 2000, "hy2Running": true,
	}})
	time.Sleep(100 * time.Millisecond)
	_ = conn.WriteJSON(map[string]any{"type": "heartbeat", "payload": map[string]any{
		"cpuPct": 2.5, "memUsed": 100, "memTotal": 200, "online": 1,
		"trafficTx": 1500, "trafficRx": 2600, "hy2Running": true,
		"version": "sha-heartbeat", "hy2Version": "v2.14.0", "xrayVersion": "", "runtime": "process",
	}})
	_ = conn.WriteJSON(map[string]any{"type": "stats", "payload": map[string]any{
		"batchId": "stats-1", "users": map[string]any{"bob": map[string]any{"tx": 10, "rx": 20}},
		"online": map[string]any{"bob": 1},
	}})
	_ = conn.WriteJSON(map[string]any{"type": "logs", "payload": map[string]any{
		"batchId": "logs-1", "entries": []map[string]any{
			{"raw": "2026-10-21T15:33:51+08:00\tINFO\tclient connected\t{\"addr\": \"198.51.100.9:1234\", \"id\": \"bob\"}"},
			{"raw": "2026-10-21T15:33:52+08:00\tERROR\tserver\tfailed to serve\t{\"error\": \"boom\"}"},
		},
	}})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		logs, total, _ := env.store.QueryLogs(context.Background(), model.LogFilter{NodeID: nodeID, Page: 1, Size: 10})
		if total >= 2 {
			if logs[0].Event == "" {
				t.Fatalf("log not parsed: %+v", logs[0])
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, total, _ := env.store.QueryLogs(context.Background(), model.LogFilter{NodeID: nodeID, Page: 1, Size: 10})
	if total < 2 {
		t.Fatalf("logs not stored: %d", total)
	}
	u, _ := env.store.GetUserByUserID(context.Background(), "bob")
	if u.TrafficTx != 10 || u.TrafficRx != 20 {
		t.Fatalf("user traffic = %+v", u)
	}
	n2, _ := env.store.GetNode(context.Background(), nodeID)
	if n2.Status != "online" || n2.TrafficTx != 500 || n2.TrafficRx != 600 {
		t.Fatalf("node = status=%s traffic=%d/%d", n2.Status, n2.TrafficTx, n2.TrafficRx)
	}
	// A heartbeat carrying version fields must refresh the node row without a
	// reconnect. The initial hello reported "test".
	versionDeadline := time.Now().Add(2 * time.Second)
	for {
		n2, _ = env.store.GetNode(context.Background(), nodeID)
		if n2.AgentVersion == "sha-heartbeat" && n2.Hy2Version == "v2.14.0" {
			break
		}
		if time.Now().After(versionDeadline) {
			t.Fatalf("heartbeat versions not stored: %+v", n2)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestAPIKeyScopes(t *testing.T) {
	env := newTestEnv(t)
	created := decode[map[string]any](t, env.do(t, "POST", "/api/api-keys", env.token, map[string]any{
		"name": "readonly", "scopes": []string{"users:read"},
	}))
	keyToken, _ := created["token"].(string)
	if !strings.HasPrefix(keyToken, "ck_") {
		t.Fatalf("token = %q", keyToken)
	}
	withKey := func(method, path string, body any) *http.Response {
		var rdr io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, env.srv.URL+path, rdr)
		req.Header.Set("X-API-Key", keyToken)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := withKey("GET", "/api/users", nil); resp.StatusCode != 200 {
		t.Fatalf("users read = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := withKey("POST", "/api/users", map[string]any{"userId": "nope"}); resp.StatusCode != 403 {
		t.Fatalf("users write = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := withKey("GET", "/api/settings", nil); resp.StatusCode != 403 {
		t.Fatalf("settings = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	statsKey := decode[map[string]any](t, env.do(t, "POST", "/api/api-keys", env.token, map[string]any{
		"name": "stats", "scopes": []string{"stats:read"},
	}))
	sk, _ := statsKey["token"].(string)
	req, _ := http.NewRequest("GET", env.srv.URL+"/api/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+sk)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("dashboard with stats key = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Revoking the key must immediately stop authentication.
	keyObj := created["key"].(map[string]any)
	keyID := int64(keyObj["id"].(float64))
	if r := env.do(t, "POST", "/api/api-keys/"+itoa(keyID)+"/disable", env.token, nil); r.StatusCode != 200 {
		t.Fatalf("disable = %d", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if resp := withKey("GET", "/api/users", nil); resp.StatusCode != 401 {
		t.Fatalf("disabled key = %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

func TestAPIKeyRateLimit(t *testing.T) {
	env := newTestEnv(t)
	created := decode[map[string]any](t, env.do(t, "POST", "/api/api-keys", env.token, map[string]any{
		"name": "limited", "scopes": []string{"users:read"}, "rateLimit": 2,
	}))
	key, _ := created["token"].(string)
	do := func() *http.Response {
		req, _ := http.NewRequest("GET", env.srv.URL+"/api/users", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	for i := 0; i < 2; i++ {
		resp := do()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d = %d", i, resp.StatusCode)
		}
		if resp.Header.Get("X-RateLimit-Limit") != "2" {
			t.Fatalf("limit header = %q", resp.Header.Get("X-RateLimit-Limit"))
		}
		resp.Body.Close()
	}
	resp := do()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third request = %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" || resp.Header.Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("headers = %v", resp.Header)
	}
}

func TestBackupCreateListAndRestoreUpload(t *testing.T) {
	env := newTestEnv(t)
	created := decode[map[string]any](t, env.do(t, "POST", "/api/backups", env.token, nil))
	name, _ := created["name"].(string)
	if !strings.HasSuffix(name, ".tar.gz") {
		t.Fatalf("backup name = %q", name)
	}
	list := decode[map[string]any](t, env.do(t, "GET", "/api/backups", env.token, nil))
	if len(list["items"].([]any)) != 1 {
		t.Fatalf("backups = %+v", list)
	}
	// Download the archive.
	dl := env.do(t, "GET", "/api/backups/"+name, env.token, nil)
	if dl.StatusCode != 200 {
		t.Fatalf("download = %d", dl.StatusCode)
	}
	body, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	if len(body) == 0 || !strings.Contains(dl.Header.Get("Content-Disposition"), name) {
		t.Fatalf("download headers = %v size=%d", dl.Header, len(body))
	}
	// Upload it back as a restore candidate.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(body)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", env.srv.URL+"/api/backups/restore", &buf)
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("restore = %d: %s", resp.StatusCode, b)
	}
	out := decode[map[string]any](t, resp)
	if out["restartRequired"] != true {
		t.Fatalf("restore response = %+v", out)
	}
	staged := env.store.Path + ".restore"
	if err := store.ValidateDatabase(staged); err != nil {
		t.Fatalf("staged database invalid: %v", err)
	}
	_ = os.Remove(staged)
}

func TestSSHInstallScript(t *testing.T) {
	env := newTestEnv(t)
	n := &model.Node{Token: "mtn_test_token", SSHInstallDocker: true}
	script := env.app.sshInstallScript(n)
	for _, want := range []string{
		"mtn_test_token",
		"-e PANEL_URL='http://panel.test'",
		"-e NODE_TOKEN='mtn_test_token'",
		"-e HY2_RUNTIME='process'",
		"/var/lib/mikvace:/data",
		"--network host",
		"curl -fsSL https://get.docker.com",
		"ghcr.io/axmishell/mikvacepanel-node:latest",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "node.yaml") || strings.Contains(script, "node.token") {
		t.Fatalf("script still writes config files:\n%s", script)
	}
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Fatalf("shellQuote = %q", got)
	}
}

func TestXrayNodeConfigAndVLESSSubscription(t *testing.T) {
	env := newTestEnv(t)
	nodeResp := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"type": "xray", "name": "Reality", "flag": "US", "ip": "203.0.113.7", "port": 443,
		"tlsMode": "selfsigned", "active": true,
		"xray": map[string]any{
			"transport": "tcp", "security": "reality", "flow": "xtls-rprx-vision",
			"realityDest": "www.cloudflare.com:443", "realitySni": []string{"www.cloudflare.com"},
		},
	}))
	nodeObj := nodeResp["node"].(map[string]any)
	nodeID := int64(nodeObj["id"].(float64))
	xray := nodeObj["xray"].(map[string]any)
	if xray["realityPrivateKey"] != "" {
		t.Fatalf("reality private key must not be returned to the browser: %+v", xray)
	}
	if xray["realityPublicKey"] == "" {
		t.Fatalf("reality public key not generated: %+v", xray)
	}
	user := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "vlessuser", "password": "secretpass", "enabled": true,
	}))
	subToken := user["subscriptionToken"].(string)
	uuid, _ := user["xrayUuid"].(string)
	if uuid == "" {
		t.Fatal("xray uuid not generated")
	}
	env.app.syncXrayNodes(context.Background())
	cfg := decode[map[string]any](t, env.do(t, "GET", "/api/nodes/"+itoa(nodeID)+"/config", env.token, nil))
	if cfg["protocol"] != "xray" {
		t.Fatalf("config protocol = %v", cfg["protocol"])
	}
	if !strings.Contains(cfg["yaml"].(string), uuid) || !strings.Contains(cfg["yaml"].(string), "vless") {
		t.Fatalf("xray config does not contain user uuid:\n%s", cfg["yaml"])
	}
	req, _ := http.NewRequest("GET", env.srv.URL+"/api/files/"+subToken+"?format=uri", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "vless://"+uuid) {
		t.Fatalf("subscription missing vless URI:\n%s", body)
	}
}

func TestHappSubscriptionHeaders(t *testing.T) {
	env := newTestEnv(t)
	env.do(t, "PUT", "/api/settings", env.token, map[string]any{
		"happ_provider_id": "com.example.panel", "happ_routing_mode": "off", "happ_announce": "Hello",
	}).Body.Close()
	env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "Happ Node", "ip": "203.0.113.11", "port": 443, "tlsMode": "selfsigned", "active": true,
	}).Body.Close()
	u := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "happuser", "password": "secretpass", "enabled": true,
	}))
	token := u["subscriptionToken"].(string)
	req, _ := http.NewRequest("GET", env.srv.URL+"/api/files/"+token, nil)
	req.Header.Set("User-Agent", "HAPP/3.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("routing") != "happ://routing/off" {
		t.Fatalf("routing = %q", resp.Header.Get("routing"))
	}
	if resp.Header.Get("providerid") != "com.example.panel" || resp.Header.Get("announce") != "Hello" {
		t.Fatalf("headers = %v", resp.Header)
	}
	if !strings.HasPrefix(string(body), "happ://routing/off\n") {
		t.Fatalf("body = %q", body)
	}
	env.do(t, "PUT", "/api/settings", env.token, map[string]any{
		"happ_routing_mode": "custom", "happ_routing_profile": `{"rules":[{"type":"field","domain":["example.com"],"outbound":"direct"}]}`,
	}).Body.Close()
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if !strings.HasPrefix(resp2.Header.Get("routing"), "happ://routing/onadd/") {
		t.Fatalf("custom routing = %q", resp2.Header.Get("routing"))
	}
}

func TestRunSSHInstall(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "root" && string(pass) == "pw" {
				return nil, nil
			}
			return nil, fmt.Errorf("denied")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "session" {
						_ = nc.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					ch, requests, err := nc.Accept()
					if err != nil {
						return
					}
					go func() {
						defer ch.Close()
						for req := range requests {
							if req.Type == "exec" {
								_ = req.Reply(true, nil)
								_, _ = ch.Write([]byte("installed\n"))
								_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
								return
							}
							_ = req.Reply(false, nil)
						}
					}()
				}
			}()
		}
	}()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	res, hostKey := runSSHInstall(host, port, "root", "pw", "", "", "echo hi")
	if !res.OK || !strings.Contains(res.Output, "installed") {
		t.Fatalf("result = %+v", res)
	}
	if hostKey == "" || !strings.HasPrefix(hostKey, "ssh-rsa ") {
		t.Fatalf("host key not captured: %q", hostKey)
	}
	// A pinned key allows the next connection.
	res, _ = runSSHInstall(host, port, "root", "pw", "", hostKey, "echo hi")
	if !res.OK {
		t.Fatalf("pinned install = %+v", res)
	}
	// A different key is rejected before the command runs.
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, err := ssh.NewPublicKey(&other.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	res, _ = runSSHInstall(host, port, "root", "pw", "", strings.TrimSpace(string(ssh.MarshalAuthorizedKey(otherPub))), "echo hi")
	if res.OK || !strings.Contains(res.Error, "host key mismatch") {
		t.Fatalf("mismatched host key accepted: %+v", res)
	}
}

func buildCelerityTestArchive(t *testing.T) []byte {
	t.Helper()
	gid := primitive.NewObjectID()
	nid := primitive.NewObjectID()
	now := time.Now().UTC()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name string, docs []bson.M) {
		var raw bytes.Buffer
		for _, doc := range docs {
			b, err := bson.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			raw.Write(b)
		}
		var z bytes.Buffer
		zw := gzip.NewWriter(&z)
		if _, err := zw.Write(raw.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: "celerity-backup-test/hysteria/" + name + ".bson.gz",
			Mode: 0o600, Size: int64(z.Len()), ModTime: now, Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(z.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	write("servergroups", []bson.M{{"_id": gid, "name": "Imported"}})
	write("hynodes", []bson.M{{
		"_id": nid, "type": "hysteria", "name": "imported-node", "ip": "203.0.113.10",
		"port": 8443, "groups": []primitive.ObjectID{gid}, "active": true,
	}})
	write("hyusers", []bson.M{{
		"_id": primitive.NewObjectID(), "userId": "imported-user", "password": "secret123",
		"enabled": true, "groups": []primitive.ObjectID{gid},
	}})
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRestoreCelerityBackup(t *testing.T) {
	env := newTestEnv(t)
	archive := buildCelerityTestArchive(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "celerity-backup-20260101-000000.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", env.srv.URL+"/api/backups/restore", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("restore %d: %s", resp.StatusCode, b)
	}
	var out struct {
		OK      bool   `json:"ok"`
		Source  string `json:"source"`
		Summary struct {
			Users  int `json:"users"`
			Nodes  int `json:"nodes"`
			Groups int `json:"groups"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Source != "celerity" || out.Summary.Users != 1 || out.Summary.Nodes != 1 || out.Summary.Groups != 1 {
		t.Fatalf("unexpected response: %+v", out)
	}

	staged := env.app.cfg.DBPath + ".restore"
	defer func() {
		_ = os.Remove(staged)
		_ = os.Remove(staged + "-wal")
		_ = os.Remove(staged + "-shm")
	}()
	if err := store.ValidateDatabase(staged); err != nil {
		t.Fatal(err)
	}
	imported, err := store.Open(staged)
	if err != nil {
		t.Fatal(err)
	}
	defer imported.Close()
	u, err := imported.GetUserByUserID(context.Background(), "imported-user")
	if err != nil || !u.Enabled {
		t.Fatalf("imported user = %+v, %v", u, err)
	}
	nodes, err := imported.ListNodes(context.Background())
	if err != nil || len(nodes) != 1 || nodes[0].Name != "imported-node" {
		t.Fatalf("imported nodes = %+v, %v", nodes, err)
	}
}

func TestHybridNodeRendersBothCores(t *testing.T) {
	env := newTestEnv(t)
	nodeResp := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "Hybrid", "flag": "JP", "ip": "203.0.113.8", "port": 443,
		"cores": []string{"hysteria", "xray"}, "tlsMode": "selfsigned", "active": true,
		"xray": map[string]any{
			"apiPort": 61000,
			"inbounds": []map[string]any{{
				"protocol": "vmess", "tag": "vmess-in", "port": 8443,
				"transport": "ws", "security": "tls", "wsPath": "/vm",
			}},
		},
	}))
	nodeObj := nodeResp["node"].(map[string]any)
	nodeID := int64(nodeObj["id"].(float64))
	cores, _ := nodeObj["cores"].([]any)
	if len(cores) != 2 {
		t.Fatalf("cores = %v", nodeObj["cores"])
	}
	cfg := decode[map[string]any](t, env.do(t, "GET", "/api/nodes/"+itoa(nodeID)+"/config", env.token, nil))
	configs, ok := cfg["configs"].(map[string]any)
	if !ok {
		t.Fatalf("configs missing: %+v", cfg)
	}
	hy2, _ := configs["hysteria"].(string)
	xray, _ := configs["xray"].(string)
	if !strings.Contains(hy2, "listen:") || !strings.Contains(hy2, "auth:") {
		t.Fatalf("hysteria config missing:\n%s", hy2)
	}
	if !strings.Contains(xray, `"protocol": "vmess"`) || !strings.Contains(xray, `"wsSettings"`) {
		t.Fatalf("xray config missing vmess/ws:\n%s", xray)
	}
}

func TestSelectiveXrayProtocols(t *testing.T) {
	env := newTestEnv(t)
	nodeResp := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "Selective", "flag": "US", "ip": "203.0.113.30", "port": 443,
		"cores": []string{"xray"}, "tlsMode": "selfsigned", "active": true,
		"xray": map[string]any{
			"apiPort": 61000,
			"inbounds": []map[string]any{
				{"protocol": "vless", "tag": "vless-in", "port": 443, "transport": "tcp", "security": "reality", "enabled": true},
				{"protocol": "vmess", "tag": "vmess-in", "port": 8443, "transport": "ws", "security": "tls", "enabled": false},
			},
		},
	}))
	nodeObj := nodeResp["node"].(map[string]any)
	nodeID := int64(nodeObj["id"].(float64))
	user := decode[map[string]any](t, env.do(t, "POST", "/api/users", env.token, map[string]any{
		"userId": "selective", "password": "secretpass", "enabled": true,
	}))
	subToken := user["subscriptionToken"].(string)

	env.app.syncXrayNodes(context.Background())
	cfg := decode[map[string]any](t, env.do(t, "GET", "/api/nodes/"+itoa(nodeID)+"/config", env.token, nil))
	body := cfg["yaml"].(string)
	if !strings.Contains(body, `"protocol": "vless"`) || strings.Contains(body, `"protocol": "vmess"`) {
		t.Fatalf("config did not respect enabled flags:\n%s", body)
	}

	req, _ := http.NewRequest("GET", env.srv.URL+"/api/files/"+subToken+"?format=uri", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(sub), "vless://") || strings.Contains(string(sub), "vmess://") {
		t.Fatalf("subscription did not respect enabled flags:\n%s", sub)
	}

	// Disabling every inbound must be rejected.
	bad := env.do(t, "POST", "/api/nodes", env.token, map[string]any{
		"name": "NoProtocol", "ip": "203.0.113.31", "port": 443,
		"cores": []string{"xray"}, "tlsMode": "selfsigned", "active": true,
		"xray": map[string]any{
			"inbounds": []map[string]any{
				{"protocol": "vless", "tag": "vless-in", "port": 443, "transport": "tcp", "security": "none", "enabled": false},
			},
		},
	})
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("all-disabled create status = %d", bad.StatusCode)
	}
}
