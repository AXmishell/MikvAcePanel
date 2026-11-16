// Package dsn centralises connection-string templates and lightweight
// validation shared by the first-run wizard and the administrator
// "storage & cache" page.
package dsn

import (
	"errors"
	"net/url"
	"strings"
)

// Kind identifies a connection-string family.
type Kind string

const (
	Postgres Kind = "postgres"
	Redis    Kind = "redis"
)

// Template is one editable example shown in the UI. Values intentionally use
// upper-case placeholders so the operator must replace them before saving.
type Template struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Value       string `json:"value"`
}

// TemplateSet groups the templates for both optional backends.
type TemplateSet struct {
	Postgres []Template `json:"postgres"`
	Redis    []Template `json:"redis"`
}

// All returns the canonical template set exposed by the panel API.
func All() TemplateSet {
	return TemplateSet{
		Postgres: Templates(Postgres),
		Redis:    Templates(Redis),
	}
}

// Templates returns the examples for one backend.
func Templates(kind Kind) []Template {
	switch kind {
	case Postgres:
		return []Template{
			{
				ID:          "postgres-local",
				Label:       "本地 / 内网",
				Description: "不加密连接，适合 Docker 同一网络或可信内网。",
				Value:       "postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=disable",
			},
			{
				ID:          "postgres-tls",
				Label:       "TLS",
				Description: "加密连接，不校验证书链，适合托管数据库。",
				Value:       "postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=require",
			},
			{
				ID:          "postgres-verify",
				Label:       "TLS + 证书校验",
				Description: "需要把 CA 文件放到面板可读路径，并替换 sslrootcert。",
				Value:       "postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=verify-full&sslrootcert=/path/to/ca.pem",
			},
			{
				ID:          "postgres-keyword",
				Label:       "Keyword DSN",
				Description: "部分托管数据库提供 key=value 格式，密码含特殊字符时更方便。",
				Value:       "host=HOST port=5432 user=USER password=PASSWORD dbname=DATABASE sslmode=disable",
			},
		}
	case Redis:
		return []Template{
			{
				ID:          "redis-plain",
				Label:       "无密码",
				Description: "仅适合本地或受保护的内网 Redis。",
				Value:       "redis://HOST:6379/0",
			},
			{
				ID:          "redis-password",
				Label:       "密码",
				Description: "使用默认用户和密码，末尾数字为数据库编号。",
				Value:       "redis://:PASSWORD@HOST:6379/0",
			},
			{
				ID:          "redis-acl",
				Label:       "ACL 用户",
				Description: "Redis 6+ 的 ACL 账号，格式为 redis://USER:PASSWORD@HOST:6379/0。",
				Value:       "redis://USER:PASSWORD@HOST:6379/0",
			},
			{
				ID:          "redis-tls",
				Label:       "TLS",
				Description: "使用 rediss:// 启用 TLS。",
				Value:       "rediss://:PASSWORD@HOST:6379/0",
			},
		}
	default:
		return nil
	}
}

// ValidatePostgres checks the shape of a PostgreSQL DSN without connecting.
// Errors deliberately avoid echoing the input because it contains a password.
func ValidatePostgres(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("PostgreSQL DSN is empty")
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		u, err := url.Parse(raw)
		if err != nil {
			return errors.New("invalid PostgreSQL DSN")
		}
		if strings.TrimSpace(u.Hostname()) == "" && strings.TrimSpace(u.Query().Get("host")) == "" {
			return errors.New("PostgreSQL DSN is missing host")
		}
		if strings.Trim(u.Path, "/") == "" {
			return errors.New("PostgreSQL DSN is missing database name")
		}
		return nil
	}
	// pgx also accepts the libpq keyword form. Require the two fields that are
	// easy to forget and that a typo would otherwise turn into a confusing
	// connection error.
	hasHost, hasDB := false, false
	for _, field := range strings.Fields(raw) {
		key, _, _ := strings.Cut(field, "=")
		switch strings.ToLower(key) {
		case "host", "hostaddr":
			hasHost = true
		case "dbname", "database":
			hasDB = true
		}
	}
	if !hasHost || !hasDB {
		return errors.New("PostgreSQL DSN must be a postgres:// URI or a keyword DSN with host and dbname")
	}
	return nil
}
