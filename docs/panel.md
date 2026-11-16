# 面板功能

本文按页面介绍 MikvAce Panel 的主要功能。

## 仪表盘

- 节点在线数、用户数、当前在线连接；
- 流量汇总、节点资源（CPU / 内存 / 磁盘 / load）；
- 最近事件：节点上下线、配置错误、认证失败、证书异常等；
- 快速跳转到节点、用户、日志页面。

## 用户

用户是 Hysteria / Xray 的代理账号，同时对应一条订阅链接。

| 字段 | 说明 |
| --- | --- |
| 用户 ID / 密码 | Hysteria 认证使用；Xray 使用 UUID |
| 启用状态 | 禁用后认证会立即拒绝 |
| 流量上限 | 按上行 + 下行累计；达到后 Hysteria 认证失败，Xray 用户会从节点配置中移除 |
| 到期时间 | 过期后自动禁用，并从 Hysteria 认证和 Xray 配置中移除 |
| 设备数 | 按唯一 IP 统计，带宽限时间 |
| 节点 / 分组 | 控制订阅中可见的节点 |
| 订阅 Token | 可重置；旧链接会失效 |

用户页支持搜索、分页、重置流量、重置订阅、重置密码、重置设备。

## 分组

分组用于组织节点，并作为订阅过滤维度：

- 一个节点可以属于多个分组；
- 用户可以被限制到指定节点或分组；
- 分组可设置订阅标题、描述、颜色和设备数上限。

## 节点

节点页面是控制面核心。

### 节点类型

节点可以选择一个或两个**运行核心**；同时启用时 Agent 会在同一台机器上并行运行两个核心，订阅会自动合并所有入站。

- **Hysteria 2**：QUIC/UDP，支持 obfs、端口跳跃、带宽限制、ACL、masquerade；
- **Xray**：TCP，支持多个入站；每个入站可独立选择协议（VLESS / VMess / Trojan / Shadowsocks）、端口、传输（TCP / WebSocket / gRPC / XHTTP / mKCP）和安全方式（REALITY / TLS / 无加密）。新建节点时可用协议开关或预设快速启用，取消勾选只禁用、不丢失配置。

### TLS 模式

| 模式 | 说明 |
| --- | --- |
| `selfsigned` | Agent 生成本地自签名证书；订阅 `insecure=1` |
| `managed` | 面板 ACME + CSR 下发，客户端校验证书；推荐 |
| `acme` | 节点 Hysteria 自行申请，仅 TLS-ALPN，不占用 80 |

详见 [证书签发](certificates.md)。

### 配置与状态

- 面板渲染 YAML/JSON，保存 revision 与 hash；
- Agent 连接时补推；应用后返回 `config.ack`；
- 支持启动、停止、重启、重新同步；
- 显示版本、运行状态、在线用户、CPU / 内存 / 磁盘、最近错误。

### 端口跳跃

设置 `portRange`（例如 `20000-50000`）后：

- 面板生成订阅的 `mport`；
- Agent 在节点上维护 iptables UDP REDIRECT 规则；
- 需要 `NET_ADMIN` 与 host 网络；
- 目前仅 IPv4。

订阅中的 **TLS** 与 **Hopping** 入口可在节点设置的“订阅入口”中分别开关（`includeTls` / `includeHopping`），避免同一节点重复出现；至少保留一个，仅保留 Hopping 时必须配置端口范围。该开关只过滤订阅输出，节点的 TLS 监听与端口跳跃规则保持不变。

面板「设置 → 订阅页面 → 新节点默认订阅入口」只作为新建节点时的默认值，已有节点以节点自身的开关为准。

### 其他参数

- `listen` / `port`、统计端口、统计密钥；
- obfs（salamander / gecko）；
- 上行/下行带宽；
- ACL 规则与自定义出站；
- masquerade URL；
- Xray：独立「Xray 设置」页签，支持协议/预设、入站列表（协议、端口、传输）、REALITY 密钥、SNI、short id、uTLS 指纹、ALPN、API 端口、日志级别、路由策略、协议嗅探、XHTTP/mKCP 参数。

