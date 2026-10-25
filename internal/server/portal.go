package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/subscription"
)

// The user portal is a self-service surface for proxy accounts. It has its
// own JWT purpose so a portal token can never call the administrator API and
// an administrator token can never call the portal API.
const portalPurpose = "portal"
const portalIssuer = "mikvace-portal"

type portalClaims struct {
	UserDBID int64  `json:"uid"`
	UserID   string `json:"userId"`
	Purpose  string `json:"purpose,omitempty"`
	jwt.RegisteredClaims
}

const portalClaimsKey ctxKey = "portalClaims"

// noStore keeps portal responses, which contain subscription tokens and
// session tokens, out of browser and intermediary caches.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func portalBoolSetting(settings map[string]string, key string, def bool) bool {
	v, ok := settings[key]
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

func portalSessionTTL(settings map[string]string) time.Duration {
	hours := parseIntSetting(settings["portal_session_hours"], 72)
	if hours <= 0 {
		hours = 72
	}
	if hours > 720 {
		hours = 720
	}
	return time.Duration(hours) * time.Hour
}

func portalEnabled(settings map[string]string) bool {
	return portalBoolSetting(settings, "portal_enabled", true)
}

var (
	dummyLoginHashOnce sync.Once
	dummyLoginHash     []byte
)

// comparePortalLoginPassword verifies the control-panel password. Legacy
// accounts created before the split have no hash and fall back to the proxy
// connection password.
func comparePortalLoginPassword(user *model.User, password string) bool {
	if user == nil {
		dummyLoginHashOnce.Do(func() {
			dummyLoginHash, _ = bcrypt.GenerateFromPassword([]byte("invalid-password-placeholder"), bcrypt.DefaultCost)
		})
		_ = bcrypt.CompareHashAndPassword(dummyLoginHash, []byte(password))
		return false
	}
	if user.LoginPasswordHash != "" {
		return bcrypt.CompareHashAndPassword([]byte(user.LoginPasswordHash), []byte(password)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(user.Password)) == 1
}

func (s *Server) migratePortalLoginPasswordHash(ctx context.Context, user *model.User, password string) {
	if user.LoginPasswordHash != "" {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return
	}
	if err := s.store.SetUserLoginPasswordHash(ctx, user.ID, string(hash)); err == nil {
		user.LoginPasswordHash = string(hash)
	}
}

func (s *Server) issuePortalToken(u *model.User, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = 72 * time.Hour
	}
	expires := time.Now().Add(ttl)
	claims := portalClaims{
		UserDBID: u.ID,
		UserID:   u.UserID,
		Purpose:  portalPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.UserID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expires),
			Issuer:    portalIssuer,
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	str, err := tok.SignedString(s.secret)
	return str, expires, err
}

func (s *Server) portalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		claims := &portalClaims{}
		tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, jwt.ErrSignatureInvalid
			}
			return s.secret, nil
		})
		if err != nil || !tok.Valid || claims.Purpose != portalPurpose ||
			claims.Issuer != portalIssuer || claims.UserDBID <= 0 {
			writeError(w, http.StatusUnauthorized, "登录状态已失效，请重新登录")
			return
		}
		expires := time.Now().Add(s.cfg.SessionTTL)
		if claims.ExpiresAt != nil {
			expires = claims.ExpiresAt.Time
		}
		if !s.sessions.Valid(r.Context(), raw, expires) {
			writeError(w, http.StatusUnauthorized, "登录状态已失效，请重新登录")
			return
		}
		ctx := context.WithValue(r.Context(), portalClaimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func portalClaimsFrom(r *http.Request) *portalClaims {
	c, _ := r.Context().Value(portalClaimsKey).(*portalClaims)
	return c
}

func (s *Server) portalUser(r *http.Request) (*model.User, error) {
	c := portalClaimsFrom(r)
	if c == nil {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return s.store.GetUserByID(r.Context(), c.UserDBID)
}

func (s *Server) handlePortalConfig(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.GetSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    portalEnabled(settings),
		"panelName":  firstNonEmpty(settings["panel_name"], "MikvAce Panel"),
		"title":      firstNonEmpty(settings["portal_title"], "用户中心"),
		"note":       firstNonEmpty(settings["portal_note"], settings["page_note"], "查看你的订阅、流量与账号状态。"),
		"logoUrl":    settings["logo_url"],
		"supportUrl": settings["support_url"],
	})
}

