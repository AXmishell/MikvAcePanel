package subscription

import (
	"fmt"

	"github.com/axmishell/mikvacepanel/internal/model"
	"gopkg.in/yaml.v3"
)

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
