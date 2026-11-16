package subscription

import (
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/xrayconfig"
)

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
