// Package subscription renders user subscriptions in the formats expected by
// Clash / Mihomo, sing-box, Shadowrocket and generic Hysteria clients.
package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// Endpoint is one client-facing Hysteria 2 endpoint derived from a node.
type Endpoint struct {
	Label        string
	Host         string
	Port         int
	PortRange    string
	HopInterval  string
	SNI          string
	HasCert      bool
	ObfsType     string
	ObfsPassword string
	Insecure     bool
}

// DetectFormat implements the same User-Agent detection order as CELERITY.
func DetectFormat(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "shadowrocket"):
		return "shadowrocket"
	case strings.Contains(ua, "happ"), strings.Contains(ua, "incy"):
		return "uri"
	case regexp.MustCompile(`hiddify|sing-?box|nekobox|nekoray|neko|sfi|sfa|sfm|sft|karing`).MatchString(ua):
		return "singbox"
	case regexp.MustCompile(`clash|stash|surge|loon`).MatchString(ua):
		return "clash"
	default:
		return "uri"
	}
}

// NormalizeFormat maps query parameter aliases.
func NormalizeFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "auto":
		return ""
	case "clash", "yaml":
		return "clash"
	case "singbox", "sing-box", "json":
		return "singbox"
	case "uri", "raw", "plain":
		return "uri"
	case "shadowrocket":
		return "shadowrocket"
	case "xray-json", "v2ray-json":
		return "xray-json"
	default:
		return strings.ToLower(strings.TrimSpace(format))
	}
}

// Endpoints derives the client endpoints of a node.
func Endpoints(n model.Node) []Endpoint {
	host := strings.TrimSpace(n.Domain)
	if host == "" {
		host = strings.TrimSpace(n.IP)
	}
	if host == "" {
		return nil
	}
	sni := strings.TrimSpace(n.Domain)
	if sni == "" {
		sni = strings.TrimSpace(n.SNI)
	}
	hasCert := (strings.EqualFold(n.TLSMode, "acme") || strings.EqualFold(n.TLSMode, "managed")) && n.Domain != ""
	base := Endpoint{
		Label: "TLS", Host: host, Port: defaultInt(n.Port, 443),
		SNI: sni, HasCert: hasCert, Insecure: !hasCert,
		ObfsType: n.ObfsType, ObfsPassword: n.ObfsPassword,
	}
	out := []Endpoint{base}
	if strings.TrimSpace(n.PortRange) != "" {
		hop := base
		hop.Label = "Hopping"
		hop.PortRange = strings.TrimSpace(n.PortRange)
		hop.HopInterval = n.HopInterval
		out = append(out, hop)
	}
	return out
}

