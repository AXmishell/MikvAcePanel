package subscription

import (
	"encoding/base64"
	"sort"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func ContentType(format string) string {
	r, err := RendererFor(format)
	if err != nil {
		return "text/plain; charset=utf-8"
	}
	return r.ContentType()
}

// Render returns the subscription body for a format.
func Render(format string, u *model.User, nodes []model.Node) (string, error) {
	r, err := RendererFor(format)
	if err != nil {
		return "", err
	}
	return r.Render(u, nodes)
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