## 日志

Agent 将代理核心 stdout/stderr 和 Xray access log 批量上报，面板解析后提供：

- 时间线、级别分布、事件分布；
- Top 客户端 IP、Top 用户、目标地址；
- 节点维度筛选与全文搜索；
- 规则告警与阈值配置。

详见 [日志分析](log-analysis.md)。

## 设置

### 常规

面板名称、公开地址、主题外观。`public_url` 会写入订阅链接和 Hysteria 认证 URL，修改后节点会在下一次配置同步时更新。

### 订阅页面

订阅落地页标题、说明、Logo、更新间隔、按钮、HAPP provider / 路由 / 公告。

### 日志分析

日志采集开关、保留天数、分析窗口、暴力破解 / 未知用户 / 错误爆发阈值。

### 安全

- 修改管理员用户名与密码（密码至少 8 位，修改需当前密码；启用 2FA 时还需验证码）；
- 修改密码或用户名后，其他管理员会话会在数秒内失效，当前会话自动续签；
- 管理员安全入口（强制开启）：管理端只通过 `/admin/<入口路径>` 进入，普通 `/admin` 返回 404；访问入口后服务端签发 HttpOnly 门禁 Cookie，管理 API 同时接受该 Cookie 或 `X-Admin-Entry` 请求头。入口丢失时可从数据目录的 `admin-entry.txt` 恢复，或在该页面重新生成；访问日志会将入口路径替换为 `<entrance>`，避免秘密写入日志；
- 两步验证（TOTP）：启用、关闭、重新生成恢复码；
- API Key：多密钥、细分权限、节点/分组资源范围、令牌轮换、速率限制、过期时间、启停与用量审计。

详见 [两步验证](2fa.md)。

### 证书签发

- ACME 目录（Let's Encrypt / LiteSSL）；
- ACME 邮箱；
- LiteSSL 需要 EAB Key ID 与 HMAC Key；
- DNS 服务商：手动 TXT、Cloudflare、阿里云 DNS、DNSPod；
- 提前续期天数。

详见 [证书签发](certificates.md)。

### 备份

立即备份、自动备份间隔、保留数量、下载、删除、上传恢复。恢复操作需要重启面板后生效。

## API Key

API Key 用于自动化和 REST API，支持多密钥、按密钥细分权限与资源范围：

| 权限组 | 作用域 |
| --- | --- |
| 用户 | `users:read`、`users:write`、`users:delete` |
| 分组 | `groups:read`、`groups:write`、`groups:delete` |
| 节点 | `nodes:read`、`nodes:write`、`nodes:operate`、`nodes:secrets`、`nodes:delete` |
| 证书 | `certs:read`、`certs:write` |
| 监控 | `dashboard:read`、`logs:read`、`logs:delete` |
| 维护 | `maintenance:read`、`maintenance:write`、`backups:read`、`backups:write` |
| 系统 | `settings:read`、`settings:write`、`sync:write` |

- **资源范围**：可将密钥限定到指定节点和/或分组；列表自动过滤，越权访问返回 `403`。留空表示全部资源。
- 旧密钥的 `users:write`、`nodes:write` 会在升级时自动展开为细分权限，保留原有能力；`stats:read` 仍然兼容。
- 每个 Key 独立限流，响应头包含 `X-RateLimit-*`；列表展示请求次数、最后使用时间与来源 IP。
- 明文只在创建时返回一次；支持随时轮换令牌、编辑权限、修改限流与到期时间。
- API Key 不能访问密钥管理、2FA、修改密码等会话级接口。

完整权限与接口说明见 [API 文档](api.md#api-key)。

## 命令行客户端

```bash
go build -o mikvace-client ./cmd/client

./mikvace-client info --url https://panel.example.com/api/files/<token>
./mikvace-client sub  --url ... --format clash --out clash.yaml
./mikvace-client check --url ...
./mikvace-client run  --url ... --node 1 --socks5 127.0.0.1:1080
```
