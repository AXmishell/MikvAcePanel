# API 参考

Base URL：`https://panel.example.com/api`

管理端接口使用 JWT。

### 登录

```http
POST /api/login
Content-Type: application/json

{"username":"admin","password":"..."}
```

首次登录不需要验证码。任意一次登录失败后，同一 IP 的后续登录都必须附带图形验证码：

```json
{"username":"admin","password":"...","captchaId":"...","captchaAnswer":"AB3DE"}
```

失败响应会返回 `captchaRequired` 和一个新的验证码（5 分钟有效、一次性）：

```json
{"error":"invalid username or password","captchaRequired":true,
 "captcha":{"id":"...","image":"data:image/svg+xml;base64,...","expiresAt":"..."}}
```

`GET /api/login/captcha` 可主动刷新验证码，返回同样的 `id/image/expiresAt` 对象。验证码在登录成功后重置；验证失败不会清除 `captchaRequired`。

未启用 2FA：

```json
{"token":"...","expiresAt":"...","admin":{"id":1,"username":"admin"},"warning":"","version":"v1.2.3"}
```

已启用 2FA 时不会直接返回 token：

```json
{"twoFactorRequired":true,"challenge":"...","expiresAt":"..."}
```

第二步：

```http
POST /api/login/2fa
Content-Type: application/json

{"challenge":"...","code":"123456"}
```

成功后返回与普通登录相同的 token。后续请求携带：

```http
Authorization: Bearer <token>
```

未认证返回 `401 {"error":"authentication required"}`。

### 管理员安全入口

安全入口强制开启，无法关闭。首次启动会在数据目录生成随机入口并写入 `admin-entry.txt`（0600）；入口地址形如：

```text
/admin/<入口路径>
```

直接访问 `/admin`、`/admin/login` 或任何不包含入口路径的管理端地址都会返回 `404 {"error":"not found"}`。访问一次入口地址后，服务端会签发 HttpOnly 门禁 Cookie `mikvace_admin_gate`；同一浏览器随后可以直接使用 `/admin`。入口路径变更后，旧 Cookie 立即失效。

API 客户端也可以不依赖 Cookie，直接在请求中携带：

```http
X-Admin-Entry: <入口路径>
```

`/api/login`、`/api/login/2fa` 以及所有管理员接口缺少入口时返回 `404 {"error":"not found"}`。`GET /api/admin/config` 是公开探测接口，只返回入口是否强制开启、当前请求是否已持有门禁 Cookie、面板名称以及是否已完成初始化（不返回入口路径）：

```json
{ "securityEntryEnabled": true, "gateActive": false, "panelName": "MikvAce Panel", "installed": true }
```

API Key（`Authorization: Bearer ck_...`）不受安全入口限制，但仍受权限范围、速率限制和过期时间约束。

---

## 公共接口

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查、版本、uptime、Agent Hub 状态、控制库/日志库/缓存后端 |
| GET | `/api/install/status` | 首次初始化状态（是否已安装、面板名称、SQLite 配置） |
| POST | `/api/install/database/test` | 测试 SQLite 数据文件路径；仅在管理员表为空时可用 |
| POST | `/api/install/database` | 保存 SQLite 数据文件路径；变化后需要重启面板 |
| POST | `/api/install/import` | 从 MikvAce / CELERITY 备份恢复；仅在管理员表为空时可用 |
| POST | `/api/install` | 创建第一个管理员；仅在管理员表为空时可用 |
| GET | `/api/agent/version` | Agent 可用版本信息 |
| GET | `/api/agent/install.sh` | 节点安装脚本（通过参数传 panel/token） |
| POST | `/api/auth` | Hysteria 2 外部认证 |
| GET | `/api/files/:token` | 用户订阅 |
| GET | `/api/files/info/:token` | 订阅信息 |
| GET | `/api/node/ws` | 节点 Agent 控制通道（WebSocket） |

### 首次初始化

`GET /api/install/status`：

```json
{
  "installed": false,
  "needsSetup": true,
  "setupMode": true,
  "panelName": "MikvAce Panel",
  "publicUrl": "https://panel.example.com",
  "database": "sqlite",
  "databaseConfig": {
    "driver": "sqlite",
    "path": "/app/data/panel.db"
  },
  "telemetryConfig": {
    "driver": "sqlite",
    "schema": "public",
    "required": false,
    "configured": false
  },
  "redisConfig": {
    "enabled": false,
    "prefix": "mikvace:",
    "required": false
  },
  "dsnTemplates": {
    "postgres": [{ "id": "postgres-local", "label": "本地 / 内网", "value": "postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=disable" }],
    "redis": [{ "id": "redis-password", "label": "密码", "value": "redis://:PASSWORD@HOST:6379/0" }]
  },
  "configPath": "/app/config/panel.yaml",
  "configWritable": true,
  "startedAt": "2026-10-24T09:00:00Z",
  "version": "v1.2.3"
}
```

