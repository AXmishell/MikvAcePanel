package client

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseURI(t *testing.T) {
	raw := "hysteria2://alice:pw@example.com:443?sni=example.com&alpn=h3&insecure=0&mport=20000-50000&mportHopInt=30&obfs=salamander&obfs-password=obfs#Tokyo"
	ep, err := ParseURI(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Host != "example.com" || ep.Port != 443 || ep.User != "alice" || ep.Password != "pw" {
		t.Fatalf("endpoint = %+v", ep)
	}
	if ep.PortRange != "20000-50000" || ep.HopInterval != 30 || ep.Obfs != "salamander" {
		t.Fatalf("endpoint = %+v", ep)
	}
	if ep.Name != "Tokyo" {
		t.Fatalf("name = %q", ep.Name)
	}
}

func TestParseURIListBase64(t *testing.T) {
	list := "hysteria2://a:b@1.2.3.4:443#One\nhysteria2://a:b@5.6.7.8:8443#Two\n"
	body := base64.StdEncoding.EncodeToString([]byte(list))
	eps, err := ParseURIList([]byte(body))
	if err != nil || len(eps) != 2 {
		t.Fatalf("eps=%d err=%v", len(eps), err)
	}
}

func TestBuildClientConfig(t *testing.T) {
	ep := &Endpoint{
		Host: "example.com", Port: 443, User: "alice", Password: "pw", SNI: "example.com",
		Obfs: "salamander", ObfsPassword: "obfs", PortRange: "20000-50000", HopInterval: 30,
	}
	text, err := BuildClientConfig(ep, "127.0.0.1:1080", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"auth: alice:pw", "server: example.com:20000-50000", "hopInterval: 30s", "socks5:", "salamander"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
}
