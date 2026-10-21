package server

import (
	"net/http"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
)

type groupPayload struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	Color             string `json:"color"`
	SubscriptionTitle string `json:"subscriptionTitle"`
	MaxDevices        int    `json:"maxDevices"`
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": groups})
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var p groupPayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		writeError(w, http.StatusBadRequest, "group name is required")
		return
	}
	if p.Color == "" {
		p.Color = "#38bdf8"
	}
	g, err := s.store.CreateGroup(r.Context(), &model.Group{
		Name: p.Name, Description: strings.TrimSpace(p.Description), Color: p.Color,
		SubscriptionTitle: strings.TrimSpace(p.SubscriptionTitle), MaxDevices: p.MaxDevices,
	})
	if err != nil {
		writeStoreError(w, err, "group")
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	existing, err := s.store.GetGroup(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "group")
		return
	}
	var p groupPayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		writeError(w, http.StatusBadRequest, "group name is required")
		return
	}
	existing.Name = strings.TrimSpace(p.Name)
	existing.Description = strings.TrimSpace(p.Description)
	if p.Color != "" {
		existing.Color = p.Color
	}
	existing.SubscriptionTitle = strings.TrimSpace(p.SubscriptionTitle)
	existing.MaxDevices = p.MaxDevices
	updated, err := s.store.UpdateGroup(r.Context(), existing)
	if err != nil {
		writeStoreError(w, err, "group")
		return
	}
	s.syncXrayNodes(r.Context())
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	if err := s.store.DeleteGroup(r.Context(), id); err != nil {
		writeStoreError(w, err, "group")
		return
	}
	s.syncXrayNodes(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
