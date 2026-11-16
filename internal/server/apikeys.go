package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

type apiKeyRequest struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Scopes      []string   `json:"scopes"`
	NodeIDs     []int64    `json:"nodeIds"`
	GroupIDs    []int64    `json:"groupIds"`
	RateLimit   int        `json:"rateLimit"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListAPIKeys(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       keys,
		"permissions": scopeCatalog(),
		"scopes":      scopeCatalog(), // backward compatibility with older UIs
		"resources":   s.apiKeyResourceOptions(r),
	})
}

// apiKeyResourceOptions lists the node/group choices used by the key editor.
func (s *Server) apiKeyResourceOptions(r *http.Request) map[string]any {
	nodes, _ := s.store.ListNodes(r.Context())
	groups, _ := s.store.ListGroups(r.Context())
	nodeItems := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		gids := make([]int64, 0, len(n.Groups))
		for _, g := range n.Groups {
			gids = append(gids, g.ID)
		}
		nodeItems = append(nodeItems, map[string]any{"id": n.ID, "name": n.Name, "groupIds": gids})
	}
	groupItems := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		groupItems = append(groupItems, map[string]any{"id": g.ID, "name": g.Name})
	}
	return map[string]any{"nodes": nodeItems, "groups": groupItems}
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req apiKeyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if msg := s.validateAPIKeyRequest(r, &req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	token := "ck_" + store.RandomToken(24)
	hash := sha256.Sum256([]byte(token))
	key, err := s.store.CreateAPIKeyFull(r.Context(), req.Name, req.Description, token[:12], hex.EncodeToString(hash[:]),
		req.Scopes, req.NodeIDs, req.GroupIDs, req.RateLimit, req.ExpiresAt)
	if err != nil {
		writeStoreError(w, err, "api key")
		return
	}
	s.store.AddEvent(r.Context(), "api_key.created", 0, "info", "API 密钥 "+key.Name+" 已创建")
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "token": token})
}

func (s *Server) handleUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	var req apiKeyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if msg := s.validateAPIKeyRequest(r, &req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateAPIKey(r.Context(), id, req.Name, req.Description, req.Scopes,
		req.NodeIDs, req.GroupIDs, req.RateLimit, req.ExpiresAt); err != nil {
		writeStoreError(w, err, "api key")
		return
	}
	key, err := s.store.GetAPIKey(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "api key")
		return
	}
	s.store.AddEvent(r.Context(), "api_key.updated", 0, "info", "API 密钥 "+key.Name+" 已更新")
	writeJSON(w, http.StatusOK, map[string]any{"key": key})
}

func (s *Server) handleRotateAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	key, err := s.store.GetAPIKey(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "api key")
		return
	}
	token := "ck_" + store.RandomToken(24)
	hash := sha256.Sum256([]byte(token))
	if err := s.store.RotateAPIKey(r.Context(), id, token[:12], hex.EncodeToString(hash[:])); err != nil {
		writeStoreError(w, err, "api key")
		return
	}
	s.store.AddEvent(r.Context(), "api_key.rotated", 0, "info", "API 密钥 "+key.Name+" 的令牌已轮换")
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "token": token})
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	if err := s.store.DeleteAPIKey(r.Context(), id); err != nil {
		writeStoreError(w, err, "api key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleEnableAPIKey(w http.ResponseWriter, r *http.Request) {
	s.setAPIKeyEnabled(w, r, true)
}

func (s *Server) handleDisableAPIKey(w http.ResponseWriter, r *http.Request) {
	s.setAPIKeyEnabled(w, r, false)
}

func (s *Server) setAPIKeyEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	if err := s.store.SetAPIKeyEnabled(r.Context(), id, enabled); err != nil {
		writeStoreError(w, err, "api key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// validateAPIKeyRequest normalizes the payload and returns an error message.
func (s *Server) validateAPIKeyRequest(r *http.Request, req *apiKeyRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if req.Name == "" {
		return "key name is required"
	}
	req.Scopes = uniqueScopes(req.Scopes)
	if len(req.Scopes) == 0 {
		return "at least one scope is required"
	}
	for _, scope := range req.Scopes {
		if !scopeKnown(scope) {
			return "unknown scope: " + scope
		}
	}
	if req.RateLimit <= 0 {
		req.RateLimit = 60
	}
	if req.RateLimit > 6000 {
		req.RateLimit = 6000
	}
	req.NodeIDs = uniqueInt64s(req.NodeIDs)
	req.GroupIDs = uniqueInt64s(req.GroupIDs)
	if msg := s.validateResourceIDs(r, "node", req.NodeIDs); msg != "" {
		return msg
	}
	if msg := s.validateResourceIDs(r, "group", req.GroupIDs); msg != "" {
		return msg
	}
	return ""
}

func (s *Server) validateResourceIDs(r *http.Request, kind string, ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	exists := map[int64]bool{}
	if kind == "node" {
		nodes, err := s.store.ListNodes(r.Context())
		if err != nil {
			return "failed to validate nodes"
		}
		for _, n := range nodes {
			exists[n.ID] = true
		}
	} else {
		groups, err := s.store.ListGroups(r.Context())
		if err != nil {
			return "failed to validate groups"
		}
		for _, g := range groups {
			exists[g.ID] = true
		}
	}
	for _, id := range ids {
		if !exists[id] {
			return "unknown " + kind + " id: " + strconv.FormatInt(id, 10)
		}
	}
	return ""
}

func uniqueScopes(scopes []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func uniqueInt64s(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
