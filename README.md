# MikvAce Panel

一个面向 **Hysteria 2** 和 **Xray VLESS / REALITY** 的自托管代理管理面板。整体思路参考 [CELERITY-panel](https://github.com/ClickDevTech/CELERITY-panel)，后端使用 Go，前端使用 Vue 3 单页应用，节点端支持 Docker 镜像，通过 WebSocket 连接服务端并接受配置下发，同时把代理核心日志上报到服务端做规则化分析。

本项目主要解决的是节点容器化和证书导致的节点80端口占用问题，面板申请证书后下发到节点部署

## 文档

在线文档：**https://mikvacepanel.gpcn.cc/**

本地预览与构建：

```bash
python3 -m venv .venv && . .venv/bin/activate
pip install -r requirements-docs.txt
mkdocs serve          # http://127.0.0.1:8000
mkdocs build --strict # 输出 site/
```

文档源文件位于 `docs/`，导航与主题配置见 `mkdocs.yml`；推送到 `main` 后由 `.github/workflows/docs.yml` 自动发布到 GitHub Pages。

> 订阅接口与 CELERITY 保持同一套约定：`GET /api/files/:token`、`GET /api/files/info/:token`，按 User-Agent 自动返回 Clash / sing-box / Shadowrocket / URI 列表，并携带 `Profile-Title`、`Subscription-Userinfo` 等标准响应头。

---

## 功能

- **中文 Web 面板**：仪表盘、用户、节点、服务器组、日志分析、设置。
- **双核心节点**：同一节点可同时运行 Hysteria 2（QUIC/UDP）与 Xray（TCP），也可只运行其中一个。
- **Xray 多协议入站**：VLESS / VMess / Trojan / Shadowsocks；TCP / WebSocket / gRPC / XHTTP / mKCP；REALITY / TLS / 无加密；管理员可在新建节点时按协议开关启用，一个节点可配置多个入站。
- **订阅分发**：Clash/Mihomo YAML、sing-box JSON、Shadowrocket Base64、原始 URI 列表、Xray JSON；支持 HAPP 路由头与 providerid。
- **终端用户中心**：用户使用 ID + 登录密码进入 `/user`，查看订阅、流量、到期与设备；登录密码与连接密码相互独立，均可由用户或管理员修改；管理端可直接指定订阅 Token。管理员控制台位于 `/admin`，与用户入口完全分离。
- **Hysteria 2 认证**：节点把客户端认证请求转发到 `POST /api/auth`，支持启用/禁用、到期、流量限额、设备数限制（按唯一 IP + 宽限期）；Agent 还会持久化用户认证快照，面板故障时 Hysteria 新连接仍可继续认证。
- **节点管理**：面板生成 Hysteria / Xray 服务端配置，通过 WebSocket 实时下发；支持版本、运行状态、配置 revision、错误回报、启动/停止/重启。
- **证书统一签发**：面板通过 ACME DNS-01 申请证书，Agent 提交 CSR、安装证书链（私钥不出节点）；节点自签 ACME 只使用 TLS-ALPN，不再占用 80 端口。
- **两步验证（2FA）**：管理员可使用标准 TOTP 应用登录，提供一次性恢复码；2FA 密钥加密存储，支持自助关闭与恢复码重置。
- **节点端 Docker 镜像**：单容器内置 Hysteria 2 + Xray + Agent，开箱即用；也可在节点表单中为每个节点选择 Docker 运行时（`runtime.type: docker`），由 Agent 管理同机的代理容器。
- **SSH 自动安装**：保存 SSH 凭据后可一键远程安装/重启节点容器（可选自动安装 Docker）。
- **备份恢复**：本地 tar.gz 备份、自动备份保留策略、上传恢复（重启生效）。
- **API Key 限流**：每个密钥独立速率限制与 `X-RateLimit-*` 响应头。
- **端口跳跃**：Agent 自动维护 iptables UDP REDIRECT 规则；也可改用 Hysteria 2.8+ 内置端口范围监听。
- **Hysteria 2 深度调优**：QUIC 窗口、BBR/Reno 与 BBR 预设、带宽补偿、UDP 开关、DNS 解析器、协议嗅探、三种伪装模式、GeoIP/GeoSite ACL、直连绑定、Gecko 分片、mTLS 客户端 CA、ECH、Realms、mimic，以及受保留键保护的额外 YAML 覆盖；内置“官方默认 / 推荐均衡 / 高性能 / 抗审查 / 弱网优化”模板，新建节点默认应用推荐模板。
- **日志上报与分析**：采集 Hysteria/Xray 的 stdout/stderr 与 Xray access log，按批可靠上报；服务端解析时间、级别、事件、用户、客户端 IP、错误等字段，支持搜索、时间线、Top IP/用户、规则告警（暴力破解、未知用户、错误爆发、重启循环、TLS/ACME/REALITY 异常、配额耗尽等），并可按当前筛选条件导出 `.log` / `.csv` / `.jsonl` 文件（最多 10 万行）。
- **流量统计**：Agent 轮询 Hysteria `trafficStats` 与 Xray `StatsService`，按用户增量上报；服务端按 batch id 幂等入库，支持管理员重置用户流量。双核心节点的流量自动合并。
- **API Key**：带 `users:read/write`、`nodes:read/write`、`stats:read`、`sync:write` 作用域的自动化凭据，支持过期时间和启停。
- **单二进制部署**：Vue 构建产物通过 `go:embed` 嵌入服务端。
- **SQLite**：无需额外数据库，WAL 模式，数据默认位于 `/data`。

---

## 架构

```
                    ┌──────────────────────────────┐
   浏览器 / API ───▶│  MikvAce Panel (Go)          │
                    │  ├─ Vue 3 SPA (embedded)      │
                    │  ├─ 用户 / 节点 / 分组管理    │
                    │  ├─ 订阅生成 (Clash/sing-box) │
                    │  ├─ /api/auth (Hy2 认证)      │
                    │  ├─ 日志解析 / 规则分析       │
                    │  └─ SQLite                   │
                    └───────────▲──────────────────┘
                                │ WebSocket (config push / logs / stats)
                    ┌───────────┴──────────────────┐
                    │  Node Agent (Go)             │
                    │  ├─ 配置落盘 + 自签名证书     │
                    │  ├─ 进程模式 / Docker 模式    │
                    │  ├─ Hysteria 日志采集         │
                    │  ├─ trafficStats 拉取         │
                    │  └─ 主机 CPU/内存/磁盘指标    │
                    └───────────┬──────────────────┘
                                │ UDP (Hysteria 2)
                         ┌──────▼──────┐
                         │   客户端     │  Clash / sing-box / Shadowrocket
                         └─────────────┘
```

目录：

```
cmd/server          面板服务端入口
cmd/node            节点 Agent 入口
cmd/client          命令行客户端（订阅 / 诊断 / 启动本地代理）
internal/server     HTTP 路由、认证、订阅、Agent Hub
internal/store      SQLite 数据层
internal/subscription  Clash / sing-box / URI / Xray 生成
internal/hy2config  Hysteria 服务端 YAML 生成
internal/logs       日志解析与规则分析
internal/agent      Agent、运行时、证书、指标、流量轮询
internal/certmanager 面板侧 ACME 签发、CSR 校验与证书下发
web/                Vue 3 + Vite 前端
docs/               API、用户中心、节点、证书签发、2FA、日志分析文档
```

---

## 快速开始（Docker Compose）

```bash
cp panel.example.yaml panel.yaml  # 修改 panel.public_url、server.data_dir 等
docker compose up -d --build
```

> **注意**：如果宿主目录里没有 `panel.yaml`，Docker / 1Panel 会把 `/config/panel.yaml` 创建成目录，日志出现 `read /config/panel.yaml: is a directory`。请先执行上面的 `cp`（或改为挂载 `./config:/config`，把配置放到 `./config/panel.yaml`）。新版面板会自动回退到 `/data/panel.yaml` 继续启动，详见[常见问题](docs/faq.md#启动日志反复出现-read-configpanelyaml-is-a-directory)。

打开 `http://服务器IP:8080/` 查看公开首页。首次启动且 `admin.password_file` 为空时，访问 `/install`：向导第一步选择数据后端（SQLite / PostgreSQL / MySQL），第二步创建管理员账号。配置了密码文件的无人值守部署会在启动时自动创建。管理员控制台是 `/admin`（旧地址 `/panel` 会自动跳转到 `/admin/dashboard`），终端用户入口是 `/user`。

应用配置**只从 `panel.yaml` 读取**，不读取环境变量，也不使用 `.env`。Compose 只负责端口和数据目录映射。

数据默认保存在仓库目录下的 `./data`。管理员账号来自 `panel.yaml` 或 `/install`：

- 用户名：`admin.username`，初始化向导会更新该字段；
- 密码：`admin.password_file` 指向的密钥文件；留空则由 `/install` 创建管理员（密码只以 bcrypt 存入数据库，不写入 YAML）。

数据库也可以在 `/install` 第一步选择。`panel.example.yaml` 默认使用 `database.driver: setup`：**首次启动只运行安装向导，不会创建 SQLite 文件**；选择并保存后端后才真正打开数据库。SQLite 使用 `database.path`；选择 PostgreSQL / MySQL 时，向导会先测试连接，再把 DSN 写入 `data_dir/database.dsn`（权限 0600），并更新 `panel.yaml` 的 `database.driver` / `database.dsn_file`。**保存后面板会自动重启一次**，管理员创建页面会在重启完成后继续。DSN 不会写进 `panel.yaml`。

首次登录后请到 **Settings → Security** 修改密码。初始化完成后 `/install` 会按 404 处理，不再暴露安装入口。同一页面还可以开启 **管理员安全入口**（默认关闭）：开启后 `/admin` 不再可用，必须使用 `/admin/<入口路径>/login` 访问，管理 API 也会校验该入口。

公开首页可以在 **Settings → 首页** 上传自定义 HTML 或 ZIP（含 CSS/JS/图片），页面在沙箱 iframe 中运行，可执行 JavaScript，但无法读取管理员登录态；该模式不参与搜索引擎收录。详见 [自定义首页](docs/homepage.md)。

生产环境建议：

1. 用 Nginx / Caddy 提供 HTTPS 反向代理到 `127.0.0.1:8080`。
2. 在 `panel.yaml` 设置 `panel.public_url: https://panel.example.com`，或在初始化向导 / **Settings → General** 中修改。
3. 使用密钥文件：`admin.password_file`、`security.jwt_secret_file`、`security.cert_key_file`，权限保持 0600。
4. 如果面板位于反向代理之后，在 `server.trusted_proxies` 声明代理地址，否则登录限流会把所有客户端视为同一个 IP。

### 可选 Redis

面板可以选用 Redis 保存管理员会话、API Key / 登录限流计数和订阅二维码缓存。`redis.url_file` 为空时使用进程内存模式；配置后如果 Redis 暂时不可用，有效 JWT 仍可继续使用，限流会回退到本机内存。

```yaml
redis:
  url_file: "/config/redis.url"   # 文件内容：redis://127.0.0.1:6379/0
  prefix: "mikvace:"
  required: false
```

- `redis.url_file`：文件内容支持 `redis://`、`rediss://`，格式为 `redis://[:password@]host:port/db`。
- `redis.prefix`：所有键的统一前缀，默认 `mikvace:`。
- `redis.required`：为 `true` 时 Redis 连接失败会直接拒绝启动；默认 `false`，自动回退内存模式。

使用 Docker Compose 时可通过 profile 一并启动 Redis：

```bash
# panel.yaml 中设置 redis.url_file，并把 redis.url 挂载到容器
docker compose --profile redis up -d --build
```

不启用 Redis 时保持 `redis.url_file` 为空即可。启用 Redis 后，新登录的管理员会话会写入 Redis，旧的无状态 JWT 在 Redis 中不存在时会失效，需要重新登录一次。

### 预构建镜像（GHCR）

推送到 `main` 或打 `v*.*.*` 标签时，GitHub Actions 会自动构建并发布面板与节点镜像到 GHCR，支持 `linux/amd64` 和 `linux/arm64`（工作流见 `.github/workflows/docker.yml`）。面板镜像在构建时先编译 Vue 前端，再把 `web/dist` 嵌入 Go 二进制，因此默认只需要一个容器，不存在单独的 API 镜像。

```bash
docker pull ghcr.io/axmishell/mikvacepanel:latest      # 面板（内嵌前端）
docker pull ghcr.io/axmishell/mikvacepanel-node:latest   # 节点 Agent
```

标签规则：`main` 分支发布 `main`、`latest`、`sha-<commit>`；`v1.2.3` 标签发布 `1.2.3`、`1.2`、`latest`、`sha-<commit>`。`latest` 始终指向最后一次成功构建的镜像，因此后续的 `main` 构建会覆盖发布标签产生的 `latest`；需要稳定版本请使用语义化版本标签。

### 本地开发

```bash
# 终端 1：后端（配置只从 panel.yaml 读取）
cp panel.example.yaml panel.yaml
go run ./cmd/server --config ./panel.yaml

# 终端 2：前端（Vite，自动代理 /api 到 8080）
cd web && npm install && npm run dev
# 打开 http://localhost:5173/admin/login
```

构建单二进制：

```bash
make build          # 先构建前端，再编译 server/node/client 到 bin/
./bin/mikvace-server --config ./panel.yaml
```

首次启动打开 `http://localhost:8080/install`，先选择 SQLite / PostgreSQL / MySQL，再创建管理员。直接运行二进制时 `server.data_dir` 默认是 `./data`，`panel.yaml` 需要可写，`/install` 才能把配置写回。

### 前后端分离 / CDN 部署（可选）

默认发行方式是单二进制内嵌前端，**不需要也不建议**单独部署前端。只有确实要把 SPA 放到 CDN 或独立域名时才使用下面的可选模式；该模式不会随 GHCR 自动发布镜像。

**1. 构建前端**

CDN 根路径部署：

```bash
cd web
VITE_BASE_PATH=/ \
VITE_API_BASE_URL=https://api.example.com/api \
npm run build
```

把 `web/dist/` 上传到 CDN，并把 SPA fallback 指向 `index.html`。

如果前端仍部署在 `/panel/` 子路径下，保持：

```bash
VITE_BASE_PATH=/panel/
VITE_API_BASE_URL=https://api.example.com/api
```

> 面板默认使用根路径（`/admin/login`、`/admin/dashboard`、`/user` 等）。访问旧的 `/panel` 会被 302 到 `/admin/dashboard`，因此升级后无需修改已收藏的链接。

**2. 启动纯 API 服务（可选）**

创建 `api-panel.yaml`：

```yaml
version: 1
panel:
  public_url: "https://api.example.com"
server:
  addr: "127.0.0.1:8080"
  data_dir: "/data"
  serve_web: false
  frontend_url: "https://panel.example.com"
  cors_origins: ["https://panel.example.com"]
  trusted_proxies: ["127.0.0.1/32"]
admin:
  username: "admin"
  password_file: "/etc/mikvace/admin.password"
```

```bash
./bin/mikvace-server-api --config ./api-panel.yaml
```

如需自行构建 API-only 镜像（非默认发布物）：

```bash
docker build -f Dockerfile.api -t mikvacepanel/api:latest .
```

或直接编译：

```bash
make server-api     # bin/mikvace-server-api，使用 -tags noweb，不依赖 web/dist
```

API 域名的 Nginx 示例见 [`deploy/nginx-api.conf`](deploy/nginx-api.conf)。注意为备份恢复调大 `client_max_body_size`，并保留 `/api/node/ws` 的 WebSocket upgrade 头。

**3. 配置字段**

| 字段 | 说明 |
|------|------|
| `server.serve_web` | `true` 时由 Go 提供内嵌前端（`/admin`、`/user` 等）；CDN 部署设为 `false` |
| `server.frontend_url` | 前端公网地址，`serve_web: false` 时根路径跳转使用 |
| `server.cors_origins` | 允许跨域访问 API 的前端 Origin，必须精确匹配 |
| `server.trusted_proxies` | 反向代理 IP/CIDR，用于正确获取客户端 IP 和登录限流 |
| `panel.public_url` | 后端公网地址，用于订阅链接和节点认证回调，不是 CDN 地址 |

注意：

- `panel.public_url` 必须指向可访问的 API 地址（例如 `https://api.example.com`），否则订阅链接和节点认证会指向错误域名。
- 如果希望订阅链接继续使用面板域名，需要在面板域名反向代理 `/api/files/*` 到后端。
- 当前认证使用 `Authorization: Bearer`，跨域不需要 Cookie/CSRF；不要把 `server.cors_origins` 配置成 `*`。
- 前端静态资源与 `index.html` 建议分别设置长缓存和 `no-store`；CDN 不要缓存 `/api/*`。
- 订阅落地页 `/api/files/:token` 仍由后端渲染，如果希望它使用面板域名，需要在面板域名反向代理该路径。

---

## 添加节点

1. 在 **Nodes → Add node** 填写名称、IP/域名、端口、TLS 模式、端口跳跃、obfs、ACL 等。
2. 创建后会弹出安装命令，复制到节点服务器执行。
3. Agent 连接成功后节点状态变为 **Online**，面板会立即下发第一份配置。

推荐的单容器命令（由面板自动生成，包含 Token 和完整的 `node.yaml`）：

```bash
sudo install -d -m 700 /etc/mikvace /var/lib/mikvace
printf '%s' 'mtn_xxx' | sudo tee /etc/mikvace/node.token >/dev/null
sudo tee /etc/mikvace/node.yaml >/dev/null <<'EOF'
version: 1
panel:
  url: "https://panel.example.com"
  token_file: "/config/node.token"
agent:
  data_dir: "/data"
  host_data_dir: "/var/lib/mikvace"
runtime:
  type: "process"
offline_auth:
  enabled: true
  listen: "127.0.0.1:9998"
EOF
docker run -d --name mikvace-node --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
  -v /etc/mikvace:/config:ro \
  -v /var/lib/mikvace:/data \
  ghcr.io/axmishell/mikvacepanel-node:latest --config /config/node.yaml
```

也可以使用节点模板与 Compose 部署（进程模式；Docker 模式改用 `docker-compose.node-docker.yml`）：

```bash
cp node.example.yaml node.yaml
printf '%s' 'mtn_xxx' > node.token && chmod 600 node.token
# 编辑 node.yaml：panel.url、agent.host_data_dir 等
docker compose -f docker-compose.node.yml up -d
```

上面的命令直接使用 GHCR 上的预构建镜像。需要本地构建时，可以构建同名镜像覆盖它：

```bash
make docker-node
# 或
docker build -f Dockerfile.node -t ghcr.io/axmishell/mikvacepanel-node:latest .
```

节点配置只从 `node.yaml` 读取，不读取环境变量：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `panel.url` | 必填 | 面板公开地址，例如 `https://panel.example.com` |
| `panel.token_file` | 必填 | 节点 Token 文件路径，容器内通常为 `/config/node.token` |
| `panel.insecure` | `false` | 面板使用自签名 HTTPS 时设为 `true` |
| `agent.data_dir` | `/data` | 容器内持久化目录 |
| `agent.host_data_dir` | `/var/lib/mikvace` | Docker 模式下的宿主机绝对路径 |
| `agent.heartbeat` / `stats_interval` | `10s` / `15s` | 上报间隔 |
| `runtime.type` | `process` | `process`：Agent 直接启动核心；`docker`：启动同机容器 |
| `runtime.hysteria_bin` / `xray_bin` | `hysteria` / `xray` | 进程模式下的二进制路径 |
| `runtime.hysteria_image` / `xray_image` | 官方镜像 | Docker 模式镜像 |
| `offline_auth.enabled` | `true` | 面板故障时用本地快照继续为 Hysteria 认证 |
| `offline_auth.listen` | `127.0.0.1:9998` | 本地认证网关地址，只允许回环地址 |
| `agent.log_level` | `info` | Agent 日志级别 |

更多说明见 [docs/node-agent.md](docs/node-agent.md)。

---

## 订阅接口兼容性

### `GET /api/files/:token`

按 User-Agent 自动识别格式：

| User-Agent 特征 | 返回格式 |
| --- | --- |
| `shadowrocket` | Base64 编码的 URI 列表 |
| `hiddify` / `sing-box` / `nekobox` / `karing` 等 | sing-box JSON |
| `clash` / `stash` / `surge` / `loon` | Clash YAML |
| 浏览器 | 带二维码的 HTML 页面 |
| 其他 | 原始 `hysteria2://` 列表 |

也可以用 `?format=` 强制指定：`clash`、`singbox`、`uri`、`shadowrocket`、`xray-json`。

响应头：

```
Content-Type: text/yaml; charset=utf-8
Profile-Title: base64:<base64 title>
Profile-Update-Interval: 12
Subscription-Userinfo: upload=...; download=...; total=...; expire=...
support-url: ...
profile-web-page-url: ...
```

### `GET /api/files/info/:token`

返回 `enabled`、`groups`、`traffic { used, limit }`、`expire`、`servers`。

### `POST /api/auth`

供 Hysteria 2 节点做外部认证，格式与 CELERITY 一致：

```json
{ "addr": "1.2.3.4:5678", "auth": "userId:password", "tx": 123 }
```

响应：

```json
{ "ok": true, "id": "userId" }
```

完整 API 见 [docs/api.md](docs/api.md)。

---

## 日志分析

Agent 会把 Hysteria 的 stdout/stderr 按 2 秒 / 100 行聚合，带 batch id 上报；服务端幂等入库并解析：

- 级别：DEBUG / INFO / WARN / ERROR / FATAL
- 事件：认证成功/失败、客户端连接/断开、启动/关闭、TCP/UDP 错误、TLS/QUIC、ACME、流量统计等
- 字段：时间、用户 ID、客户端 IP、目标地址、上下行字节、耗时、错误文本

**Logs** 页面提供时间线、级别/事件分布、Top 来源 IP、Top 用户、节点维度统计，以及以下规则告警：

| 规则 | 默认阈值 |
| --- | --- |
| 单 IP 暴力破解 | 10 次认证失败 / 窗口 |
| 未知用户尝试 | ≥5 次 |
| 单节点错误爆发 | 20 条 error / 窗口 |
| 重启循环 | ≥3 次 start / 窗口 |
| 认证失败率 | ≥30%（样本 ≥20） |
| TLS/ACME 异常 | ≥5 条 / 节点 |
| 配额耗尽 | 任意用户达到流量上限 |
| 节点离线 / 配置错误 | 实时状态 |

阈值可在 **Settings → Log analysis** 调整，日志保留天数默认为 14 天。

> 面板自身的认证拒绝也会写入日志（`logger=panel-auth`），因此即使节点日志暂时断开，也能看到认证失败行为。

---

## 备份与恢复

在 **Settings → 备份** 中：

- **立即备份**：生成 `data/backups/mikvace-*.tar.gz`（SQLite `VACUUM INTO` 一致性快照 + 元数据）。
- **自动备份**：默认每 24 小时一次，保留最近 7 份，可在页面调整。
- **恢复**：上传 `.tar.gz` 后服务端会校验数据库结构并暂存为 `panel.db.restore`，**重启面板**后替换当前数据库；原库会保留为 `panel.db.pre-restore-*`。
- **远程后端**：PostgreSQL / MySQL 的备份与恢复接口当前返回未实现，自动备份会跳过；请使用 `pg_dump` / `mysqldump` 并同时备份 `cert.key`、`jwt.secret`。
- **CELERITY 备份导入**：可直接上传 CELERITY-panel 的 `celerity-backup-*.tar.gz`（mongodump 格式）。服务端会解析 BSON 并转换为 MikvAce SQLite，导入用户、分组、Hysteria/Xray 节点、管理员、API 密钥、流量、到期时间及订阅页面设置。
  - 不支持的类型（`virtual`、`cdn`、Xray `xhttp`）和诊断探针用户会跳过并给出提示。
  - CELERITY 的 SSH 凭据和 2FA 使用其 `ENCRYPTION_KEY` 加密，无法迁移，需要重新填写/启用。
  - CELERITY 节点令牌不能复用，导入后会生成新的 MikvAce Agent 令牌，需要在节点上重新安装 Agent。

## 命令行客户端

```bash
go build -o mikvace-client ./cmd/client

# 订阅信息
./mikvace-client info --url https://panel.example.com/api/files/<token>

# 导出配置
./mikvace-client sub --url ... --format clash --out clash.yaml
./mikvace-client sub --url ... --format singbox --out singbox.json

# 诊断节点（DNS / 可选 TCP；Hysteria 本身走 UDP）
./mikvace-client check --url ...

# 启动本地 SOCKS5/HTTP 代理（需要本机安装 hysteria）
./mikvace-client run --url ... --node 1 \
  --socks5 127.0.0.1:1080 --http 127.0.0.1:8080 \
  --hysteria /usr/local/bin/hysteria
```

---

## 项目结构 / 技术栈

- **后端**：Go 1.26+、chi、gorilla/websocket、JWT、bcrypt、yaml.v3、pgx/v5、go-sql-driver/mysql、modernc.org/sqlite（纯 Go）。
- **前端**：Vue 3、Vue Router、Vite、lucide-vue-next，无 UI 框架，手写深色响应式界面。
- **节点端**：Go，进程模式或 Docker 模式，SIGHUP/SIGTERM 管理，自签名证书，iptables 端口跳跃。
- **数据**：SQLite（安装向导的默认选项，WAL）、PostgreSQL 18、MySQL 8.0 均可作为面板数据库；选择 SQLite 后文件默认为 `/data/panel.db`；JWT secret 默认持久化在 `/data/jwt.secret`。

---

## 安全建议

- 必须通过 HTTPS 暴露面板；订阅链接和 Hy2 认证回调都依赖 `public_url`。
- 修改默认管理员密码，并为 `security.jwt_secret_file` 配置至少 32 字节随机值。
- 节点 Token 等同于节点身份，泄露后请在节点页 **Reset token**。配置文件中只放 `panel.token_file` 路径，不写明文 Token。
- 数据库包含用户密码（订阅认证用）和节点统计密钥，请限制 `/data` 权限并做好备份。
- 节点 `agent.data_dir/state/users.json` 是离线认证快照，包含 Hysteria 连接密码；Agent 以 0600 写入，请确保只有 root 可读，并保持面板到节点使用 HTTPS/WSS。
- 端口跳跃需要 `NET_ADMIN`，请只在自己控制的节点上运行。
- 建议在面板前增加反向代理的速率限制和访问控制；面板自带登录失败限流。

---

## 当前范围与后续方向

当前版本已实现 Hysteria 2、Xray 多协议入站（VLESS / VMess / Trojan / Shadowsocks）、同节点双核心、HAPP 路由头、API Key 限流、SSH 自动安装、本地备份恢复。尚未实现 CELERITY 中的级联拓扑、浏览器 SSH 终端、支付/工单、S3 备份、MCP 等。订阅接口、认证接口和节点控制协议已按可扩展方式拆分：`internal/hy2config` 与 `internal/xrayconfig` 分别生成两种核心配置，Agent 通过 `configs` 映射一次应用多个核心。

---

## 开发命令

```bash
go test ./...          # 单元 + 接口 + WebSocket 集成测试
go vet ./...
make web               # 构建前端
make build             # 前端 + 三个二进制
make docker            # 面板镜像
make docker-node       # 节点镜像
```

## License

MIT
