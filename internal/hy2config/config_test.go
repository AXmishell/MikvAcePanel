package hy2config

import (
	"strings"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
	"gopkg.in/yaml.v3"
)

func TestRenderSelfSigned(t *testing.T) {
	n := &model.Node{
		ID: 7, Name: "Tokyo", IP: "203.0.113.10", Port: 443, StatsPort: 9999, StatsSecret: "s3cret",
		TLSMode: "selfsigned", ObfsType: "salamander", ObfsPassword: "obfs",
		BandwidthUp: "1 gbps", ACL: []string{"direct(all)"},
	}
	text, hash, err := Render(n, "https://panel.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" || !strings.HasPrefix(text, "listen: :443") {
		t.Fatalf("unexpected output: %s", text)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatal(err)
	}
	auth := cfg["auth"].(map[string]any)["http"].(map[string]any)
	if auth["url"] != "https://panel.example.com/api/auth?node=7" {
		t.Fatalf("auth url = %v", auth["url"])
	}
	stats := cfg["trafficStats"].(map[string]any)
	if stats["secret"] != "s3cret" || stats["listen"] != "127.0.0.1:9999" {
		t.Fatalf("stats = %v", stats)
	}
}

func TestRenderACME(t *testing.T) {
	n := &model.Node{ID: 1, Domain: "node.example.com", Port: 443, TLSMode: "acme", ACMEEmail: "a@b.c", StatsPort: 9999}
	text, _, err := Render(n, "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "tls:") || !strings.Contains(text, "acme:") || !strings.Contains(text, "node.example.com") {
		t.Fatalf("unexpected acme config:\n%s", text)
	}
	// Node-side ACME must not bind port 80 and must persist its cache.
	if !strings.Contains(text, "disableHTTP: true") || !strings.Contains(text, "dir: /data/acme") {
		t.Fatalf("acme config must use TLS-ALPN only and a persistent dir:\n%s", text)
	}
	if _, _, err := Render(&model.Node{TLSMode: "acme"}, "https://panel"); err == nil {
		t.Fatal("expected error for acme without domain")
	}
}

func TestRenderManaged(t *testing.T) {
	n := &model.Node{ID: 2, Domain: "node.example.com", Port: 443, TLSMode: "managed", StatsPort: 9999}
	text, _, err := Render(n, "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "tls:") || !strings.Contains(text, "/data/certs/cert.pem") {
		t.Fatalf("managed config must reference the panel-managed files:\n%s", text)
	}
	if strings.Contains(text, "acme:") {
		t.Fatalf("managed config must not contain acme:\n%s", text)
	}
}

func TestRenderRequiresURL(t *testing.T) {
	if _, _, err := Render(&model.Node{Port: 443}, ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestRenderExtendedOptions(t *testing.T) {
	n := &model.Node{
		ID: 9, Domain: "node.example.com", Port: 443, StatsPort: 9999,
		TLSMode: "managed", ObfsType: "gecko", ObfsPassword: "obfs",
		ACL:       []string{"direct(all)"},
		Outbounds: []model.Outbound{{Name: "direct", Type: "direct"}},
		Hy2: model.Hy2Settings{
			QUIC: &model.Hy2QUIC{
				InitStreamReceiveWindow: 8388608, MaxStreamReceiveWindow: 16777216,
				InitConnReceiveWindow: 20971520, MaxConnReceiveWindow: 41943040,
				MaxIdleTimeout: "30s", MaxIncomingStreams: 1024,
				DisablePathMTUDiscovery: true,
			},
			Congestion: &model.Hy2Congestion{Type: "bbr", BBRProfile: "aggressive"},
			Resolver:   &model.Hy2Resolver{Type: "https", Addr: "1.1.1.1:443", Timeout: "5s", SNI: "cloudflare-dns.com"},
			Sniff:      &model.Hy2Sniff{Enable: true, Timeout: "2s", TCPPorts: "80,443", UDPPorts: "all"},
			Masquerade: &model.Hy2Masquerade{
				Type: "string", Content: "hello", Headers: map[string]string{"content-type": "text/plain"},
				StatusCode: 200, ListenHTTP: ":80", ForceHTTPS: true,
			},
			TLS:                     &model.Hy2TLS{SNIGuard: "dns-san"},
			Direct:                  &model.Hy2Direct{Mode: "46", BindIPv4: "192.0.2.5", FastOpen: true},
			IgnoreClientBandwidth:   true,
			DisableLossCompensation: true,
			DisableUDP:              true,
			UDPIdleTimeout:          "60s",
			SpeedTest:               true,
			GeckoMinPacketSize:      512,
			GeckoMaxPacketSize:      1200,
			ACLFile:                 "/data/acl.txt",
			GeoIP:                   "/data/geoip.dat",
			GeoUpdateInterval:       "168h",
			TrafficListen:           "0.0.0.0",
		},
	}
	text, _, err := Render(n, "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"sniGuard: dns-san",
		"minPacketSize: 512", "maxPacketSize: 1200",
		"initStreamReceiveWindow: 8388608", "maxIncomingStreams: 1024",
		"disablePathMTUDiscovery: true",
		"type: bbr", "bbrProfile: aggressive",
		"ignoreClientBandwidth: true", "disableLossCompensation: true",
		"disableUDP: true", "udpIdleTimeout: 60s", "speedTest: true",
		"type: https", "sni: cloudflare-dns.com",
		"enable: true", "tcpPorts: 80,443", "udpPorts: all",
		"file: /data/acl.txt", "geoip: /data/geoip.dat", "geoUpdateInterval: 168h",
		"mode: \"46\"", "bindIPv4: 192.0.2.5", "fastOpen: true",
		"listen: 0.0.0.0:9999",
		"type: string", "content: hello", "listenHTTP: :80", "forceHTTPS: true",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "inline:") {
		t.Errorf("ACL file mode must not emit inline rules:\n%s", text)
	}
}

func TestRenderRealmAndECH(t *testing.T) {
	n := &model.Node{
		ID: 3, Port: 443, StatsPort: 9999, TLSMode: "selfsigned",
		Hy2: model.Hy2Settings{
			Realm: &model.Hy2Realm{
				URI:         "realm://public@realm.hy2.io/my-server-8f3a",
				STUNServers: []string{"stun.nextcloud.com:3478"}, IPMode: "v4",
				PortMapping: true, PortMapLifetime: "10m",
			},
			ECHConfig: "AEz+DQBIAAAg",
			Mimic:     &model.Hy2Mimic{Enabled: true, Interface: "eth0"},
		},
	}
	text, _, err := Render(n, "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"listen: realm://public@realm.hy2.io/my-server-8f3a",
		"stunServers:", "stun.nextcloud.com:3478", "ipMode: v4",
		"lifetime: 10m", "keyPath: /data/certs/ech.pem",
		"mimic:", "interface: eth0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("config missing %q:\n%s", want, text)
		}
	}
}

func TestRenderExtraYAML(t *testing.T) {
	n := &model.Node{
		ID: 4, Port: 443, StatsPort: 9999, TLSMode: "selfsigned",
		Hy2: model.Hy2Settings{
			ExtraYAML: "quic:\n  maxIdleTimeout: 45s\ncustom:\n  flag: true\n",
		},
	}
	text, _, err := Render(n, "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "maxIdleTimeout: 45s") || !strings.Contains(text, "custom:") {
		t.Fatalf("extra YAML not merged:\n%s", text)
	}
	n.Hy2.ExtraYAML = "auth:\n  type: password\n"
	if _, _, err := Render(n, "https://panel.example.com"); err == nil {
		t.Fatal("expected reserved key rejection")
	}
	if err := ValidateExtraYAML("listen: :444\n"); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestDefaultOutputHasNoNewKeys(t *testing.T) {
	n := &model.Node{ID: 5, Port: 443, StatsPort: 9999, TLSMode: "selfsigned"}
	text, _, err := Render(n, "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{
		"congestion:", "quic:", "resolver:", "sniff:", "mimic:", "realm:",
		"ech:", "disableUDP:", "speedTest:", "ignoreClientBandwidth:",
		"initStreamReceiveWindow:", "disableLossCompensation:",
	} {
		if strings.Contains(text, unwanted) {
			t.Errorf("default config unexpectedly contains %q:\n%s", unwanted, text)
		}
	}
}
