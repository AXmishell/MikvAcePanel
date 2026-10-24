// Package hy2config renders Hysteria 2 server configuration from panel node
// settings. The generated YAML is pushed to the node agent over the control
// websocket, so the panel remains the single source of truth.
package hy2config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// CertPath and KeyPath are the canonical paths inside a node. The agent
// rewrites the /data prefix when running the hysteria process directly.
const (
	DataDir    = "/data"
	CertPath   = "/data/certs/cert.pem"
	KeyPath    = "/data/certs/key.pem"
	ECHPEMPath = "/data/certs/ech.pem"
	ClientCA   = "/data/certs/client_ca.pem"
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

	// Extended options. Omitted entirely when unused so existing nodes keep
	// producing byte-identical YAML.
	Congestion            *CongestionConfig `yaml:"congestion,omitempty"`
	IgnoreClientBandwidth bool              `yaml:"ignoreClientBandwidth,omitempty"`
	DisableUDP            bool              `yaml:"disableUDP,omitempty"`
	UDPIdleTimeout        string            `yaml:"udpIdleTimeout,omitempty"`
	SpeedTest             bool              `yaml:"speedTest,omitempty"`
	Resolver              *ResolverConfig   `yaml:"resolver,omitempty"`
	Sniff                 *SniffConfig      `yaml:"sniff,omitempty"`
	Realm                 *RealmConfig      `yaml:"realm,omitempty"`
	ECH                   *ECHConfig        `yaml:"ech,omitempty"`
	Mimic                 *MimicConfig      `yaml:"mimic,omitempty"`
}

type TLSConfig struct {
	Cert     string `yaml:"cert"`
	Key      string `yaml:"key"`
	SNIGuard string `yaml:"sniGuard,omitempty"`
	ClientCA string `yaml:"clientCA,omitempty"`
}

type ACMEConfig struct {
	Domains    []string  `yaml:"domains"`
	Email      string    `yaml:"email,omitempty"`
	CA         string    `yaml:"ca,omitempty"`
	ListenHost string    `yaml:"listenHost,omitempty"`
	Type       string    `yaml:"type,omitempty"`
	HTTP       *ACMEHTTP `yaml:"http,omitempty"`
	TLS        *ACMETLS  `yaml:"tls,omitempty"`
	DNS        *ACMEDNS  `yaml:"dns,omitempty"`
	// Dir keeps ACME account data and certificates on the shared data volume.
	// Without it Hysteria stores them under its working directory, which is
	// ephemeral when the proxy runs in a sibling container.
	Dir string `yaml:"dir,omitempty"`
	// DisableHTTP restricts node-side ACME to the TLS-ALPN challenge so the
	// node never has to bind port 80. The legacy field is honoured by both old
	// and current Hysteria releases.
	DisableHTTP bool `yaml:"disableHTTP,omitempty"`
}

type ACMEHTTP struct {
	AltPort int `yaml:"altPort,omitempty"`
}

type ACMETLS struct {
	AltPort int `yaml:"altPort,omitempty"`
}

type ACMEDNS struct {
	Name   string            `yaml:"name,omitempty"`
	Config map[string]string `yaml:"config,omitempty"`
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
	Password      string `yaml:"password"`
	MinPacketSize int    `yaml:"minPacketSize,omitempty"`
	MaxPacketSize int    `yaml:"maxPacketSize,omitempty"`
}

type Bandwidth struct {
	Up                      string `yaml:"up,omitempty"`
	Down                    string `yaml:"down,omitempty"`
	DisableLossCompensation bool   `yaml:"disableLossCompensation,omitempty"`
}

type TrafficStats struct {
	Listen string `yaml:"listen"`
	Secret string `yaml:"secret,omitempty"`
}

type ACLConfig struct {
	Inline            []string `yaml:"inline,omitempty"`
	File              string   `yaml:"file,omitempty"`
	GeoIP             string   `yaml:"geoip,omitempty"`
	GeoSite           string   `yaml:"geosite,omitempty"`
	GeoUpdateInterval string   `yaml:"geoUpdateInterval,omitempty"`
}

type OutboundConf struct {
	Name   string      `yaml:"name"`
	Type   string      `yaml:"type"`
	Direct *DirectConf `yaml:"direct,omitempty"`
	SOCKS5 *SOCKS5Conf `yaml:"socks5,omitempty"`
	HTTP   *HTTPConf   `yaml:"http,omitempty"`
}

