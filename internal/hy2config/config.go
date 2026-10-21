// Package hy2config renders Hysteria 2 server configuration from panel node
// settings. The generated YAML is pushed to the node agent over the control
// websocket, so the panel remains the single source of truth.
package hy2config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// CertPath and KeyPath are the canonical paths inside a node. The agent
// rewrites the /data prefix when running the hysteria process directly.
const (
	DataDir  = "/data"
	CertPath = "/data/certs/cert.pem"
	KeyPath  = "/data/certs/key.pem"
)

type ServerConfig struct {
	Listen       string         `yaml:"listen,omitempty"`
	TLS          *TLSConfig     `yaml:"tls,omitempty"`
	ACME         *ACMEConfig    `yaml:"acme,omitempty"`
	Auth         AuthConfig     `yaml:"auth"`
	Obfs         *ObfsConfig    `yaml:"obfs,omitempty"`
	Bandwidth    *Bandwidth     `yaml:"bandwidth,omitempty"`
	TrafficStats *TrafficStats  `yaml:"trafficStats,omitempty"`
	ACL          *ACLConfig     `yaml:"acl,omitempty"`
	Outbounds    []OutboundConf `yaml:"outbounds,omitempty"`
	Masquerade   *Masquerade    `yaml:"masquerade,omitempty"`
	QUIC         *QUICConfig    `yaml:"quic,omitempty"`
}

type TLSConfig struct {
	Cert     string `yaml:"cert"`
	Key      string `yaml:"key"`
	SNIGuard string `yaml:"sniGuard,omitempty"`
}

type ACMEConfig struct {
	Domains []string `yaml:"domains"`
	Email   string   `yaml:"email,omitempty"`
	Type    string   `yaml:"type,omitempty"`
	// Dir keeps ACME account data and certificates on the shared data volume.
	// Without it Hysteria stores them under its working directory, which is
	// ephemeral when the proxy runs in a sibling container.
	Dir string `yaml:"dir,omitempty"`
	// DisableHTTP restricts node-side ACME to the TLS-ALPN challenge so the
	// node never has to bind port 80. The legacy field is honoured by both old
	// and current Hysteria releases.
	DisableHTTP bool `yaml:"disableHTTP,omitempty"`
}

type AuthConfig struct {
	Type string   `yaml:"type"`
	HTTP HTTPAuth `yaml:"http"`
}

type HTTPAuth struct {
	URL      string `yaml:"url"`
	Insecure bool   `yaml:"insecure,omitempty"`
}

type ObfsConfig struct {
	Type       string          `yaml:"type"`
	Salamander *ObfsSalamander `yaml:"salamander,omitempty"`
	Gecko      *ObfsGecko      `yaml:"gecko,omitempty"`
}

type ObfsSalamander struct {
	Password string `yaml:"password"`
}

type ObfsGecko struct {
	Password string `yaml:"password"`
}

type Bandwidth struct {
	Up   string `yaml:"up,omitempty"`
	Down string `yaml:"down,omitempty"`
}

type TrafficStats struct {
	Listen string `yaml:"listen"`
	Secret string `yaml:"secret,omitempty"`
}

type ACLConfig struct {
	Inline []string `yaml:"inline,omitempty"`
}

type OutboundConf struct {
	Name   string      `yaml:"name"`
	Type   string      `yaml:"type"`
	Direct *DirectConf `yaml:"direct,omitempty"`
	SOCKS5 *SOCKS5Conf `yaml:"socks5,omitempty"`
	HTTP   *HTTPConf   `yaml:"http,omitempty"`
}

type DirectConf struct {
	Mode string `yaml:"mode,omitempty"`
}

