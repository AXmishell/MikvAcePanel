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
	"github.com/axmishell/mikvacepanel/internal/xrayconfig"
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
	ECHConfig    string
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
	// Realms mode and mimic cannot be expressed in share links: clients must
	// configure the rendezvous URI manually, and mimic has to be enabled on
	// both ends. Returning no endpoint is better than publishing a dead link.
	if n.Hy2.IsRealm() || (n.Hy2.Mimic != nil && n.Hy2.Mimic.Enabled) {
		return nil
	}
	sni := strings.TrimSpace(n.Domain)
	if sni == "" {
		sni = strings.TrimSpace(n.SNI)
	}
	hasCert := (strings.EqualFold(n.TLSMode, "acme") || strings.EqualFold(n.TLSMode, "managed")) && n.Domain != ""
	// Publish the port Hysteria actually binds. The node port and the listen
	// address may differ, and the built-in range only binds its lowest port.
	base := Endpoint{
		Label: "TLS", Host: host, Port: n.EffectiveListenPort(),
		SNI: sni, HasCert: hasCert, Insecure: !hasCert,
		ObfsType: n.ObfsType, ObfsPassword: n.ObfsPassword,
		ECHConfig: strings.TrimSpace(n.Hy2.ECHConfig),
	}
	out := []Endpoint{}
	if n.Hy2.PublishesTLS() {
		out = append(out, base)
	}
	portRange := strings.TrimSpace(n.PortRange)
	if strings.TrimSpace(n.Hy2.ListenPortRange) != "" {
		portRange = strings.TrimSpace(n.Hy2.ListenPortRange)
	}
	if portRange != "" && n.Hy2.PublishesHopping() {
		hop := base
		hop.Label = "Hopping"
		hop.PortRange = portRange
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
	if e.ECHConfig != "" {
		params = append(params, "ech="+url.QueryEscape(e.ECHConfig))
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
			for _, ep := range XrayEndpoints(n) {
				uri := XrayURI(u, n, ep)
				if uri == "" {
					continue
				}
				name := l.next(ep.Label)
				if idx := strings.LastIndex(uri, "#"); idx >= 0 {
					uri = uri[:idx+1] + url.PathEscape(name)
				}
				lines = append(lines, uri)
			}
		}
		if !n.HasCore("hysteria") {
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
			for _, ep := range XrayEndpoints(n) {
				p := clashXrayProxy(u, n, ep)
				if p == nil {
					continue
				}
				name := l.next(ep.Label)
				p["name"] = name
				proxies = append(proxies, p)
				names = append(names, name)
			}
		}
		if !n.HasCore("hysteria") {
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
			for _, ep := range XrayEndpoints(n) {
				ob := singboxXrayOutbound(u, n, ep)
				if ob == nil {
					continue
				}
				tag := l.next(ep.Label)
				ob["tag"] = tag
				outbounds = append(outbounds, ob)
				tags = append(tags, tag)
			}
		}
		if !n.HasCore("hysteria") {
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
			for _, ep := range XrayEndpoints(n) {
				tag := l.next(ep.Label)
				ob := xrayJSONOutbound(u, n, ep)
				ob["tag"] = tag
				outbounds = append(outbounds, ob)
			}
		}
		if !n.HasCore("hysteria") {
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

// IsXray reports whether a node runs the Xray core.
func IsXray(n model.Node) bool { return n.HasCore("xray") }

func nodeHost(n model.Node) string {
	if strings.TrimSpace(n.Domain) != "" {
		return strings.TrimSpace(n.Domain)
	}
	return strings.TrimSpace(n.IP)
}

// XrayEndpoint is one client-facing Xray inbound of a node.
type XrayEndpoint struct {
	Inbound model.XrayInbound
	Port    int
	Label   string
}

// XrayEndpoints expands the inbounds of an Xray node into client endpoints.
func XrayEndpoints(n model.Node) []XrayEndpoint {
	all := n.Xray.EffectiveInbounds()
	ibs := make([]model.XrayInbound, 0, len(all))
	for _, ib := range all {
		if ib.IsEnabled() {
			ibs = append(ibs, ib)
		}
	}
	if len(ibs) == 0 || nodeHost(n) == "" {
		return nil
	}
	multi := len(ibs) > 1
	out := make([]XrayEndpoint, 0, len(ibs))
	for i, ib := range ibs {
		port := ib.Port
		if port == 0 {
			port = n.Port
		}
		if port == 0 {
			continue
		}
		label := endpointLabel(n)
		if multi {
			tag := strings.TrimSpace(ib.Tag)
			if tag == "" {
				tag = fmt.Sprintf("%s-%d", ib.Protocol, i+1)
			}
			label = strings.TrimSpace(label + " " + tag)
		}
		out = append(out, XrayEndpoint{Inbound: ib, Port: port, Label: label})
	}
	return out
}

// VLESSURI builds a vless:// link for the first VLESS inbound of an Xray node.
func VLESSURI(u *model.User, n model.Node) string {
	for _, ep := range XrayEndpoints(n) {
		if ep.Inbound.Protocol == "vless" || ep.Inbound.Protocol == "" {
			return XrayURI(u, n, ep)
		}
	}
	return ""
}

// XrayURI builds the share link for an Xray endpoint.
func XrayURI(u *model.User, n model.Node, ep XrayEndpoint) string {
	host := nodeHost(n)
	if host == "" {
		return ""
	}
	ib := ep.Inbound
	proto := strings.ToLower(strings.TrimSpace(ib.Protocol))
	if proto == "" {
		proto = "vless"
	}
	switch proto {
	case "vless":
		if u.XrayUUID == "" {
			return ""
		}
		q := xrayQuery(n, ib)
		q.Set("encryption", "none")
		if ib.Transport == "tcp" && ib.Security != "none" && ib.Flow != "" {
			q.Set("flow", ib.Flow)
		}
		return fmt.Sprintf("vless://%s@%s:%d?%s#%s", u.XrayUUID, host, ep.Port, q.Encode(), encodeFragment(ep.Label))
	case "vmess":
		if u.XrayUUID == "" {
			return ""
		}
		obj := map[string]any{
			"v": "2", "ps": ep.Label, "add": host, "port": ep.Port,
			"id": u.XrayUUID, "aid": 0, "scy": "auto", "type": "none",
			"net": firstNonEmpty(ib.Transport, "tcp"),
		}
		if ib.Security != "" && ib.Security != "none" {
			obj["tls"] = ib.Security
		}
		applyShareParams(obj, n, ib)
		b, err := json.Marshal(obj)
		if err != nil {
			return ""
		}
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	case "trojan":
		pw := xrayconfig.ClientPassword(u)
		q := xrayQuery(n, ib)
		return fmt.Sprintf("trojan://%s@%s:%d?%s#%s", url.PathEscape(pw), host, ep.Port, q.Encode(), encodeFragment(ep.Label))
	case "shadowsocks":
		method := firstNonEmpty(ib.SSMethod, xrayconfig.DefaultSSMethod)
		pw := xrayconfig.SSPassword(u, method)
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + pw))
		return fmt.Sprintf("ss://%s@%s:%d#%s", userinfo, host, ep.Port, encodeFragment(ep.Label))
	}
	return ""
}

