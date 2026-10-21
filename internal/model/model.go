// Package model contains the data structures shared by the panel server,
// the node agent and the command line client.
package model

import "time"

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

// XraySettings describes the VLESS inbound of an Xray node.
type XraySettings struct {
	Transport         string   `json:"transport"`
	Security          string   `json:"security"`
	Flow              string   `json:"flow"`
	Fingerprint       string   `json:"fingerprint"`
	ALPN              []string `json:"alpn"`
	RealityDest       string   `json:"realityDest"`
	RealitySNI        []string `json:"realitySni"`
	RealityPrivateKey string   `json:"realityPrivateKey"`
	RealityPublicKey  string   `json:"realityPublicKey"`
	RealityShortIDs   []string `json:"realityShortIds"`
	RealitySpiderX    string   `json:"realitySpiderX"`
	WSPath            string   `json:"wsPath"`
	WSHost            string   `json:"wsHost"`
	GRPCServiceName   string   `json:"grpcServiceName"`
	APIPort           int      `json:"apiPort"`
	InboundTag        string   `json:"inboundTag"`
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

// NodeConfig is a generated Hysteria server configuration revision.
type NodeConfig struct {
	ID        int64      `json:"id"`
	NodeID    int64      `json:"nodeId"`
	Revision  int64      `json:"revision"`
	YAML      string     `json:"yaml,omitempty"`
	Hash      string     `json:"hash"`
	CreatedAt time.Time  `json:"createdAt"`
	AppliedAt *time.Time `json:"appliedAt"`
	LastError string     `json:"lastError"`
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
