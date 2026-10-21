# API 参考

Base URL：`https://panel.example.com/api`

管理端接口使用 JWT。

### 登录

```http
POST /api/login
Content-Type: application/json

{"username":"admin","password":"..."}
```

未启用 2FA：

```json
{"token":"...","expiresAt":"...","admin":{"id":1,"username":"admin"}}
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

---

## 公共接口

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查、版本、uptime、Agent Hub 状态 |
| GET | `/api/agent/version` | Agent 可用版本信息 |
| GET | `/api/agent/install.sh` | 节点安装脚本（通过参数传 panel/token） |
| POST | `/api/auth` | Hysteria 2 外部认证 |
| GET | `/api/files/:token` | 用户订阅 |
| GET | `/api/files/info/:token` | 订阅信息 |
| GET | `/api/node/ws` | 节点 Agent 控制通道（WebSocket） |

### POST /api/auth

```json
{ "addr": "1.2.3.4:5678", "auth": "userId:password", "tx": 0 }
```

节点配置里会带 `?node=<id>`，用于把面板侧的认证拒绝写入该节点的日志流。

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
| GET | `/api/me` | 当前管理员、版本、默认密码警告 |
| POST | `/api/change-password` | `{oldPassword,newPassword}` |
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
- 创建时密码为空会自动生成；响应顶层包含一次性的 `password`。
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
| GET | `/api/nodes/:id/config` | 最新 YAML、revision、hash、appliedAt |
| GET | `/api/nodes/:id/install` | Token、Docker 命令、安装脚本、compose |
| POST | `/api/nodes/:id/ssh/install` | 通过 SSH 执行节点安装脚本，返回输出 |
| GET | `/api/nodes/:id/cert` | 证书状态、指纹、有效期、手动 TXT 记录 |
| POST | `/api/nodes/:id/cert/request` | 通知 Agent 重新提交 CSR（立即续期） |
| POST | `/api/nodes/:id/cert/upload` | 上传证书链与私钥（需 `tlsMode=managed`） |
| POST | `/api/nodes/:id/cert/confirm` | 手动 TXT 已添加，继续签发 |
| POST | `/api/nodes/:id/cert/cancel` | 取消进行中的签发 |
| GET | `/api/certs` | 所有节点的证书状态汇总 |
| GET/PUT | `/api/cert/settings` | ACME CA、邮箱、DNS 服务商与凭据 |
| POST | `/api/sync` | 同步所有节点 |

节点请求字段见 [node-agent.md](node-agent.md)。

### 备份

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/backups` | 备份列表和自动备份设置 |
| POST | `/api/backups` | 立即创建备份 |
| GET | `/api/backups/:name` | 下载备份 |
| DELETE | `/api/backups/:name` | 删除备份 |
| POST | `/api/backups/restore` | multipart 字段 `file`，上传后需重启面板生效 |

备份为 `tar.gz`，包含一致性快照 `panel.db` 和 `meta.json`。恢复文件会先校验 SQLite 表结构，再暂存为 `panel.db.restore`，下次启动时替换当前数据库。

恢复接口也兼容 CELERITY-panel 的 mongodump 备份（`celerity-backup-*.tar.gz`）。识别到 `hyusers.bson.gz`、`hynodes.bson.gz`、`servergroups.bson.gz` 等集合后，会转换为新的 SQLite 数据库并返回 `source: "celerity"` 以及 `summary`（用户/节点/分组/管理员数量）和 `warnings`。导入同样需要重启面板生效。

### 日志

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/logs?page=&pageSize=&nodeId=&level=&event=&q=&from=&to=` | 分页日志，`from/to` RFC3339 |
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

可写设置项：`panel_name`、`public_url`、`logo_url`、`page_title`、`page_note`、`support_url`、`web_page_url`、`subscription_title`、`subscription_update_interval`、`subscription_buttons`、`device_grace_minutes`、`log_retention_days`、`log_ingest_enabled`、`analysis_window_hours`、`alert_auth_fail_threshold`、`alert_error_burst_threshold`。

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
| `hello` | `version, os, arch, hy2Version, runtime, onlineUsers` |
| `heartbeat` | `cpuPct, memUsed, memTotal, diskUsed, diskTotal, load1, uptimeSec, online, trafficTx, trafficRx, hy2Running` |
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
