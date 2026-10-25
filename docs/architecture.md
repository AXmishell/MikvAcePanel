# 架构与安全

## 组件

```text
                        ┌────────────────────────────────────┐
  Admin browser ───────▶│ Panel                              │
                        │  Vue SPA (embedded in binary)      │
  Subscription ────────▶│  REST API + subscription renderer  │
                        │  WebSocket Hub                     │
  Hy2 auth callback ───▶│  SQLite / optional Redis           │
                        └───────────────┬────────────────────┘
                                        │ WSS /api/node/ws
                        ┌───────────────▼────────────────────┐
                        │ Node Agent                         │
                        │  config writer / cert installer    │
                        │  log batcher / stats poller        │
                        └───────┬──────────────────┬─────────┘
                                │ process          │ docker sibling
                        ┌───────▼──────┐   ┌───────▼──────────┐
                        │ Hysteria 2   │   │ Hysteria / Xray  │
                        └──────────────┘   └──────────────────┘
```

面板是唯一控制面，节点是数据面。代理核心不直接访问数据库，只通过 Agent 接收配置和上报数据。

## 配置流

```text
节点设置保存
  → 渲染 YAML/JSON，计算 hash
  → hash 未变化则复用 revision
  → 在线 Agent：通过 WebSocket 推送
  → Agent 原子写配置
  → 端口跳跃 / 统计 / 证书准备
  → 重启代理核心
  → config.ack（revision + 错误）
  → 面板记录 appliedRevision / configError
```

Agent 断线重连时：

1. 发送 `hello`（版本、能力、证书指纹）；
2. 面板补推证书（managed 模式）；
3. 面板强制同步一次配置；
4. Agent 比较 revision 与本地配置，相同且进程运行则直接 ack，不重启。

## Agent 协议

### Agent → 面板

| 消息 | 内容 |
| --- | --- |
| `hello` | 版本、OS、架构、代理版本、能力、证书指纹 |
| `heartbeat` | CPU/内存/磁盘/load/uptime/在线数/累计流量 |
| `logs` | 日志批次 + batch id |
| `stats` | 用户流量增量 + batch id |
| `users.request` | 请求刷新离线认证用户快照 |
| `config.ack` | revision、成功/失败、pending |
| `command.ack` | start/stop/restart 结果 |
| `cert.request` | CSR、域名、公钥指纹 |

### 面板 → Agent

| 消息 | 内容 |
| --- | --- |
| `config` | `configs` 映射（每个核心一份 YAML/JSON）、`cores`、revision、TLS 模式、域名、端口、端口跳跃 |
| `command` | start / stop / restart |
| `cert.push` | 证书链、指纹、有效期；上传模式含私钥 |
| `cert.error` | 签发失败原因 |
| `cert.renew` | 管理员触发续期 |
| `user.snapshot` | 节点离线认证快照（启用用户、密码、到期、流量、设备上限） |
| `log.ack` / `stats.ack` | 幂等确认 |

日志和统计使用 batch id，服务端以 `(node_id, batch_id)` 唯一索引保证只应用一次；Agent 未收到 ack 会重发。

## 数据模型

主要表：

| 表 | 用途 |
| --- | --- |
| `admins` | 管理员、密码哈希、TOTP 密钥与恢复码 |
| `users` | 代理用户、订阅 Token、流量、到期、设备数 |
| `panel_groups` / `user_groups` / `node_groups` | 分组与可见性 |
| `nodes` | 节点设置、Token 哈希、状态、证书元数据 |
| `node_configs` | 配置 revision、YAML、hash、应用结果 |
| `certificates` / `node_certificates` | 证书、节点映射、指纹与有效期 |
| `node_metrics` | 心跳指标 |
| `events` | 节点上下线、配置、证书、备份等事件 |
| `hy2_logs` | 解析后的代理日志 |
| `log_batches` / `stats_batches` | 幂等批次 |
| `api_keys` | 作用域、限流、过期 |
| `settings` | 面板设置、加密后的 DNS 凭据 |
| `acme_accounts` | 持久化 ACME 账户密钥 |
| `schema_migrations` | schema 与数据迁移版本 |

SQLite 使用 WAL 模式；`VACUUM INTO` 生成一致性备份快照。

存储层通过 `internal/store/dialect` 和 `store.DB` 包装层隔离方言：业务 SQL 继续使用 `?`，PostgreSQL 自动重写为 `$1`。SQLite 是安装向导的默认后端，PostgreSQL 18 和 MySQL 8.0 也可直接作为面板数据库使用；`database.driver: setup` 会先启动不打开数据库的安装器。DDL 由 `internal/store/schema` 元数据生成，`schema_migrations` 记录版本。远程库的备份/恢复尚未实现，仍会返回明确的未实现错误。

## 安全边界

### 面板

- 管理员密码使用 bcrypt；
- JWT HS256，密钥来自 `security.jwt_secret_file`，未配置时自动生成 `data_dir/jwt.secret`；
- 登录失败按 IP 限流；失败一次后，30 分钟内同一 IP 的登录必须通过一次性图形验证码（5 分钟有效）；2FA challenge 一次性、5 分钟有效；
- API Key 只存 SHA-256 哈希，带作用域与独立限流；
- 敏感数据加密：证书私钥、DNS 凭据、TOTP 密钥使用 AES-256-GCM（`cert.key`）；
- 自定义首页在无 `allow-same-origin` 的沙箱 iframe 中运行，响应同时带 `CSP: sandbox`，不能读取面板 `localStorage`、Cookie 或管理 API Token。

### 节点

- 节点 Token 只存哈希；重置后旧 Agent 失效；
- Agent 只接受面板的配置/命令，不开放入站管理端口；
- 私钥默认不出节点（CSR 模式）；
- 端口跳跃需要 `NET_ADMIN`；
- Docker 运行时挂载 `/var/run/docker.sock`，视为 root 等价权限。

### 传输

- 生产环境必须使用 HTTPS/WSS；
- 自签名面板可设置 `PANEL_INSECURE=true`（或 `node.yaml` 的 `panel.insecure`），但不建议长期使用；
- 反向代理必须支持 WebSocket Upgrade；
- ACME DNS-01 只产生面板到 DNS/CA 的出站连接。

## 部署模式

| 模式 | 说明 |
| --- | --- |
| 单面板 + 远程节点 | 最常见；节点通过公网 WebSocket 连接 |
| 面板与节点同机 | 节点仍通过面板公开地址连接，便于统一管理 |
| 节点单容器 | Agent 与 Hysteria/Xray 同一容器，`runtime.type: process` |
| 节点 Docker 运行时 | Agent 通过 docker.sock 启动兄弟代理容器 |

## 失败与恢复

| 场景 | 行为 |
| --- | --- |
| 面板重启 | 管理员会话通常保留（JWT secret / Redis 未变）；节点自动重连并补推 |
| 节点断线 | 代理继续按本地配置运行；面板标记离线并记录事件 |
| 配置被拒绝 | 面板记录错误；Agent 回滚旧配置并重启 |
| 证书续期失败 | 旧证书继续服务；到期前定时重试 |
| 面板离线 | 节点继续运行；Hysteria 使用本地认证快照，Xray 使用本地配置；managed 证书无法续期 |
| 数据库损坏 | 从 `backups/` 恢复；原库保留为 `pre-restore` |
