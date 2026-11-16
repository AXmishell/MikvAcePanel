package server

import (
	"net/http"
	"strings"
)

// APIPermission is one fine-grained capability that can be granted to an API
// key. Values follow the "resource:action" convention. Legacy coarse scopes
// are expanded through legacyScopeAliases so existing keys keep working.
type APIPermission struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Group       string `json:"group"`
	Sensitive   bool   `json:"sensitive,omitempty"`
}

var apiPermissions = []APIPermission{
	// Users.
	{Value: "users:read", Label: "查看用户", Group: "用户", Description: "列表、详情、连接信息与订阅链接"},
	{Value: "users:write", Label: "创建 / 更新用户", Group: "用户", Description: "新建、编辑、启停、重置密码 / 订阅 / 设备 / 流量"},
	{Value: "users:delete", Label: "删除用户", Group: "用户"},

	// Groups.
	{Value: "groups:read", Label: "查看分组", Group: "分组"},
	{Value: "groups:write", Label: "创建 / 更新分组", Group: "分组"},
	{Value: "groups:delete", Label: "删除分组", Group: "分组"},

	// Nodes.
	{Value: "nodes:read", Label: "查看节点", Group: "节点", Description: "节点列表、状态与运行指标"},
	{Value: "nodes:write", Label: "创建 / 更新节点", Group: "节点", Description: "新建、编辑节点与调整排序"},
	{Value: "nodes:operate", Label: "操作节点", Group: "节点", Description: "同步、重启、重置 token、SSH 安装"},
	{Value: "nodes:secrets", Label: "读取节点凭据", Group: "节点", Description: "节点配置、安装脚本与证书私钥相关输出", Sensitive: true},
	{Value: "nodes:delete", Label: "删除节点", Group: "节点"},

	// Certificates.
	{Value: "certs:read", Label: "查看证书", Group: "证书", Description: "证书列表、ACME 设置（密钥不回显）"},
	{Value: "certs:write", Label: "签发 / 管理证书", Group: "证书", Description: "修改 ACME 设置、申请、上传与取消证书", Sensitive: true},

	// Logs and monitoring.
	{Value: "dashboard:read", Label: "查看仪表盘", Group: "监控", Description: "仪表盘统计与面板事件"},
	{Value: "logs:read", Label: "查看 / 导出日志", Group: "监控", Description: "日志列表、摘要分析与导出"},
	{Value: "logs:delete", Label: "删除日志", Group: "监控"},

	// Maintenance and backups.
	{Value: "maintenance:read", Label: "查看数据用量", Group: "维护"},
	{Value: "maintenance:write", Label: "清理遥测数据", Group: "维护", Sensitive: true},
	{Value: "backups:read", Label: "查看 / 下载备份", Group: "维护", Sensitive: true},
	{Value: "backups:write", Label: "创建 / 恢复备份", Group: "维护", Sensitive: true, Description: "包含创建、删除与恢复"},

	// Settings and global sync.
	{Value: "settings:read", Label: "查看设置", Group: "系统", Sensitive: true},
	{Value: "settings:write", Label: "修改设置", Group: "系统", Sensitive: true, Description: "可修改面板对外名称、订阅与告警配置"},
	{Value: "sync:write", Label: "触发全局同步", Group: "系统"},
}

// legacyScopeAliases keeps the original coarse read scope working for keys
// that were created before fine-grained permissions existed. Write scopes are
// not aliased: they are expanded once by the data migration so that separating
// users:delete / nodes:operate / nodes:secrets is meaningful.
var legacyScopeAliases = map[string][]string{
	"stats:read": {"dashboard:read", "logs:read", "groups:read"},
}

var apiPermissionKnown = func() map[string]bool {
	out := map[string]bool{"*": true}
	for _, p := range apiPermissions {
		out[p.Value] = true
	}
	for scope := range legacyScopeAliases {
		out[scope] = true
	}
	return out
}()