func xrayQuery(n model.Node, ib model.XrayInbound) url.Values {
	q := url.Values{}
	q.Set("type", firstNonEmpty(ib.Transport, "tcp"))
	security := strings.ToLower(strings.TrimSpace(ib.Security))
	if security == "" {
		security = "none"
	}
	q.Set("security", security)
	switch security {
	case "reality":
		sni := nodeHost(n)
		if len(ib.RealitySNI) > 0 && ib.RealitySNI[0] != "" {
			sni = ib.RealitySNI[0]
		}
		q.Set("sni", sni)
		q.Set("fp", firstNonEmpty(ib.Fingerprint, "chrome"))
		if ib.RealityPublicKey != "" {
			q.Set("pbk", ib.RealityPublicKey)
		}
		if len(ib.RealityShortIDs) > 0 {
			q.Set("sid", ib.RealityShortIDs[0])
		}
		if ib.RealitySpiderX != "" {
			q.Set("spx", ib.RealitySpiderX)
		}
	case "tls":
		sni := n.Domain
		if len(ib.RealitySNI) > 0 && ib.RealitySNI[0] != "" {
			sni = ib.RealitySNI[0]
		}
		if sni != "" {
			q.Set("sni", sni)
		}
		q.Set("fp", firstNonEmpty(ib.Fingerprint, "chrome"))
		if !hasManagedCert(n) {
			q.Set("allowInsecure", "1")
		}
		if len(ib.ALPN) > 0 {
			q.Set("alpn", strings.Join(ib.ALPN, ","))
		}
	}
	switch ib.Transport {
	case "ws":
		q.Set("path", firstNonEmpty(ib.WSPath, "/"))
		if ib.WSHost != "" {
			q.Set("host", ib.WSHost)
		}
	case "grpc":
		q.Set("serviceName", firstNonEmpty(ib.GRPCServiceName, "grpc"))
	case "xhttp":
		q.Set("path", firstNonEmpty(ib.XHTTPPath, "/"))
		if ib.XHTTPHost != "" {
			q.Set("host", ib.XHTTPHost)
		}
		if ib.XHTTPMode != "" {
			q.Set("mode", ib.XHTTPMode)
		}
	case "mkcp":
		if ib.KCPSeed != "" {
			q.Set("seed", ib.KCPSeed)
		}
	}
	return q
}

