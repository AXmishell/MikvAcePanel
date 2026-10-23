// Package model contains the data structures shared by the panel server,
// the node agent and the command line client.
package model

import (
	"strings"
	"time"
)

// Admin is a panel operator account.
type Admin struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt,omitempty"`

	TOTPEnabled         bool     `json:"twoFactorEnabled"`
	TOTPSecret          string   `json:"-"`
	TOTPPending         string   `json:"-"`
	TOTPRecovery        []string `json:"-"`
	TOTPPendingRecovery []string `json:"-"`
}

// APIKey is a scoped automation credential. The plaintext token is only
// returned when it is created; only its SHA-256 hash is stored.
type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	RateLimit  int        `json:"rateLimit"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
}

// Group is a logical collection of nodes and users. A user receives the nodes
// of every group they belong to (unless direct node assignments exist).
type Group struct {
	ID                int64     `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	Color             string    `json:"color"`
	SubscriptionTitle string    `json:"subscriptionTitle"`
	MaxDevices        int       `json:"maxDevices"`
	UserCount         int       `json:"userCount"`
	NodeCount         int       `json:"nodeCount"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// User is a subscription / proxy account. Password is only used internally for
// Hysteria authentication and subscription generation.
type User struct {
	ID                int64      `json:"id"`
	UserID            string     `json:"userId"`
	Username          string     `json:"username"`
	Password          string     `json:"-"`
	SubscriptionToken string     `json:"subscriptionToken"`
	XrayUUID          string     `json:"xrayUuid"`
	Enabled           bool       `json:"enabled"`
	TrafficTx         int64      `json:"trafficTx"`
	TrafficRx         int64      `json:"trafficRx"`
	TrafficLimit      int64      `json:"trafficLimit"`
	MaxDevices        int        `json:"maxDevices"`
	ExpireAt          *time.Time `json:"expireAt"`
	Groups            []Group    `json:"groups"`
	NodeIDs           []int64    `json:"nodeIds,omitempty"`
	Online            bool       `json:"online"`
	OnlineDevices     int        `json:"onlineDevices"`
	LastSeenAt        *time.Time `json:"lastSeenAt"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// TrafficUsed returns upload + download in bytes.
func (u *User) TrafficUsed() int64 { return u.TrafficTx + u.TrafficRx }

// Expired reports whether the account's expiry date is in the past.
func (u *User) Expired(now time.Time) bool {
	return u.ExpireAt != nil && u.ExpireAt.Before(now)
}

// TrafficExceeded reports whether the user reached the configured quota.
func (u *User) TrafficExceeded() bool {
	return u.TrafficLimit > 0 && u.TrafficUsed() >= u.TrafficLimit
}

// XrayInbound describes one Xray inbound. A node may expose several inbounds
// with different protocols, ports and transports.
type XrayInbound struct {
	Protocol string `json:"protocol"`
	Tag      string `json:"tag"`
	Listen   string `json:"listen"`
	Port     int    `json:"port"`

	Transport string `json:"transport"`
	Security  string `json:"security"`
	Flow      string `json:"flow"`

	Fingerprint string   `json:"fingerprint"`
	ALPN        []string `json:"alpn,omitempty"`

	RealityDest       string   `json:"realityDest,omitempty"`
	RealitySNI        []string `json:"realitySni,omitempty"`
	RealityPrivateKey string   `json:"realityPrivateKey,omitempty"`
	RealityPublicKey  string   `json:"realityPublicKey,omitempty"`
	RealityShortIDs   []string `json:"realityShortIds,omitempty"`
	RealitySpiderX    string   `json:"realitySpiderX,omitempty"`

	WSPath          string `json:"wsPath,omitempty"`
	WSHost          string `json:"wsHost,omitempty"`
	GRPCServiceName string `json:"grpcServiceName,omitempty"`
	XHTTPPath       string `json:"xhttpPath,omitempty"`
	XHTTPHost       string `json:"xhttpHost,omitempty"`
	XHTTPMode       string `json:"xhttpMode,omitempty"`
	KCPSeed         string `json:"kcpSeed,omitempty"`
	SSMethod        string `json:"ssMethod,omitempty"`
}