type DirectConf struct {
	Mode       string `yaml:"mode,omitempty"`
	BindIPv4   string `yaml:"bindIPv4,omitempty"`
	BindIPv6   string `yaml:"bindIPv6,omitempty"`
	BindDevice string `yaml:"bindDevice,omitempty"`
	FastOpen   bool   `yaml:"fastOpen,omitempty"`
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
	Type        string      `yaml:"type"`
	Proxy       *MasqProxy  `yaml:"proxy,omitempty"`
	File        *MasqFile   `yaml:"file,omitempty"`
	Str         *MasqString `yaml:"string,omitempty"`
	ListenHTTP  string      `yaml:"listenHTTP,omitempty"`
	ListenHTTPS string      `yaml:"listenHTTPS,omitempty"`
	ForceHTTPS  bool        `yaml:"forceHTTPS,omitempty"`
}

type MasqProxy struct {
	URL         string `yaml:"url"`
	RewriteHost bool   `yaml:"rewriteHost"`
	Insecure    bool   `yaml:"insecure,omitempty"`
	XForwarded  bool   `yaml:"xForwarded,omitempty"`
}

type MasqFile struct {
	Dir string `yaml:"dir"`
}

type MasqString struct {
	Content    string            `yaml:"content"`
	Headers    map[string]string `yaml:"headers,omitempty"`
	StatusCode int               `yaml:"statusCode,omitempty"`
}

type QUICConfig struct {
	InitStreamReceiveWindow int    `yaml:"initStreamReceiveWindow,omitempty"`
	MaxStreamReceiveWindow  int    `yaml:"maxStreamReceiveWindow,omitempty"`
	InitConnReceiveWindow   int    `yaml:"initConnReceiveWindow,omitempty"`
	MaxConnReceiveWindow    int    `yaml:"maxConnReceiveWindow,omitempty"`
	MaxIdleTimeout          string `yaml:"maxIdleTimeout,omitempty"`
	MaxIncomingStreams      int    `yaml:"maxIncomingStreams,omitempty"`
	DisablePathMTUDiscovery bool   `yaml:"disablePathMTUDiscovery,omitempty"`
	DisableStatelessReset   bool   `yaml:"disableStatelessReset,omitempty"`
}

type CongestionConfig struct {
	Type       string `yaml:"type"`
	BBRProfile string `yaml:"bbrProfile,omitempty"`
}

type ResolverConfig struct {
	Type string        `yaml:"type"`
	TCP  *ResolverAddr `yaml:"tcp,omitempty"`
	UDP  *ResolverAddr `yaml:"udp,omitempty"`
	TLS  *ResolverTLS  `yaml:"tls,omitempty"`
	HTTP *ResolverTLS  `yaml:"https,omitempty"`
}

type ResolverAddr struct {
	Addr    string `yaml:"addr"`
	Timeout string `yaml:"timeout,omitempty"`
}

type ResolverTLS struct {
	Addr     string `yaml:"addr"`
	Timeout  string `yaml:"timeout,omitempty"`
	SNI      string `yaml:"sni,omitempty"`
	Insecure bool   `yaml:"insecure,omitempty"`
}

type SniffConfig struct {
	Enable        bool   `yaml:"enable"`
	Timeout       string `yaml:"timeout,omitempty"`
	RewriteDomain bool   `yaml:"rewriteDomain,omitempty"`
	TCPPorts      string `yaml:"tcpPorts,omitempty"`
	UDPPorts      string `yaml:"udpPorts,omitempty"`
}

type RealmConfig struct {
	STUNServers       []string      `yaml:"stunServers,omitempty"`
	STUNTimeout       string        `yaml:"stunTimeout,omitempty"`
	PunchTimeout      string        `yaml:"punchTimeout,omitempty"`
	HeartbeatInterval string        `yaml:"heartbeatInterval,omitempty"`
	Insecure          bool          `yaml:"insecure,omitempty"`
	IPMode            string        `yaml:"ipMode,omitempty"`
	PortMapping       *RealmPortMap `yaml:"portMapping,omitempty"`
}

type RealmPortMap struct {
	Enabled  bool   `yaml:"enabled"`
	Timeout  string `yaml:"timeout,omitempty"`
	Lifetime string `yaml:"lifetime,omitempty"`
}

type ECHConfig struct {
	KeyPath string `yaml:"keyPath"`
}

type MimicConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Interface string   `yaml:"interface,omitempty"`
	XDPMode   string   `yaml:"xdpMode,omitempty"`
	Path      string   `yaml:"path,omitempty"`
	ExtraArgs []string `yaml:"extraArgs,omitempty"`
}

