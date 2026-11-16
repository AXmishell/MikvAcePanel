package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// apiKeyScope is the resolved resource restriction of one API key. An
// unrestricted key has restricted == false and may use every resource its
// permission scopes allow.
type apiKeyScope struct {
	restricted       bool
	nodeIDs          map[int64]bool
	groupIDs         map[int64]bool
	explicitGroupIDs map[int64]bool
}

func idSet(ids []int64) map[int64]bool {
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id > 0 {
			out[id] = true
		}
	}
	return out
}

func (s *Server) resolveAPIKeyScope(ctx context.Context, key *model.APIKey) (*apiKeyScope, error) {
	sc := &apiKeyScope{
		restricted:       len(key.NodeIDs) > 0 || len(key.GroupIDs) > 0,
		nodeIDs:          idSet(key.NodeIDs),
		groupIDs:         idSet(key.GroupIDs),
		explicitGroupIDs: idSet(key.GroupIDs),
	}
	if !sc.restricted {
		return sc, nil
	}
	if len(key.GroupIDs) > 0 {
		nodes, err := s.store.ListNodeIDsByGroups(ctx, key.GroupIDs)
		if err != nil {
			return nil, err
		}
		for _, id := range nodes {
			sc.nodeIDs[id] = true
		}
	}
	if len(key.NodeIDs) > 0 {
		groups, err := s.store.ListGroupIDsByNodes(ctx, key.NodeIDs)
		if err != nil {
			return nil, err
		}
		for _, id := range groups {
			sc.groupIDs[id] = true
		}
	}
	return sc, nil
}

func (sc *apiKeyScope) nodeAllowed(id int64) bool {
	return sc == nil || !sc.restricted || sc.nodeIDs[id]
}

func (sc *apiKeyScope) groupAllowed(id int64) bool {
	return sc == nil || !sc.restricted || sc.groupIDs[id]
}

// groupManageAllowed is stricter than groupAllowed: mutating a whole group is
// only permitted when the key was explicitly restricted to that group. A key
// restricted to individual nodes must not be able to delete or rewrite a group
// that also contains out-of-scope nodes.
func (sc *apiKeyScope) groupManageAllowed(id int64) bool {
	return sc == nil || !sc.restricted || sc.explicitGroupIDs[id]
}

func (sc *apiKeyScope) userAllowed(u *model.User) bool {
	if sc == nil || !sc.restricted {
		return true
	}
	for _, g := range u.Groups {
		if sc.groupIDs[g.ID] {
			return true
		}
	}
	for _, id := range u.NodeIDs {
		if sc.nodeIDs[id] {
			return true
		}
	}
	return false
}

func (sc *apiKeyScope) allowedNodeIDs() []int64 {
	out := make([]int64, 0, len(sc.nodeIDs))
	for id := range sc.nodeIDs {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (sc *apiKeyScope) allowedGroupIDs() []int64 {
	out := make([]int64, 0, len(sc.groupIDs))
	for id := range sc.groupIDs {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func containsID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// filterNodeGroupsForRequest removes groups that a resource-restricted key is
// not allowed to see from a node payload.
func filterNodeGroupsForRequest(r *http.Request, n *model.Node) {
	sc := apiKeyScopeFrom(r)
	if sc == nil || !sc.restricted || n == nil {
		return
	}
	groups := n.Groups[:0]
	for _, g := range n.Groups {
		if sc.groupAllowed(g.ID) {
			groups = append(groups, g)
		}
	}
	n.Groups = groups
}

// pathIDSegment returns the numeric id at the given path segment.
func pathIDSegment(path string, index int) (int64, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if index < 0 || index >= len(parts) {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[index], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// globalScopeDenied reports whether a resource-restricted key is trying to use
// an endpoint that aggregates all panel resources. Those responses cannot be
// filtered safely, so they are rejected instead of leaking unrelated data.
func globalScopeDenied(method, path string) bool {
	switch path {
	case "/api/dashboard", "/api/events", "/api/sync", "/api/settings", "/api/cert/settings":
		return true
	}
	if path == "/api/logs" && method == http.MethodDelete {
		return true
	}
	if strings.HasPrefix(path, "/api/backups") || strings.HasPrefix(path, "/api/maintenance/") {
		return true
	}
	return false
}

// apiKeyResourceDenied enforces node/group restrictions for routes that target
// a single resource. List endpoints are filtered in their handlers.
func (s *Server) apiKeyResourceDenied(r *http.Request, key *model.APIKey, sc *apiKeyScope) (bool, error) {
	if sc == nil || !sc.restricted {
		return false, nil
	}
	path := normalizeAPIPath(r.URL.Path)
	method := r.Method
	if globalScopeDenied(method, path) {
		return true, nil
	}
	if id, ok := pathIDSegment(path, 2); ok {
		switch {
		case strings.HasPrefix(path, "/api/nodes/"):
			return !sc.nodeAllowed(id), nil
		case strings.HasPrefix(path, "/api/groups/"):
			if method == http.MethodGet {
				return !sc.groupAllowed(id), nil
			}
			return !sc.groupManageAllowed(id), nil
		case strings.HasPrefix(path, "/api/users/"):
			u, err := s.store.GetUserByID(r.Context(), id)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return true, nil
				}
				return true, err
			}
			return !sc.userAllowed(u), nil
		}
	}
	// Creating a node or reordering the global ranking cannot be expressed
	// inside a restricted scope.
	if path == "/api/nodes" && method == http.MethodPost {
		return true, nil
	}
	if path == "/api/nodes/reorder" && method == http.MethodPost {
		return true, nil
	}
	if path == "/api/groups" && method == http.MethodPost {
		return true, nil
	}
	return false, nil
}

// apiKeyAllowedNodes returns the node ids a restricted key may see. The second
// return value reports whether a restriction is active at all.
func (s *Server) apiKeyAllowedNodes(r *http.Request) ([]int64, bool) {
	sc := apiKeyScopeFrom(r)
	if sc == nil || !sc.restricted {
		return nil, false
	}
	return sc.allowedNodeIDs(), true
}

func apiKeyScopeFrom(r *http.Request) *apiKeyScope {
	sc, _ := r.Context().Value(apiKeyScopeKey).(*apiKeyScope)
	return sc
}

// apiKeyUserScope returns the user-list filter derived from the key's resource
// restriction.
func (s *Server) apiKeyUserScope(r *http.Request) ([]int64, []int64, bool) {
	sc := apiKeyScopeFrom(r)
	if sc == nil || !sc.restricted {
		return nil, nil, false
	}
	return sc.allowedGroupIDs(), sc.allowedNodeIDs(), true
}

// apiKeyValidateUserResources rejects user payloads that would assign a
// resource-restricted key's user to groups or nodes outside its scope.
func (s *Server) apiKeyValidateUserResources(r *http.Request, groupIDs, nodeIDs []int64) string {
	sc := apiKeyScopeFrom(r)
	if sc == nil || !sc.restricted {
		return ""
	}
	for _, id := range groupIDs {
		if !sc.groupManageAllowed(id) {
			return "API key cannot assign group " + strconv.FormatInt(id, 10)
		}
	}
	for _, id := range nodeIDs {
		if !sc.nodeAllowed(id) {
			return "API key cannot assign node " + strconv.FormatInt(id, 10)
		}
	}
	return ""
}
