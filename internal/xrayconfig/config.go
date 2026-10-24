// Package xrayconfig renders Xray-core server configuration for VLESS, VMess,
// Trojan and Shadowsocks inbounds (REALITY, TLS or plaintext) and embeds the
// panel user list.
package xrayconfig

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

const (
	CertPath   = "/data/certs/cert.pem"
	KeyPath    = "/data/certs/key.pem"
	AccessLog  = "/data/log/access.log"
	APIPort    = 61000
	InboundTag = "vless-in"

	// DefaultSSMethod is used when a Shadowsocks inbound does not specify one.
	// 2022 methods are required for per-user passwords and traffic statistics.
	DefaultSSMethod = "2022-blake3-aes-128-gcm"

	// DefaultLogLevel is Xray's balanced production default.
	DefaultLogLevel = "warning"
	// DefaultRouteStrategy resolves domains only when a routing rule needs an IP.
	DefaultRouteStrategy = "IPIfNonMatch"
)

var defaultSniffDestinations = []string{"http", "tls", "quic"}

// GenerateRealityKeypair creates an X25519 keypair in the encoding expected by
// Xray REALITY (raw URL-safe base64).
func GenerateRealityKeypair() (privateKey, publicKey string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(key.Bytes()), enc.EncodeToString(key.PublicKey().Bytes()), nil
}

// GenerateShortID creates a random REALITY short id (8 hex chars).
func GenerateShortID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SSKeyLen returns the key length used by a Shadowsocks 2022 method.
func SSKeyLen(method string) int {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
		return 32
	default:
		return 16
	}
}

// SSPassword derives a stable per-user Shadowsocks password from the user's
// Xray UUID. It is used by both the server config and client subscriptions.
func SSPassword(u *model.User, method string) string {
	seed := u.XrayUUID
	if seed == "" {
		seed = u.UserID
	}
	sum := sha256.Sum256([]byte("mikvace-ss:" + seed))
	n := SSKeyLen(method)
	return base64.StdEncoding.EncodeToString(sum[:n])
}

// SSServerKey derives the Shadowsocks-2022 server key for an inbound. It is
// never shared with clients, but Xray requires it in the server settings.
func SSServerKey(n *model.Node, tag, method string) string {
	sum := sha256.Sum256([]byte("mikvace-ss-server:" + strconv.FormatInt(n.ID, 10) + ":" + tag))
	return base64.StdEncoding.EncodeToString(sum[:SSKeyLen(method)])
}

// ClientPassword returns the password a user sends to a Trojan/SS inbound.
func ClientPassword(u *model.User) string {
	if u.Password != "" {
		return u.Password
	}
	return u.UserID
}

// foldLegacyInbounds expands the legacy single-inbound fields without
// generating or persisting new key material. Render uses it so it never emits a
// config whose REALITY key differs from what subscriptions advertise.
func foldLegacyInbounds(n *model.Node) {
	ibs := n.Xray.EffectiveInbounds()
	port := n.Port
	if port == 0 {
		port = 443
	}
	for i := range ibs {
		if ibs[i].Port == 0 {
			ibs[i].Port = port
		}
	}
	n.Xray.Inbounds = ibs
}

func normalizeGlobals(x *model.XraySettings) error {
	x.LogLevel = strings.ToLower(strings.TrimSpace(x.LogLevel))
	if x.LogLevel == "" {
		x.LogLevel = DefaultLogLevel
	}
	switch x.LogLevel {
	case "debug", "info", "warning", "error", "none":
	default:
		return fmt.Errorf("unsupported Xray log level %q", x.LogLevel)
	}

	x.RouteStrategy = strings.TrimSpace(x.RouteStrategy)
	if x.RouteStrategy == "" {
		x.RouteStrategy = DefaultRouteStrategy
	}
	switch x.RouteStrategy {
	case "IPIfNonMatch", "IPOnDemand", "AsIs":
	default:
		return fmt.Errorf("unsupported Xray routing strategy %q", x.RouteStrategy)
	}

	if x.Sniffing == nil {
		x.Sniffing = &model.XraySniffing{
			Enabled: true, DestOverride: append([]string{}, defaultSniffDestinations...), RouteOnly: true,
		}
		return nil
	}
	seen := map[string]bool{}
	dest := make([]string, 0, len(x.Sniffing.DestOverride))
	for _, raw := range x.Sniffing.DestOverride {
		value := strings.ToLower(strings.TrimSpace(raw))
		switch value {
		case "http", "tls", "quic":
		case "":
			continue
		default:
			return fmt.Errorf("unsupported Xray sniffing destination %q", value)
		}
		if !seen[value] {
			seen[value] = true
			dest = append(dest, value)
		}
	}
	if x.Sniffing.Enabled && len(dest) == 0 {
		dest = append(dest, defaultSniffDestinations...)
	}
	x.Sniffing.DestOverride = dest
	return nil
}

