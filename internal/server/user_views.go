package server

import (
	"net/http"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/subscription"
)

// userSubscriptionLinks builds the subscription URLs shown by the
// administrator's per-user connection viewer.
func (s *Server) userSubscriptionLinks(r *http.Request, user *model.User, settings map[string]string) map[string]any {
	base := publicBaseURL(r, settings["public_url"])
	subURL := base + "/api/files/" + user.SubscriptionToken
	return map[string]any{
		"url": subURL, "clashUrl": subURL + "?format=clash",
		"singboxUrl": subURL + "?format=singbox", "uriUrl": subURL + "?format=uri",
		"qr": s.qrDataURI(r.Context(), subURL),
	}
}

// userNodeConnections returns every node and endpoint a user can access.
func (s *Server) userNodeConnections(r *http.Request, user *model.User) []map[string]any {
	nodes, err := s.store.NodesForUser(r.Context(), user.ID)
	if err != nil {
		nodes = nil
	}
	if sc := apiKeyScopeFrom(r); sc != nil && sc.restricted {
		filtered := nodes[:0]
		for _, n := range nodes {
			if sc.nodeAllowed(n.ID) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}
	subscription.SortNodes(nodes)
	out := make([]map[string]any, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		out = append(out, map[string]any{
			"id": n.ID, "name": n.Name, "flag": n.Flag,
			"online": s.hub.Connected(n.ID), "cores": n.EffectiveCores(),
			"configs": nodeConfigEntries(user, n),
		})
	}
	return out
}

// userStatus returns the account state shown by the administrator's user
// viewer. Expiry is checked before the enabled flag because the expiry
// scheduler also disables accounts, and "expired" is more useful than
// "disabled".
func userStatus(u *model.User, now time.Time) (string, string) {
	switch {
	case u.Expired(now):
		return "expired", "订阅已过期"
	case !u.Enabled:
		return "disabled", "账号已停用"
	case u.TrafficExceeded():
		return "quota", "流量配额已用完"
	default:
		return "active", ""
	}
}
