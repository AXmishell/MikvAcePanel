package subscription

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
)

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
