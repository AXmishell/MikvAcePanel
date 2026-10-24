package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/configfile"
)

// SetupDriver is the sentinel database.driver value for a first-run panel that
// has not selected a backend yet. It starts the setup-only server, which never
// opens SQLite (or any other database) until the operator saves a choice.
const SetupDriver = "setup"

// Config is the panel runtime configuration, loaded from panel.yaml.
type Config struct {
	ConfigPath    string
	Addr          string
	DataDir       string
	DBDriver      string
	DBDSN         string
	DBDSNFile     string
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

	// Restart re-executes the panel after /install saves a new database
	// configuration. It is wired by cmd/server and is nil in tests.
	Restart func()
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
	// Prefer /config only when the mount point is not itself a directory.
	if st, err := os.Stat("/config"); err == nil && st.IsDir() && !isDir("/config/panel.yaml") {
		return "/config/panel.yaml"
	}
	// /data is the persistent volume in the container image, so a config
	// created there survives restarts even when /config is unusable.
	if st, err := os.Stat("/data"); err == nil && st.IsDir() && !isDir("/data/panel.yaml") {
		return "/data/panel.yaml"
	}
	return "panel.yaml"
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// LoadConfig reads panel.yaml. A missing file is allowed so /install can
// create it from the built-in defaults.
func LoadConfig(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = DefaultPanelConfigPath()
	}
	// 1Panel and Docker Compose create a directory at a single-file bind mount
	// when the host file does not exist yet. Fall back to a usable path so the
	// panel starts the installer instead of crash-looping with
	// "read /config/panel.yaml: is a directory".
	if isDir(path) {
		fallback := DefaultPanelConfigPath()
		if isDir(fallback) {
			// Last resort: the default data directory is writable in the
			// container image and in a local run.
			fallback = filepath.Join("data", "panel.yaml")
		}
		slog.Warn("config path is a directory, using fallback", "path", path, "fallback", fallback)
		path = fallback
	}
	existed := configfile.Exists(path)
	fc := defaultFileConfig()
	if existed {
		if err := configfile.Load(path, &fc); err != nil {
			return Config{}, err
		}
	} else {
		// A fresh deployment must not create data/panel.db just to show the
		// wizard. /install writes the chosen driver before the next start.
		fc.Database.Driver = SetupDriver
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
		DBDSNFile:      strings.TrimSpace(fc.Database.DSNFile),
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
	switch cfg.DBDriver {
	case "sqlite3":
		cfg.DBDriver = "sqlite"
	case "postgresql", "pg":
		cfg.DBDriver = "postgres"
	case "mariadb":
		cfg.DBDriver = "mysql"
	case "none", "unset", "ask":
		// Explicit aliases for the first-run no-database mode.
		cfg.DBDriver = SetupDriver
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
