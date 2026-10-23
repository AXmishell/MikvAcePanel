package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/axmishell/mikvacepanel/internal/certcrypto"
	"github.com/axmishell/mikvacepanel/internal/certmanager"
	"github.com/axmishell/mikvacepanel/internal/logs"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/rediscache"
	"github.com/axmishell/mikvacepanel/internal/store"
	"github.com/axmishell/mikvacepanel/internal/version"
	"github.com/axmishell/mikvacepanel/web"
)

// Server is the HTTP panel application.
type Server struct {
	cfg       Config
	store     *store.Store
	hub       *Hub
	analyzer  *logs.Analyzer
	certs     *certmanager.Manager
	log       *slog.Logger
	secret    []byte
	secretKey []byte
	started   time.Time
	mux       http.Handler
	redis     *rediscache.Client
	sessions  *sessionStore

	trustedProxies []*net.IPNet

	loginMu        sync.Mutex
	loginTries     map[string][]time.Time
	twoFactorTries map[string]int
	challengeUsed  map[string]time.Time

	apiKeyMu   sync.Mutex
	apiKeyRate map[int64][]time.Time

	authLogMu     sync.Mutex
	authLogWindow map[int64]authLogBucket

	quotaMu       sync.Mutex
	quotaBlocked  map[string]bool
	quotaChecked  time.Time
	quotaSyncedAt time.Time

	configSyncMu sync.Mutex
	backupMu     sync.Mutex
}

type authLogBucket struct {
	window int64
	count  int
}

// New creates a server, bootstraps the administrator and builds the router.
func New(cfg Config, st *store.Store, logger *slog.Logger) (*Server, error) {
	secret, err := loadJWTSecret(cfg)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg: cfg, store: st, log: logger, secret: secret,
		started:        time.Now().UTC(),
		loginTries:     map[string][]time.Time{},
		trustedProxies: parseTrustedProxies(cfg.TrustedProxies),
		apiKeyRate:     map[int64][]time.Time{},
		twoFactorTries: map[string]int{},
		challengeUsed:  map[string]time.Time{},
		authLogWindow:  map[int64]authLogBucket{},
		quotaBlocked:   map[string]bool{},
	}
	redisClient, err := rediscache.Open(context.Background(), cfg.RedisURL, cfg.RedisPrefix, logger)
	if err != nil {
		if cfg.RedisRequired {
			return nil, fmt.Errorf("redis: %w", err)
		}
		logger.Warn("redis unavailable, falling back to in-memory mode", "error", err)
	} else if redisClient != nil {
		logger.Info("redis enabled", "prefix", cfg.RedisPrefix)
	}
	s.redis = redisClient
	s.sessions = newSessionStore(redisClient, cfg.SessionTTL, logger)
	s.hub = NewHub(st, logger)
	s.analyzer = logs.New(st)
	certKey, err := certcrypto.LoadKey(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("certificate encryption key: %w", err)
	}
	s.secretKey = certKey
	s.certs = certmanager.New(st, logger, certKey, func(nodeID int64, msgType string, payload any) bool {
		return s.hub.SendToNode(nodeID, msgType, payload)
	})
	if err := s.bootstrap(context.Background()); err != nil {
		return nil, err
	}
	s.mux = s.routes()
	return s, nil
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

// Close releases optional external resources such as the Redis pool.
func (s *Server) Close() {
	if s.certs != nil {
		s.certs.Close()
	}
	if s.redis != nil {
		_ = s.redis.Close()
	}
}

// Store exposes the persistence layer to the scheduler.
func (s *Server) Store() *store.Store { return s.store }

// Hub exposes live agent state.
func (s *Server) Hub() *Hub { return s.hub }

// Analyzer exposes the log analyzer.
func (s *Server) Analyzer() *logs.Analyzer { return s.analyzer }

