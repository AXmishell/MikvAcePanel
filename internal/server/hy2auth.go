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
	logReject := func(reason string) {
		if nodeID <= 0 {
			return
		}
		entry := model.LogEntry{
			NodeID: nodeID, TS: time.Now().UTC(), Level: "WARN", Logger: "panel-auth",
			Message: "authentication failed: " + reason, Event: "auth_fail",
			ClientIP: clientIP(req.Addr, req.Addr), UserID: userID,
			Raw:   "panel auth rejected user " + userID + " from " + req.Addr + ": " + reason,
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

func requestIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	return clientIP(r.RemoteAddr, r.RemoteAddr)
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
