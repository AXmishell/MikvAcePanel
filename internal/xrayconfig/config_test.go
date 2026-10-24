package xrayconfig

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func fixture() (*model.Node, []model.User) {
	n := &model.Node{
		ID: 1, Type: "xray", Name: "Reality", IP: "203.0.113.1", Port: 443,
		Xray: model.XraySettings{
			Transport: "tcp", Security: "reality", Flow: "xtls-rprx-vision",
			RealityDest: "www.cloudflare.com:443", RealitySNI: []string{"www.cloudflare.com"},
		},
	}
	if err := Normalize(n); err != nil {
		panic(err)
	}
	users := []model.User{
		{UserID: "alice", XrayUUID: "11111111-1111-4111-8111-111111111111", Enabled: true},
		{UserID: "bob", XrayUUID: "22222222-2222-4222-8222-222222222222", Enabled: true},
		{UserID: "disabled", XrayUUID: "33333333-3333-4333-8333-333333333333", Enabled: false},
	}
	return n, users
}

func TestRenderReality(t *testing.T) {
	n, users := fixture()
	if n.Xray.RealityPrivateKey == "" || n.Xray.RealityPublicKey == "" {
		t.Fatal("keys not generated")
	}
	text, hash, err := Render(n, users)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("missing hash")
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatal(err)
	}
	inbounds := cfg["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("inbounds = %d", len(inbounds))
	}
	main := inbounds[1].(map[string]any)
	clients := main["settings"].(map[string]any)["clients"].([]any)
	if len(clients) != 2 {
		t.Fatalf("clients = %d", len(clients))
	}
	stream := main["streamSettings"].(map[string]any)
	reality := stream["realitySettings"].(map[string]any)
	if reality["privateKey"] != n.Xray.RealityPrivateKey || reality["dest"] != "www.cloudflare.com:443" {
		t.Fatalf("reality = %#v", reality)
	}
	if !strings.Contains(text, "/data/log/access.log") {
		t.Fatal("access log path missing")
	}
}

func TestRenderSkipsExpiredAndOverQuota(t *testing.T) {
	n, users := fixture()
	expiredAt := time.Now().Add(-time.Hour)
	users = append(users,
		model.User{UserID: "expired", XrayUUID: "44444444-4444-4444-8444-444444444444", Enabled: true, ExpireAt: &expiredAt},
		model.User{UserID: "quota", XrayUUID: "55555555-5555-4555-8555-555555555555", Enabled: true, TrafficLimit: 10, TrafficTx: 10},
	)
	text, _, err := Render(n, users)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatal(err)
	}
	main := cfg["inbounds"].([]any)[1].(map[string]any)
	clients := main["settings"].(map[string]any)["clients"].([]any)
	if len(clients) != 2 {
		t.Fatalf("expired/over-quota clients were rendered: %d", len(clients))
	}
}

func TestRenderTLSAndWS(t *testing.T) {
	n := &model.Node{ID: 2, Type: "xray", Domain: "node.example.com", Port: 8443, Xray: model.XraySettings{
		Transport: "ws", Security: "tls", WSPath: "/vless", WSHost: "cdn.example.com",
	}}
	if err := Normalize(n); err != nil {
		t.Fatal(err)
	}
	text, _, err := Render(n, nil)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal([]byte(text), &cfg)
	if !strings.Contains(text, `"certificateFile": "/data/certs/cert.pem"`) ||
		!strings.Contains(text, `"path": "/vless"`) {
		t.Fatalf("tls/ws config = %s", text)
	}
}

func TestRenderMultiProtocolInbounds(t *testing.T) {
	n := &model.Node{ID: 9, Type: "xray", Name: "multi", IP: "203.0.113.9", Port: 443, Xray: model.XraySettings{
		Inbounds: []model.XrayInbound{
			{Protocol: "vless", Tag: "vless-in", Port: 443, Transport: "tcp", Security: "reality"},
			{Protocol: "vmess", Tag: "vmess-in", Port: 8443, Transport: "ws", Security: "tls", WSPath: "/vm"},
			{Protocol: "trojan", Tag: "trojan-in", Port: 8444, Transport: "grpc", Security: "none", GRPCServiceName: "tj"},
			{Protocol: "shadowsocks", Tag: "ss-in", Port: 8445, Transport: "tcp", Security: "none"},
		},
	}}
	if err := Normalize(n); err != nil {
		t.Fatal(err)
	}
	if n.Xray.Inbounds[0].RealityPrivateKey == "" {
		t.Fatal("reality key not generated")
	}
	users := []model.User{
		{UserID: "alice", Password: "pw", XrayUUID: "11111111-1111-4111-8111-111111111111", Enabled: true},
	}
	text, _, err := Render(n, users)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatal(err)
	}
	inbounds := cfg["inbounds"].([]any)
	if len(inbounds) != 5 {
		t.Fatalf("inbounds = %d", len(inbounds))
	}
	seen := map[string]bool{}
	for _, raw := range inbounds[1:] {
		ib := raw.(map[string]any)
		proto := ib["protocol"].(string)
		seen[proto] = true
		clients, _ := ib["settings"].(map[string]any)["clients"].([]any)
		if len(clients) != 1 {
			t.Fatalf("%s clients = %d", proto, len(clients))
		}
	}
	for _, proto := range []string{"vless", "vmess", "trojan", "shadowsocks"} {
		if !seen[proto] {
			t.Fatalf("missing %s inbound: %s", proto, text)
		}
	}
	if !strings.Contains(text, `"xhttpSettings"`) && !strings.Contains(text, `"method": "2022-blake3-aes-128-gcm"`) {
		t.Fatalf("shadowsocks settings missing: %s", text)
	}
}

