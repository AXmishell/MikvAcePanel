package server

import (
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestParseECHConfigPEM(t *testing.T) {
	list := []byte{0x00, 0x05, 0xfe, 0x0d, 0x00, 0x01, 0x00}
	blob := pem.EncodeToMemory(&pem.Block{Type: "ECH KEYS", Bytes: []byte{0x00, 0x01, 0xaa}})
	blob = append(blob, pem.EncodeToMemory(&pem.Block{Type: "ECH CONFIGS", Bytes: list})...)
	got, err := parseECHConfigPEM(string(blob))
	if err != nil {
		t.Fatal(err)
	}
	if want := base64.StdEncoding.EncodeToString(list); got != want {
		t.Fatalf("config = %q, want %q", got, want)
	}
	if _, err := parseECHConfigPEM(string(pem.EncodeToMemory(&pem.Block{Type: "ECH CONFIGS", Bytes: list}))); err == nil {
		t.Fatal("expected error without ECH KEYS")
	}
}

func TestValidateHy2VersionGate(t *testing.T) {
	existing := &model.Node{Hy2Version: "v2.7.0"}
	h := &model.Hy2Settings{Congestion: &model.Hy2Congestion{Type: "bbr"}}
	if err := validateHy2(h, existing, "selfsigned", "", "", []string{"hysteria"}, nil); err == nil {
		t.Fatal("expected congestion version error")
	}
	existing.Hy2Version = "v2.13.0"
	if err := validateHy2(h, existing, "selfsigned", "", "", []string{"hysteria"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateHy2SubscriptionEndpoints(t *testing.T) {
	on, off := true, false
	hysteria := []string{"hysteria"}

	// Both disabled would remove the node from every subscription.
	h := &model.Hy2Settings{IncludeTLS: &off, IncludeHopping: &off}
	if err := validateHy2(h, nil, "selfsigned", "", "", hysteria, nil); err == nil ||
		!strings.Contains(err.Error(), "at least one") {
		t.Fatalf("expected both-disabled rejection, got %v", err)
	}

	// Hopping only needs a range.
	h = &model.Hy2Settings{IncludeTLS: &off, IncludeHopping: &on}
	if err := validateHy2(h, nil, "selfsigned", "", "", hysteria, nil); err == nil ||
		!strings.Contains(err.Error(), "port range") {
		t.Fatalf("expected range requirement, got %v", err)
	}
	if err := validateHy2(h, nil, "selfsigned", "20000-50000", "", hysteria, nil); err != nil {
		t.Fatalf("agent range should allow hopping-only: %v", err)
	}
	h.ListenPortRange = "20000-50000"
	if err := validateHy2(h, nil, "selfsigned", "", "", hysteria, nil); err != nil {
		t.Fatalf("built-in range should allow hopping-only: %v", err)
	}

	// TLS only needs no range.
	h = &model.Hy2Settings{IncludeTLS: &on, IncludeHopping: &off}
	if err := validateHy2(h, nil, "selfsigned", "", "", hysteria, nil); err != nil {
		t.Fatalf("tls-only should pass: %v", err)
	}

	// The toggles are irrelevant on a pure Xray node.
	h = &model.Hy2Settings{IncludeTLS: &off, IncludeHopping: &off}
	if err := validateHy2(h, nil, "selfsigned", "", "", []string{"xray"}, nil); err != nil {
		t.Fatalf("pure xray node must not be constrained: %v", err)
	}

	// Realms and mimic do not publish share links, so they are exempt.
	h = &model.Hy2Settings{
		IncludeTLS: &off, IncludeHopping: &off,
		Realm: &model.Hy2Realm{URI: "realm://public@realm.hy2.io/my-server-8f3a"},
	}
	if err := validateHy2(h, nil, "selfsigned", "", "", hysteria, nil); err != nil {
		t.Fatalf("realm node must be exempt: %v", err)
	}
}

func TestValidateHy2Conflicts(t *testing.T) {
	h := &model.Hy2Settings{
		ListenPortRange: "20000-50000",
		Mimic:           &model.Hy2Mimic{Enabled: true},
	}
	if err := validateHy2(h, nil, "selfsigned", "", "", []string{"hysteria"}, nil); err == nil ||
		!strings.Contains(err.Error(), "mimic") {
		t.Fatalf("expected mimic/port range conflict, got %v", err)
	}
}

func TestValidateRealmURI(t *testing.T) {
	if err := validateHy2(&model.Hy2Settings{Realm: &model.Hy2Realm{URI: "realm://public@realm.hy2.io/home"}}, nil,
		"selfsigned", "", "", []string{"hysteria"}, nil); err == nil {
		t.Fatal("expected short realm name rejection")
	}
	if err := validateHy2(&model.Hy2Settings{Realm: &model.Hy2Realm{URI: "realm://public@realm.hy2.io/my-server-8f3a"}}, nil,
		"selfsigned", "", "", []string{"hysteria"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMasqueradeXrayConflict(t *testing.T) {
	m := &model.Hy2Masquerade{Type: "proxy", ProxyURL: "https://example.com", ListenHTTPS: ":8443"}
	inbounds := []model.XrayInbound{{Protocol: "vless", Tag: "vless-in", Port: 8443}}
	if err := validateMasquerade(m, []string{"hysteria", "xray"}, inbounds); err == nil {
		t.Fatal("expected port conflict")
	}
	if err := validateMasquerade(m, []string{"hysteria"}, inbounds); err != nil {
		t.Fatalf("pure hysteria must ignore xray ports: %v", err)
	}
}

func TestPublicNodeMasksECHKeys(t *testing.T) {
	n := &model.Node{ID: 1, Hy2: model.Hy2Settings{
		ECHPEM:    "-----BEGIN ECH KEYS-----\nsecret\n-----END ECH KEYS-----",
		ECHConfig: "AEz+DQBIAAAg",
		ClientCA:  "ca-pem",
	}}
	pub := publicNode(n)
	if pub.Hy2.ECHPEM != "" {
		t.Fatal("ECH private keys leaked")
	}
	if pub.Hy2.ECHConfig != "AEz+DQBIAAAg" || pub.Hy2.ClientCA != "ca-pem" {
		t.Fatalf("public fields missing: %+v", pub.Hy2)
	}
	if n.Hy2.ECHPEM == "" {
		t.Fatal("publicNode must not mutate the original node")
	}
}
