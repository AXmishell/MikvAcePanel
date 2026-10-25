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

安全入口默认关闭。开启后（**设置 → 安全**），`/api/login`、`/api/login/2fa` 以及所有管理员接口都需要额外的请求头：

```http
X-Admin-Entry: <入口路径>
```

缺少或校验失败返回 `404 {"error":"not found"}`。`GET /api/admin/config` 是公开探测接口，只返回是否启用、面板名称以及是否已完成初始化（不返回入口路径）：

```json
{ "securityEntryEnabled": true, "panelName": "MikvAce Panel", "installed": true }
```

API Key（`Authorization: Bearer ck_...`）不受安全入口限制。

---

## 公共接口

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查、版本、uptime、Agent Hub 状态、数据库驱动 |
| GET | `/api/install/status` | 首次初始化状态（是否已安装、面板名称、数据库驱动与配置） |
| POST | `/api/install/database/test` | 测试 SQLite / PostgreSQL / MySQL 连接；仅在管理员表为空时可用 |
| POST | `/api/install/database` | 保存数据库配置；切换后端后需要重启面板 |
| POST | `/api/install` | 创建第一个管理员；仅在管理员表为空时可用 |
| GET | `/homepage/*` | 自定义首页静态资源（沙箱 CSP） |
| GET | `/preview/homepage/:token/*` | 自定义首页短时预览 |
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
    "path": "/data/panel.db",
    "dsnFile": "",
    "dsnConfigured": false
  },
  "configPath": "/config/panel.yaml",
  "configWritable": true,
  "startedAt": "2026-10-24T09:00:00Z",
  "version": "v1.2.3"
}
```

`databaseConfig` 只描述磁盘上的配置，**不会返回 DSN 内容**。`startedAt` 在进程重启后变化，安装页用它判断数据库切换是否完成。

`setupMode: true` 表示当前进程还没有选择数据库（`database.driver: setup` 或配置文件不存在），此时不会打开或创建 SQLite；保存数据库后进程重启，`setupMode` 变为 `false`。

`POST /api/install/database/test` 与 `POST /api/install/database` 接收相同请求体：

```json
{
  "driver": "postgres",
  "path": "/data/panel.db",
  "dsn": "postgres://user:pass@db:5432/mikvace?sslmode=disable"
}
```

- SQLite 只使用 `path`，留空默认为 `data_dir/panel.db`；
- PostgreSQL / MySQL 只使用 `dsn`；若与当前驱动相同，可留空表示沿用现有 DSN；
- 测试接口只建立连接，不建表；保存接口会先连接并执行一次幂等迁移，确认建表权限后再写入配置；
- 保存时 DSN 写入 `data_dir/database.dsn`（0600），`panel.yaml` 只记录文件路径；
- 返回 `{"ok":true,"driver":"postgres","restartRequired":true}`。`restartRequired` 为 `true` 时进程会自行重启；
- 已有管理员时两个接口都返回 `409`。

`POST /api/install` 创建第一个管理员，请求体：

```json
{
  "username": "admin",
  "password": "至少 12 个字符",
  "panelName": "MikvAce Panel",
  "publicUrl": "https://panel.example.com"
}
```

成功返回与 `/api/login` 相同结构的 `token` / `expiresAt` / `admin` / `version`，前端会直接进入控制台。已有管理员时返回 `409 {"error":"panel is already installed"}`。用户名规则为 3–32 位字母、数字、点、下划线或短横线。

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

## 用户中心

终端用户自助接口，完整说明见 [用户中心](user-portal.md)。

| Method | Path | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/portal/config` | 无 | 是否启用及页面品牌信息 |
| POST | `/api/portal/login` | 无 | `{ "userId": "...", "password": "..." }` |
| POST | `/api/portal/logout` | Portal JWT | 注销会话 |
| GET | `/api/portal/me` | Portal JWT | 账号、流量、设备、订阅与节点 |
| POST | `/api/portal/password` | Portal JWT | `{ "oldPassword": "...", "newPassword": "..." }`，修改登录密码 |
| POST | `/api/portal/connection-password` | Portal JWT | `{ "currentPassword": "...", "newPassword": "..." }`，修改连接密码 |
| POST | `/api/portal/subscription/reset` | Portal JWT | 重置订阅 Token（需管理员开放） |

