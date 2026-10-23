package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

var userIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{2,64}$`)
var subscriptionTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,128}$`)

type userPayload struct {
	UserID            string     `json:"userId"`
	XrayUUID          string     `json:"xrayUuid"`
	Username          string     `json:"username"`
	Password          string     `json:"password"`
	LoginPassword     string     `json:"loginPassword"`
	SubscriptionToken string     `json:"subscriptionToken"`
	Enabled           *bool      `json:"enabled"`
	TrafficLimit      int64      `json:"trafficLimit"`
	TrafficGB         float64    `json:"trafficLimitGb"`
	MaxDevices        int        `json:"maxDevices"`
	ExpireAt          *time.Time `json:"expireAt"`
	GroupIDs          []int64    `json:"groupIds"`
	Groups            []int64    `json:"groups"`
	NodeIDs           []int64    `json:"nodeIds"`
	Nodes             []int64    `json:"nodes"`
}

func (p *userPayload) groups() []int64 {
	if p.GroupIDs == nil && p.Groups == nil {
		return nil
	}
	out := append([]int64{}, p.GroupIDs...)
	out = append(out, p.Groups...)
	return out
}

func (p *userPayload) nodes() []int64 {
	if p.NodeIDs == nil && p.Nodes == nil {
		return nil
	}
	out := append([]int64{}, p.NodeIDs...)
	out = append(out, p.Nodes...)
	return out
}

func (p *userPayload) limitBytes() int64 {
	if p.TrafficLimit > 0 {
		return p.TrafficLimit
	}
	if p.TrafficGB > 0 {
		return int64(p.TrafficGB * 1024 * 1024 * 1024)
	}
	return 0
}

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
	users, total, err := s.store.ListUsers(r.Context(), q.Get("q"), q.Get("status"), page, size)
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
	u, err := s.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "user")
		return
	}
	writeJSON(w, http.StatusOK, s.decorateUser(u))
}