func TestSSPasswordLengths(t *testing.T) {
	u := &model.User{XrayUUID: "11111111-1111-4111-8111-111111111111"}
	for _, method := range []string{"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm"} {
		pw := SSPassword(u, method)
		raw, err := base64.StdEncoding.DecodeString(pw)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) != SSKeyLen(method) {
			t.Fatalf("%s key = %d bytes", method, len(raw))
		}
	}
}

func boolPtr(v bool) *bool { return &v }

func TestDisabledInboundIsSkipped(t *testing.T) {
	n := &model.Node{ID: 11, Type: "xray", IP: "203.0.113.11", Port: 443, Xray: model.XraySettings{
		Inbounds: []model.XrayInbound{
			{Protocol: "vless", Tag: "vless-in", Port: 443, Transport: "tcp", Security: "reality"},
			{Protocol: "vmess", Tag: "vmess-in", Port: 8443, Transport: "ws", Security: "tls", Enabled: boolPtr(false)},
		},
	}}
	if err := Normalize(n); err != nil {
		t.Fatal(err)
	}
	text, _, err := Render(n, []model.User{{UserID: "alice", XrayUUID: "11111111-1111-4111-8111-111111111111", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, `"protocol": "vmess"`) || !strings.Contains(text, `"protocol": "vless"`) {
		t.Fatalf("disabled inbound was rendered:\n%s", text)
	}
	var cfg map[string]any
	_ = json.Unmarshal([]byte(text), &cfg)
	if got := len(cfg["inbounds"].([]any)); got != 2 {
		t.Fatalf("inbounds = %d, want API + vless", got)
	}
}

func TestNormalizeRejectsAllDisabledInbounds(t *testing.T) {
	n := &model.Node{ID: 12, Type: "xray", IP: "203.0.113.12", Port: 443, Xray: model.XraySettings{
		Inbounds: []model.XrayInbound{
			{Protocol: "vless", Tag: "vless-in", Port: 443, Transport: "tcp", Security: "none", Enabled: boolPtr(false)},
		},
	}}
	if err := Normalize(n); err == nil {
		t.Fatal("expected an error when every inbound is disabled")
	}
}

func TestNormalizeXrayGlobalDefaults(t *testing.T) {
	n := &model.Node{ID: 20, Type: "xray", IP: "203.0.113.20", Port: 443}
	if err := Normalize(n); err != nil {
		t.Fatal(err)
	}
	if n.Xray.LogLevel != DefaultLogLevel {
		t.Fatalf("log level = %q", n.Xray.LogLevel)
	}
	if n.Xray.RouteStrategy != DefaultRouteStrategy {
		t.Fatalf("route strategy = %q", n.Xray.RouteStrategy)
	}
	if n.Xray.Sniffing == nil || !n.Xray.Sniffing.Enabled || !n.Xray.Sniffing.RouteOnly {
		t.Fatalf("sniffing = %+v", n.Xray.Sniffing)
	}
	if got := strings.Join(n.Xray.Sniffing.DestOverride, ","); got != "http,tls,quic" {
		t.Fatalf("sniff destinations = %q", got)
	}
}

func TestNormalizeXrayGlobalValidation(t *testing.T) {
	cases := []model.XraySettings{
		{LogLevel: "verbose"},
		{RouteStrategy: "sometimes"},
		{Sniffing: &model.XraySniffing{Enabled: true, DestOverride: []string{"fakedns"}}},
	}
	for _, x := range cases {
		n := &model.Node{ID: 21, Type: "xray", IP: "203.0.113.21", Port: 443, Xray: x}
		if err := Normalize(n); err == nil {
			t.Fatalf("expected validation error for %+v", x)
		}
	}
}

func TestRenderXrayGlobalSettings(t *testing.T) {
	n, users := fixture()
	n.Xray.LogLevel = "debug"
	n.Xray.RouteStrategy = "AsIs"
	n.Xray.Sniffing = &model.XraySniffing{
		Enabled: true, DestOverride: []string{"tls"}, RouteOnly: false,
	}
	text, _, err := Render(n, users)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg["log"].(map[string]any)["loglevel"]; got != "debug" {
		t.Fatalf("loglevel = %v", got)
	}
	if got := cfg["routing"].(map[string]any)["domainStrategy"]; got != "AsIs" {
		t.Fatalf("domainStrategy = %v", got)
	}
	sniff := cfg["inbounds"].([]any)[1].(map[string]any)["sniffing"].(map[string]any)
	if sniff["enabled"] != true || sniff["routeOnly"] != false {
		t.Fatalf("sniffing = %#v", sniff)
	}
	if got := sniff["destOverride"].([]any); len(got) != 1 || got[0] != "tls" {
		t.Fatalf("destOverride = %#v", got)
	}
}

func TestRenderXraySniffingDisabled(t *testing.T) {
	n, users := fixture()
	n.Xray.Sniffing = &model.XraySniffing{Enabled: false}
	text, _, err := Render(n, users)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatal(err)
	}
	sniff := cfg["inbounds"].([]any)[1].(map[string]any)["sniffing"].(map[string]any)
	if sniff["enabled"] != false {
		t.Fatalf("sniffing = %#v", sniff)
	}
	if _, ok := sniff["destOverride"]; ok {
		t.Fatalf("disabled sniffing should not include destOverride: %#v", sniff)
	}
}
