package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/xrayconfig"
)

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
