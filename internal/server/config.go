package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the panel runtime configuration, loaded from environment variables.
type Config struct {
	Addr          string
	DataDir       string
	DBPath        string
	JWTSecret     string
	PublicURL     string
	AdminUsername string
	AdminPassword string
	CORSOrigins   []string
	LogLevel      string
	SessionTTL    time.Duration
	RedisURL      string
	RedisPrefix   string
	RedisRequired bool
}

// LoadConfig reads environment variables with sensible local-development
// defaults.
func LoadConfig() Config {
	dataDir := env("DATA_DIR", "./data")
	c := Config{
		Addr:          env("ADDR", ":8080"),
		DataDir:       dataDir,
		DBPath:        env("DB_PATH", filepath.Join(dataDir, "panel.db")),
		JWTSecret:     env("JWT_SECRET", ""),
		PublicURL:     strings.TrimRight(env("PANEL_PUBLIC_URL", "http://localhost:8080"), "/"),
		AdminUsername: env("ADMIN_USERNAME", "admin"),
		AdminPassword: env("ADMIN_PASSWORD", "admin123"),
		LogLevel:      env("LOG_LEVEL", "info"),
		SessionTTL:    24 * time.Hour,
		RedisURL:      env("REDIS_URL", ""),
		RedisPrefix:   env("REDIS_PREFIX", "mikvace:"),
		RedisRequired: envBool("REDIS_REQUIRED", false),
	}
	if raw := env("CORS_ORIGINS", ""); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				c.CORSOrigins = append(c.CORSOrigins, p)
			}
		}
	}
	if h := env("SESSION_TTL_HOURS", ""); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 {
			c.SessionTTL = time.Duration(n) * time.Hour
		}
	}
	return c
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
