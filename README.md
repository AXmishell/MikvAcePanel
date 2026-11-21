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
- **管理员控制台**：管理员后台只通过 `/admin/<安全入口>` 进入，直接访问 `/admin` 返回 404；访问入口后获得 HttpOnly 门禁 Cookie。用户、节点、分组、订阅与设置均在控制台内管理；旧的 `/user` 用户中心入口已移除并返回 404。
- **Hysteria 2 认证**：节点把客户端认证请求转发到 `POST /api/auth`，支持启用/禁用、到期、流量限额、设备数限制（按唯一 IP + 宽限期）；Agent 还会持久化用户认证快照，面板故障时 Hysteria 新连接仍可继续认证。
- **节点管理**：面板生成 Hysteria / Xray 服务端配置，通过 WebSocket 实时下发；支持版本、运行状态、配置 revision、错误回报、启动/停止、整节点重启（在线走 Agent、离线走 SSH），以及卡片/列表视图和鼠标拖动调整订阅排序权重。
- **异常自动重启**：可全局与按节点开关；核心“应运行但已停止”时自动下发重启，Agent 失联时用已保存的 SSH 凭据重启容器/服务，带持续阈值、冷却与每小时次数上限；手动停止的核心不会被自动拉起。
- **证书统一签发**：面板通过 ACME DNS-01 申请证书，Agent 提交 CSR、安装证书链（私钥不出节点）；节点自签 ACME 只使用 TLS-ALPN，不再占用 80 端口。
- **两步验证（2FA）**：管理员可使用标准 TOTP 应用登录，提供一次性恢复码；2FA 密钥加密存储，支持自助关闭与恢复码重置。
- **节点端 Docker 镜像**：单容器内置 Hysteria 2 + Xray + Agent，开箱即用；也可在节点表单中为每个节点选择 Docker 运行时（`runtime.type: docker`），由 Agent 管理同机的代理容器。
- **SSH 自动安装**：保存 SSH 凭据后可一键远程安装节点容器（可选自动安装 Docker）；Agent 离线时同一凭据也用于手动重启与异常自动恢复。
- **备份恢复**：本地 tar.gz 备份、自动备份保留策略、上传恢复（重启生效）。
- **数据清理**：可配置日志与监控采样保留天数，清理后自动回收空闲页（必要时 `VACUUM`），控制数据库和备份体积。
- **API Key 限流**：每个密钥独立速率限制与 `X-RateLimit-*` 响应头，并记录请求次数、最后使用时间与来源 IP。
- **端口跳跃**：Agent 自动维护 iptables UDP REDIRECT 规则；也可改用 Hysteria 2.8+ 内置端口范围监听。
- **Hysteria 2 深度调优**：QUIC 窗口、BBR/Reno 与 BBR 预设、带宽补偿、UDP 开关、DNS 解析器、协议嗅探、三种伪装模式、GeoIP/GeoSite ACL、直连绑定、Gecko 分片、mTLS 客户端 CA、ECH、Realms、mimic，以及受保留键保护的额外 YAML 覆盖；内置“官方默认 / 推荐均衡 / 高性能 / 抗审查 / 弱网优化”模板，新建节点默认应用推荐模板。
- **日志上报与分析**：采集 Hysteria/Xray 的 stdout/stderr 与 Xray access log，按批可靠上报；服务端解析时间、级别、事件、用户、客户端 IP、错误等字段，支持搜索、时间线、Top IP/用户、规则告警（暴力破解、未知用户、错误爆发、重启循环、TLS/ACME/REALITY 异常、配额耗尽等），并可按当前筛选条件导出 `.log` / `.csv` / `.jsonl` 文件（最多 10 万行）。
- **流量统计**：Agent 轮询 Hysteria `trafficStats` 与 Xray `StatsService`，按用户增量上报；服务端按 batch id 幂等入库，支持管理员重置用户流量。双核心节点的流量自动合并。
- **API Key**：多密钥、细分权限（用户 / 分组 / 节点 / 证书 / 日志 / 维护 / 系统）、可按节点与分组限定资源范围，支持编辑权限、轮换令牌、过期时间、启停、独立限流与用量审计。
- **单二进制部署**：Vue 构建产物通过 `go:embed` 嵌入服务端。
- **SQLite**：无需额外数据库，WAL 模式，数据默认位于 `/app/data`。
- **可选 PostgreSQL 日志库**：`telemetry.driver: postgres` 可把 `hy2_logs` / `log_batches` 外置到 PostgreSQL；日志按天分区、过期直接删分区，用户与节点仍留在 SQLite。PostgreSQL 不可用时默认回退到本地日志表，认证与订阅不受影响。详见 [外部日志库](docs/telemetry-postgres.md)。
- **安装向导**：首次启动可在 `/install` 配置 SQLite、可选 PostgreSQL 日志库与 Redis，测试连接后把 DSN / URL 写入 0600 密钥文件并自动重启；也可以在第一步直接导入本面板或 CELERITY 备份，恢复已有用户、节点和管理员。
- **存储与缓存设置**：管理员可在 **设置 → 存储与缓存** 查看日志库 / Redis 状态、测试连接、修改配置并重启；内置 PostgreSQL / Redis DSN 模板，API Key 无权调用。
- **Redis 高级缓存**：会话、限流、验证码、二维码与日志分析二级缓存；多实例共享分析结果，Redis 不可用时自动退回本地内存。

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
internal/server     HTTP 路由、认证、订阅、Agent Hub（适配层）
internal/app        用户/节点等应用服务（用例编排）
internal/ports      仓储与集成接口
internal/store      SQLite 数据层
internal/subscription  Clash / sing-box / URI / Xray 生成
internal/hy2config  Hysteria 服务端 YAML 生成
internal/logs       日志解析与规则分析
internal/agent      Agent、运行时、证书、指标、流量轮询
internal/certmanager 面板侧 ACME 签发、CSR 校验与证书下发
web/                Vue 3 + Vite 前端
docs/               API、节点、证书签发、2FA、日志分析文档
```

---

## 快速开始（Docker Compose）

```bash
mkdir -p config
cp panel.example.yaml config/panel.yaml  # 修改 panel.public_url、server.data_dir 等
docker compose up -d --build
```

> **注意**：compose 使用 `./config:/app/config` 目录映射，配置放在 `./config/panel.yaml`。如果沿用旧版的单文件挂载 `./panel.yaml:/app/config/panel.yaml`，而宿主机上没有该文件，Docker / 1Panel 会把挂载点创建成目录，日志出现 `read /app/config/panel.yaml: is a directory`。新版面板会自动回退到 `/app/data/panel.yaml` 继续启动，详见[常见问题](docs/faq.md#启动日志反复出现-read-appconfigpanelyaml-is-a-directory)。

访问 `http://服务器IP:8080/install` 初始化面板。首次启动且 `admin.password_file` 为空时，向导第一步确认 SQLite 数据文件（也可以直接上传本面板 `.tar.gz` 或 CELERITY mongodump 备份恢复），第二步创建管理员账号。配置了密码文件的无人值守部署会在启动时自动创建。管理员控制台只通过安全入口 `/admin/<入口>` 访问，直接访问 `/admin` 返回 404；入口会在首次启动时随机生成并写入数据目录的 `admin-entry.txt`（0600），安装向导完成后也会自动跳转到该入口（旧地址 `/panel` 与用户中心 `/user` 均已停用并直接返回 404）。站点根路径 `/` 不提供公开首页，初始化完成后直接返回 404；尚未创建管理员时会跳转到 `/install`。