Portal JWT 带有 `purpose=portal`，不能访问管理接口；管理员 JWT 也不能访问 `/api/portal/*`。

---

## 管理接口

### 会话

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/me` | 当前管理员、创建/最近登录时间、版本、默认密码警告 |
| PUT | `/api/admin/account` | `{username,currentPassword,code}`，修改登录用户名；启用 2FA 时 `code` 必填；返回新 token |
| POST | `/api/change-password` | `{oldPassword,newPassword,code}`，新密码至少 12 位；启用 2FA 时 `code` 必填；返回新 token |
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
- 创建时密码为空会自动生成；响应顶层包含一次性的 `password`（连接密码）和 `loginPassword`（控制台登录密码）。
- 用户密码分为 `password`（连接密码）与 `loginPassword`（登录密码）；更新时留空表示保持不变。
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
| PUT | `/api/nodes/:id` | 更新并尝试下发新配置 |
| DELETE | `/api/nodes/:id` | 删除并断开 Agent |
| POST | `/api/nodes/:id/token` | 重置 Agent Token |
| POST | `/api/nodes/:id/sync` | 生成/保存配置，在线则推送 |
| POST | `/api/nodes/:id/command` | `{action:"start"|"stop"|"restart"|"sync"}` |
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

节点请求字段见 [node-agent.md](node-agent.md)。其中 `runtimeMode` 可选 `process`（默认）或 `docker`，会影响 `/install` 返回的 `installCommand`、`installScript` 与 `compose`；Agent 实际回报的运行方式在只读字段 `runtime` 中。

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
| GET | `/api/homepage` | 自定义首页模式、启用版本与版本列表 |
| POST | `/api/homepage/upload` | 上传 `.html` / `.zip` 首页（仅暂存） |
| POST | `/api/homepage/activate` | 启用指定首页版本 |
| POST | `/api/homepage/preview` | 生成 10 分钟有效的预览链接 |
| PUT | `/api/homepage/mode` | 切换 `builtin` / `upload` |
| DELETE | `/api/homepage/versions/:version` | 删除非当前首页版本 |
| DELETE | `/api/homepage` | 恢复内置首页 |
| GET | `/api/backups` | 备份列表和自动备份设置 |
| POST | `/api/backups` | 立即创建备份 |
| GET | `/api/backups/:name` | 下载备份 |
| DELETE | `/api/backups/:name` | 删除备份 |
| POST | `/api/backups/restore` | multipart 字段 `file`，上传后需重启面板生效 |

备份为 `tar.gz`，包含一致性快照 `panel.db`、`meta.json`，以及当前选择的自定义首页版本（若存在）。恢复文件会先校验 SQLite 表结构，再暂存为 `panel.db.restore`，下次启动时替换当前数据库；自定义首页文件同时在下次启动时换入。

恢复接口也兼容 CELERITY-panel 的 mongodump 备份（`celerity-backup-*.tar.gz`）。识别到 `hyusers.bson.gz`、`hynodes.bson.gz`、`servergroups.bson.gz` 等集合后，会转换为新的 SQLite 数据库并返回 `source: "celerity"` 以及 `summary`（用户/节点/分组/管理员数量）和 `warnings`。导入同样需要重启面板生效。

### 自定义首页

`POST /api/homepage/upload` 使用 multipart 字段 `file`，支持 `.html`、`.htm` 或 `.zip`。上传只会暂存版本，不会立即替换 `/`。

```json
// POST /api/homepage/activate
{ "version": "20261024-173000-a1b2c3" }

// PUT /api/homepage/mode
{ "mode": "upload" }