`databaseConfig` 描述磁盘上的 SQLite 配置。`startedAt` 在进程重启后变化，安装页用它判断数据库切换是否完成。

`setupMode: true` 表示当前进程还没有保存数据库配置（`database.driver: setup` 或配置文件不存在），此时不会打开或创建 SQLite；保存路径后进程重启，`setupMode` 变为 `false`。

`POST /api/install/database/test` 与 `POST /api/install/database` 接收相同请求体，主库固定为 SQLite，PostgreSQL 与 Redis 均为可选：

```json
{
  "driver": "sqlite",
  "path": "/app/data/panel.db",
  "telemetry": {
    "enabled": true,
    "dsn": "postgres://panel:pw@db:5432/mikvace?sslmode=disable",
    "schema": "public",
    "required": false
  },
  "redis": {
    "enabled": true,
    "url": "redis://:password@cache:6379/0",
    "prefix": "mikvace:",
    "required": false
  }
}
```

- `driver` 只接受 `sqlite`（`sqlite3` 视为同义），留空也按 SQLite 处理；`telemetry` / `redis` 可省略；
- `telemetry.enabled` 为 `true` 时 DSN 必填（重启后再次保存可留空以复用已保存的密钥）；`required` 决定 PostgreSQL 不可用时是否拒绝启动；
- `redis.enabled` 为 `true` 时 URL 必填（同样支持留空复用）；`required` 决定 Redis 不可用时是否拒绝启动；
- 测试接口验证所有已启用后端但不写配置；保存接口先执行 SQLite 幂等迁移，再测试可选后端。`required: false` 的后端测试失败只记录警告，仍会保存并允许启动时回退；
- DSN / Redis URL 写入 `data_dir/telemetry.pg.url` 与 `data_dir/redis.url`（0600），`panel.yaml` 只保存 `*_file` 路径；
- 返回 `{"ok":true,"driver":"sqlite","telemetry":"postgres","redis":true,"restartRequired":true}`。`restartRequired` 为 `true` 时进程会自行重启；
- 已有管理员时两个接口都返回 `409`。

`POST /api/install` 创建第一个管理员，请求体：

```json
{
  "username": "admin",
  "password": "至少 8 个字符",
  "panelName": "MikvAce Panel",
  "publicUrl": "https://panel.example.com"
}
```

成功返回与 `/api/login` 相同结构的 `token` / `expiresAt` / `admin` / `version`，前端会直接进入控制台。已有管理员时返回 `409 {"error":"panel is already installed"}`。用户名规则为 3–32 位字母、数字、点、下划线或短横线。

### 从备份导入

首次初始化时也可以直接恢复已有数据，而不是创建新的空面板。`POST /api/install/import` 使用 `multipart/form-data`：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `file` | 是 | `.tar.gz` 备份文件，最大 512 MB |
| `path` | 否 | 目标 SQLite 路径；留空使用当前安装器配置的默认路径 |

支持两种归档：

- 本面板导出的 `mikvace-*.tar.gz`：读取其中的 `panel.db`，校验核心表后暂存，并同时恢复 `cert.key`（若存在）以解密证书私钥；
- CELERITY-panel 的 mongodump（`celerity-backup-*.tar.gz`）：会转换为新的 SQLite 数据库，返回 `source: "celerity"` 以及 `summary` / `warnings`。转换出的数据库必须包含可用管理员账号。

成功响应示例：

```json
{
  "ok": true,
  "restartRequired": true,
  "source": "mikvace",
  "path": "/app/data/panel.db",
  "summary": { "users": 12, "groups": 3, "nodes": 4, "admins": 1, "apiKeys": 2 },
  "warnings": [],
  "hasAdmin": true,
  "adminEntry": "Ab3xY9_q",
  "message": "备份已导入：12 用户 / 4 节点 / 3 分组 / 1 管理员。重启后请使用备份中的管理员账号登录。"
}
```

数据库暂存为 `<path>.restore`，`panel.yaml` 写入 `database.driver: sqlite` 与目标路径，随后自动重启；下次启动时替换数据库，原文件保留为 `.pre-restore-<时间>`。备份中没有管理员时 `hasAdmin` 为 `false`，重启后安装页会继续要求创建管理员；已有管理员时接口返回 `409`。导入过程与保存数据库配置、创建管理员共用同一把安装锁，并按 IP 限速。

