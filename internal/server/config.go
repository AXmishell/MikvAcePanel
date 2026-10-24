package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/configfile"
)

// Config is the panel runtime configuration, loaded from panel.yaml.
type Config struct {
	ConfigPath    string
	Addr          string
	DataDir       string
	DBDriver      string
	DBDSN         string
	DBPath        string
	DBMaxOpen     int
	DBMaxIdle     int
	DBConnMaxLife time.Duration
	JWTSecret     string
	JWTSecretFile string
	CertKeyFile   string
	PublicURL     string
	PanelName     string
	ServeWeb      bool
	FrontendURL   string
	AdminUsername string
	AdminPassword string
	CORSOrigins   []string
	// TrustedProxies lists IPs/CIDRs whose X-Forwarded-For headers are trusted.
	TrustedProxies []string
	LogLevel       string
	SessionTTL     time.Duration
	RedisURL       string
	RedisPrefix    string
	RedisRequired  bool
}

// fileConfig mirrors panel.yaml.
type fileConfig struct {
	Version int `yaml:"version"`
	Panel   struct {
		Name      string `yaml:"name"`
		PublicURL string `yaml:"public_url"`
	} `yaml:"panel"`
	Server struct {
		Addr           string   `yaml:"addr"`
		DataDir        string   `yaml:"data_dir"`
		ServeWeb       bool     `yaml:"serve_web"`
		FrontendURL    string   `yaml:"frontend_url"`
		CORSOrigins    []string `yaml:"cors_origins"`
		TrustedProxies []string `yaml:"trusted_proxies"`
		LogLevel       string   `yaml:"log_level"`
	} `yaml:"server"`
	Admin struct {
		Username     string `yaml:"username"`
		PasswordFile string `yaml:"password_file"`
	} `yaml:"admin"`
	Session struct {
		TTL string `yaml:"ttl"`
	} `yaml:"session"`
	Security struct {
		JWTSecretFile string `yaml:"jwt_secret_file"`
		CertKeyFile   string `yaml:"cert_key_file"`
	} `yaml:"security"`
	Database struct {
		Driver          string `yaml:"driver"`
		Path            string `yaml:"path"`
		DSNFile         string `yaml:"dsn_file"`
		MaxOpen         int    `yaml:"max_open"`
		MaxIdle         int    `yaml:"max_idle"`
		ConnMaxLifetime string `yaml:"conn_max_lifetime"`
	} `yaml:"database"`
	Redis struct {
		URLFile  string `yaml:"url_file"`
		Prefix   string `yaml:"prefix"`
		Required bool   `yaml:"required"`
	} `yaml:"redis"`
}

func defaultFileConfig() fileConfig {
	f := fileConfig{Version: 1}
	f.Panel.Name = "MikvAce Panel"
	f.Server.Addr = ":8080"
	f.Server.DataDir = "./data"
	f.Server.ServeWeb = true
	f.Server.LogLevel = "info"
	f.Admin.Username = "admin"
	f.Session.TTL = "24h"
	f.Database.Driver = "sqlite"
	f.Database.ConnMaxLifetime = "1h"
	f.Redis.Prefix = "mikvace:"
	return f
}

// DefaultPanelConfigPath returns the first conventional panel.yaml location.
func DefaultPanelConfigPath() string {
	for _, p := range []string{"panel.yaml", "config/panel.yaml", "/config/panel.yaml", "/data/panel.yaml"} {
		if configfile.Exists(p) {
			return p
		}
	}
	if st, err := os.Stat("/config"); err == nil && st.IsDir() {
		return "/config/panel.yaml"
	}
	return "panel.yaml"
}