// Normalize fills defaults for an Xray node. It also guarantees that REALITY
// key material and at least one short id exist and folds the legacy
// single-inbound fields into Inbounds.
func Normalize(n *model.Node) error {
	x := &n.Xray
	if err := normalizeGlobals(x); err != nil {
		return err
	}
	// Fold legacy fields into a single inbound when the node predates
	// multi-inbound support.
	if len(x.Inbounds) == 0 {
		port := n.Port
		if port == 0 {
			port = 443
		}
		tag := strings.TrimSpace(x.InboundTag)
		if tag == "" {
			tag = InboundTag
		}
		x.Inbounds = []model.XrayInbound{{
			Protocol: "vless", Tag: tag, Port: port,
			Transport: x.Transport, Security: x.Security, Flow: x.Flow,
			Fingerprint: x.Fingerprint, ALPN: x.ALPN,
			RealityDest: x.RealityDest, RealitySNI: x.RealitySNI,
			RealityPrivateKey: x.RealityPrivateKey, RealityPublicKey: x.RealityPublicKey,
			RealityShortIDs: x.RealityShortIDs, RealitySpiderX: x.RealitySpiderX,
			WSPath: x.WSPath, WSHost: x.WSHost, GRPCServiceName: x.GRPCServiceName,
		}}
	}
	if x.APIPort == 0 {
		x.APIPort = APIPort
	}
	if x.APIPort < 1 || x.APIPort > 65535 {
		return fmt.Errorf("Xray API port must be between 1 and 65535")
	}
	seenTags := map[string]bool{}
	seenPorts := map[int]bool{x.APIPort: true}
	enabled := 0
	for i := range x.Inbounds {
		ib := &x.Inbounds[i]
		if err := normalizeInbound(n, ib, i); err != nil {
			return err
		}
		if ib.IsEnabled() {
			enabled++
		}
		if seenPorts[ib.Port] {
			return fmt.Errorf("Xray inbound port %d is used more than once", ib.Port)
		}
		seenPorts[ib.Port] = true
		if seenTags[ib.Tag] {
			return fmt.Errorf("Xray inbound tag %q is used more than once", ib.Tag)
		}
		seenTags[ib.Tag] = true
	}
	if enabled == 0 {
		return fmt.Errorf("at least one Xray inbound must be enabled")
	}
	// Keep the legacy fields in sync with the first inbound so old agents,
	// exports and the panel's compatibility views keep working.
	first := x.Inbounds[0]
	x.Transport, x.Security, x.Flow = first.Transport, first.Security, first.Flow
	x.Fingerprint, x.ALPN = first.Fingerprint, append([]string{}, first.ALPN...)
	x.RealityDest, x.RealitySNI = first.RealityDest, append([]string{}, first.RealitySNI...)
	x.RealityPrivateKey, x.RealityPublicKey = first.RealityPrivateKey, first.RealityPublicKey
	x.RealityShortIDs = append([]string{}, first.RealityShortIDs...)
	x.RealitySpiderX = first.RealitySpiderX
	x.WSPath, x.WSHost, x.GRPCServiceName = first.WSPath, first.WSHost, first.GRPCServiceName
	if first.Tag != "" {
		x.InboundTag = first.Tag
	}
	return nil
}