初始化完成后，`/install` 页面按 404 处理（前端路由直接渲染 NotFound），这是预期行为，不是故障。

### POST /api/auth

```json
{ "addr": "1.2.3.4:5678", "auth": "userId:password", "tx": 0 }
```

节点配置里会带 `?node=<id>`，用于把面板侧的认证拒绝写入该节点的日志流。`node` 必须是已存在节点；失败日志按节点限速（每分钟最多 120 条），避免公开端点被用于刷库。

成功：

```json
{ "ok": true, "id": "userId" }
```

失败（用户不存在、密码错误、禁用、过期、超流量、设备超限）始终返回 HTTP 200 和：

```json
{ "ok": false }
```

设备限制：

1. 用户 `maxDevices > 0` 时优先使用；
2. 为 `0` 时取用户所有分组中大于 0 的最小值；
3. `-1` 或最终为 `0` 表示不限制；
4. 以唯一 IP 计数，超过 `device_grace_minutes` 的 IP 会被忘记。

### GET /api/files/:token

格式选择：

1. `?format=clash|yaml` → Clash YAML
2. `?format=singbox|sing-box|json` → sing-box JSON
3. `?format=uri|raw` → `hysteria2://` 列表
4. `?format=shadowrocket` → Base64 URI 列表
5. `?format=xray-json|v2ray-json` → Xray JSON
6. 未指定时按 User-Agent 自动检测；浏览器（Accept: text/html 且 UA 为浏览器）返回 HTML 页面。

User-Agent 检测顺序（与 CELERITY 一致）：

```
shadowrocket
happ / incy        -> uri
hiddify / sing-box / nekobox / nekoray / neko / sfi / sfa / sfm / sft / karing -> singbox
clash / stash / surge / loon -> clash
其他 -> uri
```

响应头：

```
Content-Type: text/yaml | application/json | text/plain
Content-Disposition: attachment; filename="<username>"; filename*=UTF-8''...
Profile-Title: base64:<base64(title)>
Profile-Update-Interval: 12
Subscription-Userinfo: upload=<tx>; download=<rx>; total=<limit>; expire=<unix>
support-url: <ascii>
profile-web-page-url: <ascii>
providerid: <HAPP provider id>
routing: happ://routing/off 或 happ://routing/onadd/<base64 profile>
announce: <HAPP 公告，非 ASCII 时 base64:...>
X-Subscription-Format: clash|singbox|uri|shadowrocket|xray-json
Cache-Control: no-store
```

HAPP/Incy 请求还会把 routing 指令前置到 URI 订阅正文。

账号无效时返回 `403`，纯文本 `# Expired` / `# Inactive` / `# Traffic exceeded`；没有可用节点返回 `503 # No servers available`。

订阅节点选择规则：

1. 用户配置了直接节点（`nodeIds`）时只使用这些节点；
2. 否则使用“用户分组内的节点”+“未加入任何分组的公共节点”；
3. 用户没有任何分组时只使用公共节点。

### GET /api/files/info/:token

```json
{
  "enabled": true,
  "groups": [{ "id": 1, "name": "Europe", "subscriptionTitle": "EU" }],
  "traffic": { "used": 123456, "limit": 5368709120 },
  "expire": "2027-01-01T00:00:00Z",
  "servers": 3,
  "userId": "alice",
  "username": "Alice"
}
```

---

## 管理接口

### 会话

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/me` | 当前管理员、创建/最近登录时间、版本、默认密码警告 |
| PUT | `/api/admin/account` | `{username,currentPassword,code}`，修改登录用户名；启用 2FA 时 `code` 必填；返回新 token |
| POST | `/api/change-password` | `{oldPassword,newPassword,code}`，新密码至少 8 位；启用 2FA 时 `code` 必填；返回新 token |
| GET | `/api/2fa` | 2FA 状态与剩余恢复码数量 |
| POST | `/api/2fa/setup` | 生成 TOTP 密钥、二维码与恢复码（尚未启用） |
| POST | `/api/2fa/enable` | `{code}` 确认启用 |
| POST | `/api/2fa/disable` | `{password, code}` 关闭 2FA |
| POST | `/api/2fa/recovery` | `{password, code}` 重新生成恢复码 |

### 用户

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/users?q=&status=&page=&pageSize=` | 列表，`status` 可为 `active/disabled/expired` |
| GET | `/api/users/:id` | 详情 |
| GET | `/api/users/:id/connections` | 该用户的订阅、节点与连接 URI（管理端查看） |
| POST | `/api/users` | 创建 |
| PUT | `/api/users/:id` | 更新 |
| DELETE | `/api/users/:id` | 删除 |
| POST | `/api/users/:id/enable` | 启用 |
| POST | `/api/users/:id/disable` | 禁用 |
| POST | `/api/users/:id/reset-subscription` | 重新生成订阅 token |
| POST | `/api/users/:id/reset-password` | 重置 Hy2 密码（空值自动生成） |
| POST | `/api/users/:id/reset-devices` | 清空设备 IP 记录 |
| POST | `/api/users/:id/reset-traffic` | 将用户上下行流量计数清零 |

