package model

// Hy2Settings holds the extended Hysteria 2 server options that are not part
// of the legacy Node columns. It is stored as one JSON blob so new upstream
// fields do not require a database migration, mirroring XraySettings.
//
// Every field is optional: the zero value must render exactly the same YAML
// that the panel produced before this struct existed.
type Hy2Settings struct {
	// QUIC transport tuning.
	QUIC *Hy2QUIC `json:"quic,omitempty"`
	// Congestion selects the non-Brutal controller (BBR/Reno).
	Congestion *Hy2Congestion `json:"congestion,omitempty"`
	// Resolver configures the DNS resolver used for client requests.
	Resolver *Hy2Resolver `json:"resolver,omitempty"`
	// Sniff enables protocol sniffing so ACL domain rules work with IP targets.
	Sniff *Hy2Sniff `json:"sniff,omitempty"`
	// Masquerade describes the HTTP/3 disguise behaviour.
	Masquerade *Hy2Masquerade `json:"masquerade,omitempty"`
	// ACME holds advanced ACME options. Basic domains/email stay in Node.
	ACME *Hy2ACME `json:"acme,omitempty"`
	// TLS holds extra TLS options.
	TLS *Hy2TLS `json:"tls,omitempty"`
	// Direct customises direct outbounds.
	Direct *Hy2Direct `json:"direct,omitempty"`
	// Realm enables the NAT-punching P2P mode.
	Realm *Hy2Realm `json:"realm,omitempty"`

	// ECH is the operator supplied ech.pem (private keys + client configs).
	// It is stored as-is but stripped by publicNode before API responses.
	ECHPEM string `json:"echPem,omitempty"`
	// ECHConfig is the base64 client config list derived from ECHPEM.
	ECHConfig string `json:"echConfig,omitempty"`
	// ClientCA is a PEM CA bundle used for mTLS client verification.
	ClientCA string `json:"clientCA,omitempty"`
	// Mimic disguises QUIC packets as TCP (Linux only, requires mimic).
	Mimic *Hy2Mimic `json:"mimic,omitempty"`

	// ListenPortRange asks Hysteria itself to listen on a UDP port range.
	// Mutually exclusive with the agent managed iptables PortRange.
	ListenPortRange string `json:"listenPortRange,omitempty"`
	// IncludeTLS and IncludeHopping choose which client endpoints are
	// published in subscriptions. nil keeps both for nodes created before the
	// toggles existed.
	IncludeTLS     *bool `json:"includeTls,omitempty"`
	IncludeHopping *bool `json:"includeHopping,omitempty"`
	// TrafficListen overrides the host of trafficStats.listen (port stays
	// panel managed).
	TrafficListen string `json:"trafficListen,omitempty"`
	// Limits.
	IgnoreClientBandwidth   bool   `json:"ignoreClientBandwidth,omitempty"`
	DisableLossCompensation bool   `json:"disableLossCompensation,omitempty"`
	DisableUDP              bool   `json:"disableUDP,omitempty"`
	UDPIdleTimeout          string `json:"udpIdleTimeout,omitempty"`
	SpeedTest               bool   `json:"speedTest,omitempty"`
	// Gecko obfuscation tuning. Zero values use Hysteria defaults.
	GeckoMinPacketSize int `json:"geckoMinPacketSize,omitempty"`
	GeckoMaxPacketSize int `json:"geckoMaxPacketSize,omitempty"`
	// ACLFile switches the ACL from inline rules to a file on the node.
	ACLFile string `json:"aclFile,omitempty"`
	// GeoIP/GeoSite database paths and refresh interval.
	GeoIP             string `json:"geoip,omitempty"`
	GeoSite           string `json:"geosite,omitempty"`
	GeoUpdateInterval string `json:"geoUpdateInterval,omitempty"`
	// ExtraYAML is merged into the rendered server config as an escape hatch.
	// Reserved top level keys (listen/auth/trafficStats/tls/acme/ech) are
	// rejected by the panel.
	ExtraYAML string `json:"extraYaml,omitempty"`
}

// Hy2QUIC mirrors Hysteria's quic block.
type Hy2QUIC struct {
	InitStreamReceiveWindow int    `json:"initStreamReceiveWindow,omitempty"`
	MaxStreamReceiveWindow  int    `json:"maxStreamReceiveWindow,omitempty"`
	InitConnReceiveWindow   int    `json:"initConnReceiveWindow,omitempty"`
	MaxConnReceiveWindow    int    `json:"maxConnReceiveWindow,omitempty"`
	MaxIdleTimeout          string `json:"maxIdleTimeout,omitempty"`
	MaxIncomingStreams      int    `json:"maxIncomingStreams,omitempty"`
	DisablePathMTUDiscovery bool   `json:"disablePathMTUDiscovery,omitempty"`
	DisableStatelessReset   bool   `json:"disableStatelessReset,omitempty"`
}