// reservedExtraKeys are managed by the panel and must not be overridden by
// the advanced YAML escape hatch.
var reservedExtraKeys = map[string]bool{
	"listen": true, "auth": true, "trafficStats": true,
	"tls": true, "acme": true, "ech": true, "realm": true, "mimic": true,
	"<<": true,
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
	h := n.Hy2
	port := n.Port
	if port < 1 || port > 65535 {
		port = 443
	}
	listen := strings.TrimSpace(n.Listen)
	if h.IsRealm() {
		listen = strings.TrimSpace(h.Realm.URI)
	} else if h.ListenPortRange != "" {
		listen = ":" + strings.TrimSpace(h.ListenPortRange)
	} else {
		listen = ensureListenPort(listen, port)
	}
	cfg := &ServerConfig{
		Listen: listen,
		Auth: AuthConfig{
			Type: "http",
			HTTP: HTTPAuth{URL: fmt.Sprintf("%s/api/auth?node=%d", panelURL, n.ID)},
		},
		TrafficStats: &TrafficStats{
			Listen: statsListen(h.TrafficListen, defaultInt(n.StatsPort, 9999)),
			Secret: n.StatsSecret,
		},
	}
	switch strings.ToLower(n.TLSMode) {
	case "acme":
		domain := strings.TrimSpace(n.Domain)
		if domain == "" {
			return nil, fmt.Errorf("ACME TLS requires a domain")
		}
		cfg.ACME = buildACME(n, domain)
	case "managed", "selfsigned", "":
		cfg.TLS = &TLSConfig{Cert: CertPath, Key: KeyPath, SNIGuard: sniGuard(h)}
		if h.ClientCA != "" {
			cfg.TLS.ClientCA = ClientCA
		}
	default:
		cfg.TLS = &TLSConfig{Cert: CertPath, Key: KeyPath, SNIGuard: sniGuard(h)}
		if h.ClientCA != "" {
			cfg.TLS.ClientCA = ClientCA
		}
	}
	if n.ObfsType != "" && n.ObfsPassword != "" {
		switch n.ObfsType {
		case "salamander", "gecko":
			cfg.Obfs = &ObfsConfig{Type: n.ObfsType}
			if n.ObfsType == "salamander" {
				cfg.Obfs.Salamander = &ObfsSalamander{Password: n.ObfsPassword}
			} else {
				cfg.Obfs.Gecko = &ObfsGecko{
					Password:      n.ObfsPassword,
					MinPacketSize: h.GeckoMinPacketSize,
					MaxPacketSize: h.GeckoMaxPacketSize,
				}
			}
		}
	}
	if n.BandwidthUp != "" || n.BandwidthDown != "" || h.DisableLossCompensation {
		cfg.Bandwidth = &Bandwidth{
			Up: n.BandwidthUp, Down: n.BandwidthDown,
			DisableLossCompensation: h.DisableLossCompensation,
		}
	}
	if len(n.ACL) > 0 || h.ACLFile != "" || h.GeoIP != "" || h.GeoSite != "" {
		cfg.ACL = &ACLConfig{
			GeoIP: h.GeoIP, GeoSite: h.GeoSite, GeoUpdateInterval: h.GeoUpdateInterval,
		}
		if h.ACLFile != "" {
			cfg.ACL.File = h.ACLFile
		} else {
			cfg.ACL.Inline = n.ACL
		}
	}
	direct := DirectConf{Mode: "auto"}
	if h.Direct != nil {
		if h.Direct.Mode != "" {
			direct.Mode = h.Direct.Mode
		}
		direct.BindIPv4 = h.Direct.BindIPv4
		direct.BindIPv6 = h.Direct.BindIPv6
		direct.BindDevice = h.Direct.BindDevice
		direct.FastOpen = h.Direct.FastOpen
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
			d := direct
			cfg.Outbounds = append(cfg.Outbounds, OutboundConf{Name: name, Type: "direct", Direct: &d})
		}
	}
	if m := buildMasquerade(n); m != nil {
		cfg.Masquerade = m
	}
	if q := buildQUIC(h); q != nil {
		cfg.QUIC = q
	}
	if h.Congestion != nil && h.Congestion.Type != "" {
		cfg.Congestion = &CongestionConfig{Type: h.Congestion.Type, BBRProfile: h.Congestion.BBRProfile}
	}
	cfg.IgnoreClientBandwidth = h.IgnoreClientBandwidth
	cfg.DisableUDP = h.DisableUDP
	cfg.UDPIdleTimeout = h.UDPIdleTimeout
	cfg.SpeedTest = h.SpeedTest
	if r := buildResolver(h.Resolver); r != nil {
		cfg.Resolver = r
	}
	if h.Sniff != nil && h.Sniff.Enable {
		cfg.Sniff = &SniffConfig{
			Enable: true, Timeout: h.Sniff.Timeout, RewriteDomain: h.Sniff.RewriteDomain,
			TCPPorts: h.Sniff.TCPPorts, UDPPorts: h.Sniff.UDPPorts,
		}
	}
	if h.IsRealm() {
		cfg.Realm = buildRealm(h.Realm)
	}
	if h.ECHConfig != "" {
		cfg.ECH = &ECHConfig{KeyPath: ECHPEMPath}
	}
	if h.Mimic != nil && h.Mimic.Enabled {
		cfg.Mimic = &MimicConfig{
			Enabled: true, Interface: strings.TrimSpace(h.Mimic.Interface),
			XDPMode: strings.TrimSpace(h.Mimic.XDPMode), Path: strings.TrimSpace(h.Mimic.Path),
			ExtraArgs: h.Mimic.ExtraArgs,
		}
	}
	return cfg, nil
}

