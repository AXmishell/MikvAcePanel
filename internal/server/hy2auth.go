package server

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// handleHy2Auth implements the Hysteria 2 external HTTP authentication
// contract:
//
//	POST /api/auth?node=<id>
//	{"addr":"1.2.3.4:5678","auth":"userId:password","tx":123}
//	=> {"ok":true,"id":"userId"}
func (s *Server) handleHy2Auth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Addr string `json:"addr"`
		Auth string `json:"auth"`
		Tx   uint64 `json:"tx"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false})
		return
	}
	userID, password, ok := strings.Cut(req.Auth, ":")
	if !ok || userID == "" || password == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false})
		return
	}
	ctx := r.Context()
	nodeID, _ := strconv.ParseInt(r.URL.Query().Get("node"), 10, 64)
	if len(userID) > 128 {
		userID = userID[:128]
	}
	// The address is supplied by the node and ends up in a log row, so bound
	// it to keep oversized request bodies from bloating the database.
	addr := strings.TrimSpace(req.Addr)
	if len(addr) > 128 {
		addr = addr[:128]
	}
	logReject := func(reason string) {
		if nodeID <= 0 {
			return
		}
		// Never write logs for node ids that do not exist: this endpoint is
		// unauthenticated, so otherwise anyone could inject arbitrary rows.
		if exists, err := s.store.NodeExists(ctx, nodeID); err != nil || !exists {
			return
		}
		if !s.allowAuthFailureLog(nodeID) {
			return
		}
		entry := model.LogEntry{
			NodeID: nodeID, TS: time.Now().UTC(), Level: "WARN", Logger: "panel-auth",
			Message: "authentication failed: " + reason, Event: "auth_fail",
			ClientIP: clientIP(addr, r.RemoteAddr), UserID: userID,
			Raw:   "panel auth rejected user " + userID + " from " + addr + ": " + reason,
			Error: reason,
		}
		_, _ = s.store.InsertLogBatch(ctx, nodeID, "panel-"+store.RandomToken(8), []model.LogEntry{entry})
	}
	user, err := s.store.GetUserByUserID(ctx, userID)
	if err != nil {
		logReject("unknown user")
		writeJSON(w, http.StatusOK, map[string]any{"ok": false})
		return
	}
	nowT := time.Now().UTC()
	if !user.Enabled || user.Expired(nowT) || user.TrafficExceeded() ||
		subtle.ConstantTimeCompare([]byte(password), []byte(user.Password)) != 1 {
		switch {
		case !user.Enabled:
			logReject("account disabled")
		case user.Expired(nowT):
			logReject("subscription expired")
		case user.TrafficExceeded():
			logReject("traffic limit exceeded")
		default:
			logReject("invalid password")
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false})
		return
	}

	limit := effectiveDeviceLimit(user.Groups, user.MaxDevices)
	ip := clientIP(req.Addr, r.RemoteAddr)
	grace := parseIntSetting(settingOr(s, r, "device_grace_minutes", "15"), 15)
	if allowed, _ := s.hub.CheckDeviceLimit(user.UserID, ip, limit, grace); !allowed {
		logReject("device limit exceeded")
		writeJSON(w, http.StatusOK, map[string]any{"ok": false})
		return
	}
	s.hub.MarkAuthOnline(user.UserID)
	s.store.TouchUserSeen(ctx, user.UserID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": user.UserID})
}

func effectiveDeviceLimit(groups []model.Group, maxDevices int) int {
	if maxDevices != 0 {
		return maxDevices
	}
	limits := []int{}
	for _, g := range groups {
		if g.MaxDevices > 0 {
			limits = append(limits, g.MaxDevices)
		}
	}
	if len(limits) == 0 {
		return 0
	}
	min := limits[0]
	for _, v := range limits[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

// authFailureLogLimit caps how many rejected authentication rows one node can
// write per minute. The endpoint is public and Hysteria calls it for every
// connection attempt, so unbounded inserts are a trivial disk-fill vector.
const authFailureLogLimit = 120

func (s *Server) allowAuthFailureLog(nodeID int64) bool {
	window := time.Now().Unix() / 60
	s.authLogMu.Lock()
	defer s.authLogMu.Unlock()
	b := s.authLogWindow[nodeID]
	if b.window != window {
		b = authLogBucket{window: window}
	}
	if b.count >= authFailureLogLimit {
		s.authLogWindow[nodeID] = b
		return false
	}
	b.count++
	s.authLogWindow[nodeID] = b
	return true
}

func clientIP(addr, fallback string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err == nil {
		return host
	}
	if ip := net.ParseIP(strings.TrimSpace(addr)); ip != nil {
		return ip.String()
	}
	host, _, err = net.SplitHostPort(fallback)
	if err == nil {
		return host
	}
	return strings.TrimSpace(addr)
}

func settingOr(s *Server, r *http.Request, key, def string) string {
	v, err := s.store.GetSetting(r.Context(), key)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