创建/更新请求：

```json
{
  "userId": "alice",
  "username": "Alice",
  "password": "optional",
  "enabled": true,
  "trafficLimit": 0,
  "trafficLimitGb": 10,
  "maxDevices": 2,
  "expireAt": "2027-01-01T00:00:00Z",
  "groupIds": [1],
  "nodeIds": []
}
```

- `trafficLimit` 单位为字节，`trafficLimitGb` 为 GB 便捷字段；两者同时存在时以字节字段优先。
- 创建时密码为空会自动生成；响应顶层包含一次性的 `password`（连接密码）。
- 更新时 `password` 留空表示保持当前连接密码不变。
- `subscriptionToken` 可由管理员指定，6–128 位字母、数字、下划线或短横线；修改后旧订阅链接立即失效。
- 响应包含 `trafficUsed`、`online`、`onlineDevices`、`subscriptionUrl` 等派生字段。

### 分组

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/groups` | 列表（含 nodeCount/userCount） |
| POST | `/api/groups` | 创建 |
| PUT | `/api/groups/:id` | 更新 |
| DELETE | `/api/groups/:id` | 删除 |

```json
{ "name": "Europe", "description": "", "color": "#38bdf8", "subscriptionTitle": "MikvAce EU", "maxDevices": 3 }
```

### 节点

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/nodes` | 列表（含 latest metrics、connected） |
| GET | `/api/nodes/:id` | 详情 |
| POST | `/api/nodes` | 创建，返回安装命令 |
| POST | `/api/nodes/reorder` | `{ids:[...]}` 按数组顺序重写排序权重（拖动排序） |
| PUT | `/api/nodes/:id` | 更新并尝试下发新配置 |
| DELETE | `/api/nodes/:id` | 删除并断开 Agent |
| POST | `/api/nodes/:id/token` | 重置 Agent Token |
| POST | `/api/nodes/:id/sync` | 生成/保存配置，在线则推送 |
| POST | `/api/nodes/:id/command` | `{action:"start"|"stop"|"restart"|"restart-node"|"sync"}` |
| POST | `/api/nodes/:id/restart` | 重启整节点：在线时经 Agent 重启 Agent 与核心，离线时用已保存的 SSH 凭据重启容器/服务 |
| GET | `/api/nodes/:id/config` | 最新配置：`configs`（每个核心一份）、`cores`、revision、hash、appliedAt |
| GET | `/api/nodes/:id/install` | Token、Docker 命令、安装脚本、compose |
| POST | `/api/nodes/:id/ssh/install` | 通过 SSH 执行节点安装脚本，返回输出 |
| GET | `/api/nodes/:id/cert` | 证书状态、指纹、有效期、手动 TXT 记录 |
| POST | `/api/nodes/:id/cert/request` | 通知 Agent 重新提交 CSR（立即续期） |
| POST | `/api/nodes/:id/cert/upload` | 上传证书链与私钥（需 `tlsMode=managed`） |
| POST | `/api/nodes/:id/cert/confirm` | 手动 TXT 已添加，继续签发 |
| POST | `/api/nodes/:id/cert/cancel` | 取消进行中的签发 |
| GET | `/api/certs` | 所有节点的证书状态汇总 |
| GET/PUT | `/api/cert/settings` | ACME CA、邮箱、EAB 凭据、DNS 服务商与凭据 |
| POST | `/api/sync` | 同步所有节点 |

节点请求字段见 [node-agent.md](node-agent.md)。其中 `runtimeMode` 可选 `process`（默认）或 `docker`，会影响 `/install` 返回的 `installCommand`、`installScript` 与 `compose`；Agent 实际回报的运行方式在只读字段 `runtime` 中。`autoRestart`（默认 `true`）控制该节点是否参与面板的异常自动重启；全局开关与阈值在设置接口的 `node_auto_restart_*` 项中。

