package subscription

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
	"gopkg.in/yaml.v3"
)

func fixture() (*model.User, []model.Node) {
	u := &model.User{UserID: "alice", Username: "Alice", Password: "secret", SubscriptionToken: "tok"}
	u.Groups = []model.Group{{Name: "Europe", SubscriptionTitle: "EU Premium"}}
	n := model.Node{
		ID: 1, Name: "Tokyo Edge", Flag: "JP", IP: "203.0.113.10", Port: 443,
		PortRange: "20000-50000", HopInterval: "30s", ObfsType: "salamander", ObfsPassword: "obfs-pass",
	}
	n2 := model.Node{ID: 2, Name: "Osaka", Domain: "osaka.example.com", Port: 8443, TLSMode: "acme"}
	return u, []model.Node{n, n2}
}

func TestDetectFormat(t *testing.T) {
	cases := map[string]string{
		"Shadowrocket/1.0": "shadowrocket",
		"HiddifyNext/4.0.5 (android) like ClashMeta v2ray": "singbox",
		"ClashMeta/1.19.0": "clash",
		"sing-box 1.11.0":  "singbox",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36": "uri",
		"HAPP/3.0": "uri",
	}
	for ua, want := range cases {
		if got := DetectFormat(ua); got != want {
			t.Errorf("DetectFormat(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestURI(t *testing.T) {
	u, nodes := fixture()
	e := Endpoints(nodes[0])[1]
	raw := URI(u, nodes[0], e)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "hysteria2" || parsed.Host != "203.0.113.10:443" {
		t.Fatalf("bad uri: %s", raw)
	}
	if !strings.Contains(parsed.Fragment, "Tokyo Edge") {
		t.Fatalf("fragment = %q", parsed.Fragment)
	}
	q := parsed.Query()
	if q.Get("insecure") != "1" || q.Get("alpn") != "h3" {
		t.Fatalf("query = %v", q)
	}
	if q.Get("obfs") != "salamander" || q.Get("obfs-password") != "obfs-pass" {
		t.Fatalf("obfs = %v", q)
	}
	if q.Get("mport") != "20000-50000" || q.Get("mportHopInt") != "30" {
		t.Fatalf("hopping = %v", q)
	}
	if !strings.Contains(raw, "alice:secret@") {
		t.Fatalf("auth missing: %s", raw)
	}
}

func TestClashAndSingbox(t *testing.T) {
	u, nodes := fixture()
	clash, err := Clash(u, nodes)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(clash), &doc); err != nil {
		t.Fatal(err)
	}
	proxies, ok := doc["proxies"].([]any)
	if !ok || len(proxies) != 3 {
		t.Fatalf("proxies = %#v", doc["proxies"])
	}
	first := proxies[0].(map[string]any)
	if first["type"] != "hysteria2" || first["password"] != "alice:secret" {
		t.Fatalf("first proxy = %#v", first)
	}
	if doc["rules"] == nil {
		t.Fatal("rules missing")
	}

	sb, err := Singbox(u, nodes)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(sb), &cfg); err != nil {
		t.Fatal(err)
	}
	outbounds := cfg["outbounds"].([]any)
	if len(outbounds) < 4 {
		t.Fatalf("outbounds = %d", len(outbounds))
	}
}

func TestShadowrocketAndXray(t *testing.T) {
	u, nodes := fixture()
	body := Shadowrocket(u, nodes)
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), "hysteria2://") {
		t.Fatalf("bad body: %s", decoded)
	}
	xray, err := Xray(u, nodes)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(xray), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["outbounds"] == nil {
		t.Fatal("outbounds missing")
	}
}

func TestTitleAndHeaders(t *testing.T) {
	u, _ := fixture()
	if got := Title(u, "fallback"); got != "EU Premium" {
		t.Fatalf("title = %q", got)
	}
	if !strings.HasPrefix(EncodeTitle("hello"), "base64:") {
		t.Fatal("title encoding")
	}
}

func TestEndpointsDomain(t *testing.T) {
	_, nodes := fixture()
	eps := Endpoints(nodes[1])
	if len(eps) != 1 {
		t.Fatalf("endpoints = %d", len(eps))
	}
	if !eps[0].HasCert || eps[0].Insecure || eps[0].SNI != "osaka.example.com" {
		t.Fatalf("endpoint = %+v", eps[0])
	}
}