// permissionRules maps an HTTP route to the permissions that may access it.
// The first matching rule wins, so literal segments such as
// /api/nodes/reorder must be listed before /api/nodes/{id}.
var permissionRules = []permissionRule{
	// Global surfaces.
	{http.MethodGet, "/api/dashboard", []string{"dashboard:read"}},
	{http.MethodGet, "/api/events", []string{"dashboard:read"}},
	{http.MethodPost, "/api/sync", []string{"sync:write"}},

	// Users.
	{http.MethodGet, "/api/users", []string{"users:read"}},
	{http.MethodPost, "/api/users", []string{"users:write"}},
	{http.MethodGet, "/api/users/{id}/connections", []string{"users:read"}},
	{http.MethodGet, "/api/users/{id}", []string{"users:read"}},
	{http.MethodPut, "/api/users/{id}", []string{"users:write"}},
	{http.MethodDelete, "/api/users/{id}", []string{"users:delete"}},
	{http.MethodPost, "/api/users/{id}/enable", []string{"users:write"}},
	{http.MethodPost, "/api/users/{id}/disable", []string{"users:write"}},
	{http.MethodPost, "/api/users/{id}/reset-subscription", []string{"users:write"}},
	{http.MethodPost, "/api/users/{id}/reset-password", []string{"users:write"}},
	{http.MethodPost, "/api/users/{id}/reset-devices", []string{"users:write"}},
	{http.MethodPost, "/api/users/{id}/reset-traffic", []string{"users:write"}},

	// Groups.
	{http.MethodGet, "/api/groups", []string{"groups:read"}},
	{http.MethodPost, "/api/groups", []string{"groups:write"}},
	{http.MethodPut, "/api/groups/{id}", []string{"groups:write"}},
	{http.MethodDelete, "/api/groups/{id}", []string{"groups:delete"}},

	// Nodes. Ordered from most specific to least specific.
	{http.MethodPost, "/api/nodes/reorder", []string{"nodes:write"}},
	{http.MethodPost, "/api/nodes/{id}/token", []string{"nodes:operate"}},
	{http.MethodPost, "/api/nodes/{id}/sync", []string{"nodes:operate", "sync:write"}},
	{http.MethodPost, "/api/nodes/{id}/command", []string{"nodes:operate"}},
	{http.MethodPost, "/api/nodes/{id}/ssh/install", []string{"nodes:operate"}},
	{http.MethodGet, "/api/nodes/{id}/config", []string{"nodes:secrets"}},
	{http.MethodGet, "/api/nodes/{id}/install", []string{"nodes:secrets"}},
	{http.MethodGet, "/api/nodes/{id}/cert", []string{"nodes:secrets"}},
	{http.MethodPost, "/api/nodes/{id}/cert/request", []string{"certs:write"}},
	{http.MethodPost, "/api/nodes/{id}/cert/upload", []string{"certs:write"}},
	{http.MethodPost, "/api/nodes/{id}/cert/confirm", []string{"certs:write"}},
	{http.MethodPost, "/api/nodes/{id}/cert/cancel", []string{"certs:write"}},
	{http.MethodGet, "/api/nodes", []string{"nodes:read"}},
	{http.MethodPost, "/api/nodes", []string{"nodes:write"}},
	{http.MethodGet, "/api/nodes/{id}", []string{"nodes:read"}},
	{http.MethodPut, "/api/nodes/{id}", []string{"nodes:write"}},
	{http.MethodDelete, "/api/nodes/{id}", []string{"nodes:delete"}},

	// Certificates.
	{http.MethodGet, "/api/certs", []string{"certs:read"}},
	{http.MethodGet, "/api/cert/settings", []string{"certs:read"}},
	{http.MethodPut, "/api/cert/settings", []string{"certs:write"}},

	// Logs.
	{http.MethodGet, "/api/logs", []string{"logs:read"}},
	{http.MethodGet, "/api/logs/export", []string{"logs:read"}},
	{http.MethodGet, "/api/logs/summary", []string{"logs:read"}},
	{http.MethodGet, "/api/logs/options", []string{"logs:read"}},
	{http.MethodDelete, "/api/logs", []string{"logs:delete"}},

	// Maintenance.
	{http.MethodGet, "/api/maintenance/usage", []string{"maintenance:read"}},
	{http.MethodPost, "/api/maintenance/cleanup", []string{"maintenance:write"}},

	// Backups.
	{http.MethodGet, "/api/backups", []string{"backups:read"}},
	{http.MethodPost, "/api/backups", []string{"backups:write"}},
	{http.MethodPost, "/api/backups/restore", []string{"backups:write"}},
	{http.MethodGet, "/api/backups/{name}", []string{"backups:read"}},
	{http.MethodDelete, "/api/backups/{name}", []string{"backups:write"}},

	// Settings.
	{http.MethodGet, "/api/settings", []string{"settings:read"}},
	{http.MethodPut, "/api/settings", []string{"settings:write"}},
}

type permissionRule struct {
	Method string
	Path   string
	AnyOf  []string
}

func normalizeAPIPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func matchPermissionPath(pattern, path string) bool {
	if pattern == path {
		return true
	}
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	xs := strings.Split(strings.Trim(path, "/"), "/")
	if len(ps) != len(xs) {
		return false
	}
	for i := range ps {
		if strings.HasPrefix(ps[i], "{") && strings.HasSuffix(ps[i], "}") {
			if xs[i] == "" {
				return false
			}
			continue
		}
		if ps[i] != xs[i] {
			return false
		}
	}
	return true
}

// apiKeyAllows reports whether the granted scopes permit a request. Routes that
// are not present in permissionRules are deliberately denied so new endpoints
// are never exposed to API keys by accident.
func apiKeyAllows(scopes []string, method, path string) bool {
	if path == "/api/me" && method == http.MethodGet {
		return true
	}
	path = normalizeAPIPath(path)
	for _, rule := range permissionRules {
		if rule.Method != method || !matchPermissionPath(rule.Path, path) {
			continue
		}
		for _, need := range rule.AnyOf {
			if scopeGranted(scopes, need) {
				return true
			}
		}
		return false
	}
	return false
}

func scopeGranted(scopes []string, need string) bool {
	for _, s := range scopes {
		if s == "*" || s == need {
			return true
		}
		for _, implied := range legacyScopeAliases[s] {
			if implied == need {
				return true
			}
		}
	}
	return false
}

func scopeKnown(scope string) bool {
	return apiPermissionKnown[scope]
}

// requiredPermissions returns the permissions a route accepts, for error
// messages and documentation tooling.
func requiredPermissions(method, path string) []string {
	path = normalizeAPIPath(path)
	for _, rule := range permissionRules {
		if rule.Method == method && matchPermissionPath(rule.Path, path) {
			return rule.AnyOf
		}
	}
	return nil
}

func scopeCatalog() []APIPermission {
	return append([]APIPermission{}, apiPermissions...)
}