func (s *Server) bootstrap(ctx context.Context) error {
	if err := s.store.EnsureDefaultSettings(ctx); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	created, err := s.store.EnsureAdmin(ctx, s.cfg.AdminUsername, string(hash))
	if err != nil {
		return err
	}
	if created {
		if s.cfg.AdminPasswordGenerated {
			// Print the generated password exactly once so the operator can log
			// in; it is not stored in plaintext anywhere else.
			s.log.Warn("bootstrap administrator created with a generated password; store it now and change it after login",
				"username", s.cfg.AdminUsername, "password", s.cfg.AdminPassword)
		} else {
			s.log.Warn("bootstrap administrator created", "username", s.cfg.AdminUsername,
				"password_source", "ADMIN_PASSWORD environment variable")
		}
	}
	if pub, _ := s.store.GetSetting(ctx, "public_url"); strings.TrimSpace(pub) == "" && s.cfg.PublicURL != "" {
		_ = s.store.SetSetting(ctx, "public_url", strings.TrimRight(s.cfg.PublicURL, "/"))
	}
	if created && (s.cfg.AdminPasswordGenerated || s.cfg.AdminPassword == "admin123" ||
		s.cfg.AdminPassword == "change-me-now" || len(s.cfg.AdminPassword) < 12) {
		_ = s.store.SetSetting(ctx, "bootstrap_warning", "The administrator password is weak or was generated automatically. Change it in Settings → Security.")
	}
	return nil
}

