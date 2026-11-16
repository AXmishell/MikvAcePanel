# 架构与安全

## 组件

```text
                        ┌────────────────────────────────────┐
  Admin browser ───────▶│ Panel                              │
                        │  Vue SPA (embedded in binary)      │
  Subscription ────────▶│  REST API + subscription renderer  │
                        │  WebSocket Hub                     │
  Hy2 auth callback ───▶│  SQLite / optional PG logs / Redis │
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

### 代码分层

```text
transport/httpapi + transport/agentws   HTTP / WebSocket 适配
        │
        ▼
internal/app/{users,nodes,...}          用例编排、校验、事务边界
        │  ports（仓储 / 通知 / 配置推送接口）
        ▼
internal/store (SQLite)                 控制面持久化实现
internal/store/pglogs (PostgreSQL)      可选日志持久化实现
internal/domain (model)                 实体、值对象、领域错误
```

- handler 只负责请求解析、调用应用服务、映射响应；
- `internal/app` 不依赖 HTTP 和具体数据库；
- `internal/ports` 由消费方定义接口，`store` 隐式实现；
- `internal/agent` 不依赖 store/server，只通过 WebSocket 协议与面板交互。

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
| `hello` | 版本、OS、架构、代理版本、能力、证书指纹、生命周期状态 |
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
| `hy2_logs` | 解析后的代理日志（可外置到 PostgreSQL） |
| `log_batches` / `stats_batches` | 幂等批次（`log_batches` 随日志外置） |
| `api_keys` | 细分权限、节点/分组资源范围、限流、用量、过期 |
| `settings` | 面板设置、加密后的 DNS 凭据 |
| `acme_accounts` | 持久化 ACME 账户密钥 |
| `schema_migrations` | schema 与数据迁移版本 |

SQLite 使用 WAL 模式；`VACUUM INTO` 生成一致性备份快照。

控制面只使用 SQLite，业务 SQL 统一使用 `?` 占位符；`internal/store/dialect` 提供 SQLite DDL、内省和 DML 片段。`database.driver: setup` 会先启动不打开数据库的安装器。DDL 由 `internal/store/schema` 元数据生成，`schema_migrations` 记录版本。备份/恢复直接操作 SQLite 文件。

### 可选 PostgreSQL 日志库

日志是唯一可以外置的数据域：

```text
internal/store.LogStore
  ├─ *store.Store            SQLite（默认）
  └─ *pglogs.Store           PostgreSQL（telemetry.driver: postgres）
```

- 只有 `hy2_logs` 和 `log_batches` 会进入 PostgreSQL；用户、节点、设置、证书、审计事件、心跳采样和流量批次仍在 SQLite；
- PostgreSQL 表按 UTC 天分区，清理时优先 `DROP` 过期分区；
- 节点名称与用户存在性无法跨库 JOIN，由 `store.LogDirectory` 分两步解析；
- 日志写入失败不会阻塞认证与订阅，Agent 会按 batch id 重发；`required: false` 时面板可回退到本地 SQLite 日志表；
- 面板备份只包含 SQLite 控制库，PostgreSQL 日志用 `pg_dump` 独立备份。

详见 [外部 PostgreSQL 日志库](telemetry-postgres.md)。

### 缓存层

`internal/cache` 提供两级缓存：

```text
分析请求 ──▶ 本地 LRU（5s） ──miss──▶ Redis（与配置 TTL 一致）
                                │
                                └─ 未配置 Redis：只使用本地 LRU
```

- 日志分析结果（仪表盘 60s、日志页 20s）是主要缓存对象；
- 本地层保证单进程热点请求不访问 Redis，Redis 层让多个面板实例共享结果；
- Redis 不可用时自动退化为本地计算，不影响功能；
- `/api/health` 的 `cache` 字段和设置页“数据清理”显示后端、命中率、条目数与错误数。

### DSN 模板

`internal/dsn` 统一维护 PostgreSQL / Redis 连接模板与 PostgreSQL DSN 格式校验：

- `/api/install/status` 与 `/api/infra` 在 `dsnTemplates` 中返回模板；
- 安装向导和设置页用同一个 `DsnTemplate` 组件渲染，点击“填入”后只需替换大写占位符；
- 保存前仍会调用真实连接测试，模板本身不会被自动保存。

## 安全边界

### 面板

- 管理员密码使用 bcrypt；
- JWT HS256，密钥来自 `security.jwt_secret_file`，未配置时自动生成 `data_dir/jwt.secret`；
- 登录失败按 IP 限流；失败一次后，30 分钟内同一 IP 的登录必须通过一次性图形验证码（5 分钟有效）；2FA challenge 一次性、5 分钟有效；
- API Key 只存 SHA-256 哈希，带细分权限、节点/分组资源范围与独立限流；
- 敏感数据加密：证书私钥、DNS 凭据、TOTP 密钥使用 AES-256-GCM（`cert.key`）。

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