func (s *Server) handlePortalLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, _ := s.store.GetSettings(ctx)
	if !portalEnabled(settings) {
		writeError(w, http.StatusForbidden, "用户中心未开放")
		return
	}
	ip := s.clientIP(r)
	if !s.loginAllowed(ctx, "portal", ip) {
		writeError(w, http.StatusTooManyRequests, "尝试次数过多，请稍后再试")
		return
	}
	var req struct {
		UserID   string `json:"userId"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	userID := strings.TrimSpace(req.UserID)
	if userID == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "请输入用户 ID 和密码")
		return
	}
	user, err := s.store.GetUserByUserID(ctx, userID)
	if err != nil {
		user = nil
	}
	if !comparePortalLoginPassword(user, req.Password) || user == nil {
		s.recordLoginFailure(ctx, "portal", ip)
		writeError(w, http.StatusUnauthorized, "用户 ID 或密码错误")
		return
	}
	s.migratePortalLoginPasswordHash(ctx, user, req.Password)
	token, expires, err := s.issuePortalToken(user, portalSessionTTL(settings))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法创建会话")
		return
	}
	s.sessions.Add(ctx, token, expires)
	status, reason := portalStatus(user, time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": expires,
		"user": map[string]any{
			"id": user.ID, "userId": user.UserID, "username": firstNonEmpty(user.Username, user.UserID),
			"status": status, "statusReason": reason,
		},
	})
}

func (s *Server) handlePortalLogout(w http.ResponseWriter, r *http.Request) {
	if raw := bearerToken(r); raw != "" {
		s.sessions.Delete(r.Context(), raw)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// userSubscriptionLinks builds the subscription URLs shared by the user
// portal and the administrator's per-user connection viewer.
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

func (s *Server) handlePortalMe(w http.ResponseWriter, r *http.Request) {
	user, err := s.portalUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "账号不存在或已删除")
		return
	}
	settings, _ := s.store.GetSettings(r.Context())
	nowT := time.Now().UTC()
	status, reason := portalStatus(user, nowT)
	used := user.TrafficUsed()
	percent := 0
	if user.TrafficLimit > 0 {
		percent = int(used * 100 / user.TrafficLimit)
		if percent > 100 {
			percent = 100
		}
	}

	portalSubscription := s.userSubscriptionLinks(r, user, settings)
	portalNodes := s.userNodeConnections(r, user)

	groups := make([]map[string]any, 0, len(user.Groups))
	for _, g := range user.Groups {
		groups = append(groups, map[string]any{"id": g.ID, "name": g.Name, "color": g.Color})
	}

	grace := parseIntSetting(settings["device_grace_minutes"], 15)
	if grace <= 0 {
		grace = 15
	}
	devices := s.hub.Devices(user.UserID, time.Duration(grace)*time.Minute)

	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id": user.ID, "userId": user.UserID, "username": firstNonEmpty(user.Username, user.UserID),
			"enabled": user.Enabled, "status": status, "statusReason": reason,
			"expireAt": user.ExpireAt, "createdAt": user.CreatedAt, "lastSeenAt": user.LastSeenAt,
			"groups": groups,
		},
		"traffic": map[string]any{
			"used": used, "tx": user.TrafficTx, "rx": user.TrafficRx,
			"limit": user.TrafficLimit, "percent": percent, "unlimited": user.TrafficLimit <= 0,
		},
		"devices": map[string]any{
			"online": s.hub.OnlineDevices(user.UserID), "tracked": len(devices),
			"limit":        effectiveDeviceLimit(user.Groups, user.MaxDevices),
			"graceMinutes": grace, "items": devices,
		},
		"subscription": portalSubscription,
		"nodes":        portalNodes,
		"capabilities": map[string]bool{
			"password":     portalBoolSetting(settings, "portal_allow_password_change", true),
			"subscription": portalBoolSetting(settings, "portal_allow_sub_reset", false),
		},
		"branding": map[string]any{
			"panelName":  firstNonEmpty(settings["panel_name"], "MikvAce Panel"),
			"title":      firstNonEmpty(settings["portal_title"], "用户中心"),
			"note":       firstNonEmpty(settings["portal_note"], settings["page_note"], "查看你的订阅、流量与账号状态。"),
			"logoUrl":    settings["logo_url"],
			"supportUrl": settings["support_url"],
		},
		"now": nowT,
	})
}

func (s *Server) handlePortalChangePassword(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.GetSettings(r.Context())
	if !portalBoolSetting(settings, "portal_allow_password_change", true) {
		writeError(w, http.StatusForbidden, "管理员未开放自助修改密码")
		return
	}
	user, err := s.portalUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "账号不存在或已删除")
		return
	}
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "新密码至少需要 8 个字符")
		return
	}
	if req.NewPassword == req.OldPassword {
		writeError(w, http.StatusBadRequest, "新密码不能与当前密码相同")
		return
	}
	if !comparePortalLoginPassword(user, req.OldPassword) {
		writeError(w, http.StatusForbidden, "当前登录密码不正确")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法更新密码")
		return
	}
	if err := s.store.SetUserLoginPasswordHash(r.Context(), user.ID, string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, "无法更新密码")
		return
	}
	s.store.AddEvent(r.Context(), "portal.login_password", 0, "info", "用户 "+user.UserID+" 修改了控制台登录密码")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePortalChangeConnectionPassword(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.GetSettings(r.Context())
	if !portalBoolSetting(settings, "portal_allow_password_change", true) {
		writeError(w, http.StatusForbidden, "管理员未开放自助修改密码")
		return
	}
	user, err := s.portalUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "账号不存在或已删除")
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "新连接密码至少需要 8 个字符")
		return
	}
	if !comparePortalLoginPassword(user, req.CurrentPassword) {
		writeError(w, http.StatusForbidden, "当前登录密码不正确")
		return
	}
	if req.NewPassword == user.Password {
		writeError(w, http.StatusBadRequest, "新连接密码不能与当前连接密码相同")
		return
	}
	if err := s.store.ResetUserPassword(r.Context(), user.ID, req.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "无法更新连接密码")
		return
	}
	s.syncXrayNodes(r.Context())
	s.store.AddEvent(r.Context(), "portal.connection_password", 0, "info", "用户 "+user.UserID+" 修改了连接密码")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePortalResetSubscription(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.GetSettings(r.Context())
	if !portalBoolSetting(settings, "portal_allow_sub_reset", false) {
		writeError(w, http.StatusForbidden, "管理员未开放自助重置订阅")
		return
	}
	user, err := s.portalUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "账号不存在或已删除")
		return
	}
	token, err := s.store.ResetUserSubscriptionToken(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法重置订阅链接")
		return
	}
	base := publicBaseURL(r, settings["public_url"])
	subURL := base + "/api/files/" + token
	s.store.AddEvent(r.Context(), "portal.subscription", 0, "info", "用户 "+user.UserID+" 重置了订阅链接")
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        subURL,
		"clashUrl":   subURL + "?format=clash",
		"singboxUrl": subURL + "?format=singbox",
		"uriUrl":     subURL + "?format=uri",
		"qr":         s.qrDataURI(r.Context(), subURL),
	})
}

// portalStatus returns the account state shown by the portal. Expiry is
// checked before the enabled flag because the expiry scheduler also disables
// accounts, and "expired" is more useful to the user than "disabled".
func portalStatus(u *model.User, now time.Time) (string, string) {
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