func loadJWTSecret(cfg Config) ([]byte, error) {
	if strings.TrimSpace(cfg.JWTSecret) != "" {
		return []byte(cfg.JWTSecret), nil
	}
	path := filepath.Join(cfg.DataDir, "jwt.secret")
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b, nil
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	secret := []byte(base64.RawURLEncoding.EncodeToString(buf))
	if err := os.WriteFile(path, secret, 0o600); err != nil {
		return nil, err
	}
	return secret, nil
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.recoverer)
	r.Use(s.requestLogger)
	r.Use(s.securityHeaders)
	r.Use(s.cors)

	r.Get("/api/health", s.handleHealth)
	r.Post("/api/login", s.handleLogin)
	r.Post("/api/login/2fa", s.handleLogin2FA)
	r.Post("/api/auth", s.handleHy2Auth)
	r.Get("/api/files/info/{token}", s.handleSubInfo)
	r.Get("/api/files/{token}", s.handleSubscription)
	r.Get("/api/agent/install.sh", s.handleInstallScript)
	r.Get("/api/agent/version", s.handleAgentVersion)
	r.Get("/api/node/ws", s.handleAgentWS)

	r.Group(func(pr chi.Router) {
		pr.Use(s.adminAuth)
		pr.Get("/api/me", s.handleMe)
		pr.Post("/api/logout", s.handleLogout)
		pr.Post("/api/change-password", s.handleChangePassword)
		pr.Get("/api/2fa", s.handleTwoFactorStatus)
		pr.Post("/api/2fa/setup", s.handleTwoFactorSetup)
		pr.Post("/api/2fa/enable", s.handleTwoFactorEnable)
		pr.Post("/api/2fa/disable", s.handleTwoFactorDisable)
		pr.Post("/api/2fa/recovery", s.handleTwoFactorRecovery)

		pr.Get("/api/dashboard", s.handleDashboard)
		pr.Get("/api/events", s.handleEvents)
		pr.Post("/api/sync", s.handleSyncAll)

		pr.Get("/api/users", s.handleListUsers)
		pr.Post("/api/users", s.handleCreateUser)
		pr.Get("/api/users/{id}", s.handleGetUser)
		pr.Put("/api/users/{id}", s.handleUpdateUser)
		pr.Delete("/api/users/{id}", s.handleDeleteUser)
		pr.Post("/api/users/{id}/enable", s.handleEnableUser)
		pr.Post("/api/users/{id}/disable", s.handleDisableUser)
		pr.Post("/api/users/{id}/reset-subscription", s.handleResetUserSubscription)
		pr.Post("/api/users/{id}/reset-password", s.handleResetUserPassword)
		pr.Post("/api/users/{id}/reset-devices", s.handleResetDevices)
		pr.Post("/api/users/{id}/reset-traffic", s.handleResetUserTraffic)

		pr.Get("/api/groups", s.handleListGroups)
		pr.Post("/api/groups", s.handleCreateGroup)
		pr.Put("/api/groups/{id}", s.handleUpdateGroup)
		pr.Delete("/api/groups/{id}", s.handleDeleteGroup)

		pr.Get("/api/nodes", s.handleListNodes)
		pr.Post("/api/nodes", s.handleCreateNode)
		pr.Get("/api/nodes/{id}", s.handleGetNode)
		pr.Put("/api/nodes/{id}", s.handleUpdateNode)
		pr.Delete("/api/nodes/{id}", s.handleDeleteNode)
		pr.Post("/api/nodes/{id}/token", s.handleResetNodeToken)
		pr.Post("/api/nodes/{id}/sync", s.handleSyncNode)
		pr.Post("/api/nodes/{id}/command", s.handleNodeCommand)
		pr.Post("/api/nodes/{id}/ssh/install", s.handleNodeSSHInstall)
		pr.Get("/api/nodes/{id}/config", s.handleNodeConfig)
		pr.Get("/api/nodes/{id}/install", s.handleNodeInstall)
		pr.Get("/api/nodes/{id}/cert", s.handleGetNodeCert)
		pr.Post("/api/nodes/{id}/cert/request", s.handleRequestNodeCert)
		pr.Post("/api/nodes/{id}/cert/upload", s.handleUploadNodeCert)
		pr.Post("/api/nodes/{id}/cert/confirm", s.handleConfirmNodeCert)
		pr.Post("/api/nodes/{id}/cert/cancel", s.handleCancelNodeCert)
		pr.Get("/api/certs", s.handleListNodeCerts)
		pr.Get("/api/cert/settings", s.handleGetCertSettings)
		pr.Put("/api/cert/settings", s.handleUpdateCertSettings)

		pr.Get("/api/logs", s.handleLogs)
		pr.Get("/api/logs/summary", s.handleLogSummary)
		pr.Get("/api/logs/options", s.handleLogOptions)
		pr.Delete("/api/logs", s.handleDeleteLogs)

		pr.Get("/api/settings", s.handleGetSettings)
		pr.Put("/api/settings", s.handleUpdateSettings)

		pr.Get("/api/backups", s.handleListBackups)
		pr.Post("/api/backups", s.handleCreateBackup)
		pr.Get("/api/backups/{name}", s.handleDownloadBackup)
		pr.Delete("/api/backups/{name}", s.handleDeleteBackup)
		pr.Post("/api/backups/restore", s.handleRestoreBackup)

		pr.Get("/api/api-keys", s.handleListAPIKeys)
		pr.Post("/api/api-keys", s.handleCreateAPIKey)
		pr.Delete("/api/api-keys/{id}", s.handleDeleteAPIKey)
		pr.Post("/api/api-keys/{id}/enable", s.handleEnableAPIKey)
		pr.Post("/api/api-keys/{id}/disable", s.handleDisableAPIKey)
	})

	if s.cfg.ServeWeb {
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/panel/", http.StatusFound)
		})
		r.Get("/panel", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/panel/", http.StatusFound)
		})
		r.Get("/panel/*", s.serveSPA)
		r.Get("/favicon.ico", s.serveSPA)
		r.NotFound(s.serveSPA)
	} else {
		s.registerAPIOnlyRoutes(r)
	}
	return r
}

// registerAPIOnlyRoutes keeps the server usable as a pure API when the
// frontend is deployed separately (for example behind a CDN).
func (s *Server) registerAPIOnlyRoutes(r chi.Router) {
	redirectToFrontend := func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.FrontendURL != "" {
			http.Redirect(w, r, s.cfg.FrontendURL, http.StatusFound)
			return
		}
		writeError(w, http.StatusNotFound, "frontend is served separately")
	}
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.FrontendURL != "" {
			http.Redirect(w, r, s.cfg.FrontendURL, http.StatusFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "service": "MikvAce Panel API", "frontend": "separate",
		})
	})
	r.Get("/panel", redirectToFrontend)
	r.Get("/panel/*", redirectToFrontend)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
}

