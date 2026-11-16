package subscription

import (
	"encoding/json"
	"fmt"

	"github.com/axmishell/mikvacepanel/internal/model"
)

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