func normalizeInbound(n *model.Node, ib *model.XrayInbound, index int) error {
	ib.Protocol = strings.ToLower(strings.TrimSpace(ib.Protocol))
	if ib.Protocol == "" {
		ib.Protocol = "vless"
	}
	switch ib.Protocol {
	case "vless", "vmess", "trojan", "shadowsocks":
	default:
		return fmt.Errorf("unsupported Xray inbound protocol %q", ib.Protocol)
	}
	if ib.Port == 0 {
		ib.Port = n.Port
	}
	if ib.Port == 0 {
		ib.Port = 443
	}
	if ib.Port < 1 || ib.Port > 65535 {
		return fmt.Errorf("Xray inbound port must be between 1 and 65535")
	}
	ib.Listen = strings.TrimSpace(ib.Listen)
	if ib.Listen == "" {
		ib.Listen = strings.TrimSpace(n.Listen)
	}
	ib.Transport = strings.ToLower(strings.TrimSpace(ib.Transport))
	if ib.Transport == "" {
		ib.Transport = "tcp"
	}
	switch ib.Transport {
	case "tcp", "ws", "grpc", "xhttp", "mkcp":
	default:
		return fmt.Errorf("unsupported Xray transport %q", ib.Transport)
	}
	ib.Security = strings.ToLower(strings.TrimSpace(ib.Security))
	if ib.Security == "" {
		if ib.Protocol == "shadowsocks" {
			ib.Security = "none"
		} else {
			ib.Security = "reality"
		}
	}
	switch ib.Security {
	case "reality", "tls", "none":
	default:
		return fmt.Errorf("unsupported Xray security %q", ib.Security)
	}
	if ib.Protocol == "shadowsocks" && ib.Security != "none" {
		return fmt.Errorf("Shadowsocks inbounds only support security=none")
	}
	if ib.Security == "reality" && ib.Transport != "tcp" && ib.Transport != "xhttp" && ib.Transport != "grpc" {
		return fmt.Errorf("REALITY only supports the TCP, XHTTP and gRPC transports")
	}
	if ib.Protocol != "vless" {
		ib.Flow = ""
	}
	if ib.Protocol == "vless" && ib.Transport == "tcp" && ib.Security != "none" && ib.Flow == "" {
		ib.Flow = "xtls-rprx-vision"
	}
	if ib.Transport != "tcp" {
		ib.Flow = ""
	}
	if ib.Fingerprint == "" {
		ib.Fingerprint = "chrome"
	}
	if ib.WSPath == "" {
		ib.WSPath = "/"
	}
	if ib.GRPCServiceName == "" {
		ib.GRPCServiceName = "grpc"
	}
	if ib.XHTTPPath == "" {
		ib.XHTTPPath = "/"
	}
	if ib.XHTTPMode == "" {
		ib.XHTTPMode = "auto"
	}
	if ib.Protocol == "shadowsocks" {
		ib.SSMethod = strings.ToLower(strings.TrimSpace(ib.SSMethod))
		if ib.SSMethod == "" {
			ib.SSMethod = DefaultSSMethod
		}
		switch ib.SSMethod {
		case "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm":
		default:
			return fmt.Errorf("unsupported Shadowsocks method %q (multi-user supports blake3-aes-*-gcm only)", ib.SSMethod)
		}
	}
	if len(ib.ALPN) == 0 && ib.Security != "none" {
		ib.ALPN = []string{"h2", "http/1.1"}
	}
	if ib.Security == "reality" {
		if ib.RealityDest == "" {
			ib.RealityDest = "www.cloudflare.com:443"
		}
		if len(ib.RealitySNI) == 0 {
			host := strings.Split(ib.RealityDest, ":")[0]
			ib.RealitySNI = []string{host}
		}
		if ib.RealityPrivateKey == "" || ib.RealityPublicKey == "" {
			priv, pub, err := GenerateRealityKeypair()
			if err != nil {
				return err
			}
			ib.RealityPrivateKey, ib.RealityPublicKey = priv, pub
		}
		if len(ib.RealityShortIDs) == 0 {
			ib.RealityShortIDs = []string{GenerateShortID()}
		}
	}
	if strings.TrimSpace(ib.Tag) == "" {
		ib.Tag = ib.Protocol + "-in"
		if index > 0 {
			ib.Tag = fmt.Sprintf("%s-%d", ib.Tag, index+1)
		}
	}
	ib.Tag = strings.TrimSpace(ib.Tag)
	return nil
}