// XraySettings holds the Xray inbounds of a node. The legacy single-inbound
// fields are kept so that nodes created before multi-inbound support keep
// working; Normalize folds them into Inbounds.
type XraySettings struct {
	Transport         string        `json:"transport"`
	Security          string        `json:"security"`
	Flow              string        `json:"flow"`
	Fingerprint       string        `json:"fingerprint"`
	ALPN              []string      `json:"alpn"`
	RealityDest       string        `json:"realityDest"`
	RealitySNI        []string      `json:"realitySni"`
	RealityPrivateKey string        `json:"realityPrivateKey"`
	RealityPublicKey  string        `json:"realityPublicKey"`
	RealityShortIDs   []string      `json:"realityShortIds"`
	RealitySpiderX    string        `json:"realitySpiderX"`
	WSPath            string        `json:"wsPath"`
	WSHost            string        `json:"wsHost"`
	GRPCServiceName   string        `json:"grpcServiceName"`
	APIPort           int           `json:"apiPort"`
	InboundTag        string        `json:"inboundTag"`
	Inbounds          []XrayInbound `json:"inbounds,omitempty"`
}

// EffectiveInbounds returns the configured inbounds, or a single inbound
// derived from the legacy fields.
func (x XraySettings) EffectiveInbounds() []XrayInbound {
	if len(x.Inbounds) > 0 {
		return x.Inbounds
	}
	proto := "vless"
	tag := x.InboundTag
	if tag == "" {
		tag = "vless-in"
	}
	transport := x.Transport
	if transport == "" {
		transport = "tcp"
	}
	security := x.Security
	if security == "" {
		security = "reality"
	}
	fingerprint := x.Fingerprint
	if fingerprint == "" {
		fingerprint = "chrome"
	}
	alpn := x.ALPN
	if len(alpn) == 0 && security != "none" {
		alpn = []string{"h2", "http/1.1"}
	}
	return []XrayInbound{{
		Protocol: proto, Tag: tag, Port: 0,
		Transport: transport, Security: security, Flow: x.Flow,
		Fingerprint: fingerprint, ALPN: alpn,
		RealityDest: x.RealityDest, RealitySNI: x.RealitySNI,
		RealityPrivateKey: x.RealityPrivateKey, RealityPublicKey: x.RealityPublicKey,
		RealityShortIDs: x.RealityShortIDs, RealitySpiderX: x.RealitySpiderX,
		WSPath: x.WSPath, WSHost: x.WSHost, GRPCServiceName: x.GRPCServiceName,
		SSMethod: "2022-blake3-aes-128-gcm",
	}}
}

// Outbound is a custom proxy usable from ACL rules.
type Outbound struct {
	Name     string `json:"name" yaml:"name"`
	Type     string `json:"type" yaml:"type"`
	Addr     string `json:"addr" yaml:"addr"`
	Username string `json:"username,omitempty" yaml:"username,omitempty"`
	Password string `json:"password,omitempty" yaml:"password,omitempty"`
	Insecure bool   `json:"insecure,omitempty" yaml:"insecure,omitempty"`
}