func applyShareParams(obj map[string]any, n model.Node, ib model.XrayInbound) {
	switch ib.Transport {
	case "ws":
		obj["path"] = firstNonEmpty(ib.WSPath, "/")
		if ib.WSHost != "" {
			obj["host"] = ib.WSHost
		}
	case "grpc":
		obj["path"] = firstNonEmpty(ib.GRPCServiceName, "grpc")
	case "xhttp":
		obj["path"] = firstNonEmpty(ib.XHTTPPath, "/")
		obj["host"] = ib.XHTTPHost
		obj["mode"] = firstNonEmpty(ib.XHTTPMode, "auto")
	case "mkcp":
		obj["path"] = ib.KCPSeed
	}
	switch ib.Security {
	case "reality":
		if len(ib.RealitySNI) > 0 {
			obj["sni"] = ib.RealitySNI[0]
		}
		obj["fp"] = firstNonEmpty(ib.Fingerprint, "chrome")
		obj["pbk"] = ib.RealityPublicKey
		if len(ib.RealityShortIDs) > 0 {
			obj["sid"] = ib.RealityShortIDs[0]
		}
		if ib.RealitySpiderX != "" {
			obj["spx"] = ib.RealitySpiderX
		}
	case "tls":
		obj["sni"] = firstNonEmpty(firstOf(ib.RealitySNI), n.Domain, nodeHost(n))
		obj["fp"] = firstNonEmpty(ib.Fingerprint, "chrome")
		if len(ib.ALPN) > 0 {
			obj["alpn"] = strings.Join(ib.ALPN, ",")
		}
	}
}