type SOCKS5Conf struct {
	Addr     string `yaml:"addr"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

type HTTPConf struct {
	URL      string `yaml:"url"`
	Insecure bool   `yaml:"insecure,omitempty"`
}

type Masquerade struct {
	Type  string      `yaml:"type"`
	Proxy *MasqProxy  `yaml:"proxy,omitempty"`
	File  *MasqFile   `yaml:"file,omitempty"`
	Str   *MasqString `yaml:"string,omitempty"`
}

type MasqProxy struct {
	URL         string `yaml:"url"`
	RewriteHost bool   `yaml:"rewriteHost"`
}

type MasqFile struct {
	Dir string `yaml:"dir"`
}

type MasqString struct {
	Content    string `yaml:"content"`
	StatusCode int    `yaml:"statusCode"`
}

type QUICConfig struct {
	MaxIdleTimeout string `yaml:"maxIdleTimeout,omitempty"`
}

// Build renders the Hysteria server config for a node. panelURL must be the
// externally reachable base URL of the panel (no trailing slash).
func Build(n *model.Node, panelURL string) (*ServerConfig, error) {
	if n == nil {
		return nil, fmt.Errorf("nil node")
	}
	panelURL = strings.TrimRight(panelURL, "/")
	if panelURL == "" {
		return nil, fmt.Errorf("panel public URL is not configured")
	}
	listen := n.Listen
	if listen == "" {
		port := n.Port
		if port == 0 {
			port = 443
		}
		listen = fmt.Sprintf(":%d", port)
	}
	cfg := &ServerConfig{
		Listen: listen,
		Auth: AuthConfig{
			Type: "http",
			HTTP: HTTPAuth{URL: fmt.Sprintf("%s/api/auth?node=%d", panelURL, n.ID)},
		},
		TrafficStats: &TrafficStats{
			Listen: fmt.Sprintf("127.0.0.1:%d", defaultInt(n.StatsPort, 9999)),
			Secret: n.StatsSecret,
		},
	}
	switch strings.ToLower(n.TLSMode) {
	case "acme":
		domain := strings.TrimSpace(n.Domain)
		if domain == "" {
			return nil, fmt.Errorf("ACME TLS requires a domain")
		}
		cfg.ACME = &ACMEConfig{
			Domains: []string{domain}, Email: n.ACMEEmail,
			Dir: "/data/acme", DisableHTTP: true,
		}
	case "managed":
		// The panel issues the certificate and pushes it to the node. The
		// Hysteria config just points at the files the agent maintains.
		cfg.TLS = &TLSConfig{Cert: CertPath, Key: KeyPath, SNIGuard: "disable"}
	default:
		cfg.TLS = &TLSConfig{Cert: CertPath, Key: KeyPath, SNIGuard: "disable"}
	}
	if n.ObfsType != "" && n.ObfsPassword != "" {
		switch n.ObfsType {
		case "salamander", "gecko":
			cfg.Obfs = &ObfsConfig{Type: n.ObfsType}
			if n.ObfsType == "salamander" {
				cfg.Obfs.Salamander = &ObfsSalamander{Password: n.ObfsPassword}
			} else {
				cfg.Obfs.Gecko = &ObfsGecko{Password: n.ObfsPassword}
			}
		}
	}
	if n.BandwidthUp != "" || n.BandwidthDown != "" {
		cfg.Bandwidth = &Bandwidth{Up: n.BandwidthUp, Down: n.BandwidthDown}
	}
	if len(n.ACL) > 0 {
		cfg.ACL = &ACLConfig{Inline: n.ACL}
	}
	for _, o := range n.Outbounds {
		name := strings.TrimSpace(o.Name)
		if name == "" {
			continue
		}
		switch o.Type {
		case "socks5":
			cfg.Outbounds = append(cfg.Outbounds, OutboundConf{
				Name: name, Type: "socks5",
				SOCKS5: &SOCKS5Conf{Addr: o.Addr, Username: o.Username, Password: o.Password},
			})
		case "http":
			cfg.Outbounds = append(cfg.Outbounds, OutboundConf{
				Name: name, Type: "http",
				HTTP: &HTTPConf{URL: o.Addr, Insecure: o.Insecure},
			})
		case "direct":
			cfg.Outbounds = append(cfg.Outbounds, OutboundConf{Name: name, Type: "direct", Direct: &DirectConf{Mode: "auto"}})
		}
	}
	if n.MasqueradeURL != "" {
		cfg.Masquerade = &Masquerade{Type: "proxy", Proxy: &MasqProxy{URL: n.MasqueradeURL, RewriteHost: true}}
	}
	return cfg, nil
}

// Render returns YAML and its SHA-256 hash.
func Render(n *model.Node, panelURL string) (string, string, error) {
	cfg, err := Build(n, panelURL)
	if err != nil {
		return "", "", err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}

func defaultInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