// Node is a Hysteria 2 node managed by the panel.
type Node struct {
	ID               int64        `json:"id"`
	Name             string       `json:"name"`
	Type             string       `json:"type"`
	Flag             string       `json:"flag"`
	IP               string       `json:"ip"`
	Domain           string       `json:"domain"`
	SNI              string       `json:"sni"`
	Listen           string       `json:"listen"`
	Port             int          `json:"port"`
	PortRange        string       `json:"portRange"`
	HopInterval      string       `json:"hopInterval"`
	ObfsType         string       `json:"obfsType"`
	ObfsPassword     string       `json:"obfsPassword"`
	StatsPort        int          `json:"statsPort"`
	StatsSecret      string       `json:"-"`
	SSHPort          int          `json:"sshPort"`
	SSHUser          string       `json:"sshUser"`
	SSHPassword      string       `json:"-"`
	SSHKey           string       `json:"-"`
	SSHHostKey       string       `json:"sshHostKey,omitempty"`
	SSHInstallDocker bool         `json:"sshInstallDocker"`
	TLSMode          string       `json:"tlsMode"`
	ACMEEmail        string       `json:"acmeEmail"`
	BandwidthUp      string       `json:"bandwidthUp"`
	BandwidthDown    string       `json:"bandwidthDown"`
	MasqueradeURL    string       `json:"masqueradeUrl"`
	ACL              []string     `json:"acl"`
	Outbounds        []Outbound   `json:"outbounds"`
	Xray             XraySettings `json:"xray"`
	Cores            []string     `json:"cores"`
	Groups           []Group      `json:"groups"`
	MaxOnline        int          `json:"maxOnline"`
	Ranking          float64      `json:"ranking"`
	Active           bool         `json:"active"`
	Status           string       `json:"status"`
	Token            string       `json:"-"`
	TokenPrefix      string       `json:"tokenPrefix"`
	AgentVersion     string       `json:"agentVersion"`
	OS               string       `json:"os"`
	Arch             string       `json:"arch"`
	Hy2Version       string       `json:"hy2Version"`
	XrayVersion      string       `json:"xrayVersion"`
	Runtime          string       `json:"runtime"`
	Capabilities     []string     `json:"capabilities,omitempty"`
	CertFingerprint  string       `json:"certFingerprint,omitempty"`
	CertNotAfter     *time.Time   `json:"certNotAfter,omitempty"`
	LastSeenAt       *time.Time   `json:"lastSeenAt"`
	ConfigRevision   int64        `json:"configRevision"`
	AppliedRevision  int64        `json:"appliedRevision"`
	ConfigError      string       `json:"configError"`
	TrafficTx        int64        `json:"trafficTx"`
	TrafficRx        int64        `json:"trafficRx"`
	OnlineUsers      int          `json:"onlineUsers"`
	CreatedAt        time.Time    `json:"createdAt"`
	UpdatedAt        time.Time    `json:"updatedAt"`
}

// ValidCore reports whether name is a proxy core managed by the agent.
func ValidCore(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "hysteria", "xray":
		return true
	}
	return false
}

// EffectiveCores returns the cores a node should run. Nodes created before
// multi-core support fall back to their legacy Type.
func (n *Node) EffectiveCores() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, core := range n.Cores {
		core = strings.ToLower(strings.TrimSpace(core))
		if !ValidCore(core) || seen[core] {
			continue
		}
		seen[core] = true
		out = append(out, core)
	}
	if len(out) == 0 {
		core := strings.ToLower(strings.TrimSpace(n.Type))
		if !ValidCore(core) {
			core = "hysteria"
		}
		out = []string{core}
	}
	return out
}

// HasCore reports whether the node runs the given core.
func (n *Node) HasCore(core string) bool {
	core = strings.ToLower(strings.TrimSpace(core))
	for _, c := range n.EffectiveCores() {
		if c == core {
			return true
		}
	}
	return false
}

// PrimaryCore is the first configured core; it keeps legacy API/UI fields
// (Type, YAML) meaningful.
func (n *Node) PrimaryCore() string {
	cores := n.EffectiveCores()
	if len(cores) == 0 {
		return "hysteria"
	}
	return cores[0]
}

// NodeConfig is a generated configuration revision for one or more cores.
type NodeConfig struct {
	ID        int64             `json:"id"`
	NodeID    int64             `json:"nodeId"`
	Revision  int64             `json:"revision"`
	YAML      string            `json:"yaml,omitempty"`
	Configs   map[string]string `json:"configs,omitempty"`
	Cores     []string          `json:"cores,omitempty"`
	Hash      string            `json:"hash"`
	CreatedAt time.Time         `json:"createdAt"`
	AppliedAt *time.Time        `json:"appliedAt"`
	LastError string            `json:"lastError"`
}

