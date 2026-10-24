package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

const (
	authKeyFile      = "auth.key"
	authSnapshotFile = "users.json"
	authMaxBody      = 64 << 10
	// authPanelBackoff is how long the gateway stops calling the panel after a
	// network or 5xx failure and serves the cached snapshot instead.
	authPanelBackoff = 15 * time.Second
)

// localAuthServer is the node-local Hysteria auth gateway.
//
// Hysteria is pointed at this loopback endpoint instead of the panel. Every
// request is forwarded to the panel while it is reachable. If the panel is
// down, the last user snapshot pushed by the panel is used instead.
type localAuthServer struct {
	cfg    Config
	log    *slog.Logger
	client *http.Client
	online func() bool

	mu           sync.RWMutex
	secret       string
	snapshot     *model.UserAuthSnapshot
	users        map[string]model.UserAuth
	grace        time.Duration
	addr         string
	ready        bool
	offlineUntil time.Time

	devices *deviceTracker
	srv     *http.Server
}

func newLocalAuthServer(cfg Config, logger *slog.Logger, online func() bool) *localAuthServer {
	s := &localAuthServer{
		cfg: cfg, log: logger, online: online,
		users:   map[string]model.UserAuth{},
		grace:   15 * time.Minute,
		devices: newDeviceTracker(),
	}
	s.secret = loadOrCreateAuthKey(cfg.DataDir, logger)
	s.client = &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.PanelInsecure},
		},
	}
	if snap, err := loadAuthSnapshot(cfg.DataDir); err != nil {
		logger.Warn("load cached auth snapshot", "error", err)
	} else if snap != nil {
		s.applySnapshot(*snap)
		logger.Info("cached auth snapshot loaded", "users", len(snap.Users), "generatedAt", snap.GeneratedAt)
	}
	return s
}

// Start binds the loopback auth gateway.
func (s *localAuthServer) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.AuthListen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.srv = srv
	s.ready = true
	s.mu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Warn("local auth server stopped", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	return nil
}

// Close stops the local listener.
func (s *localAuthServer) Close() {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.ready = false
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// Ready reports whether the gateway is accepting requests.
func (s *localAuthServer) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

// Addr is the bound loopback address.
func (s *localAuthServer) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

func (s *localAuthServer) Secret() string { return s.secret }

// SetSnapshot stores and persists a snapshot from the panel.
func (s *localAuthServer) SetSnapshot(snap model.UserAuthSnapshot) error {
	s.applySnapshot(snap)
	return persistAuthSnapshot(s.cfg.DataDir, snap)
}

func (s *localAuthServer) applySnapshot(snap model.UserAuthSnapshot) {
	users := make(map[string]model.UserAuth, len(snap.Users))
	for _, u := range snap.Users {
		if u.UserID != "" {
			users[u.UserID] = u
		}
	}
	grace := time.Duration(snap.GraceMinutes) * time.Minute
	if grace <= 0 {
		grace = 15 * time.Minute
	}
	s.mu.Lock()
	s.snapshot = &snap
	s.users = users
	s.grace = grace
	s.mu.Unlock()
}

// Rewrite points Hysteria's auth.http.url at this gateway.
func (s *localAuthServer) Rewrite(text string) string {
	if !s.Ready() {
		return text
	}
	return rewriteAuthURL(text, "http://"+s.Addr(), s.secret)
}

func (s *localAuthServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/auth" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(s.secret)) != 1 {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, authMaxBody))
	if err != nil {
		writeAuthResult(w, http.StatusBadRequest, false, "", "offline")
		return
	}
	if data, ok := s.tryPanel(r.Context(), r.URL.Query().Get("node"), body); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-MikvAce-Auth", "panel")
		_, _ = w.Write(data)
		return
	}
	s.handleCached(w, body)
}

func (s *localAuthServer) tryPanel(ctx context.Context, node string, body []byte) ([]byte, bool) {
	if s.online != nil && !s.online() {
		return nil, false
	}
	s.mu.RLock()
	until := s.offlineUntil
	s.mu.RUnlock()
	if time.Now().Before(until) {
		return nil, false
	}
	upstream := strings.TrimRight(s.cfg.PanelURL, "/") + "/api/auth"
	if node != "" {
		upstream += "?node=" + url.QueryEscape(node)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream, bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		s.panelUnavailable(err)
		return nil, false
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, authMaxBody))
	if resp.StatusCode != http.StatusOK {
		s.panelUnavailable(fmt.Errorf("panel auth status %s", resp.Status))
		return nil, false
	}
	var probe struct {
		OK *bool `json:"ok"`
	}
	if json.Unmarshal(data, &probe) != nil || probe.OK == nil {
		s.panelUnavailable(errors.New("invalid panel auth response"))
		return nil, false
	}
	return data, true
}