只读版本字段 `agentVersion`、`hy2Version`、`xrayVersion`、`os`、`arch` 来自 Agent 的 `hello`；`heartbeat` 也会携带 `version/hy2Version/xrayVersion/runtime`，因此替换核心二进制后无需重连，面板会在下一次心跳刷新核心版本（Agent 自身版本仍需 Agent 进程重启后重新上报）。

Hysteria 2 扩展选项放在请求体的 `hy2` 对象中（证书私钥等敏感字段不会出现在响应里）：

| 字段 | 说明 |
| --- | --- |
| `quic.*` | `initStreamReceiveWindow`、`maxStreamReceiveWindow`、`initConnReceiveWindow`、`maxConnReceiveWindow`、`maxIdleTimeout`、`maxIncomingStreams`、`disablePathMTUDiscovery`、`disableStatelessReset` |
| `congestion.type` / `bbrProfile` | `bbr` 或 `reno`，BBR 预设 `standard/conservative/aggressive`（2.8+） |
| `resolver` | `type`（udp/tcp/tls/https）、`addr`、`timeout`、`sni`、`insecure` |
| `sniff` | `enable`、`timeout`、`rewriteDomain`、`tcpPorts`、`udpPorts` |
| `masquerade` | `type`（proxy/file/string）及对应子字段，另有 `listenHttp`、`listenHttps`、`forceHttps` |
| `acme` | `ca`、`listenHost`、`type`（http/tls/dns）、`httpPort`、`tlsPort`、`dnsName`、`dnsConfig` |
| `tls.sniGuard` | `strict` / `dns-san` / `disable` |
| `direct` | `mode`、`bindIPv4`、`bindIPv6`、`bindDevice`、`fastOpen` |
| `realm` | Realms 模式与 STUN/打洞调优（2.9+，与端口跳跃互斥） |
| `mimic` | `enabled`、`interface`、`xdpMode`、`path`、`extraArgs`（2.12+，与端口跳跃互斥） |
| `listenPortRange` | Hysteria 内置 UDP 端口范围监听（2.8+，与 Agent 端口跳跃互斥） |
| `includeTls` / `includeHopping` | 是否在订阅中输出 TLS / Hopping 入口，缺省 `true`；至少保留一个，仅保留 Hopping 时需要配置端口范围 |
| `trafficListen` | `trafficStats.listen` 主机部分 |
| `ignoreClientBandwidth` / `disableLossCompensation` / `disableUDP` / `udpIdleTimeout` / `speedTest` | 服务端限速与 UDP 行为 |
| `geckoMinPacketSize` / `geckoMaxPacketSize` | Gecko 混淆分片大小（2.9.2+） |
| `aclFile` / `geoip` / `geosite` / `geoUpdateInterval` | 文件式 ACL 与 Geo 数据库 |
| `clientCA` | mTLS 客户端 CA（PEM），不能与节点自签 ACME 同用 |
| `extraYaml` | 深合并到生成配置；禁止覆盖 `listen/auth/trafficStats/tls/acme/ech/realm/mimic` |
| `echPem`（顶层） | `hysteria ech` 生成的完整 `ech.pem`，只写不读；`clearEch: true` 可清除 |

Xray 扩展选项放在请求体的 `xray` 对象中：

| 字段 | 说明 |
| --- | --- |
| `apiPort` | StatsService 本地端口，默认 `61000`，只监听 `127.0.0.1` |
| `logLevel` | `debug` / `info` / `warning` / `error` / `none`，默认 `warning` |
| `routeStrategy` | `IPIfNonMatch` / `IPOnDemand` / `AsIs`，默认 `IPIfNonMatch` |
| `sniffing.enabled` | 是否启用协议嗅探 |
| `sniffing.routeOnly` | 嗅探结果是否只用于路由 |
| `sniffing.destOverride` | 嗅探目标，可选 `http`、`tls`、`quic` |
| `inbounds` | 入站数组；协议、端口、传输、安全与 REALITY/TLS 参数，见 [node-agent.md](node-agent.md) |