// Render produces the Xray JSON config and its SHA-256 hash.
func Render(n *model.Node, users []model.User) (string, string, error) {
	if n == nil {
		return "", "", fmt.Errorf("nil node")
	}
	if len(n.Xray.Inbounds) == 0 {
		foldLegacyInbounds(n)
	}
	x := n.Xray
	for _, ib := range x.Inbounds {
		if !ib.IsEnabled() {
			continue
		}
		if ib.Security == "reality" && (ib.RealityPrivateKey == "" || len(ib.RealityShortIDs) == 0) {
			return "", "", fmt.Errorf("Xray inbound %q is missing REALITY key material; save the node again", ib.Tag)
		}
	}
	apiPort := x.APIPort
	if apiPort == 0 {
		apiPort = APIPort
	}
	nowT := time.Now().UTC()

	apiInbound := map[string]any{
		"listen": "127.0.0.1", "port": apiPort, "protocol": "dokodemo-door",
		"settings": map[string]any{"address": "127.0.0.1"}, "tag": "API_INBOUND",
	}
	inbounds := []map[string]any{apiInbound}
	enabled := 0
	for _, ib := range x.Inbounds {
		if !ib.IsEnabled() {
			continue
		}
		inbound, err := renderInbound(n, ib, users, nowT)
		if err != nil {
			return "", "", err
		}
		inbounds = append(inbounds, inbound)
		enabled++
	}
	if enabled == 0 {
		return "", "", fmt.Errorf("at least one Xray inbound must be enabled")
	}

	rules := []map[string]any{{
		"type": "field", "inboundTag": []string{"API_INBOUND"}, "outboundTag": "API",
	}}
	logLevel := firstNonEmpty(x.LogLevel, DefaultLogLevel)
	routeStrategy := firstNonEmpty(x.RouteStrategy, DefaultRouteStrategy)
	config := map[string]any{
		"log":   map[string]any{"loglevel": logLevel, "access": AccessLog, "error": ""},
		"api":   map[string]any{"tag": "API", "services": []string{"HandlerService", "StatsService"}},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{"0": map[string]any{
				"statsUserUplink": true, "statsUserDownlink": true,
			}},
			"system": map[string]any{
				"statsInboundUplink": true, "statsInboundDownlink": true,
				"statsOutboundUplink": true, "statsOutboundDownlink": true,
			},
		},
		"inbounds": inbounds,
		"outbounds": []map[string]any{
			{"protocol": "freedom", "tag": "direct"},
			{"protocol": "blackhole", "tag": "block"},
		},
		"routing": map[string]any{
			"domainStrategy": routeStrategy,
			"rules":          rules,
		},
	}
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}

func renderInbound(n *model.Node, ib model.XrayInbound, users []model.User, nowT time.Time) (map[string]any, error) {
	inbound := map[string]any{
		"listen": firstNonEmpty(ib.Listen, "0.0.0.0"),
		"port":   ib.Port, "protocol": ib.Protocol, "tag": ib.Tag,
		"streamSettings": streamSettings(n, ib),
		"sniffing":       sniffingSettings(n.Xray),
	}
	switch ib.Protocol {
	case "vless":
		clients := []map[string]any{}
		for _, u := range users {
			if !userActive(u, nowT) {
				continue
			}
			c := map[string]any{"id": u.XrayUUID, "email": u.UserID, "level": 0}
			if ib.Transport == "tcp" && ib.Security != "none" && ib.Flow != "" {
				c["flow"] = ib.Flow
			}
			clients = append(clients, c)
		}
		inbound["settings"] = map[string]any{"clients": clients, "decryption": "none"}
	case "vmess":
		clients := []map[string]any{}
		for _, u := range users {
			if !userActive(u, nowT) {
				continue
			}
			clients = append(clients, map[string]any{"id": u.XrayUUID, "alterId": 0, "email": u.UserID, "level": 0})
		}
		inbound["settings"] = map[string]any{"clients": clients}
	case "trojan":
		clients := []map[string]any{}
		for _, u := range users {
			if !userActive(u, nowT) {
				continue
			}
			clients = append(clients, map[string]any{"password": ClientPassword(&u), "email": u.UserID, "level": 0})
		}
		inbound["settings"] = map[string]any{"clients": clients}
	case "shadowsocks":
		method := ib.SSMethod
		if method == "" {
			method = DefaultSSMethod
		}
		clients := []map[string]any{}
		for _, u := range users {
			if !userActive(u, nowT) {
				continue
			}
			clients = append(clients, map[string]any{"password": SSPassword(&u, method), "email": u.UserID, "level": 0})
		}
		inbound["settings"] = map[string]any{"method": method, "password": SSServerKey(n, ib.Tag, method), "clients": clients}
	}
	return inbound, nil
}