func (s *localAuthServer) panelUnavailable(err error) {
	s.mu.Lock()
	s.offlineUntil = time.Now().Add(authPanelBackoff)
	s.mu.Unlock()
	s.log.Debug("panel auth unavailable, serving cached snapshot", "error", err, "retryAfter", authPanelBackoff)
}

func (s *localAuthServer) handleCached(w http.ResponseWriter, body []byte) {
	var req struct {
		Addr string `json:"addr"`
		Auth string `json:"auth"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeAuthResult(w, http.StatusOK, false, "", "offline")
		return
	}
	userID, password, ok := strings.Cut(req.Auth, ":")
	if !ok || userID == "" || password == "" {
		writeAuthResult(w, http.StatusOK, false, "", "offline")
		return
	}
	s.mu.RLock()
	user, found := s.users[userID]
	grace := s.grace
	s.mu.RUnlock()
	if !found {
		s.reject(userID, "unknown user")
		writeAuthResult(w, http.StatusOK, false, "", "offline")
		return
	}
	nowT := time.Now().UTC()
	switch {
	case !user.Enabled:
		s.reject(userID, "account disabled")
	case user.Expired(nowT):
		s.reject(userID, "subscription expired")
	case user.TrafficExceeded():
		s.reject(userID, "traffic limit exceeded")
	case subtle.ConstantTimeCompare([]byte(password), []byte(user.Password)) != 1:
		s.reject(userID, "invalid password")
	case !s.devices.Allow(userID, authIP(req.Addr), user.MaxDevices, grace):
		s.reject(userID, "device limit exceeded")
	default:
		writeAuthResult(w, http.StatusOK, true, userID, "offline")
		return
	}
	writeAuthResult(w, http.StatusOK, false, "", "offline")
}

func (s *localAuthServer) reject(userID, reason string) {
	s.log.Info("offline auth rejected", "user", userID, "reason", reason)
}

func writeAuthResult(w http.ResponseWriter, status int, ok bool, id, source string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-MikvAce-Auth", source)
	w.WriteHeader(status)
	if ok {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false})
}

func authIP(addr string) string {
	addr = strings.TrimSpace(addr)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(addr, "[]")
}

// deviceTracker mirrors the panel's unique-IP device limiting for offline
// authentication. It is only consulted while the panel is unreachable.
type deviceTracker struct {
	mu    sync.Mutex
	users map[string]map[string]time.Time
}

func newDeviceTracker() *deviceTracker {
	return &deviceTracker{users: map[string]map[string]time.Time{}}
}

func (d *deviceTracker) Allow(userID, ip string, limit int, grace time.Duration) bool {
	if limit <= 0 || ip == "" {
		return true
	}
	if grace <= 0 {
		grace = 15 * time.Minute
	}
	nowT := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	devices := d.users[userID]
	if devices == nil {
		devices = map[string]time.Time{}
		d.users[userID] = devices
	}
	for addr, last := range devices {
		if nowT.Sub(last) > grace {
			delete(devices, addr)
		}
	}
	devices[ip] = nowT
	if len(devices) > limit {
		delete(devices, ip)
		return false
	}
	return true
}

var authURLLineRE = regexp.MustCompile(`(?m)^([ \t]*)url:[ \t]*["']?[^"'\s]*?/api/auth\?node=([0-9]+)[^"'\s]*["']?[ \t]*$`)

// rewriteAuthURL replaces the generated panel auth URL with a local one while
// preserving the node id. It is intentionally line based: the rest of the
// Hysteria YAML (including the ExtraYAML escape hatch) must stay untouched.
func rewriteAuthURL(text, base, key string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return text
	}
	return authURLLineRE.ReplaceAllStringFunc(text, func(line string) string {
		m := authURLLineRE.FindStringSubmatch(line)
		if m == nil {
			return line
		}
		target := base + "/api/auth?node=" + m[2]
		if key != "" {
			target += "&key=" + url.QueryEscape(key)
		}
		return m[1] + `url: "` + target + `"`
	})
}

func loadOrCreateAuthKey(dataDir string, logger *slog.Logger) string {
	dir := filepath.Join(dataDir, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logger.Warn("create state directory", "error", err)
	}
	path := filepath.Join(dir, authKeyFile)
	if b, err := os.ReadFile(path); err == nil {
		key := strings.TrimSpace(string(b))
		if len(key) >= 32 {
			return key
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		logger.Warn("generate auth gateway key", "error", err)
		return randomID()
	}
	key := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		logger.Warn("persist auth gateway key", "error", err)
	}
	return key
}

func loadAuthSnapshot(dataDir string) (*model.UserAuthSnapshot, error) {
	path := filepath.Join(dataDir, "state", authSnapshotFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap model.UserAuthSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func persistAuthSnapshot(dataDir string, snap model.UserAuthSnapshot) error {
	dir := filepath.Join(dataDir, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, authSnapshotFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
