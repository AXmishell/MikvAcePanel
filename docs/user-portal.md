# 用户中心

用户中心（User Portal）是面向普通订阅用户的自助页面。管理员仍在 `/admin` 登录控制台，终端用户则通过 `/user` 查看自己的订阅、流量、到期时间与设备状态。

用户中心与订阅落地页的区别：

| | 订阅落地页 | 用户中心 |
| --- | --- | --- |
| 入口 | `/api/files/<token>` | `/user` |
| 认证 | 订阅 Token 本身 | 用户 ID + 密码 |
| 适合 | 已拿到链接、需要导入客户端 | 需要查看状态、改密码、管理订阅 |
| 可修改 | 无 | 密码、可选订阅链接 |

## 启用与设置

管理员进入 **设置 → 用户中心**：

| 设置项 | 默认值 | 说明 |
| --- | --- | --- |
| 启用用户中心 | 开 | 关闭后 `/user` 登录返回 403，已有订阅链接不受影响 |
| 页面标题 | 用户中心 | 登录页与页面头部显示 |
| 页面说明 | 空 | 留空时复用订阅页面的说明 |
| 登录有效期 | 72 小时 | 1–720 小时，超出范围会按边界处理 |
| 修改密码 | 开 | 允许用户自助修改控制台登录密码与连接密码 |
| 重置订阅链接 | 关 | 允许用户生成新 Token，旧链接立即失效 |

入口地址会显示在设置页，格式为：

```text
https://panel.example.com/user
```

> 用户中心使用 `public_url` 生成订阅链接；如果没有配置 `public_url`，会回退到当前请求的域名。生产环境建议显式配置。

## 两个密码

每个用户有两个相互独立的密码：

| 密码 | 用途 | 存储 |
| --- | --- | --- |
| 控制台登录密码 | 登录 `/user` 用户中心 | bcrypt 哈希 |
| 连接密码 | Hysteria 认证，并嵌入订阅 URI | 保持可读，用于生成订阅和认证 |

例如：

```text
hysteria2://demo:<连接密码>@192.168.44.130:443?...
```

- 新建用户时，如果只填写连接密码，登录密码默认与连接密码相同。
- 管理员可以在 **用户 → 编辑用户** 中分别修改两个密码；留空表示保持不变。
- 用户可以登录用户中心后，在 **修改密码** 弹窗中分别修改。修改连接密码时需要输入当前登录密码，修改后已有连接不受影响，新连接和客户端需要更新密码。
- 旧版本创建的用户只有一份密码。首次用旧密码登录用户中心时，系统会自动把该密码迁移为登录密码哈希，连接密码保持不变。

## 订阅 Token

订阅 URL 中的 `/api/files/<token>` 由管理员控制：

- 管理员的用户编辑表单可以直接填写新 Token，保存后旧订阅链接立即失效；
- **重置订阅** 按钮会随机生成新 Token；
- 用户中心是否允许自助重置由设置项 **重置订阅链接** 决定，默认关闭；
- Token 只允许字母、数字、下划线和短横线，长度 6–128，且不能与其他用户重复。

## 用户视角

登录后可以看到：

- **账号状态**：正常、已停用、已过期、流量已用完；
- **流量**：已用、剩余、上限与进度条；
- **到期时间**：剩余天数或永不过期；
- **在线设备**：当前在线数、已登记设备、设备上限与最近 IP；
- **订阅连接**：订阅地址、Clash / sing-box / URI 快捷链接和二维码；
- **节点列表**：每个节点的协议与可用配置，支持逐条复制 URI；
- **自助操作**：修改密码、可选重置订阅链接。

> 设备计数只能由管理员在 **用户 → 重置设备** 中清除。如果允许用户自助重置，用户可以在每次新增设备前清空计数，从而绕过 `maxDevices` 限制，因此用户中心不提供该操作。

管理员也可以在 **用户 → 连接信息** 中查看同一份订阅链接、节点和 URI，无需使用用户密码登录。

账号被停用、过期或超流量后仍然可以登录查看原因，但订阅内容会按原有规则拒绝下发。管理员可以通过事件日志看到用户的自助操作：

```text
portal.password
portal.subscription
```

## API

用户中心使用独立的 `/api/portal` 命名空间。除 `config` 与 `login` 外，都需要 `Authorization: Bearer <portal-token>`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/portal/config` | 是否启用、面板名称、标题、说明、Logo、支持链接 |
| POST | `/api/portal/login` | 使用 `userId` + `password` 登录 |
| POST | `/api/portal/logout` | 注销当前会话 |
| GET | `/api/portal/me` | 账号、流量、设备、订阅、节点与能力开关 |
| POST | `/api/portal/password` | 修改控制台登录密码，需提供 `oldPassword` 与 `newPassword` |
| POST | `/api/portal/connection-password` | 修改连接密码，需提供 `currentPassword`（当前登录密码）与 `newPassword` |
| POST | `/api/portal/subscription/reset` | 重置订阅 Token，需管理员开放 |

登录示例：

```bash
curl -s https://panel.example.com/api/portal/login \
  -H 'Content-Type: application/json' \
  -d '{"userId":"alice","password":"secretpass"}'
```

```json
{
  "token": "<portal-jwt>",
  "expiresAt": "2026-10-26T12:00:00Z",
  "user": { "id": 1, "userId": "alice", "username": "Alice", "status": "active" }
}
```

查看状态：

```bash
curl -s https://panel.example.com/api/portal/me \
  -H "Authorization: Bearer <portal-jwt>"
```

返回结构：

```json
{
  "user": { "userId": "alice", "status": "active", "expireAt": null, "groups": [] },
  "traffic": { "used": 0, "limit": 0, "percent": 0, "unlimited": true },
  "devices": { "online": 0, "tracked": 0, "limit": 2, "items": [] },
  "subscription": { "url": "https://panel.example.com/api/files/<token>" },
  "nodes": [],
  "capabilities": { "password": true, "subscription": false },
  "branding": { "panelName": "MikvAce Panel", "title": "用户中心" }
}
```

## 安全模型

- **会话隔离**：用户中心的 JWT 带有 `purpose=portal` 与独立签发者。管理员 Token 不能访问 `/api/portal/*`，用户 Token 也不能访问 `/api/users` 等管理接口。
- **登录限流**：用户中心与管理员登录使用独立的限流计数，互不影响；默认每 IP 每分钟最多 10 次失败。
- **会话存储**：启用 Redis 时会话写入 Redis，注销后立即失效；未启用 Redis 时为无状态 JWT，过期前仍然有效。
- **密码分离**：用户中心登录使用 bcrypt 哈希的登录密码；Hysteria 认证和订阅 URI 使用连接密码。两者可以分别修改。
- **订阅 Token**：管理员可以手动指定或重置；重置后旧链接立即失效。自助重置默认关闭。
- **设备重置**：仅管理员可在用户列表中操作；它只清除服务端的唯一 IP 计数，不会主动断开现有连接。

> 用户中心不提供管理员 2FA、工单、支付或流量明细图表。流量是按账号累计的计数器，用于配额判断，不提供按小时的历史曲线。

## 反向代理与 CDN

用户中心是 SPA 路由，和面板其他页面使用同一套前端资源。只需要：

1. 把 `/user` 交给 SPA fallback（`index.html`）；
2. 把 `/api/portal/*` 转发到后端；
3. 如果前端与 API 分离部署，把 `VITE_API_BASE_URL` 指向 API 域名，并在 `CORS_ORIGINS` 中加入前端域名。

Nginx 示例：

```nginx
location /user {
    try_files $uri $uri/ /index.html;
}

location /api/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
}
```