// LogEntry is a parsed Hysteria 2 log line stored by the panel.
type LogEntry struct {
	ID         int64     `json:"id"`
	NodeID     int64     `json:"nodeId"`
	NodeName   string    `json:"nodeName,omitempty"`
	TS         time.Time `json:"ts"`
	Level      string    `json:"level"`
	Logger     string    `json:"logger"`
	Message    string    `json:"message"`
	Raw        string    `json:"raw"`
	Event      string    `json:"event"`
	ClientIP   string    `json:"clientIp"`
	UserID     string    `json:"userId"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	BytesTx    int64     `json:"bytesTx"`
	BytesRx    int64     `json:"bytesRx"`
	DurationMS int64     `json:"durationMs"`
	Error      string    `json:"error"`
}

// IncomingLog is the shape sent by a node agent before parsing.
type IncomingLog struct {
	TS      time.Time `json:"ts"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Raw     string    `json:"raw"`
}

// NodeMetric is one heartbeat sample.
type NodeMetric struct {
	ID         int64     `json:"id"`
	NodeID     int64     `json:"nodeId"`
	TS         time.Time `json:"ts"`
	CPUPct     float64   `json:"cpuPct"`
	MemUsed    int64     `json:"memUsed"`
	MemTotal   int64     `json:"memTotal"`
	DiskUsed   int64     `json:"diskUsed"`
	DiskTotal  int64     `json:"diskTotal"`
	Load1      float64   `json:"load1"`
	UptimeSec  int64     `json:"uptimeSec"`
	Online     int       `json:"online"`
	TrafficTx  int64     `json:"trafficTx"`
	TrafficRx  int64     `json:"trafficRx"`
	Hy2Running bool      `json:"hy2Running"`
}

// Event is a panel side audit / status event.
type Event struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	NodeID    int64     `json:"nodeId,omitempty"`
	NodeName  string    `json:"nodeName,omitempty"`
	Message   string    `json:"message"`
	Severity  string    `json:"severity"`
	CreatedAt time.Time `json:"createdAt"`
}

// Finding is a rule based log-analysis result.
type Finding struct {
	ID          string    `json:"id"`
	Severity    string    `json:"severity"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	NodeID      int64     `json:"nodeId,omitempty"`
	NodeName    string    `json:"nodeName,omitempty"`
	Count       int64     `json:"count"`
	FirstSeen   time.Time `json:"firstSeen"`
	LastSeen    time.Time `json:"lastSeen"`
	Details     []string  `json:"details,omitempty"`
}

// LogFilter controls log queries.
type LogFilter struct {
	NodeID int64
	Level  string
	Event  string
	Query  string
	From   time.Time
	To     time.Time
	Page   int
	Size   int
}

// LogStatsOptions controls the analysis endpoint.
type LogStatsOptions struct {
	NodeID int64
	From   time.Time
	To     time.Time
}

// Batch is a serialisable log batch sent over the agent websocket.
type LogBatch struct {
	BatchID string        `json:"batchId"`
	Entries []IncomingLog `json:"entries"`
}

// UserTraffic is a per-user traffic delta reported by the node agent.
type UserTraffic struct {
	Tx int64 `json:"tx"`
	Rx int64 `json:"rx"`
}

// StatsBatch carries a traffic sample from a node. Deltas are collected with
// Hysteria's traffic API in clear mode, so they must be applied exactly once.
type StatsBatch struct {
	BatchID   string                 `json:"batchId"`
	Users     map[string]UserTraffic `json:"users"`
	Online    map[string]int         `json:"online"`
	TrafficTx int64                  `json:"trafficTx"`
	TrafficRx int64                  `json:"trafficRx"`
}
