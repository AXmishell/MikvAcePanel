package subscription

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
)

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
