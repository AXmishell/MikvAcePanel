package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

var allowedScopes = map[string]bool{
	"users:read": true, "users:write": true,
	"nodes:read": true, "nodes:write": true,
	"stats:read": true, "sync:write": true,
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.ListAPIKeys(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": keys, "scopes": scopeList()})
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string     `json:"name"`
		Scopes    []string   `json:"scopes"`
		RateLimit int        `json:"rateLimit"`
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "key name is required")
		return
	}
	if len(req.Scopes) == 0 {
		writeError(w, http.StatusBadRequest, "at least one scope is required")
		return
	}
	for _, scope := range req.Scopes {
		if !allowedScopes[scope] {
			writeError(w, http.StatusBadRequest, "unknown scope: "+scope)
			return
		}
	}
	token := "ck_" + store.RandomToken(24)
	hash := sha256.Sum256([]byte(token))
	if req.RateLimit <= 0 {
		req.RateLimit = 60
	}
	key, err := s.store.CreateAPIKey(r.Context(), req.Name, token[:12], hex.EncodeToString(hash[:]), req.Scopes, req.RateLimit, req.ExpiresAt)
	if err != nil {
		writeStoreError(w, err, "api key")
		return
	}
	s.store.AddEvent(r.Context(), "api_key.created", 0, "info", "API 密钥 "+key.Name+" 已创建")
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "token": token})
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

func scopeList() []map[string]string {
	return []map[string]string{
		{"value": "users:read", "label": "Read users"},
		{"value": "users:write", "label": "Create, update and delete users"},
		{"value": "nodes:read", "label": "Read nodes and configurations"},
		{"value": "nodes:write", "label": "Create, update, sync and command nodes"},
		{"value": "stats:read", "label": "Read dashboard, groups and logs"},
		{"value": "sync:write", "label": "Trigger global sync"},
	}
}