type Hy2Congestion struct {
	Type       string `json:"type,omitempty"`
	BBRProfile string `json:"bbrProfile,omitempty"`
}

type Hy2Resolver struct {
	Type     string `json:"type,omitempty"` // udp | tcp | tls | https
	Addr     string `json:"addr,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
	SNI      string `json:"sni,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
}

type Hy2Sniff struct {
	Enable        bool   `json:"enable,omitempty"`
	Timeout       string `json:"timeout,omitempty"`
	RewriteDomain bool   `json:"rewriteDomain,omitempty"`
	TCPPorts      string `json:"tcpPorts,omitempty"`
	UDPPorts      string `json:"udpPorts,omitempty"`
}

type Hy2Masquerade struct {
	Type        string            `json:"type,omitempty"` // proxy | file | string
	ProxyURL    string            `json:"proxyUrl,omitempty"`
	RewriteHost bool              `json:"rewriteHost,omitempty"`
	Insecure    bool              `json:"insecure,omitempty"`
	XForwarded  bool              `json:"xForwarded,omitempty"`
	FileDir     string            `json:"fileDir,omitempty"`
	Content     string            `json:"content,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	StatusCode  int               `json:"statusCode,omitempty"`
	ListenHTTP  string            `json:"listenHttp,omitempty"`
	ListenHTTPS string            `json:"listenHttps,omitempty"`
	ForceHTTPS  bool              `json:"forceHttps,omitempty"`
}

type Hy2ACME struct {
	CA         string            `json:"ca,omitempty"` // letsencrypt | zerossl
	ListenHost string            `json:"listenHost,omitempty"`
	Type       string            `json:"type,omitempty"` // http | tls | dns
	HTTPPort   int               `json:"httpPort,omitempty"`
	TLSPort    int               `json:"tlsPort,omitempty"`
	DNSName    string            `json:"dnsName,omitempty"`
	DNSConfig  map[string]string `json:"dnsConfig,omitempty"`
}

type Hy2TLS struct {
	SNIGuard string `json:"sniGuard,omitempty"` // strict | dns-san | disable
}

type Hy2Direct struct {
	Mode       string `json:"mode,omitempty"` // auto | 4 | 6 | 46 | 64
	BindIPv4   string `json:"bindIPv4,omitempty"`
	BindIPv6   string `json:"bindIPv6,omitempty"`
	BindDevice string `json:"bindDevice,omitempty"`
	FastOpen   bool   `json:"fastOpen,omitempty"`
}

type Hy2Realm struct {
	URI               string   `json:"uri,omitempty"`
	STUNServers       []string `json:"stunServers,omitempty"`
	STUNTimeout       string   `json:"stunTimeout,omitempty"`
	PunchTimeout      string   `json:"punchTimeout,omitempty"`
	HeartbeatInterval string   `json:"heartbeatInterval,omitempty"`
	Insecure          bool     `json:"insecure,omitempty"`
	IPMode            string   `json:"ipMode,omitempty"` // dual | v4 | v6
	PortMapping       bool     `json:"portMapping,omitempty"`
	PortMapTimeout    string   `json:"portMapTimeout,omitempty"`
	PortMapLifetime   string   `json:"portMapLifetime,omitempty"`
}

type Hy2Mimic struct {
	Enabled   bool     `json:"enabled,omitempty"`
	Interface string   `json:"interface,omitempty"`
	XDPMode   string   `json:"xdpMode,omitempty"` // native | skb
	Path      string   `json:"path,omitempty"`
	ExtraArgs []string `json:"extraArgs,omitempty"`
}

// Hy2ListenPortRange returns the built-in server listen range, if enabled.
func (h Hy2Settings) Hy2ListenPortRange() string {
	return h.ListenPortRange
}

// IsRealm reports whether the node uses the Realms P2P mode.
func (h Hy2Settings) IsRealm() bool {
	return h.Realm != nil && h.Realm.URI != ""
}

// PublishesTLS reports whether the plain TLS endpoint is included in
// subscriptions. A missing toggle defaults to true for backward compatibility.
func (h Hy2Settings) PublishesTLS() bool {
	return h.IncludeTLS == nil || *h.IncludeTLS
}

// PublishesHopping reports whether the port-hopping endpoint is included in
// subscriptions. A missing toggle defaults to true for backward compatibility.
func (h Hy2Settings) PublishesHopping() bool {
	return h.IncludeHopping == nil || *h.IncludeHopping
}