Docker 部署时应用配置**只从 `config/panel.yaml` 读取**，不读取环境变量，也不使用 `.env`。Compose 只负责端口、配置目录和数据目录映射。

数据默认保存在仓库目录下的 `./data`。管理员账号来自 `config/panel.yaml` 或 `/install`：

- 用户名：`admin.username`，初始化向导会更新该字段；
- 密码：`admin.password_file` 指向的密钥文件；留空则由 `/install` 创建管理员（密码只以 bcrypt 存入数据库，不写入 YAML）。

控制面只使用 SQLite，代理日志可选外置到 PostgreSQL。`panel.example.yaml` 默认使用 `database.driver: setup`：**首次启动只运行安装向导，不会创建 SQLite 文件**；在 `/install` 确认 `database.path` 并保存后才真正打开数据库，同时把 `database.driver` / `database.path` 写回 `config/panel.yaml`。**首次保存路径后面板会自动重启一次**，管理员创建页面会在重启完成后继续。日志库通过 `telemetry` 配置节启用，默认为 `sqlite`，不改变现有部署行为。

首次登录后请到 **Settings → Security** 修改密码。初始化完成后 `/install` 会按 404 处理，不再暴露安装入口。**管理员安全入口强制开启**：首次启动会在数据目录生成随机入口（同时写入 `admin-entry.txt`，权限 0600），必须使用 `/admin/<入口路径>` 访问；直接访问 `/admin` 会返回 404。访问入口后浏览器会获得 HttpOnly 门禁 Cookie，管理 API 同时接受该 Cookie 或 `X-Admin-Entry` 请求头。