func sniffingSettings(x model.XraySettings) map[string]any {
	if x.Sniffing == nil {
		return map[string]any{
			"enabled": true, "destOverride": append([]string{}, defaultSniffDestinations...), "routeOnly": true,
		}
	}
	if !x.Sniffing.Enabled {
		return map[string]any{"enabled": false}
	}
	dest := x.Sniffing.DestOverride
	if len(dest) == 0 {
		dest = defaultSniffDestinations
	}
	return map[string]any{
		"enabled": true, "destOverride": append([]string{}, dest...), "routeOnly": x.Sniffing.RouteOnly,
	}
}

func userActive(u model.User, nowT time.Time) bool {
	return u.Enabled && u.XrayUUID != "" && !u.Expired(nowT) && !u.TrafficExceeded()
}

func streamSettings(n *model.Node, ib model.XrayInbound) map[string]any {
	stream := map[string]any{"network": ib.Transport}
	switch ib.Security {
	case "reality":
		rs := map[string]any{
			"dest":        ib.RealityDest,
			"serverNames": ib.RealitySNI,
			"privateKey":  ib.RealityPrivateKey,
			"shortIds":    ib.RealityShortIDs,
		}
		if ib.RealitySpiderX != "" {
			rs["spiderX"] = ib.RealitySpiderX
		}
		stream["security"] = "reality"
		stream["realitySettings"] = rs
	case "tls":
		serverName := ""
		if len(ib.RealitySNI) > 0 {
			serverName = ib.RealitySNI[0]
		}
		if serverName == "" {
			serverName = n.Domain
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = map[string]any{
			"serverName": serverName,
			"minVersion": "1.2",
			"alpn":       ib.ALPN,
			"certificates": []map[string]any{{
				"certificateFile": CertPath,
				"keyFile":         KeyPath,
			}},
		}
	default:
		stream["security"] = "none"
	}
	switch ib.Transport {
	case "ws":
		headers := map[string]any{}
		if ib.WSHost != "" {
			headers["Host"] = ib.WSHost
		}
		stream["wsSettings"] = map[string]any{"path": firstNonEmpty(ib.WSPath, "/"), "headers": headers}
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": firstNonEmpty(ib.GRPCServiceName, "grpc")}
	case "xhttp":
		headers := map[string]any{}
		if ib.XHTTPHost != "" {
			headers["Host"] = ib.XHTTPHost
		}
		stream["xhttpSettings"] = map[string]any{
			"path":    firstNonEmpty(ib.XHTTPPath, "/"),
			"host":    ib.XHTTPHost,
			"mode":    firstNonEmpty(ib.XHTTPMode, "auto"),
			"headers": headers,
		}
	case "mkcp":
		kcp := map[string]any{}
		if ib.KCPSeed != "" {
			kcp["seed"] = ib.KCPSeed
		}
		stream["kcpSettings"] = kcp
	}
	return stream
}

// ListenPorts returns the public TCP/UDP ports an Xray node listens on.
func ListenPorts(n *model.Node) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, ib := range n.Xray.EffectiveInbounds() {
		if !ib.IsEnabled() {
			continue
		}
		port := ib.Port
		if port == 0 {
			port = n.Port
		}
		if port > 0 && !seen[port] {
			seen[port] = true
			out = append(out, port)
		}
	}
	return out
}

// PortList returns the inbounds' ports as a comma separated list.
func PortList(n *model.Node) string {
	ports := ListenPorts(n)
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, strconv.Itoa(p))
	}
	return strings.Join(parts, ",")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
