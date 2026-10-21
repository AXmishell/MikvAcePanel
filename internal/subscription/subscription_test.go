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
