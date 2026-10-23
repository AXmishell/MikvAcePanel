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

### 其他参数

- `listen` / `port`、统计端口、统计密钥；
- obfs（salamander / gecko）；
- 上行/下行带宽；
- ACL 规则与自定义出站；
- masquerade URL；
- Xray：入站列表（协议、端口、传输）、REALITY 密钥、SNI、short id、uTLS 指纹、ALPN、API 端口、入站标签、XHTTP/mKCP 参数。

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

- 修改管理员密码；
- 管理员安全入口（默认关闭）：开启后管理端登录地址变为 `/admin/<入口路径>/login`，普通 `/admin` 返回 404，管理 API 也会校验 `X-Admin-Entry`；
- 两步验证（TOTP）：启用、关闭、重新生成恢复码；
- API Key：创建、作用域、速率限制、过期时间、启停。

详见 [两步验证](2fa.md)。

### 证书签发

- ACME 目录（Let's Encrypt / Staging）；
- ACME 邮箱；
- DNS 服务商：手动 TXT、Cloudflare、阿里云 DNS、DNSPod；
- 提前续期天数。

详见 [证书签发](certificates.md)。

### 备份

立即备份、自动备份间隔、保留数量、下载、删除、上传恢复。恢复操作需要重启面板后生效。

## API Key

API Key 用于自动化和 REST API：

| 作用域 | 能力 |
| --- | --- |
| `users:read` / `users:write` | 用户查询 / 修改 |
| `nodes:read` / `nodes:write` | 节点查询 / 修改 |
| `stats:read` | 仪表盘、日志、统计 |
| `sync:write` | 同步节点配置 |

- 每个 Key 独立限流，响应头包含 `X-RateLimit-*`；
- 明文只在创建时返回一次；
- API Key 不能访问 2FA 管理、修改密码等会话级接口；
- 支持过期时间和启停。

## 命令行客户端

```bash
go build -o mikvace-client ./cmd/client

./mikvace-client info --url https://panel.example.com/api/files/<token>
./mikvace-client sub  --url ... --format clash --out clash.yaml
./mikvace-client check --url ...
./mikvace-client run  --url ... --node 1 --socks5 127.0.0.1:1080
```