生产环境建议：

1. 用 Nginx / Caddy 提供 HTTPS 反向代理到 `127.0.0.1:8080`。
2. 在 `config/panel.yaml` 设置 `panel.public_url: https://panel.example.com`，或在初始化向导 / **Settings → General** 中修改。
3. 使用密钥文件：`admin.password_file`、`security.jwt_secret_file`、`security.cert_key_file`，权限保持 0600。
4. 如果面板位于反向代理之后，在 `server.trusted_proxies` 声明代理地址，否则登录限流会把所有客户端视为同一个 IP。

### 可选 Redis

面板可以选用 Redis 保存管理员会话、API Key / 登录限流计数、订阅二维码缓存，并启用**高级缓存**：日志分析结果写入 Redis 二级缓存，多实例共享同一份结果。`redis.url_file` 为空时使用进程内存模式；配置后如果 Redis 暂时不可用，有效 JWT 仍可继续使用，限流和缓存会回退到本机内存。安装向导 `/install` 可以直接勾选并测试 Redis。

```yaml
redis:
  url_file: "/app/config/redis.url"   # 文件内容：redis://127.0.0.1:6379/0
  prefix: "mikvace:"
  required: false
```

- `redis.url_file`：文件内容支持 `redis://`、`rediss://`，格式为 `redis://[:password@]host:port/db`。
- `redis.prefix`：所有键的统一前缀，默认 `mikvace:`。
- `redis.required`：为 `true` 时 Redis 连接失败会直接拒绝启动；默认 `false`，自动回退内存模式。

使用 Docker Compose 时可通过 profile 一并启动 Redis：

```bash
# config/panel.yaml 中设置 redis.url_file，并把 redis.url 放到 ./config/ 下
docker compose --profile redis up -d --build
```

不启用 Redis 时保持 `redis.url_file` 为空即可。启用 Redis 后，新登录的管理员会话会写入 Redis，旧的无状态 JWT 在 Redis 中不存在时会失效，需要重新登录一次。

### 预构建镜像（GHCR）

发布 GitHub Release 时，GitHub Actions 会自动构建并发布面板与节点镜像到 GHCR，支持 `linux/amd64` 和 `linux/arm64`（工作流见 `.github/workflows/images.yml`）。推送 `main` 或推送 tag 都不会触发镜像构建。面板镜像在构建时先编译 Vue 前端，再把 `web/dist` 嵌入 Go 二进制，因此默认只需要一个容器，不存在单独的 API 镜像。

```bash
docker pull ghcr.io/axmishell/mikvacepanel:latest      # 面板（内嵌前端）
docker pull ghcr.io/axmishell/mikvacepanel-node:latest   # 节点 Agent
```

标签规则：稳定 Release（例如 `v1.2.3`）发布 `1.2.3`、`1.2`、`latest`、`sha-<commit>`；预发行版（`v1.2.3-rc.1`）只发布 `1.2.3-rc.1` 与 `sha-<commit>`，不会移动 `1.2` 和 `latest`。`latest` 只由最近一次稳定 Release 更新，因此建议固定使用语义化版本标签。

### 预构建二进制（GitHub Releases）

发布 GitHub Release 时，`.github/workflows/release.yml` 会运行测试、重建前端并生成以下归档，然后上传到该 Release。工作流只响应「发布 Release」事件：`git push origin v1.2.3` 只创建 tag，不会触发任何运行；在 GitHub 上创建或发布 Release 时才会构建并上传产物。