func clashXrayProxy(u *model.User, n model.Node, ep XrayEndpoint) map[string]any {
	ib := ep.Inbound
	proto := strings.ToLower(strings.TrimSpace(ib.Protocol))
	if proto == "" {
		proto = "vless"
	}
	transport := clashNetwork(ib.Transport)
	if strings.EqualFold(ib.Transport, "mkcp") {
		// Clash/Mihomo has no mKCP transport.
		return nil
	}
	p := map[string]any{
		"name": ep.Label, "server": nodeHost(n), "port": ep.Port,
		"udp": true, "network": transport,
	}
	switch proto {
	case "vless":
		p["type"] = "vless"
		p["uuid"] = u.XrayUUID
		if ib.Transport == "tcp" && ib.Security != "none" && ib.Flow != "" {
			p["flow"] = ib.Flow
		}
	case "vmess":
		p["type"] = "vmess"
		p["uuid"] = u.XrayUUID
		p["alterId"] = 0
		p["cipher"] = "auto"
	case "trojan":
		p["type"] = "trojan"
		p["password"] = xrayconfig.ClientPassword(u)
	case "shadowsocks":
		method := firstNonEmpty(ib.SSMethod, xrayconfig.DefaultSSMethod)
		p["type"] = "ss"
		p["cipher"] = method
		p["password"] = xrayconfig.SSPassword(u, method)
		return p
	}
	switch strings.ToLower(ib.Security) {
	case "reality":
		p["tls"] = true
		p["servername"] = firstNonEmpty(firstOf(ib.RealitySNI), nodeHost(n))
		p["client-fingerprint"] = firstNonEmpty(ib.Fingerprint, "chrome")
		p["reality-opts"] = map[string]string{
			"public-key": ib.RealityPublicKey,
			"short-id":   firstOf(ib.RealityShortIDs),
		}
	case "tls":
		p["tls"] = true
		p["servername"] = firstNonEmpty(firstOf(ib.RealitySNI), n.Domain, nodeHost(n))
		p["skip-cert-verify"] = !hasManagedCert(n)
		p["client-fingerprint"] = firstNonEmpty(ib.Fingerprint, "chrome")
		if len(ib.ALPN) > 0 {
			p["alpn"] = ib.ALPN
		}
	}
	switch transport {
	case "ws":
		headers := map[string]any{}
		if ib.WSHost != "" {
			headers["Host"] = ib.WSHost
		}
		p["ws-opts"] = map[string]any{"path": firstNonEmpty(ib.WSPath, "/"), "headers": headers}
	case "grpc":
		p["grpc-opts"] = map[string]any{"grpc-service-name": firstNonEmpty(ib.GRPCServiceName, "grpc")}
	case "xhttp":
		opts := map[string]any{
			"path": firstNonEmpty(ib.XHTTPPath, "/"),
			"mode": firstNonEmpty(ib.XHTTPMode, "auto"),
		}
		if ib.XHTTPHost != "" {
			opts["host"] = ib.XHTTPHost
		}
		p["xhttp-opts"] = opts
	case "http":
		opts := map[string]any{"path": []string{firstNonEmpty(ib.XHTTPPath, "/")}}
		if ib.XHTTPHost != "" {
			opts["headers"] = map[string]any{"Host": []string{ib.XHTTPHost}}
		}
		p["h2-opts"] = opts
	}
	return p
}

func clashNetwork(transport string) string {
	switch strings.ToLower(transport) {
	case "ws":
		return "ws"
	case "grpc":
		return "grpc"
	case "xhttp":
		return "xhttp"
	default:
		return "tcp"
	}
}

func singboxXrayOutbound(u *model.User, n model.Node, ep XrayEndpoint) map[string]any {
	ib := ep.Inbound
	proto := strings.ToLower(strings.TrimSpace(ib.Protocol))
	if proto == "" {
		proto = "vless"
	}
	if strings.EqualFold(ib.Transport, "mkcp") {
		// sing-box has no mKCP transport; Xray/v2rayN clients can use it.
		return nil
	}
	ob := map[string]any{"server": nodeHost(n), "server_port": ep.Port}
	switch proto {
	case "vless":
		ob["type"] = "vless"
		ob["uuid"] = u.XrayUUID
		if ib.Transport == "tcp" && ib.Security != "none" && ib.Flow != "" {
			ob["flow"] = ib.Flow
		}
	case "vmess":
		ob["type"] = "vmess"
		ob["uuid"] = u.XrayUUID
		ob["security"] = "auto"
		ob["alter_id"] = 0
	case "trojan":
		ob["type"] = "trojan"
		ob["password"] = xrayconfig.ClientPassword(u)
	case "shadowsocks":
		method := firstNonEmpty(ib.SSMethod, xrayconfig.DefaultSSMethod)
		ob["type"] = "shadowsocks"
		ob["method"] = method
		ob["password"] = xrayconfig.SSPassword(u, method)
		return ob
	}
	ob["tls"] = singboxTLS(n, ib)
	switch strings.ToLower(ib.Transport) {
	case "ws":
		tr := map[string]any{"type": "ws", "path": firstNonEmpty(ib.WSPath, "/")}
		if ib.WSHost != "" {
			tr["headers"] = map[string]any{"Host": ib.WSHost}
		}
		ob["transport"] = tr
	case "grpc":
		ob["transport"] = map[string]any{"type": "grpc", "service_name": firstNonEmpty(ib.GRPCServiceName, "grpc")}
	case "xhttp":
		tr := map[string]any{"type": "xhttp", "path": firstNonEmpty(ib.XHTTPPath, "/")}
		if ib.XHTTPHost != "" {
			tr["host"] = ib.XHTTPHost
		}
		if ib.XHTTPMode != "" {
			tr["mode"] = ib.XHTTPMode
		}
		ob["transport"] = tr
	}
	return ob
}