// LoadConfig reads panel.yaml. A missing file is allowed so /install can
// create it from the built-in defaults.
func LoadConfig(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultPanelConfigPath()
	}
	fc := defaultFileConfig()
	if configfile.Exists(path) {
		if err := configfile.Load(path, &fc); err != nil {
			return Config{}, err
		}
	}
	ttl := 24 * time.Hour
	if strings.TrimSpace(fc.Session.TTL) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(fc.Session.TTL))
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("session.ttl: invalid duration %q", fc.Session.TTL)
		}
		ttl = d
	}
	life := time.Hour
	if strings.TrimSpace(fc.Database.ConnMaxLifetime) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(fc.Database.ConnMaxLifetime))
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("database.conn_max_lifetime: invalid duration %q", fc.Database.ConnMaxLifetime)
		}
		life = d
	}
	cfg := Config{
		ConfigPath:     path,
		Addr:           strings.TrimSpace(fc.Server.Addr),
		DataDir:        strings.TrimSpace(fc.Server.DataDir),
		DBDriver:       strings.ToLower(strings.TrimSpace(fc.Database.Driver)),
		DBPath:         strings.TrimSpace(fc.Database.Path),
		DBMaxOpen:      fc.Database.MaxOpen,
		DBMaxIdle:      fc.Database.MaxIdle,
		DBConnMaxLife:  life,
		JWTSecretFile:  strings.TrimSpace(fc.Security.JWTSecretFile),
		CertKeyFile:    strings.TrimSpace(fc.Security.CertKeyFile),
		PublicURL:      strings.TrimRight(strings.TrimSpace(fc.Panel.PublicURL), "/"),
		PanelName:      strings.TrimSpace(fc.Panel.Name),
		ServeWeb:       fc.Server.ServeWeb,
		FrontendURL:    strings.TrimRight(strings.TrimSpace(fc.Server.FrontendURL), "/"),
		AdminUsername:  strings.TrimSpace(fc.Admin.Username),
		CORSOrigins:    fc.Server.CORSOrigins,
		TrustedProxies: fc.Server.TrustedProxies,
		LogLevel:       strings.TrimSpace(fc.Server.LogLevel),
		SessionTTL:     ttl,
		RedisPrefix:    strings.TrimSpace(fc.Redis.Prefix),
		RedisRequired:  fc.Redis.Required,
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.DBDriver == "" {
		cfg.DBDriver = "sqlite"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.DataDir, "panel.db")
	}
	if cfg.AdminUsername == "" {
		cfg.AdminUsername = "admin"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.RedisPrefix == "" {
		cfg.RedisPrefix = "mikvace:"
	}
	if cfg.PanelName == "" {
		cfg.PanelName = "MikvAce Panel"
	}
	if fc.Admin.PasswordFile != "" {
		pw, err := configfile.Secret(fc.Admin.PasswordFile)
		if err != nil {
			return Config{}, fmt.Errorf("admin.password_file: %w", err)
		}
		if pw == "" {
			return Config{}, fmt.Errorf("admin.password_file %q is empty", fc.Admin.PasswordFile)
		}
		cfg.AdminPassword = pw
	}
	if fc.Database.DSNFile != "" {
		dsn, err := configfile.Secret(fc.Database.DSNFile)
		if err != nil {
			return Config{}, fmt.Errorf("database.dsn_file: %w", err)
		}
		cfg.DBDSN = dsn
	}
	if fc.Redis.URLFile != "" {
		u, err := configfile.Secret(fc.Redis.URLFile)
		if err != nil {
			return Config{}, fmt.Errorf("redis.url_file: %w", err)
		}
		cfg.RedisURL = u
	}
	return cfg, nil
}

// loadJWTSecret resolves security.jwt_secret_file, or creates DATA_DIR/jwt.secret.
func loadJWTSecret(cfg Config) ([]byte, error) {
	if strings.TrimSpace(cfg.JWTSecret) != "" {
		return []byte(cfg.JWTSecret), nil
	}
	if strings.TrimSpace(cfg.JWTSecretFile) != "" {
		b, err := os.ReadFile(cfg.JWTSecretFile)
		if err != nil {
			return nil, fmt.Errorf("security.jwt_secret_file: %w", err)
		}
		secret := []byte(strings.TrimSpace(string(b)))
		if len(secret) < 32 {
			return nil, fmt.Errorf("security.jwt_secret_file %q must contain at least 32 bytes", cfg.JWTSecretFile)
		}
		return secret, nil
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