// POST /api/homepage/preview
{ "version": "20261024-173000-a1b2c3" }
```

预览返回：

```json
{
  "url": "/preview/homepage/<token>/index.html",
  "expiresAt": "2026-10-24T09:30:00Z"
}
```

自定义页面通过 `/` 的沙箱 iframe 加载，资源位于 `/homepage/*`，响应包含 `Content-Security-Policy: sandbox ...`。上传限制、ZIP 校验和备份行为见 [自定义首页](homepage.md)。

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

### 仪表盘 / 设置

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/dashboard` | 统计、节点、24h 流量、最近日志、告警 |
| GET | `/api/events?nodeId=&limit=` | 面板事件 |
| POST | `/api/sync` | 同步所有节点，返回 `{nodes,pushed}` |
| GET | `/api/settings` | 全部设置 + 版本/数据目录 |
| PUT | `/api/settings` | 更新白名单设置项 |

### API Key

管理端接口也支持 `X-API-Key: ck_...` 或 `Authorization: Bearer ck_...`。API Key 只能在 **Settings → Security → API keys** 中创建，明文仅在创建时返回一次。

作用域：

| scope | 允许 |
| --- | --- |
| `users:read` | `GET /api/users*` |
| `users:write` | 用户创建、更新、删除、启停、重置 |
| `nodes:read` | `GET /api/nodes*` |
| `nodes:write` | 节点创建、更新、删除、token、命令、配置 |
| `stats:read` | dashboard、events、logs、groups 读取 |
| `sync:write` | `POST /api/sync`，以及节点 sync |

管理接口：

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/api-keys` | 列表（不返回明文） |
| POST | `/api/api-keys` | `{name,scopes:[],rateLimit,expiresAt}`，返回一次性 `token` |
| DELETE | `/api/api-keys/:id` | 删除 |
| POST | `/api/api-keys/:id/enable` | 启用 |
| POST | `/api/api-keys/:id/disable` | 禁用 |

`/api/settings`、`/api/me`、`/api/change-password`、`/api/api-keys` 只接受 JWT，API Key 无法访问。

每个 Key 有独立的每分钟请求上限（默认 60）。响应包含：

```
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 58
```

超限返回 `429` 和 `Retry-After: 60`。

可写设置项：`panel_name`、`public_url`、`logo_url`、`page_title`、`page_note`、`support_url`、`web_page_url`、`subscription_title`、`subscription_update_interval`、`subscription_buttons`、`subscription_hy2_endpoints`、`device_grace_minutes`、`log_retention_days`、`log_ingest_enabled`、`analysis_window_hours`、`alert_auth_fail_threshold`、`alert_error_burst_threshold`。

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
| `heartbeat` | `cpuPct, memUsed, memTotal, diskUsed, diskTotal, load1, uptimeSec, online, trafficTx, trafficRx, hy2Running, coresRunning, version, hy2Version, xrayVersion, runtime` |
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
| `config` | `revision, yaml, tlsMode, domain, host, port, portRange` |
| `cert.push` | `requestId, revision, domains, notBefore, notAfter, fingerprint, certPEM, keyPEM` |
| `cert.error` | `requestId, error` |
| `cert.renew` | `reason` |
| `command` | `id, action: start/stop/restart` |
| `log.ack` | `batchId` |
| `stats.ack` | `batchId` |
| `ping` | `{ts}` |

`logs` / `stats` 使用 batch id 幂等处理；Agent 在 10 秒内没收到 ack 会重发，重连后也会重发未确认批次。

订阅侧说明：

- `echConfig` 会写入 `hysteria2://` 分享链接的 `ech` 参数；Clash/sing-box 输出暂不注入 ECH，客户端仍可正常连接（服务端向后兼容未启用 ECH 的客户端）。
- `realm` 与 `mimic` 模式无法用分享链接表达，这类节点不会出现在订阅中，需要客户端手动配置。
- `clientCA`（mTLS）只负责服务端校验，客户端证书需另行签发和分发。
