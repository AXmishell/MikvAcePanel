package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	usersvc "github.com/axmishell/mikvacepanel/internal/app/users"
	"github.com/axmishell/mikvacepanel/internal/model"
)

func (s *Server) decorateUser(u *model.User) map[string]any {
	onlineMap := s.hub.UserOnlineMap()
	devices := s.hub.DeviceCounts()
	pub, _ := s.store.GetSetting(context.Background(), "public_url")
	pub = strings.TrimRight(pub, "/")
	subURL := ""
	if pub != "" {
		subURL = pub + "/api/files/" + u.SubscriptionToken
	}
	return map[string]any{
		"id": u.ID, "userId": u.UserID, "username": u.Username,
		"subscriptionToken": u.SubscriptionToken, "xrayUuid": u.XrayUUID, "subscriptionUrl": subURL,
		"enabled": u.Enabled, "trafficTx": u.TrafficTx, "trafficRx": u.TrafficRx,
		"trafficUsed": u.TrafficUsed(), "trafficLimit": u.TrafficLimit,
		"maxDevices": u.MaxDevices, "expireAt": u.ExpireAt,
		"groups": u.Groups, "nodeIds": u.NodeIDs,
		"online": onlineMap[u.UserID], "onlineDevices": devices[u.UserID],
		"lastSeenAt": u.LastSeenAt, "createdAt": u.CreatedAt, "updatedAt": u.UpdatedAt,
	}
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("pageSize"))
	groupIDs, nodeIDs, restricted := s.apiKeyUserScope(r)
	var users []model.User
	var total int
	var err error
	if restricted {
		users, total, err = s.store.ListUsersScoped(r.Context(), q.Get("q"), q.Get("status"), page, size, groupIDs, nodeIDs)
	} else {
		users, total, err = s.users.List(r.Context(), q.Get("q"), q.Get("status"), page, size)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(users))
	for i := range users {
		items = append(items, s.decorateUser(&users[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total,
		"page": maxInt(page, 1), "pageSize": clampInt(size, 20, 1, 200)})
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.users.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "user")
		return
	}
	writeJSON(w, http.StatusOK, s.decorateUser(u))
}

// handleUserConnections is the administrator-facing connection viewer. It
// returns the user's subscription links, nodes and endpoints without requiring
// the user's password.
func (s *Server) handleUserConnections(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.users.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "user")
		return
	}
	settings, _ := s.store.GetSettings(r.Context())
	status, reason := userStatus(u, time.Now().UTC())
	used := u.TrafficUsed()
	percent := 0
	if u.TrafficLimit > 0 {
		percent = int(used * 100 / u.TrafficLimit)
		if percent > 100 {
			percent = 100
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id": u.ID, "userId": u.UserID, "username": firstNonEmpty(u.Username, u.UserID),
			"enabled": u.Enabled, "status": status, "statusReason": reason,
			"expireAt": u.ExpireAt, "lastSeenAt": u.LastSeenAt,
		},
		"traffic": map[string]any{
			"used": used, "tx": u.TrafficTx, "rx": u.TrafficRx,
			"limit": u.TrafficLimit, "percent": percent, "unlimited": u.TrafficLimit <= 0,
		},
		"subscription": s.userSubscriptionLinks(r, u, settings),
		"nodes":        s.userNodeConnections(r, u),
	})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var p userPayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if msg := s.apiKeyValidateUserResources(r, p.groups(), p.nodes()); msg != "" {
		writeError(w, http.StatusForbidden, msg)
		return
	}
	result, err := s.users.Create(r.Context(), p.input())
	if err != nil {
		writeUserError(w, err, "create user")
		return
	}
	resp := s.decorateUser(result.User)
	resp["password"] = result.Password
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var p userPayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if msg := s.apiKeyValidateUserResources(r, p.groups(), p.nodes()); msg != "" {
		writeError(w, http.StatusForbidden, msg)
		return
	}
	updated, err := s.users.Update(r.Context(), id, p.input())
	if err != nil {
		writeUserError(w, err, "update user")
		return
	}
	resp := s.decorateUser(updated)
	if p.Password != "" {
		resp["password"] = p.Password
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := s.users.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err, "user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleEnableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserEnabled(w, r, true)
}

func (s *Server) handleDisableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserEnabled(w, r, false)
}

func (s *Server) setUserEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := s.users.SetEnabled(r.Context(), id, enabled); err != nil {
		writeStoreError(w, err, "user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleResetUserSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	token, err := s.users.ResetSubscriptionToken(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "user")
		return
	}
	pub, _ := s.store.GetSetting(r.Context(), "public_url")
	writeJSON(w, http.StatusOK, map[string]string{
		"subscriptionToken": token,
		"subscriptionUrl":   strings.TrimRight(pub, "/") + "/api/files/" + token,
	})
}

func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	_ = decodeJSON(r, &req)
	password, err := s.users.ResetPassword(r.Context(), id, req.Password)
	if err != nil {
		writeUserError(w, err, "user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"password": password})
}

func (s *Server) handleResetUserTraffic(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := s.users.ResetTraffic(r.Context(), id); err != nil {
		writeStoreError(w, err, "user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleResetDevices(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := s.users.ResetDevices(r.Context(), id); err != nil {
		writeStoreError(w, err, "user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writeUserError maps application errors to HTTP responses without leaking
// implementation details.
func writeUserError(w http.ResponseWriter, err error, operation string) {
	var validation *usersvc.ValidationError
	if errors.As(err, &validation) {
		writeError(w, http.StatusBadRequest, validation.Message)
		return
	}
	var conflict *usersvc.ConflictError
	if errors.As(err, &conflict) {
		writeError(w, http.StatusConflict, conflict.Message)
		return
	}
	writeStoreError(w, err, operation)
}