func TestVLESSURIAndFormats(t *testing.T) {
	u := &model.User{UserID: "alice", Password: "pw", XrayUUID: "11111111-1111-4111-8111-111111111111"}
	n := model.Node{
		ID: 9, Type: "xray", Name: "Reality Edge", Flag: "US", IP: "203.0.113.9", Port: 443,
		Xray: model.XraySettings{
			Transport: "tcp", Security: "reality", Flow: "xtls-rprx-vision", Fingerprint: "chrome",
			RealityDest: "www.microsoft.com:443", RealitySNI: []string{"www.microsoft.com"},
			RealityPublicKey: "PUBKEY", RealityShortIDs: []string{"abcd1234"},
		},
	}
	uri := VLESSURI(u, n)
	if !strings.HasPrefix(uri, "vless://11111111-1111-4111-8111-111111111111@203.0.113.9:443?") {
		t.Fatalf("uri = %s", uri)
	}
	for _, want := range []string{"security=reality", "pbk=PUBKEY", "sid=abcd1234", "flow=xtls-rprx-vision", "fp=chrome", "type=tcp"} {
		if !strings.Contains(uri, want) {
			t.Fatalf("uri missing %q: %s", want, uri)
		}
	}

	clash, err := Clash(u, []model.Node{n})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(clash), &doc); err != nil {
		t.Fatal(err)
	}
	proxy := doc["proxies"].([]any)[0].(map[string]any)
	if proxy["type"] != "vless" || proxy["uuid"] != u.XrayUUID {
		t.Fatalf("clash proxy = %#v", proxy)
	}

	sb, err := Singbox(u, []model.Node{n})
	if err != nil {
		t.Fatal(err)
	}
	var sbCfg map[string]any
	if err := json.Unmarshal([]byte(sb), &sbCfg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ob := range sbCfg["outbounds"].([]any) {
		m := ob.(map[string]any)
		if m["type"] == "vless" && m["uuid"] == u.XrayUUID {
			found = true
		}
	}
	if !found {
		t.Fatal("vless outbound missing from sing-box")
	}

	xray, err := Xray(u, []model.Node{n})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xray, `"protocol": "vless"`) {
		t.Fatalf("xray config = %s", xray)
	}
}