### 备份

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/backups` | 备份列表和自动备份设置 |
| POST | `/api/backups` | 立即创建备份 |
| GET | `/api/backups/:name` | 下载备份 |
| DELETE | `/api/backups/:name` | 删除备份 |
| POST | `/api/backups/restore` | multipart 字段 `file`，上传后需重启面板生效 |

备份为 `tar.gz`，包含一致性快照 `panel.db`、`meta.json` 和加密密钥文件（若存在）。恢复文件会先校验 SQLite 表结构，再暂存为 `panel.db.restore`，下次启动时替换当前数据库。

恢复接口也兼容 CELERITY-panel 的 mongodump 备份（`celerity-backup-*.tar.gz`）。识别到 `hyusers.bson.gz`、`hynodes.bson.gz`、`servergroups.bson.gz` 等集合后，会转换为新的 SQLite 数据库并返回 `source: "celerity"` 以及 `summary`（用户/节点/分组/管理员数量）和 `warnings`。导入同样需要重启面板生效。

### 日志

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/logs?page=&pageSize=&nodeId=&level=&event=&q=&from=&to=` | 分页日志，`from/to` RFC3339 |
| GET | `/api/logs/export?format=log\|csv\|jsonl&nodeId=&level=&event=&q=&from=&to=` | 按当前筛选条件导出日志文件（同名筛选参数；最多 10 万行） |
| GET | `/api/logs/summary?range=24h&nodeId=` | 聚合分析，`range` 支持 `1h/6h/24h/7d/30d` |
| GET | `/api/logs/options` | 节点、级别、事件选项 |
| DELETE | `/api/logs?before=RFC3339` | 删除指定时间之前的日志；不带参数删除全部 |

`summary` 返回：

```json
{
  "from": "...", "to": "...", "generatedAt": "...",
  "total": 0, "errors": 0, "warnings": 0,
  "authSuccess": 0, "authFailures": 0, "online": 0,
  "levels": [{ "name": "INFO", "count": 10 }],
  "events": [{ "name": "connect", "count": 3 }],
  "timeline": [{ "time": "...", "total": 0, "errors": 0, "authFailures": 0 }],
  "topIps": [{ "ip": "...", "count": 0, "failures": 0, "lastSeen": "..." }],
  "topUsers": [{ "userId": "...", "count": 0, "tx": 0, "rx": 0, "lastSeen": "..." }],
  "nodes": [{ "nodeId": 1, "nodeName": "...", "total": 0, "errors": 0, "warnings": 0, "authFailures": 0 }],
  "findings": [{ "id": "...", "severity": "critical", "title": "...", "description": "...", "count": 3, "nodeName": "...", "lastSeen": "..." }]
}
```

### 数据清理

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/maintenance/usage` | 数据库大小、WAL 大小、空闲页（`freeBytes`/`freelistPages`/`liveBytes`/`autoVacuum`）和各遥测表行数 |
| GET | `/api/infra` | 日志库、Redis 与缓存状态（仅管理员会话） |
| POST | `/api/infra/test` | 测试 PostgreSQL / Redis 连接（仅管理员会话） |
| PUT | `/api/infra` | 保存 PostgreSQL / Redis 配置并重启（仅管理员会话） |
| POST | `/api/maintenance/cleanup` | 立即清理过期数据；请求体 `{"vacuum":true,"logRetentionDays":14,"metricRetentionDays":30}`，两个天数可省略以使用设置值 |

清理会删除超过保留期的 `hy2_logs`、`log_batches`、`events`、`stats_batches` 与 `node_metrics`。配置 `telemetry.driver: postgres` 时，前两张表位于 PostgreSQL，`DELETE /api/logs` 与清理接口会改写外部日志库；PostgreSQL 使用分区删除与自动 vacuum，SQLite 的 `reclaimedPages`/`freeBytes` 只描述控制库。`DELETE` 只把数据页放回 freelist，文件要靠后续回收才会缩小：`vacuum: true` 在清理后立即执行 `VACUUM`（完整重写，可能短暂阻塞写入）；未勾选时执行增量回收（`PRAGMA incremental_vacuum`），若空闲空间超过 64 MiB 或占文件 25% 以上则自动改为 `VACUUM` 并切换到增量自动回收模式。响应中的 `reclaimedPages`、`freeBytes` 表示本次回收的页数与剩余空闲字节。

`DELETE /api/logs?before=...` 删除日志后也会自动执行同样的回收，并在响应中返回 `reclaimedPages`/`freeBytes`（回收失败时为 `maintenanceError`，日志删除本身仍然成功）。

```json
GET /api/maintenance/usage
{
  "databaseBytes": 121114624, "walBytes": 395552,
  "liveBytes": 15728640, "freeBytes": 105385984,
  "freelistPages": 25729, "pageSize": 4096, "autoVacuum": 0,
  "logs": 0, "logBatches": 0, "events": 12, "statsBatches": 91209, "nodeMetrics": 0,
  "logRetentionDays": 14, "metricRetentionDays": 30,
  "telemetryDriver": "sqlite", "telemetryConfigured": "sqlite", "telemetryFallback": false,
  "telemetryBytes": 0, "telemetryRows": 0, "telemetryPartitions": 0, "telemetryOk": true,
  "cache": { "backend": "redis", "entries": 4, "hits": 18, "misses": 3,
             "sets": 3, "deletes": 0, "errors": 0, "hitRate": 0.857 }
}
```

### 存储与缓存

仅管理员会话可调用；API Key 会收到 `403`。

```json
GET /api/infra
{
  "telemetry": {
    "driver": "postgres", "activeDriver": "postgres", "schema": "mikvace",
    "required": false, "configured": true, "ok": true, "fallback": false,
    "stats": { "driver": "postgres", "bytes": 24576, "rows": 1200, "partitions": 3, "detail": "mikvace" }
  },
  "redis": { "enabled": true, "prefix": "mikvace:", "required": false, "configured": true, "ok": true },
  "cache": { "backend": "redis", "entries": 4, "hits": 18, "misses": 3, "hitRate": 0.857 },
  "dsnTemplates": {
    "postgres": [{ "id": "postgres-local", "label": "本地 / 内网", "value": "postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=disable" }],
    "redis": [{ "id": "redis-password", "label": "密码", "value": "redis://:PASSWORD@HOST:6379/0" }]
  }
}
```

```json
POST /api/infra/test
PUT  /api/infra
{
  "telemetry": {
    "enabled": true,
    "dsn": "postgres://panel:***@db:5432/mikvace",
    "schema": "mikvace",
    "required": false
  },
  "redis": {
    "enabled": true,
    "url": "redis://:***@cache:6379/0",
    "prefix": "mikvace:",
    "required": false
  }
}
```

- 密钥留空表示复用当前已保存的 DSN / Redis URL；
- `required: false` 的后端测试失败只告警，保存后启动时回退（日志回退 SQLite，缓存回退内存）；
- 保存成功返回 `{"ok":true,"telemetry":"postgres","redis":true,"restartRequired":true}`，DSN / URL 写入 `server.data_dir` 下的 0600 文件。

### 仪表盘 / 设置

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/dashboard` | 统计、节点、24h 流量、最近日志、告警 |
| GET | `/api/events?nodeId=&limit=` | 面板事件 |
| POST | `/api/sync` | 同步所有节点，返回 `{nodes,pushed}` |
| GET | `/api/settings` | 全部设置 + 版本/数据目录 |
| PUT | `/api/settings` | 更新白名单设置项 |

