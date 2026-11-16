package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	nodesvc "github.com/axmishell/mikvacepanel/internal/app/nodes"
	usersvc "github.com/axmishell/mikvacepanel/internal/app/users"
	"github.com/axmishell/mikvacepanel/internal/certcrypto"
	"github.com/axmishell/mikvacepanel/internal/certmanager"
	"github.com/axmishell/mikvacepanel/internal/logs"
	"github.com/axmishell/mikvacepanel/internal/rediscache"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// Server is the HTTP panel application.
type Server struct {
	cfg       Config
	store     *store.Store
	users     *usersvc.Service
	nodes     *nodesvc.Service
	hub       *Hub
	analyzer  *logs.Analyzer
	analysis  *analysisCache
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

	captchaMu   sync.Mutex
	captchas    map[string]captchaEntry
	captchaGate map[string]time.Time

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
	installMu    sync.Mutex

	adminVerMu sync.Mutex
	adminVer   map[int64]adminVersionEntry
}

type authLogBucket struct {
	window int64
	count  int
}

type adminVersionEntry struct {
	version int
	expires time.Time
}

// adminVersionTTL bounds how long a process caches an administrator's session
// version. Revocations therefore take effect within a few seconds even when
// several panel instances share one database.
const adminVersionTTL = 10 * time.Second

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
		captchas:       map[string]captchaEntry{},
		captchaGate:    map[string]time.Time{},
		authLogWindow:  map[int64]authLogBucket{},
		quotaBlocked:   map[string]bool{},
		adminVer:       map[int64]adminVersionEntry{},
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
	s.analysis = newAnalysisCache(maxCachedAnalyses)
	certKey, err := certcrypto.LoadKey(cfg.DataDir, cfg.CertKeyFile)
	if err != nil {
		return nil, fmt.Errorf("certificate encryption key: %w", err)
	}
	s.secretKey = certKey
	s.certs = certmanager.New(st, logger, certKey, func(nodeID int64, msgType string, payload any) bool {
		return s.hub.SendToNode(nodeID, msgType, payload)
	})
	s.users = usersvc.New(st, st, st, s.hub, serverNodeSyncer{s})
	s.nodes = nodesvc.New(st, serverNodeConfigPusher{s}, s.hub, st)
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
	if s.cfg.PanelName != "" {
		if name, _ := s.store.GetSetting(ctx, "panel_name"); name == "" || name == "MikvAce Panel" {
			_ = s.store.SetSetting(ctx, "panel_name", s.cfg.PanelName)
		}
	}
	if pub, _ := s.store.GetSetting(ctx, "public_url"); strings.TrimSpace(pub) == "" && s.cfg.PublicURL != "" {
		_ = s.store.SetSetting(ctx, "public_url", strings.TrimRight(s.cfg.PublicURL, "/"))
	}
	// Admin bootstrap is optional. Without admin.password_file the panel starts
	// uninstalled and the /install wizard creates the first administrator.
	if s.cfg.AdminPassword == "" {
		if n, err := s.store.CountAdmins(ctx); err == nil && n == 0 {
			s.log.Info("panel installation required", "path", "/install", "config", s.cfg.ConfigPath)
		}
		return nil
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
		s.log.Warn("bootstrap administrator created", "username", s.cfg.AdminUsername,
			"password_source", "admin.password_file")
	}
	if created && (s.cfg.AdminPassword == "admin123" ||
		s.cfg.AdminPassword == "change-me-now" || len(s.cfg.AdminPassword) < 8) {
		_ = s.store.SetSetting(ctx, "bootstrap_warning", "The administrator password is weak. Change it in Settings → Security.")
	}
	return nil
}