- `mikvace-server_<version>_<os>_<arch>.tar.gz` / `.zip`：面板，内嵌前端，支持 Linux / macOS / Windows 的 amd64、arm64；
- `mikvace-node_<version>_linux_<arch>.tar.gz`：节点 Agent，仅 Linux amd64 / arm64（依赖 iptables 与 Docker）；归档不包含 Hysteria/Xray 核心，进程模式需自行安装，推荐直接使用 GHCR 节点镜像；
- `mikvace-client_<version>_<os>_<arch>.tar.gz` / `.zip`：命令行客户端，平台同面板；
- `checksums.txt`：所有归档的 SHA-256。

带 `-` 的标签（例如 `v1.2.3-rc.1`）会发布为预发行版。下载后先校验再安装：

```bash
VERSION=1.2.3
BASE="https://github.com/AXmishell/MikvAcePanel/releases/download/v${VERSION}"
curl -fLO "$BASE/mikvace-server_${VERSION}_linux_amd64.tar.gz"
curl -fLO "$BASE/checksums.txt"
sha256sum -c checksums.txt --ignore-missing
tar -xzf "mikvace-server_${VERSION}_linux_amd64.tar.gz"
sudo install -m 0755 mikvace-server /usr/local/bin/
```

发布新版本（推送 tag 本身不会触发任何工作流，需要在 GitHub 上发布 Release）：

```bash
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3

# 二选一：在 GitHub 的 Releases 页面手动创建并发布，或直接用 gh：
gh release create v1.2.3 --title v1.2.3 --generate-notes
# 预发行版加 --prerelease，例如 v1.2.3-rc.1
```

发布 Release 后，`.github/workflows/release.yml` 上传跨平台归档，`.github/workflows/images.yml` 并行构建并推送 `1.2.3` 等标签的镜像；两个工作流互不依赖。

### 本地开发

```bash
# 一键启动：编译后端 + Vite 热更新（后台运行，局域网可访问）
./scripts/dev.sh start          # 或 make dev-up

./scripts/dev.sh status         # PID、端口、健康检查
./scripts/dev.sh restart        # 重启前后端
./scripts/dev.sh restart --build # 重新编译后端后重启
./scripts/dev.sh stop           # 停止
./scripts/dev.sh logs           # 跟踪面板与 Vite 日志
# 打开 http://<局域网IP>:5173/install
```

脚本会在 `panel.yaml` 不存在时自动生成 SQLite 开发配置，PID 与日志位于 `data/run/`。默认绑定 `0.0.0.0`，可通过 `PANEL_PORT`、`WEB_PORT`、`DEV_HOST` 覆盖。直接运行 `./scripts/dev.sh` 会显示常用命令帮助。

手动方式（两个终端）：

```bash
# 终端 1：后端（配置只从 panel.yaml 读取）
cp panel.example.yaml panel.yaml
go run ./cmd/server --config ./panel.yaml

# 终端 2：前端（Vite，自动代理 /api 到 8080）
cd web && npm install && npm run dev
# 从 ./data/admin-entry.txt 读取入口，打开 http://localhost:5173/admin/<入口>/login
```

构建单二进制：

```bash
make build          # 先构建前端，再编译 server/node/client 到 bin/
./bin/mikvace-server --config ./panel.yaml
```

首次启动打开 `http://localhost:8080/install`，先确认 SQLite 数据文件路径，再创建管理员。直接运行二进制时 `server.data_dir` 默认是 `./data`，`panel.yaml` 需要可写，`/install` 才能把配置写回。

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

> 面板默认使用根路径（`/admin/<入口>/login`、`/admin/<入口>/dashboard` 等）。直接访问 `/admin` 会返回 404；入口见数据目录的 `admin-entry.txt`。旧的 `/panel` 与 `/user` 已停用，访问时会直接返回 404。

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
| `server.serve_web` | `true` 时由 Go 提供内嵌前端（`/admin` 等）；CDN 部署设为 `false` |
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

推荐的单容器命令（由面板自动生成，令牌通过环境变量传入，不需要 `node.yaml`）：

```bash
sudo install -d -m 700 /var/lib/mikvace
docker run -d --name mikvace-node --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
  -e PANEL_URL='https://panel.example.com' \
  -e NODE_TOKEN='mtn_xxx' \
  -e HY2_RUNTIME=process \
  -v /var/lib/mikvace:/data \
  ghcr.io/axmishell/mikvacepanel-node:latest
```

