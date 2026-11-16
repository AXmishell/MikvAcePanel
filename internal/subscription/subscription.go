// Package subscription renders user subscriptions in the formats expected by
// Clash / Mihomo, sing-box, Shadowrocket and generic Hysteria clients.
package subscription

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

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