// ServeHTTP allows the server value to be used directly as an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	ok := true
	if err := s.store.Ping(r.Context()); err != nil {
		status = http.StatusServiceUnavailable
		ok = false
	}
	redisInfo := map[string]any{"enabled": s.redis != nil}
	if s.redis != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		err := s.redis.Ping(ctx)
		cancel()
		redisInfo["ok"] = err == nil
		if err != nil {
			if s.cfg.RedisRequired {
				status = http.StatusServiceUnavailable
				ok = false
			}
		}
	}
	writeJSON(w, status, map[string]any{
		"ok": ok, "version": version.String(), "uptime": int64(time.Since(s.started).Seconds()),
		"hub": s.hub.Stats(), "redis": redisInfo,
	})
}

func (s *Server) handleAgentVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": version.String(), "platform": version.Platform()})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "error", fmt.Sprint(rec), "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Hijack forwards websocket upgrades through the logging wrapper.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		logPath := r.URL.Path
		if strings.HasPrefix(logPath, "/api/files/") {
			logPath = "/api/files/<redacted>"
		}
		s.log.Info("http", "method", r.Method, "path", logPath, "status", status,
			"bytes", sw.bytes, "duration", time.Since(start).Round(time.Millisecond).String(), "ip", s.clientIP(r))
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			// Always vary on Origin so shared caches never reuse a response
			// with the wrong CORS headers.
			w.Header().Add("Vary", "Origin")
			if s.originAllowed(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition, X-RateLimit-Limit, X-RateLimit-Remaining, Retry-After")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originAllowed(origin string) bool {
	for _, o := range s.cfg.CORSOrigins {
		if strings.EqualFold(strings.TrimRight(o, "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	// Vite dev server convenience.
	return strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:")
}

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	dist := webui.FS()
	upath := strings.TrimPrefix(r.URL.Path, "/panel")
	upath = strings.TrimPrefix(upath, "/")
	if upath == "" || upath == "index.html" {
		upath = "index.html"
	}
	if strings.HasPrefix(upath, "assets/") {
		if b, err := fs.ReadFile(dist, upath); err == nil {
			ct := mime.TypeByExtension(path.Ext(upath))
			if ct == "" {
				ct = "application/octet-stream"
			}
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = w.Write(b)
			return
		}
	}
	if upath != "index.html" {
		if b, err := fs.ReadFile(dist, upath); err == nil && !strings.HasSuffix(upath, "/") {
			ct := mime.TypeByExtension(path.Ext(upath))
			if ct == "" {
				ct = "application/octet-stream"
			}
			w.Header().Set("Content-Type", ct)
			_, _ = w.Write(b)
			return
		}
	}
	b, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "frontend is not built")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// ---- authentication -----------------------------------------------------

type adminClaims struct {
	AdminID  int64  `json:"aid"`
	Username string `json:"username"`
	Purpose  string `json:"purpose,omitempty"`
	jwt.RegisteredClaims
}

type ctxKey string

const (
	claimsKey ctxKey = "claims"
	apiKeyKey ctxKey = "apiKey"
)

func (s *Server) issueToken(a *model.Admin) (string, time.Time, error) {
	expires := time.Now().Add(s.cfg.SessionTTL)
	claims := adminClaims{
		AdminID: a.ID, Username: a.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: a.Username, IssuedAt: jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expires), Issuer: "mikvace-panel",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	str, err := tok.SignedString(s.secret)
	return str, expires, err
}

func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if strings.HasPrefix(raw, "ck_") {
			sum := sha256.Sum256([]byte(raw))
			key, err := s.store.GetAPIKeyByHash(r.Context(), hex.EncodeToString(sum[:]))
			if err != nil || !key.Enabled || (key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now())) {
				writeError(w, http.StatusUnauthorized, "invalid or expired API key")
				return
			}
			limit := key.RateLimit
			if limit <= 0 {
				limit = 60
			}
			remaining, allowed := s.checkAPIKeyRate(r.Context(), key.ID, limit)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			if !allowed {
				w.Header().Set("Retry-After", "60")
				writeError(w, http.StatusTooManyRequests, "API key rate limit exceeded")
				return
			}
			if !apiKeyAllows(key.Scopes, r.Method, r.URL.Path) {
				writeError(w, http.StatusForbidden, "API key lacks the required scope")
				return
			}
			go s.store.TouchAPIKey(context.Background(), key.ID)
			ctx := context.WithValue(r.Context(), apiKeyKey, key)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		claims := &adminClaims{}
		tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, errors.New("unexpected signing method")
			}
			return s.secret, nil
		})
		if err != nil || !tok.Valid {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		if claims.Purpose != "" {
			writeError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		sessionExpires := time.Now().Add(s.cfg.SessionTTL)
		if claims.ExpiresAt != nil {
			sessionExpires = claims.ExpiresAt.Time
		}
		if !s.sessions.Valid(r.Context(), raw, sessionExpires) {
			writeError(w, http.StatusUnauthorized, "session expired or revoked")
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) logRedisError(msg string, err error) {
	if errors.Is(err, rediscache.ErrUnavailable) {
		s.log.Debug(msg, "error", err)
		return
	}
	s.log.Warn(msg, "error", err)
}

func (s *Server) checkAPIKeyRate(ctx context.Context, keyID int64, limit int) (int, bool) {
	if s.redis != nil {
		window := time.Now().Unix() / 60
		name := fmt.Sprintf("ratelimit:api:%d:%d", keyID, window)
		n, err := s.redis.Incr(ctx, name, 2*time.Minute)
		if err == nil {
			remaining := limit - int(n)
			if remaining < 0 {
				remaining = 0
			}
			return remaining, n <= int64(limit)
		}
		s.logRedisError("redis api key rate limit failed, falling back to memory", err)
	}
	s.apiKeyMu.Lock()
	defer s.apiKeyMu.Unlock()
	nowT := time.Now()
	cutoff := nowT.Add(-time.Minute)
	tries := s.apiKeyRate[keyID]
	kept := tries[:0]
	for _, t := range tries {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		s.apiKeyRate[keyID] = kept
		return 0, false
	}
	kept = append(kept, nowT)
	s.apiKeyRate[keyID] = kept
	return limit - len(kept), true
}

func apiKeyFrom(r *http.Request) *model.APIKey {
	k, _ := r.Context().Value(apiKeyKey).(*model.APIKey)
	return k
}

func apiKeyAllows(scopes []string, method, path string) bool {
	has := func(need string) bool {
		for _, s := range scopes {
			if s == need || s == "*" {
				return true
			}
		}
		return false
	}
	switch {
	case path == "/api/users" || strings.HasPrefix(path, "/api/users/"):
		if method == http.MethodGet {
			return has("users:read")
		}
		return has("users:write")
	case path == "/api/nodes" || strings.HasPrefix(path, "/api/nodes/"):
		if method == http.MethodGet {
			if strings.HasSuffix(path, "/install") {
				return has("nodes:write")
			}
			return has("nodes:read")
		}
		if strings.HasSuffix(path, "/sync") && has("sync:write") {
			return true
		}
		return has("nodes:write")
	case path == "/api/sync" && method == http.MethodPost:
		return has("sync:write")
	case path == "/api/groups" && method == http.MethodGet:
		return has("stats:read")
	case path == "/api/dashboard" || path == "/api/events":
		return has("stats:read")
	case path == "/api/logs" || strings.HasPrefix(path, "/api/logs/"):
		if method == http.MethodGet {
			return has("stats:read")
		}
		return false
	default:
		return false
	}
}

func claimsFrom(r *http.Request) *adminClaims {
	c, _ := r.Context().Value(claimsKey).(*adminClaims)
	return c
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k
	}
	return ""
}

func (s *Server) loginAllowed(ctx context.Context, ip string) bool {
	if s.redis != nil {
		window := time.Now().Unix() / 60
		n, err := s.redis.GetInt(ctx, fmt.Sprintf("ratelimit:login:%s:%d", ip, window))
		if err == nil {
			return n < 10
		}
		s.logRedisError("redis login rate limit failed, falling back to memory", err)
	}
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	nowT := time.Now()
	tries := s.loginTries[ip]
	kept := tries[:0]
	for _, t := range tries {
		if nowT.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(s.loginTries, ip)
	} else {
		s.loginTries[ip] = kept
	}
	s.pruneLoginTriesLocked(nowT)
	return len(kept) < 10
}

// pruneLoginTriesLocked keeps the fallback limiter from growing without bound.
// Callers must hold loginMu.
func (s *Server) pruneLoginTriesLocked(nowT time.Time) {
	if len(s.loginTries) <= 1024 {
		return
	}
	for key, tries := range s.loginTries {
		fresh := false
		for _, t := range tries {
			if nowT.Sub(t) < time.Minute {
				fresh = true
				break
			}
		}
		if !fresh {
			delete(s.loginTries, key)
		}
	}
}

func (s *Server) recordLoginFailure(ctx context.Context, ip string) {
	if s.redis != nil {
		window := time.Now().Unix() / 60
		name := fmt.Sprintf("ratelimit:login:%s:%d", ip, window)
		_, err := s.redis.Incr(ctx, name, 2*time.Minute)
		if err == nil {
			return
		}
		s.logRedisError("redis login rate limit failed, falling back to memory", err)
	}
	s.loginMu.Lock()
	s.loginTries[ip] = append(s.loginTries[ip], time.Now())
	s.loginMu.Unlock()
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.loginAllowed(r.Context(), ip) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	admin, err := s.store.GetAdminByUsername(r.Context(), strings.TrimSpace(req.Username))
	if err != nil {
		s.recordLoginFailure(r.Context(), ip)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
		s.recordLoginFailure(r.Context(), ip)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if admin.TOTPEnabled {
		challenge, expires, err := s.issueTOTPChallenge(admin)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not create challenge")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"twoFactorRequired": true,
			"challenge":         challenge,
			"expiresAt":         expires,
		})
		return
	}
	token, expires, err := s.issueToken(admin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	s.sessions.Add(r.Context(), token, expires)
	_ = s.store.TouchAdminLogin(r.Context(), admin.ID)
	warning, _ := s.store.GetSetting(r.Context(), "bootstrap_warning")
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token, "expiresAt": expires,
		"admin":   map[string]any{"id": admin.ID, "username": admin.Username},
		"warning": warning,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if raw := bearerToken(r); raw != "" {
		s.sessions.Delete(r.Context(), raw)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	warning, _ := s.store.GetSetting(r.Context(), "bootstrap_warning")
	writeJSON(w, http.StatusOK, map[string]any{
		"admin":   map[string]any{"id": admin.ID, "username": admin.Username, "lastLoginAt": admin.LastLoginAt, "twoFactorEnabled": admin.TOTPEnabled},
		"warning": warning, "version": version.String(),
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r)
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	admin, err := s.store.GetAdminByID(r.Context(), c.AdminID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "account not found")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.OldPassword)) != nil {
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := s.store.UpdateAdminPassword(r.Context(), admin.ID, string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update password")
		return
	}
	_ = s.store.SetSetting(r.Context(), "bootstrap_warning", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- helpers ------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	dec.UseNumber()
	return dec.Decode(dst)
}

func pathID(r *http.Request) (int64, error) {
	var id int64
	_, err := fmt.Sscan(chi.URLParam(r, "id"), &id)
	return id, err
}

func routeParam(r *http.Request, key string) string { return chi.URLParam(r, key) }

func jsonUnmarshal(raw string, v any) error { return json.Unmarshal([]byte(raw), v) }

func parseBoolSetting(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func parseIntSetting(v string, def int) int {
	var n int
	if _, err := fmt.Sscan(strings.TrimSpace(v), &n); err == nil {
		return n
	}
	return def
}