// Label returns the display name used by clients.
func Label(n model.Node, e Endpoint) string {
	parts := []string{}
	if n.Flag != "" {
		parts = append(parts, n.Flag)
	}
	parts = append(parts, n.Name, e.Label)
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func defaultInt(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// userInfo returns the URL-escaped userinfo used inside share links.
func userInfo(u *model.User) string {
	return url.UserPassword(u.UserID, u.Password).String()
}

// rawAuth returns the literal value clients must send to Hysteria's auth
// endpoint. Client config formats (Clash, sing-box, Xray JSON) take this value
// as-is, so it must not be URL-escaped.
func rawAuth(u *model.User) string {
	return u.UserID + ":" + u.Password
}

func encodeFragment(s string) string {
	return url.PathEscape(s)
}

// URI builds a hysteria2:// share link.
func URI(u *model.User, n model.Node, e Endpoint) string {
	params := []string{}
	if e.SNI != "" {
		params = append(params, "sni="+url.QueryEscape(e.SNI))
	}
	params = append(params, "alpn=h3")
	if e.HasCert {
		params = append(params, "insecure=0")
	} else {
		params = append(params, "insecure=1")
	}
	if e.PortRange != "" {
		params = append(params, "mport="+url.QueryEscape(e.PortRange))
		if sec := HopSeconds(e.HopInterval); sec > 0 {
			params = append(params, "mportHopInt="+strconv.Itoa(sec))
		}
	}
	if e.ObfsType != "" && e.ObfsPassword != "" {
		params = append(params, "obfs="+url.QueryEscape(e.ObfsType))
		params = append(params, "obfs-password="+url.QueryEscape(e.ObfsPassword))
	}
	return fmt.Sprintf("hysteria2://%s@%s:%d?%s#%s", userInfo(u), e.Host, e.Port,
		strings.Join(params, "&"), encodeFragment(Label(n, e)))
}

// HopSeconds converts a hop interval such as "30s" or "1m" to seconds.
func HopSeconds(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return int(d.Seconds())
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return 0
}

type labeler struct {
	seen map[string]int
}

func newLabeler() *labeler { return &labeler{seen: map[string]int{}} }

func (l *labeler) next(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Node"
	}
	n := l.seen[base]
	l.seen[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s (%d)", base, n+1)
}

// URIList renders plain share links.
func URIList(u *model.User, nodes []model.Node) string {
	l := newLabeler()
	lines := []string{}
	for _, n := range nodes {
		if IsXray(n) {
			uri := VLESSURI(u, n)
			if uri == "" {
				continue
			}
			name := l.next(endpointLabel(n))
			if idx := strings.LastIndex(uri, "#"); idx >= 0 {
				uri = uri[:idx+1] + url.PathEscape(name)
			}
			lines = append(lines, uri)
			continue
		}
		for _, e := range Endpoints(n) {
			uri := URI(u, n, e)
			// Keep the duplicate label logic identical to Clash/sing-box.
			name := l.next(Label(n, e))
			if idx := strings.LastIndex(uri, "#"); idx >= 0 {
				uri = uri[:idx+1] + url.PathEscape(name)
			}
			lines = append(lines, uri)
		}
	}
	return strings.Join(lines, "\n")
}

// Shadowrocket returns the base64 encoded URI list.
func Shadowrocket(u *model.User, nodes []model.Node) string {
	return base64.StdEncoding.EncodeToString([]byte(URIList(u, nodes)))
}

type clashProxy struct {
	Name           string   `yaml:"name"`
	Type           string   `yaml:"type"`
	Server         string   `yaml:"server"`
	Port           int      `yaml:"port"`
	Password       string   `yaml:"password"`
	SNI            string   `yaml:"sni,omitempty"`
	SkipCertVerify bool     `yaml:"skip-cert-verify"`
	ALPN           []string `yaml:"alpn,omitempty"`
	Ports          string   `yaml:"ports,omitempty"`
	HopInterval    int      `yaml:"hop-interval,omitempty"`
	Obfs           string   `yaml:"obfs,omitempty"`
	ObfsPassword   string   `yaml:"obfs-password,omitempty"`
}

type clashGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

type clashVlessProxy struct {
	Name              string            `yaml:"name"`
	Type              string            `yaml:"type"`
	Server            string            `yaml:"server"`
	Port              int               `yaml:"port"`
	UUID              string            `yaml:"uuid"`
	UDP               bool              `yaml:"udp,omitempty"`
	Network           string            `yaml:"network,omitempty"`
	TLS               bool              `yaml:"tls,omitempty"`
	Flow              string            `yaml:"flow,omitempty"`
	Servername        string            `yaml:"servername,omitempty"`
	SkipCertVerify    bool              `yaml:"skip-cert-verify,omitempty"`
	ClientFingerprint string            `yaml:"client-fingerprint,omitempty"`
	ALPN              []string          `yaml:"alpn,omitempty"`
	RealityOpts       map[string]string `yaml:"reality-opts,omitempty"`
	WSOpts            map[string]any    `yaml:"ws-opts,omitempty"`
	GRPCOpts          map[string]any    `yaml:"grpc-opts,omitempty"`
}

type clashConfig struct {
	Proxies     []any        `yaml:"proxies"`
	ProxyGroups []clashGroup `yaml:"proxy-groups"`
	Rules       []string     `yaml:"rules"`
}

// Clash renders a Mihomo/Clash compatible profile.
func Clash(u *model.User, nodes []model.Node) (string, error) {
	auth := rawAuth(u)
	l := newLabeler()
	proxies := []any{}
	names := []string{}
	for _, n := range nodes {
		if IsXray(n) {
			name := l.next(endpointLabel(n))
			p := clashVlessProxy{
				Name: name, Type: "vless", Server: nodeHost(n), Port: n.Port,
				UUID: u.XrayUUID, UDP: true, Network: firstNonEmpty(n.Xray.Transport, "tcp"),
			}
			switch strings.ToLower(n.Xray.Security) {
			case "reality":
				p.TLS = true
				p.Servername = firstNonEmpty(firstOf(n.Xray.RealitySNI), nodeHost(n))
				p.ClientFingerprint = firstNonEmpty(n.Xray.Fingerprint, "chrome")
				p.RealityOpts = map[string]string{
					"public-key": n.Xray.RealityPublicKey,
					"short-id":   firstOf(n.Xray.RealityShortIDs),
				}
				if n.Xray.Transport == "tcp" && n.Xray.Flow != "" {
					p.Flow = n.Xray.Flow
				}
			case "tls":
				p.TLS = true
				p.Servername = firstNonEmpty(firstOf(n.Xray.RealitySNI), n.Domain, nodeHost(n))
				p.SkipCertVerify = !(strings.EqualFold(n.TLSMode, "managed") && n.Domain != "")
				p.ClientFingerprint = firstNonEmpty(n.Xray.Fingerprint, "chrome")
				p.ALPN = n.Xray.ALPN
				if n.Xray.Transport == "tcp" && n.Xray.Flow != "" {
					p.Flow = n.Xray.Flow
				}
			}
			switch n.Xray.Transport {
			case "ws":
				headers := map[string]any{}
				if n.Xray.WSHost != "" {
					headers["Host"] = n.Xray.WSHost
				}
				p.WSOpts = map[string]any{"path": firstNonEmpty(n.Xray.WSPath, "/"), "headers": headers}
			case "grpc":
				p.GRPCOpts = map[string]any{"grpc-service-name": firstNonEmpty(n.Xray.GRPCServiceName, "grpc")}
			}
			proxies = append(proxies, p)
			names = append(names, name)
			continue
		}
		for _, e := range Endpoints(n) {
			name := l.next(Label(n, e))
			p := clashProxy{
				Name: name, Type: "hysteria2", Server: e.Host, Port: e.Port,
				Password: auth, SNI: e.SNI, SkipCertVerify: !e.HasCert,
				ALPN: []string{"h3"},
			}
			if e.PortRange != "" {
				p.Ports = e.PortRange
				p.HopInterval = HopSeconds(e.HopInterval)
			}
			if e.ObfsType != "" && e.ObfsPassword != "" {
				p.Obfs = e.ObfsType
				p.ObfsPassword = e.ObfsPassword
			}
			proxies = append(proxies, p)
			names = append(names, name)
		}
	}
	if len(proxies) == 0 {
		return "", fmt.Errorf("no servers available")
	}
	cfg := clashConfig{
		Proxies: proxies,
		ProxyGroups: []clashGroup{
			{Name: "Proxy", Type: "select", Proxies: names},
			{Name: "Auto", Type: "url-test", Proxies: names},
		},
		Rules: []string{"MATCH,Proxy"},
	}
	// url-test requires url/interval; they are not part of clashGroup above.
	cfg.ProxyGroups[1].Proxies = names
	yamlBytes, err := yaml.Marshal(clashGroupWithTest(cfg))
	if err != nil {
		return "", err
	}
	return string(yamlBytes), nil
}

// clashGroupWithTest injects url-test specific fields without complicating the
// serialisation of the basic group shape.
func clashGroupWithTest(c clashConfig) map[string]any {
	groups := []map[string]any{}
	for i, g := range c.ProxyGroups {
		m := map[string]any{"name": g.Name, "type": g.Type, "proxies": g.Proxies}
		if i == 1 {
			m["url"] = "https://www.gstatic.com/generate_204"
			m["interval"] = 300
		}
		groups = append(groups, m)
	}
	return map[string]any{
		"proxies":      c.Proxies,
		"proxy-groups": groups,
		"rules":        c.Rules,
	}
}

// Singbox renders a sing-box 1.11+ profile with a TUN inbound and a selector.
func Singbox(u *model.User, nodes []model.Node) (string, error) {
	auth := rawAuth(u)
	l := newLabeler()
	outbounds := []map[string]any{}
	tags := []string{}
	for _, n := range nodes {
		if IsXray(n) {
			tag := l.next(endpointLabel(n))
			tls := map[string]any{"enabled": true}
			switch strings.ToLower(n.Xray.Security) {
			case "reality":
				tls["server_name"] = firstNonEmpty(firstOf(n.Xray.RealitySNI), nodeHost(n))
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": firstNonEmpty(n.Xray.Fingerprint, "chrome")}
				tls["reality"] = map[string]any{
					"enabled": true, "public_key": n.Xray.RealityPublicKey,
					"short_id": firstOf(n.Xray.RealityShortIDs),
				}
			case "tls":
				tls["server_name"] = firstNonEmpty(firstOf(n.Xray.RealitySNI), n.Domain, nodeHost(n))
				tls["insecure"] = !(strings.EqualFold(n.TLSMode, "managed") && n.Domain != "")
				if len(n.Xray.ALPN) > 0 {
					tls["alpn"] = n.Xray.ALPN
				}
			default:
				tls = map[string]any{"enabled": false}
			}
			ob := map[string]any{
				"type": "vless", "tag": tag, "server": nodeHost(n), "server_port": n.Port,
				"uuid": u.XrayUUID, "tls": tls,
			}
			if n.Xray.Transport == "tcp" && n.Xray.Security != "none" && n.Xray.Flow != "" {
				ob["flow"] = n.Xray.Flow
			}
			switch n.Xray.Transport {
			case "ws":
				tr := map[string]any{"type": "ws", "path": firstNonEmpty(n.Xray.WSPath, "/")}
				if n.Xray.WSHost != "" {
					tr["headers"] = map[string]any{"Host": n.Xray.WSHost}
				}
				ob["transport"] = tr
			case "grpc":
				ob["transport"] = map[string]any{"type": "grpc", "service_name": firstNonEmpty(n.Xray.GRPCServiceName, "grpc")}
			}
			outbounds = append(outbounds, ob)
			tags = append(tags, tag)
			continue
		}
		for _, e := range Endpoints(n) {
			tag := l.next(Label(n, e))
			ob := map[string]any{
				"type": "hysteria2", "tag": tag, "server": e.Host, "password": auth,
				"tls": map[string]any{
					"enabled": true, "server_name": e.SNI, "insecure": !e.HasCert,
					"alpn": []string{"h3"},
				},
			}
			if e.PortRange != "" {
				ob["server_ports"] = []string{strings.ReplaceAll(e.PortRange, "-", ":")}
				if e.HopInterval != "" {
					ob["hop_interval"] = e.HopInterval
				}
			} else {
				ob["server_port"] = e.Port
			}
			if e.ObfsType != "" && e.ObfsPassword != "" {
				ob["obfs"] = map[string]any{"type": e.ObfsType, "password": e.ObfsPassword}
			}
			outbounds = append(outbounds, ob)
			tags = append(tags, tag)
		}
	}
	if len(outbounds) == 0 {
		return "", fmt.Errorf("no servers available")
	}
	all := []map[string]any{
		{
			"type": "selector", "tag": "proxy", "outbounds": append([]string{"auto"}, tags...),
			"default": tags[0], "interrupt_exist_connections": true,
		},
		{
			"type": "urltest", "tag": "auto", "outbounds": tags,
			"url": "https://www.gstatic.com/generate_204", "interval": "3m",
			"tolerance": 50, "interrupt_exist_connections": true,
		},
	}
	all = append(all, outbounds...)
	all = append(all, map[string]any{"type": "direct", "tag": "direct"})
	cfg := map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"dns": map[string]any{
			"servers": []map[string]any{
				{"type": "tls", "tag": "dns-remote", "server": "8.8.8.8", "domain_resolver": "dns-local", "detour": "proxy"},
				{"type": "udp", "tag": "dns-local", "server": "223.5.5.5", "detour": "direct"},
			},
			"final": "dns-remote",
		},
		"inbounds": []map[string]any{{
			"type": "tun", "tag": "tun-in",
			"address": []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
			"mtu":     9000, "auto_route": true, "strict_route": true, "stack": "system",
		}},
		"outbounds": all,
		"route": map[string]any{
			"rules": []map[string]any{
				{"protocol": "dns", "action": "hijack-dns"},
				{"inbound": "tun-in", "action": "sniff"},
				{"ip_is_private": true, "action": "route", "outbound": "direct"},
			},
			"final": "proxy", "auto_detect_interface": true,
			"default_domain_resolver": "dns-local",
		},
		"experimental": map[string]any{"cache_file": map[string]any{"enabled": true}},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Xray renders a minimal Xray/V2Ray JSON profile with Hysteria 2 outbounds.
func Xray(u *model.User, nodes []model.Node) (string, error) {
	l := newLabeler()
	outbounds := []map[string]any{}
	for _, n := range nodes {
		if IsXray(n) {
			tag := l.next(endpointLabel(n))
			stream := map[string]any{"network": firstNonEmpty(n.Xray.Transport, "tcp")}
			switch strings.ToLower(n.Xray.Security) {
			case "reality":
				stream["security"] = "reality"
				stream["realitySettings"] = map[string]any{
					"fingerprint": firstNonEmpty(n.Xray.Fingerprint, "chrome"),
					"serverName":  firstNonEmpty(firstOf(n.Xray.RealitySNI), nodeHost(n)),
					"publicKey":   n.Xray.RealityPublicKey,
					"shortId":     firstOf(n.Xray.RealityShortIDs),
					"spiderX":     n.Xray.RealitySpiderX,
				}
			case "tls":
				stream["security"] = "tls"
				stream["tlsSettings"] = map[string]any{
					"serverName":    firstNonEmpty(firstOf(n.Xray.RealitySNI), n.Domain, nodeHost(n)),
					"allowInsecure": !(strings.EqualFold(n.TLSMode, "managed") && n.Domain != ""),
				}
			}
			switch n.Xray.Transport {
			case "ws":
				stream["wsSettings"] = map[string]any{"path": firstNonEmpty(n.Xray.WSPath, "/"), "headers": map[string]any{"Host": n.Xray.WSHost}}
			case "grpc":
				stream["grpcSettings"] = map[string]any{"serviceName": firstNonEmpty(n.Xray.GRPCServiceName, "grpc")}
			}
			vnextUser := map[string]any{"id": u.XrayUUID, "encryption": "none"}
			if n.Xray.Transport == "tcp" && n.Xray.Security != "none" && n.Xray.Flow != "" {
				vnextUser["flow"] = n.Xray.Flow
			}
			outbounds = append(outbounds, map[string]any{
				"tag": tag, "protocol": "vless",
				"settings": map[string]any{"vnext": []map[string]any{{
					"address": nodeHost(n), "port": n.Port, "users": []map[string]any{vnextUser},
				}}},
				"streamSettings": stream,
			})
			continue
		}
		for _, e := range Endpoints(n) {
			tag := l.next(Label(n, e))
			hy := map[string]any{"version": 2, "auth": rawAuth(u)}
			if e.PortRange != "" {
				hop := map[string]any{"port": e.PortRange}
				if sec := HopSeconds(e.HopInterval); sec > 0 {
					hop["interval"] = sec
				}
				hy["udphop"] = hop
			}
			stream := map[string]any{
				"network": "hysteria", "hysteriaSettings": hy, "security": "tls",
				"tlsSettings": map[string]any{
					"serverName": e.SNI, "allowInsecure": !e.HasCert, "alpn": []string{"h3"},
				},
			}
			if e.ObfsType != "" && e.ObfsPassword != "" {
				stream["udpmasks"] = []map[string]any{{
					"type": e.ObfsType, "settings": map[string]any{"password": e.ObfsPassword},
				}}
			}
			outbounds = append(outbounds, map[string]any{
				"tag": tag, "protocol": "hysteria",
				"settings":       map[string]any{"version": 2, "address": e.Host, "port": e.Port},
				"streamSettings": stream,
			})
		}
	}
	if len(outbounds) == 0 {
		return "", fmt.Errorf("no servers available")
	}
	outbounds = append(outbounds,
		map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{}},
		map[string]any{"tag": "block", "protocol": "blackhole", "settings": map[string]any{}},
	)
	cfg := map[string]any{"outbounds": outbounds}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ContentType returns the HTTP content type for a format.
func ContentType(format string) string {
	switch format {
	case "clash":
		return "text/yaml; charset=utf-8"
	case "singbox", "xray-json":
		return "application/json; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}

// Render returns the subscription body for a format.
func Render(format string, u *model.User, nodes []model.Node) (string, error) {
	switch format {
	case "clash":
		return Clash(u, nodes)
	case "singbox":
		return Singbox(u, nodes)
	case "shadowrocket":
		return Shadowrocket(u, nodes), nil
	case "xray-json":
		return Xray(u, nodes)
	case "uri", "":
		return URIList(u, nodes), nil
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}
}

// Title returns the profile title from the first group or settings.
func Title(u *model.User, fallback string) string {
	if len(u.Groups) > 0 {
		if t := strings.TrimSpace(u.Groups[0].SubscriptionTitle); t != "" {
			return t
		}
		if t := strings.TrimSpace(u.Groups[0].Name); t != "" {
			return t
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "MikvAce"
}

// EncodeTitle emits the Marzban-compatible base64 profile title header.
func EncodeTitle(title string) string {
	return "base64:" + base64.StdEncoding.EncodeToString([]byte(title))
}

// SortNodes orders nodes by ranking then name for stable subscriptions.
func SortNodes(nodes []model.Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Ranking == nodes[j].Ranking {
			return nodes[i].ID < nodes[j].ID
		}
		return nodes[i].Ranking > nodes[j].Ranking
	})
}

// IsXray reports whether a node uses the Xray/VLESS protocol.
func IsXray(n model.Node) bool { return strings.EqualFold(n.Type, "xray") }

func nodeHost(n model.Node) string {
	if strings.TrimSpace(n.Domain) != "" {
		return strings.TrimSpace(n.Domain)
	}
	return strings.TrimSpace(n.IP)
}

// VLESSURI builds a vless:// link for an Xray node.
func VLESSURI(u *model.User, n model.Node) string {
	host, uri, ok := vlessParts(u, n)
	if !ok {
		return ""
	}
	return uri + "#" + encodeFragment(host)
}

func vlessParts(u *model.User, n model.Node) (label, uri string, ok bool) {
	host := nodeHost(n)
	if host == "" || u.XrayUUID == "" || n.Port == 0 {
		return "", "", false
	}
	x := n.Xray
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("type", x.Transport)
	if q.Get("type") == "" {
		q.Set("type", "tcp")
	}
	security := strings.ToLower(x.Security)
	if security == "" {
		security = "reality"
	}
	q.Set("security", security)
	switch security {
	case "reality":
		sni := host
		if len(x.RealitySNI) > 0 && x.RealitySNI[0] != "" {
			sni = x.RealitySNI[0]
		}
		q.Set("sni", sni)
		q.Set("fp", firstNonEmpty(x.Fingerprint, "chrome"))
		if x.RealityPublicKey != "" {
			q.Set("pbk", x.RealityPublicKey)
		}
		if len(x.RealityShortIDs) > 0 {
			q.Set("sid", x.RealityShortIDs[0])
		}
		if x.RealitySpiderX != "" {
			q.Set("spx", x.RealitySpiderX)
		}
	case "tls":
		sni := n.Domain
		if len(x.RealitySNI) > 0 && x.RealitySNI[0] != "" {
			sni = x.RealitySNI[0]
		}
		if sni != "" {
			q.Set("sni", sni)
		}
		q.Set("fp", firstNonEmpty(x.Fingerprint, "chrome"))
		// Only fall back to skipping verification when the node has no
		// panel-issued certificate for its domain.
		if !(strings.EqualFold(n.TLSMode, "managed") && n.Domain != "") {
			q.Set("allowInsecure", "1")
		}
		if len(x.ALPN) > 0 {
			q.Set("alpn", strings.Join(x.ALPN, ","))
		}
	}
	switch x.Transport {
	case "ws":
		q.Set("path", firstNonEmpty(x.WSPath, "/"))
		if x.WSHost != "" {
			q.Set("host", x.WSHost)
		}
	case "grpc":
		q.Set("serviceName", firstNonEmpty(x.GRPCServiceName, "grpc"))
	}
	if x.Transport == "tcp" && security != "none" && x.Flow != "" {
		q.Set("flow", x.Flow)
	}
	label = strings.TrimSpace(strings.Join([]string{n.Flag, n.Name}, " "))
	uri = fmt.Sprintf("vless://%s@%s:%d?%s", u.XrayUUID, host, n.Port, q.Encode())
	return label, uri, true
}

func endpointLabel(n model.Node) string {
	return strings.TrimSpace(strings.Join([]string{n.Flag, n.Name}, " "))
}

func firstOf(vals []string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