// ensureListenPort keeps a complete host:port listen address, appends the node
// port to a legacy bare host, and falls back to ":port" when listen is empty.
// Rows written before listen validation existed may contain "0.0.0.0" or
// "::", which Hysteria rejects with "missing port in address".
func ensureListenPort(listen string, port int) string {
	if port < 1 || port > 65535 {
		port = 443
	}
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return fmt.Sprintf(":%d", port)
	}
	if _, _, err := net.SplitHostPort(listen); err == nil {
		return listen
	}
	// Legacy rows may contain a bare port such as "15443".
	if p, err := strconv.Atoi(listen); err == nil && p >= 1 && p <= 65535 {
		return ":" + listen
	}
	host := listen
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func sniGuard(h model.Hy2Settings) string {
	if h.TLS != nil && h.TLS.SNIGuard != "" {
		return h.TLS.SNIGuard
	}
	return "disable"
}

func buildACME(n *model.Node, domain string) *ACMEConfig {
	h := n.Hy2
	c := &ACMEConfig{Domains: []string{domain}, Email: n.ACMEEmail, Dir: "/data/acme"}
	if h.ACME == nil {
		c.DisableHTTP = true
		return c
	}
	c.CA = h.ACME.CA
	c.ListenHost = h.ACME.ListenHost
	switch h.ACME.Type {
	case "http":
		c.Type = "http"
		c.HTTP = &ACMEHTTP{AltPort: h.ACME.HTTPPort}
	case "tls":
		c.Type = "tls"
		c.TLS = &ACMETLS{AltPort: h.ACME.TLSPort}
		c.DisableHTTP = true
	case "dns":
		c.Type = "dns"
		c.DNS = &ACMEDNS{Name: h.ACME.DNSName, Config: h.ACME.DNSConfig}
		c.DisableHTTP = true
	default:
		c.DisableHTTP = true
	}
	return c
}

func buildMasquerade(n *model.Node) *Masquerade {
	h := n.Hy2
	m := h.Masquerade
	if m == nil || m.Type == "" {
		if n.MasqueradeURL != "" {
			return &Masquerade{Type: "proxy", Proxy: &MasqProxy{URL: n.MasqueradeURL, RewriteHost: true}}
		}
		return nil
	}
	out := &Masquerade{
		Type: m.Type, ListenHTTP: m.ListenHTTP, ListenHTTPS: m.ListenHTTPS, ForceHTTPS: m.ForceHTTPS,
	}
	switch m.Type {
	case "proxy":
		out.Proxy = &MasqProxy{
			URL: m.ProxyURL, RewriteHost: m.RewriteHost,
			Insecure: m.Insecure, XForwarded: m.XForwarded,
		}
	case "file":
		out.File = &MasqFile{Dir: m.FileDir}
	case "string":
		out.Str = &MasqString{Content: m.Content, Headers: m.Headers, StatusCode: m.StatusCode}
	}
	return out
}

