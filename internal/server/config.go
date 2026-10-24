package server

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store/dialect"
)

// Config is the panel runtime configuration, loaded from environment variables.
type Config struct {
	Addr          string
	DataDir       string
	DBDriver      string
	DBDSN         string
	DBPath        string
	DBMaxOpen     int
	DBMaxIdle     int
	DBConnMaxLife time.Duration
	JWTSecret     string
	PublicURL     string
	ServeWeb      bool
	FrontendURL   string
	AdminUsername string
	AdminPassword string
	// AdminPasswordGenerated reports that no ADMIN_PASSWORD was configured and
	// a random bootstrap password was created. It is printed once on first boot.
	AdminPasswordGenerated bool
	CORSOrigins            []string
	// TrustedProxies lists IPs/CIDRs whose X-Forwarded-For headers are trusted.
	// When empty, forwarded headers are ignored and the direct peer address is
	// used for rate limiting and audit logging.
	TrustedProxies []string
	LogLevel       string
	SessionTTL     time.Duration
	RedisURL       string
	RedisPrefix    string
	RedisRequired  bool
}

// LoadConfig reads environment variables with sensible local-development
// defaults.
func LoadConfig() Config {
	dataDir := env("DATA_DIR", "./data")
	dbDSN := env("DATABASE_URL", env("DB_DSN", ""))
	dbDriver := strings.ToLower(strings.TrimSpace(env("DB_DRIVER", "")))
	if dbDriver == "" {
		dbDriver = dialect.ForDSN(dbDSN)
	}
	if dbDriver == "" {
		dbDriver = dialect.SQLite
	}
	c := Config{
		Addr:          env("ADDR", ":8080"),
		DataDir:       dataDir,
		DBDriver:      dbDriver,
		DBDSN:         dbDSN,
		DBPath:        env("DB_PATH", filepath.Join(dataDir, "panel.db")),
		DBMaxOpen:     envInt("DB_MAX_OPEN", 0),
		DBMaxIdle:     envInt("DB_MAX_IDLE", 0),
		DBConnMaxLife: envDuration("DB_CONN_LIFETIME", 0),
		JWTSecret:     env("JWT_SECRET", ""),
		PublicURL:     strings.TrimRight(env("PANEL_PUBLIC_URL", "http://localhost:8080"), "/"),
		ServeWeb:      envBool("SERVE_WEB", true),
		FrontendURL:   strings.TrimRight(env("FRONTEND_URL", ""), "/"),
		AdminUsername: env("ADMIN_USERNAME", "admin"),
		LogLevel:      env("LOG_LEVEL", "info"),
		SessionTTL:    24 * time.Hour,
		RedisURL:      env("REDIS_URL", ""),
		RedisPrefix:   env("REDIS_PREFIX", "mikvace:"),
		RedisRequired: envBool("REDIS_REQUIRED", false),
	}
	if v, ok := os.LookupEnv("ADMIN_PASSWORD"); ok && strings.TrimSpace(v) != "" {
		c.AdminPassword = v
	} else {
		// Never ship a well-known default administrator password. A random one
		// is generated and printed once by the bootstrap log message.
		c.AdminPassword = randomAdminPassword()
		c.AdminPasswordGenerated = true
	}
	c.TrustedProxies = splitList(env("TRUSTED_PROXIES", ""))
	c.CORSOrigins = splitList(env("CORS_ORIGINS", ""))
	if h := env("SESSION_TTL_HOURS", ""); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 {
			c.SessionTTL = time.Duration(n) * time.Hour
		}
	}
	return c
}

func splitList(raw string) []string {
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func randomAdminPassword() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to a long random-looking
		// value instead of silently keeping a predictable password.
		return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(time.Now().UnixNano(), 36)))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func envDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d <= 0 {
		return def
	}
	return d
}