// handleUserConnections is the administrator-facing connection viewer. It
// returns the same subscription links, nodes and endpoints that the user
// portal shows, without requiring the user's password.
func (s *Server) handleUserConnections(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "user")
		return
	}
	settings, _ := s.store.GetSettings(r.Context())
	status, reason := portalStatus(u, time.Now().UTC())
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
	userID := strings.TrimSpace(p.UserID)
	if userID == "" {
		userID = store.RandomToken(6)
	}
	if !userIDPattern.MatchString(userID) {
		writeError(w, http.StatusBadRequest, "userId must be 2-64 characters (letters, digits, dot, underscore, dash)")
		return
	}
	password := p.Password
	if password == "" {
		password = store.RandomToken(10)
	}
	if len(password) < 6 {
		writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}
	loginPassword := p.LoginPassword
	if loginPassword == "" {
		loginPassword = password
	}
	if len(loginPassword) < 6 {
		writeError(w, http.StatusBadRequest, "loginPassword must be at least 6 characters")
		return
	}
	loginHash, err := bcrypt.GenerateFromPassword([]byte(loginPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash login password")
		return
	}
	subToken := strings.TrimSpace(p.SubscriptionToken)
	if subToken != "" && !subscriptionTokenPattern.MatchString(subToken) {
		writeError(w, http.StatusBadRequest, "subscriptionToken must be 6-128 characters (letters, digits, underscore, dash)")
		return
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	uuid := strings.TrimSpace(p.XrayUUID)
	if uuid == "" {
		uuid = store.RandomUUID()
	}
	u := &model.User{
		UserID: userID, Username: strings.TrimSpace(p.Username), Password: password,
		LoginPasswordHash: string(loginHash), SubscriptionToken: subToken, XrayUUID: uuid,
		Enabled: enabled, TrafficLimit: p.limitBytes(), MaxDevices: p.MaxDevices, ExpireAt: p.ExpireAt,
	}
	created, err := s.store.CreateUser(r.Context(), u, p.groups(), p.nodes())
	if err != nil {
		writeStoreError(w, err, "create user")
		return
	}
	s.store.AddEvent(r.Context(), "user.created", 0, "info", "用户 "+created.UserID+" 已创建")
	s.syncXrayNodes(r.Context())
	resp := s.decorateUser(created)
	resp["password"] = password
	resp["loginPassword"] = loginPassword
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	existing, err := s.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "user")
		return
	}
	var p userPayload
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if strings.TrimSpace(p.UserID) != "" {
		if !userIDPattern.MatchString(strings.TrimSpace(p.UserID)) {
			writeError(w, http.StatusBadRequest, "invalid userId")
			return
		}
		existing.UserID = strings.TrimSpace(p.UserID)
	}
	existing.Username = strings.TrimSpace(p.Username)
	if strings.TrimSpace(p.XrayUUID) != "" {
		existing.XrayUUID = strings.TrimSpace(p.XrayUUID)
	}
	if p.Password != "" {
		if len(p.Password) < 6 {
			writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
			return
		}
		existing.Password = p.Password
	}
	if p.LoginPassword != "" {
		if len(p.LoginPassword) < 6 {
			writeError(w, http.StatusBadRequest, "loginPassword must be at least 6 characters")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(p.LoginPassword), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not hash login password")
			return
		}
		existing.LoginPasswordHash = string(hash)
	}
	newToken := strings.TrimSpace(p.SubscriptionToken)
	if newToken != "" && newToken != existing.SubscriptionToken {
		if !subscriptionTokenPattern.MatchString(newToken) {
			writeError(w, http.StatusBadRequest, "subscriptionToken must be 6-128 characters (letters, digits, underscore, dash)")
			return
		}
		if other, err := s.store.GetUserByToken(r.Context(), newToken); err == nil && other.ID != existing.ID {
			writeError(w, http.StatusConflict, "subscription token already in use")
			return
		}
	}
	if p.Enabled != nil {
		existing.Enabled = *p.Enabled
	}
	existing.TrafficLimit = p.limitBytes()
	existing.MaxDevices = p.MaxDevices
	existing.ExpireAt = p.ExpireAt
	gids := p.groups()
	nids := p.nodes()
	if gids == nil {
		gids = nil
	}
	updated, err := s.store.UpdateUser(r.Context(), existing, gids, nids)
	if err != nil {
		writeStoreError(w, err, "update user")
		return
	}
	if newToken != "" && newToken != updated.SubscriptionToken {
		if err := s.store.SetUserSubscriptionToken(r.Context(), updated.ID, newToken); err != nil {
			writeStoreError(w, err, "update subscription token")
			return
		}
		updated.SubscriptionToken = newToken
	}
	s.store.AddEvent(r.Context(), "user.updated", 0, "info", "用户 "+updated.UserID+" 已更新")
	s.syncXrayNodes(r.Context())
	resp := s.decorateUser(updated)
	if p.Password != "" {
		resp["password"] = p.Password
	}
	if p.LoginPassword != "" {
		resp["loginPassword"] = p.LoginPassword
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, _ := s.store.GetUserByID(r.Context(), id)
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		writeStoreError(w, err, "user")
		return
	}
	if u != nil {
		s.hub.ResetDeviceLimit(u.UserID)
		s.store.AddEvent(r.Context(), "user.deleted", 0, "info", "用户 "+u.UserID+" 已删除")
		s.syncXrayNodes(r.Context())
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
	if err := s.store.SetUserEnabled(r.Context(), id, enabled); err != nil {
		writeStoreError(w, err, "user")
		return
	}
	u, _ := s.store.GetUserByID(r.Context(), id)
	if u != nil {
		typ := "user.disabled"
		if enabled {
			typ = "user.enabled"
		}
		s.store.AddEvent(r.Context(), typ, 0, "info", "用户 "+u.UserID+" "+(map[bool]string{true: "已启用", false: "已禁用"})[enabled])
		s.syncXrayNodes(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleResetUserSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	token, err := s.store.ResetUserSubscriptionToken(r.Context(), id)
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
		Kind     string `json:"kind"`
	}
	_ = decodeJSON(r, &req)
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	password := req.Password
	if password == "" {
		password = store.RandomToken(10)
	}
	if len(password) < 6 {
		writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}
	if kind == "login" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not hash login password")
			return
		}
		if err := s.store.SetUserLoginPasswordHash(r.Context(), id, string(hash)); err != nil {
			writeStoreError(w, err, "user")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"loginPassword": password})
		return
	}
	if err := s.store.ResetUserPassword(r.Context(), id, password); err != nil {
		writeStoreError(w, err, "user")
		return
	}
	s.syncXrayNodes(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"password": password})
}

func (s *Server) handleResetUserTraffic(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "user")
		return
	}
	if err := s.store.ResetUserTraffic(r.Context(), id); err != nil {
		writeStoreError(w, err, "user")
		return
	}
	s.store.AddEvent(r.Context(), "user.traffic_reset", 0, "info", "已重置用户 "+u.UserID+" 的流量统计")
	// The user may have been excluded from Xray because of the quota; re-add
	// them immediately instead of waiting for the next scheduler pass.
	s.syncXrayNodes(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleResetDevices(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "user")
		return
	}
	s.hub.ResetDeviceLimit(u.UserID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeStoreError(w http.ResponseWriter, err error, entity string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, entity+" not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, entity+" already exists")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func maxInt(v, def int) int {
	if v > def {
		return v
	}
	return def
}

func clampInt(v, def, min, max int) int {
	if v == 0 {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