func buildQUIC(h model.Hy2Settings) *QUICConfig {
	if h.QUIC == nil {
		return nil
	}
	q := h.QUIC
	cfg := &QUICConfig{
		InitStreamReceiveWindow: q.InitStreamReceiveWindow,
		MaxStreamReceiveWindow:  q.MaxStreamReceiveWindow,
		InitConnReceiveWindow:   q.InitConnReceiveWindow,
		MaxConnReceiveWindow:    q.MaxConnReceiveWindow,
		MaxIdleTimeout:          q.MaxIdleTimeout,
		MaxIncomingStreams:      q.MaxIncomingStreams,
		DisablePathMTUDiscovery: q.DisablePathMTUDiscovery,
		DisableStatelessReset:   q.DisableStatelessReset,
	}
	if *cfg == (QUICConfig{}) {
		return nil
	}
	return cfg
}

func buildResolver(r *model.Hy2Resolver) *ResolverConfig {
	if r == nil || r.Type == "" || r.Addr == "" {
		return nil
	}
	cfg := &ResolverConfig{Type: r.Type}
	switch r.Type {
	case "udp":
		cfg.UDP = &ResolverAddr{Addr: r.Addr, Timeout: r.Timeout}
	case "tcp":
		cfg.TCP = &ResolverAddr{Addr: r.Addr, Timeout: r.Timeout}
	default:
		cfg.TLS = &ResolverTLS{Addr: r.Addr, Timeout: r.Timeout, SNI: r.SNI, Insecure: r.Insecure}
	}
	if r.Type == "https" {
		cfg.HTTP = cfg.TLS
		cfg.TLS = nil
	}
	return cfg
}

func buildRealm(r *model.Hy2Realm) *RealmConfig {
	cfg := &RealmConfig{
		STUNServers: r.STUNServers, STUNTimeout: r.STUNTimeout,
		PunchTimeout: r.PunchTimeout, HeartbeatInterval: r.HeartbeatInterval,
		Insecure: r.Insecure, IPMode: r.IPMode,
	}
	if r.PortMapping {
		cfg.PortMapping = &RealmPortMap{Enabled: true, Timeout: r.PortMapTimeout, Lifetime: r.PortMapLifetime}
	}
	if len(cfg.STUNServers) == 0 && cfg.STUNTimeout == "" && cfg.PunchTimeout == "" &&
		cfg.HeartbeatInterval == "" && !cfg.Insecure && cfg.IPMode == "" && cfg.PortMapping == nil {
		return nil
	}
	return cfg
}

func statsListen(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		// Host already includes a port; keep it intact for flexibility.
		if _, _, err := splitHostPortLoose(host); err == nil {
			return host
		}
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func splitHostPortLoose(v string) (string, string, error) {
	i := strings.LastIndex(v, ":")
	if i < 0 {
		return v, "", fmt.Errorf("no port")
	}
	return v[:i], v[i+1:], nil
}

// Render returns YAML and its SHA-256 hash. extra YAML is merged after the
// structured config, with panel-managed keys rejected.
func Render(n *model.Node, panelURL string) (string, string, error) {
	cfg, err := Build(n, panelURL)
	if err != nil {
		return "", "", err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return "", "", err
	}
	if extra := strings.TrimSpace(n.Hy2.ExtraYAML); extra != "" {
		b, err = mergeExtraYAML(b, extra)
		if err != nil {
			return "", "", err
		}
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}

// ValidateExtraYAML checks the escape hatch document before a node is saved.
func ValidateExtraYAML(extra string) error {
	if strings.TrimSpace(extra) == "" {
		return nil
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(extra), &m); err != nil {
		return fmt.Errorf("extra YAML is invalid: %w", err)
	}
	for k := range m {
		if reservedExtraKeys[strings.ToLower(k)] {
			return fmt.Errorf("extra YAML cannot override reserved key %q", k)
		}
	}
	return nil
}

// mergeExtraYAML deep-merges an operator supplied YAML document into the
// rendered base config. Reserved top-level keys are rejected.
func mergeExtraYAML(base []byte, extra string) ([]byte, error) {
	var baseMap map[string]any
	if err := yaml.Unmarshal(base, &baseMap); err != nil {
		return nil, err
	}
	if err := ValidateExtraYAML(extra); err != nil {
		return nil, err
	}
	var extraMap map[string]any
	if err := yaml.Unmarshal([]byte(extra), &extraMap); err != nil {
		return nil, fmt.Errorf("extra YAML is invalid: %w", err)
	}
	merged := deepMerge(baseMap, extraMap)
	out, err := yaml.Marshal(merged)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func deepMerge(dst, src map[string]any) map[string]any {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				dst[k] = deepMerge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}

func defaultInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