func TestSpecialCharacterPasswordStaysRawInClientConfigs(t *testing.T) {
	u := &model.User{UserID: "alice", Password: "p@ss:word", XrayUUID: "11111111-1111-4111-8111-111111111111"}
	nodes := []model.Node{{ID: 1, Name: "Edge", IP: "203.0.113.1", Port: 443}}
	wantAuth := "alice:p@ss:word"

	clash, err := Clash(u, nodes)
	if err != nil {
		t.Fatal(err)
	}
	var clashDoc map[string]any
	if err := yaml.Unmarshal([]byte(clash), &clashDoc); err != nil {
		t.Fatal(err)
	}
	proxy := clashDoc["proxies"].([]any)[0].(map[string]any)
	if proxy["password"] != wantAuth {
		t.Fatalf("clash password = %v", proxy["password"])
	}

	sb, err := Singbox(u, nodes)
	if err != nil {
		t.Fatal(err)
	}
	var sbDoc map[string]any
	if err := json.Unmarshal([]byte(sb), &sbDoc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, raw := range sbDoc["outbounds"].([]any) {
		ob := raw.(map[string]any)
		if ob["type"] == "hysteria2" {
			found = true
			if ob["password"] != wantAuth {
				t.Fatalf("sing-box password = %v", ob["password"])
			}
		}
	}
	if !found {
		t.Fatal("hysteria2 outbound missing")
	}

	xr, err := Xray(u, nodes)
	if err != nil {
		t.Fatal(err)
	}
	var xrDoc map[string]any
	if err := json.Unmarshal([]byte(xr), &xrDoc); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, raw := range xrDoc["outbounds"].([]any) {
		ob := raw.(map[string]any)
		if ob["protocol"] == "hysteria" {
			found = true
			stream := ob["streamSettings"].(map[string]any)
			hysteria := stream["hysteriaSettings"].(map[string]any)
			if hysteria["auth"] != wantAuth {
				t.Fatalf("xray auth = %v", hysteria["auth"])
			}
		}
	}
	if !found {
		t.Fatal("hysteria outbound missing")
	}

	eps := Endpoints(nodes[0])
	link := URI(u, nodes[0], eps[0])
	if !strings.Contains(link, "alice:p%40ss%3Aword@") {
		t.Fatalf("share link should be escaped: %s", link)
	}
}

func TestVLESSURLCertificateVerification(t *testing.T) {
	u := &model.User{XrayUUID: "11111111-1111-4111-8111-111111111111"}
	n := model.Node{
		Type: "xray", Name: "TLS Edge", Domain: "edge.example.com", Port: 443, TLSMode: "managed",
		Xray: model.XraySettings{Transport: "tcp", Security: "tls", ALPN: []string{"h2", "http/1.1"}},
	}
	uri := VLESSURI(u, n)
	if strings.Contains(uri, "allowInsecure=1") {
		t.Fatalf("managed certificate link skips verification: %s", uri)
	}
	n.TLSMode = "selfsigned"
	uri = VLESSURI(u, n)
	if !strings.Contains(uri, "allowInsecure=1") {
		t.Fatalf("self-signed link should allow insecure mode: %s", uri)
	}
}

func TestEndpointsManagedCertificate(t *testing.T) {
	_, nodes := fixture()
	n := nodes[1]
	n.TLSMode = "managed"
	eps := Endpoints(n)
	if len(eps) != 1 || !eps[0].HasCert || eps[0].Insecure {
		t.Fatalf("managed endpoint = %+v", eps)
	}
	link := URI(&model.User{UserID: "u", Password: "p"}, n, eps[0])
	if !strings.Contains(link, "insecure=0") {
		t.Fatalf("managed link should verify the certificate: %s", link)
	}
}

func TestXrayMultiProtocolSubscriptions(t *testing.T) {
	n := model.Node{
		ID: 7, Type: "xray", Name: "Multi", IP: "203.0.113.7", Port: 443,
		Xray: model.XraySettings{APIPort: 61000, Inbounds: []model.XrayInbound{
			{Protocol: "vless", Tag: "vless-in", Port: 443, Transport: "tcp", Security: "reality", RealitySNI: []string{"www.example.com"}, RealityPublicKey: "pk", RealityShortIDs: []string{"abcd"}, Fingerprint: "chrome"},
			{Protocol: "vmess", Tag: "vmess-in", Port: 8443, Transport: "ws", Security: "tls", WSPath: "/vm", WSHost: "cdn.example.com"},
			{Protocol: "trojan", Tag: "trojan-in", Port: 8444, Transport: "grpc", Security: "tls", GRPCServiceName: "tj"},
			{Protocol: "shadowsocks", Tag: "ss-in", Port: 8445, Transport: "tcp", Security: "none", SSMethod: "2022-blake3-aes-128-gcm"},
		}},
	}
	u := &model.User{UserID: "alice", Password: "secret", XrayUUID: "11111111-1111-4111-8111-111111111111", Enabled: true}

	list := URIList(u, []model.Node{n})
	for _, prefix := range []string{"vless://", "vmess://", "trojan://", "ss://"} {
		if !strings.Contains(list, prefix) {
			t.Fatalf("missing %s in:\n%s", prefix, list)
		}
	}

	clash, err := Clash(u, []model.Node{n})
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"type: vless", "type: vmess", "type: trojan", "type: ss"} {
		if !strings.Contains(clash, typ) {
			t.Fatalf("missing %s in:\n%s", typ, clash)
		}
	}

	sing, err := Singbox(u, []model.Node{n})
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{`"type": "vless"`, `"type": "vmess"`, `"type": "trojan"`, `"type": "shadowsocks"`} {
		if !strings.Contains(sing, typ) {
			t.Fatalf("missing %s in:\n%s", typ, sing)
		}
	}

	xr, err := Xray(u, []model.Node{n})
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{`"protocol": "vless"`, `"protocol": "vmess"`, `"protocol": "trojan"`, `"protocol": "shadowsocks"`} {
		if !strings.Contains(xr, typ) {
			t.Fatalf("missing %s in:\n%s", typ, xr)
		}
	}
}

func TestHybridNodeEmitsBothCores(t *testing.T) {
	n := model.Node{
		ID: 8, Type: "hysteria", Cores: []string{"hysteria", "xray"},
		Name: "Hybrid", IP: "203.0.113.8", Port: 443,
		Xray: model.XraySettings{Inbounds: []model.XrayInbound{
			{Protocol: "vless", Tag: "vless-in", Port: 8443, Transport: "tcp", Security: "reality", RealitySNI: []string{"www.example.com"}, RealityPublicKey: "pk", RealityShortIDs: []string{"abcd"}},
		}},
	}
	u := &model.User{UserID: "alice", Password: "secret", XrayUUID: "11111111-1111-4111-8111-111111111111", Enabled: true}
	list := URIList(u, []model.Node{n})
	if !strings.Contains(list, "hysteria2://") || !strings.Contains(list, "vless://") {
		t.Fatalf("hybrid subscription missing a core:\n%s", list)
	}
}