### API Key

管理端接口也支持 `X-API-Key: ck_...` 或 `Authorization: Bearer ck_...`。API Key 只能在 **Settings → Security → API keys** 中创建，明文仅在创建时返回一次；令牌可随时轮换，旧令牌立即失效。

API Key 采用「权限 + 资源范围」双重控制：

- **权限**按资源细分，未列出的路由默认拒绝；`GET /api/me` 对任意有效密钥开放，用于自检。
- **资源范围**可限定到节点和/或分组。留空表示全部资源。
- 限定资源后，节点、分组、用户、证书列表会自动过滤；访问范围外的单个资源返回 `403`。仪表盘、事件、设置、备份、维护与全局同步等无法安全过滤的全局接口也会返回 `403`。

细分权限：

| scope | 允许 |
| --- | --- |
| `users:read` | 用户列表、详情、连接信息 |
| `users:write` | 创建、更新、启停、重置密码 / 订阅 / 设备 / 流量 |
| `users:delete` | 删除用户 |
| `groups:read` / `groups:write` / `groups:delete` | 分组查询 / 创建更新 / 删除 |
| `nodes:read` | 节点列表、状态与指标 |
| `nodes:write` | 创建、更新节点与排序 |
| `nodes:operate` | 同步、重启、重置 token、SSH 安装 |
| `nodes:secrets` | 读取节点配置、安装脚本、证书（敏感） |
| `nodes:delete` | 删除节点 |
| `certs:read` / `certs:write` | 查看证书 / 修改 ACME 设置与签发证书 |
| `dashboard:read` | 仪表盘与事件 |
| `logs:read` / `logs:delete` | 日志查询与导出 / 删除日志 |
| `maintenance:read` / `maintenance:write` | 数据用量 / 清理遥测 |
| `backups:read` / `backups:write` | 查看下载备份 / 创建、删除、恢复备份 |
| `settings:read` / `settings:write` | 查看 / 修改面板设置（敏感） |
| `sync:write` | `POST /api/sync`，以及节点同步 |

兼容作用域：升级时会自动把旧密钥的 `users:write`、`nodes:write` 展开为对应的细分权限（保留原有删除、操作与凭据读取能力）；`stats:read` 仍然有效，等价于仪表盘、日志与分组读取。