Compose 部署也是一个文件，令牌直接写在 `environment` 里：

```yaml
services:
  node:
    image: ghcr.io/axmishell/mikvacepanel-node:latest
    container_name: mikvace-node
    restart: unless-stopped
    network_mode: host
    cap_add:
      - NET_ADMIN
    environment:
      PANEL_URL: "https://panel.example.com"
      NODE_TOKEN: "mtn_xxx"
      HY2_RUNTIME: "process"
    volumes:
      - /var/lib/mikvace:/data
```

```bash
docker compose -f docker-compose.node.yml up -d
```

`HY2_RUNTIME=process` 时 Agent 在自身容器内启动核心；`HY2_RUNTIME=docker` 时通过 Docker socket 启动独立容器，需要额外挂载 `/var/run/docker.sock`，并保持 `HOST_DATA_DIR` 与 `/data` 的宿主机路径一致。

上面的命令直接使用 GHCR 上的预构建镜像。需要本地构建时，可以构建同名镜像覆盖它：

```bash
make docker-node
# 或
docker build -f Dockerfile.node -t ghcr.io/axmishell/mikvacepanel-node:latest .
```

节点优先读取环境变量，其次才是可选的 `node.yaml`（二进制 / systemd 部署仍可使用）：

| 环境变量 | 对应字段 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `PANEL_URL` | `panel.url` | 必填 | 面板公开地址，例如 `https://panel.example.com` |
| `NODE_TOKEN` | 令牌 | 必填 | 节点 Token；也可用 `NODE_TOKEN_FILE` 指向文件 |
| `HY2_RUNTIME` | `runtime.type` | `process` | `process`：Agent 直接启动核心；`docker`：启动同机容器 |
| `PANEL_INSECURE` | `panel.insecure` | `false` | 面板使用自签名 HTTPS 时设为 `true` |
| `DATA_DIR` | `agent.data_dir` | `/data` | 容器内持久化目录 |
| `HOST_DATA_DIR` | `agent.host_data_dir` | `/var/lib/mikvace` | Docker 模式下的宿主机绝对路径 |
| `LOG_LEVEL` | `agent.log_level` | `info` | Agent 日志级别 |
| `HEARTBEAT` / `STATS_INTERVAL` | 上报间隔 | `10s` / `15s` | 心跳与统计上报间隔 |
| `OFFLINE_AUTH` | `offline_auth.enabled` | `true` | 面板故障时用本地快照继续为 Hysteria 认证 |
| `OFFLINE_AUTH_LISTEN` | `offline_auth.listen` | `127.0.0.1:9998` | 本地认证网关地址，只允许回环地址 |
| `HYSTERIA_IMAGE` / `XRAY_IMAGE` | Docker 模式镜像 | 官方镜像 | 运行 `HY2_RUNTIME=docker` 时使用 |

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

- **后端**：Go 1.26+、chi、gorilla/websocket、JWT、bcrypt、yaml.v3、modernc.org/sqlite（纯 Go）。
- **前端**：Vue 3、Vue Router、Vite、lucide-vue-next，无 UI 框架，手写深色响应式界面。
- **节点端**：Go，进程模式或 Docker 模式，SIGHUP/SIGTERM 管理，自签名证书，iptables 端口跳跃。
- **数据**：SQLite（WAL），文件默认为 `/app/data/panel.db`；可选 PostgreSQL 存放代理日志；JWT secret 默认持久化在 `/app/data/jwt.secret`。

---

## 安全建议

- 必须通过 HTTPS 暴露面板；订阅链接和 Hy2 认证回调都依赖 `public_url`。
- 修改默认管理员密码，并为 `security.jwt_secret_file` 配置至少 32 字节随机值。
- 节点 Token 等同于节点身份，泄露后请在节点页 **Reset token**。配置文件中只放 `panel.token_file` 路径，不写明文 Token。
- 数据库包含用户密码（订阅认证用）和节点统计密钥，请限制 `/app/data` 权限并做好备份。
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
./scripts/dev.sh       # 后台启动面板 + Vite 热更新
./scripts/dev.sh restart
make web               # 构建前端
make build             # 前端 + 三个二进制
make release           # 生成 dist/ 下的跨平台发布归档
make docker            # 面板镜像
make docker-node       # 节点镜像
```

## License

MIT