func singboxTLS(n model.Node, ib model.XrayInbound) map[string]any {
	switch strings.ToLower(ib.Security) {
	case "reality":
		sni := firstNonEmpty(firstOf(ib.RealitySNI), nodeHost(n))
		return map[string]any{
			"enabled": true, "server_name": sni,
			"utls":    map[string]any{"enabled": true, "fingerprint": firstNonEmpty(ib.Fingerprint, "chrome")},
			"reality": map[string]any{"enabled": true, "public_key": ib.RealityPublicKey, "short_id": firstOf(ib.RealityShortIDs)},
		}
	case "tls":
		tls := map[string]any{
			"enabled":     true,
			"server_name": firstNonEmpty(firstOf(ib.RealitySNI), n.Domain, nodeHost(n)),
			"insecure":    !hasManagedCert(n),
		}
		if len(ib.ALPN) > 0 {
			tls["alpn"] = ib.ALPN
		}
		return tls
	default:
		return map[string]any{"enabled": false}
	}
}

func xrayJSONOutbound(u *model.User, n model.Node, ep XrayEndpoint) map[string]any {
	ib := ep.Inbound
	proto := strings.ToLower(strings.TrimSpace(ib.Protocol))
	if proto == "" {
		proto = "vless"
	}
	stream := map[string]any{"network": firstNonEmpty(ib.Transport, "tcp")}
	switch strings.ToLower(ib.Security) {
	case "reality":
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"fingerprint": firstNonEmpty(ib.Fingerprint, "chrome"),
			"serverName":  firstNonEmpty(firstOf(ib.RealitySNI), nodeHost(n)),
			"publicKey":   ib.RealityPublicKey,
			"shortId":     firstOf(ib.RealityShortIDs),
			"spiderX":     ib.RealitySpiderX,
		}
	case "tls":
		stream["security"] = "tls"
		stream["tlsSettings"] = map[string]any{
			"serverName":    firstNonEmpty(firstOf(ib.RealitySNI), n.Domain, nodeHost(n)),
			"allowInsecure": !hasManagedCert(n),
		}
	}
	switch ib.Transport {
	case "ws":
		stream["wsSettings"] = map[string]any{"path": firstNonEmpty(ib.WSPath, "/"), "headers": map[string]any{"Host": ib.WSHost}}
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": firstNonEmpty(ib.GRPCServiceName, "grpc")}
	case "xhttp":
		stream["xhttpSettings"] = map[string]any{"path": firstNonEmpty(ib.XHTTPPath, "/"), "host": ib.XHTTPHost, "mode": firstNonEmpty(ib.XHTTPMode, "auto")}
	case "mkcp":
		stream["kcpSettings"] = map[string]any{"seed": ib.KCPSeed}
	}
	address := nodeHost(n)
	ob := map[string]any{"streamSettings": stream}
	switch proto {
	case "vless":
		vnextUser := map[string]any{"id": u.XrayUUID, "encryption": "none"}
		if ib.Transport == "tcp" && ib.Security != "none" && ib.Flow != "" {
			vnextUser["flow"] = ib.Flow
		}
		ob["protocol"] = "vless"
		ob["settings"] = map[string]any{"vnext": []map[string]any{{
			"address": address, "port": ep.Port, "users": []map[string]any{vnextUser},
		}}}
	case "vmess":
		ob["protocol"] = "vmess"
		ob["settings"] = map[string]any{"vnext": []map[string]any{{
			"address": address, "port": ep.Port,
			"users": []map[string]any{{"id": u.XrayUUID, "alterId": 0, "security": "auto"}},
		}}}
	case "trojan":
		ob["protocol"] = "trojan"
		ob["settings"] = map[string]any{"servers": []map[string]any{{
			"address": address, "port": ep.Port, "password": xrayconfig.ClientPassword(u),
		}}}
	case "shadowsocks":
		method := firstNonEmpty(ib.SSMethod, xrayconfig.DefaultSSMethod)
		ob["protocol"] = "shadowsocks"
		ob["settings"] = map[string]any{"servers": []map[string]any{{
			"address": address, "port": ep.Port, "method": method,
			"password": xrayconfig.SSPassword(u, method),
		}}}
	}
	return ob
}

func hasManagedCert(n model.Node) bool {
	return strings.EqualFold(n.TLSMode, "managed") && n.Domain != ""
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