管理接口（仅 JWT）：

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/api-keys` | 列表（不返回明文），同时返回权限目录与可选节点/分组 |
| POST | `/api/api-keys` | `{name,description,scopes:[],nodeIds:[],groupIds:[],rateLimit,expiresAt}`，返回一次性 `token` |
| PUT | `/api/api-keys/:id` | 更新名称、说明、权限、资源范围、限流与到期时间 |
| POST | `/api/api-keys/:id/rotate` | 轮换令牌，返回一次性新 `token` |
| DELETE | `/api/api-keys/:id` | 删除 |
| POST | `/api/api-keys/:id/enable` | 启用 |
| POST | `/api/api-keys/:id/disable` | 禁用 |

`/api/api-keys`、`/api/change-password`、2FA 管理接口只接受 JWT，API Key 无法访问。

每个 Key 有独立的每分钟请求上限（默认 60），列表中的 `requestCount`、`lastUsedIp`、`lastUsedAt` 可用于审计。响应包含：

```
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 58
```

超限返回 `429` 和 `Retry-After: 60`。

可写设置项：`panel_name`、`public_url`、`logo_url`、`page_title`、`page_note`、`support_url`、`web_page_url`、`subscription_title`、`subscription_update_interval`、`subscription_buttons`、`subscription_hy2_endpoints`、`device_grace_minutes`、`log_retention_days`、`metric_retention_days`、`log_ingest_enabled`、`analysis_window_hours`、`alert_auth_fail_threshold`、`alert_error_burst_threshold`、`node_auto_restart_enabled`、`node_auto_restart_grace_seconds`、`node_auto_restart_cooldown_seconds`、`node_auto_restart_max_attempts`。

节点自动恢复：面板发现核心“应运行但已停止”超过阈值时下发 `restart`；Agent 心跳超过阈值未上报且节点启用了 SSH 凭据时，使用 SSH 重启 `mikvace-node` 容器或 systemd 服务。每个节点受冷却时间与每小时尝试次数限制，手停的核心不会被自动拉起。

`subscription_hy2_endpoints` 取值为 `both`、`tls` 或 `hopping`，仅作为新建节点时 `includeTls` / `includeHopping` 的初始值；已有节点的订阅输出始终以节点自身的开关为准。

修改 `public_url` 后会自动重新生成并推送所有节点配置。

---

## Agent WebSocket 协议

连接：

```
GET /api/node/ws
Authorization: Bearer <node token>
```

消息统一为：

```json
{ "type": "...", "payload": { ... } }
```

### Agent → 面板

| type | payload |
| --- | --- |
| `hello` | `version, os, arch, hy2Version, xrayVersion, cores, runtime, onlineUsers, capabilities` |
| `heartbeat` | `cpuPct, memUsed, memTotal, diskUsed, diskTotal, load1, uptimeSec, online, trafficTx, trafficRx, hy2Running, coresRunning, coresDesired, version, hy2Version, xrayVersion, runtime` |
| `stats` | `batchId, users {userId:{tx,rx}}, online {userId:count}, trafficTx, trafficRx` |
| `logs` | `batchId, entries [{ts,level,message,raw}]` |
| `config.ack` | `revision, ok, pending, error` |
| `cert.request` | `requestId, reason, domains, csrPEM, keyFingerprint` |
| `cert.ack` | `requestId, revision, ok, fingerprint, error` |
| `command.ack` | `id, action, ok, output` |
| `pong` | `{ts}` |

`trafficTx/Rx` 是 Agent 自启动以来的累计值；面板计算增量并保存，因此 Agent 重启不会破坏面板累计流量。

### 面板 → Agent

| type | payload |
| --- | --- |
| `config` | `revision, yaml, tlsMode, domain, host, port, portRange, autoRestart` |
| `cert.push` | `requestId, revision, domains, notBefore, notAfter, fingerprint, certPEM, keyPEM` |
| `cert.error` | `requestId, error` |
| `cert.renew` | `reason` |
| `command` | `id, action: start/stop/restart/restart-node` |
| `log.ack` | `batchId` |
| `stats.ack` | `batchId` |
| `ping` | `{ts}` |

`logs` / `stats` 使用 batch id 幂等处理；Agent 在 10 秒内没收到 ack 会重发，重连后也会重发未确认批次。

订阅侧说明：

- `echConfig` 会写入 `hysteria2://` 分享链接的 `ech` 参数；Clash/sing-box 输出暂不注入 ECH，客户端仍可正常连接（服务端向后兼容未启用 ECH 的客户端）。
- `realm` 与 `mimic` 模式无法用分享链接表达，这类节点不会出现在订阅中，需要客户端手动配置。
- `clientCA`（mTLS）只负责服务端校验，客户端证书需另行签发和分发。
