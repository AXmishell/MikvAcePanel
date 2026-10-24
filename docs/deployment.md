# 部署指南

## 部署组成

最简部署只有面板一个进程：

```text
浏览器 ──HTTPS──▶ Nginx / Caddy ──▶ MikvAce Panel :8080
                                           │
节点 Agent ───────WSS /api/node/ws─────────┘
```

面板自身不终止 TLS，生产环境建议使用 Nginx、Caddy 或云负载均衡提供 HTTPS。

## 配置文件

面板配置**只从 YAML 读取**，不读取任何环境变量，也不使用 `.env`。面板模板为 `panel.example.yaml`，复制为 `panel.yaml` 后按需修改。Docker Compose 使用目录映射，宿主机路径为 `config/panel.yaml`，容器内为 `/config/panel.yaml`。节点 Agent 不同：它优先读取环境变量（`PANEL_URL`、`NODE_TOKEN`、`HY2_RUNTIME` 等），容器部署不再需要 `node.yaml`；二进制 / systemd 仍可使用 `node.example.yaml`。

查找顺序：`--config` 参数 → `panel.yaml` → `config/panel.yaml` → `/config/panel.yaml` → `/data/panel.yaml`。

面板主要字段：

| 字段 | 默认 | 说明 |
| --- | --- | --- |
| `server.addr` | `:8080` | 面板监听地址 |
| `server.data_dir` | `./data` | 数据目录，Docker 镜像内为 `/data` |
| `server.serve_web` | `true` | 是否由面板提供内嵌前端 |
| `server.frontend_url` | 空 | 前后端分离时的前端地址 |
| `server.cors_origins` | 空 | 允许跨域的前端 Origin |
| `server.trusted_proxies` | 空 | 可信反向代理的 IP/CIDR |
| `server.log_level` | `info` | `debug/info/warn/error` |
| `panel.name` | `MikvAce Panel` | 面板名称，`/install` 会更新 |
| `panel.public_url` | 空 | 对外地址，用于订阅和节点认证回调 |
| `admin.username` | `admin` | 初始化管理员用户名 |
| `admin.password_file` | 空 | 密码文件；留空走 `/install`。不允许内联密码 |
| `session.ttl` | `24h` | 管理员会话有效期 |
| `security.jwt_secret_file` | 空 | JWT/恢复码签名密钥文件；留空自动生成 `data_dir/jwt.secret` |
| `security.cert_key_file` | 空 | 证书/DNS/2FA 加密密钥文件；留空自动生成 `data_dir/cert.key` |
| `database.driver` | `sqlite` | `sqlite` / `postgres` / `mysql` |
| `database.path` | `<data_dir>/panel.db` | SQLite 路径 |
| `database.dsn_file` | 空 | 远程数据库 DSN 文件 |
| `database.max_open` / `max_idle` | 后端默认 | 连接池大小 |
| `database.conn_max_lifetime` | `1h` | 连接最长复用时间 |
| `redis.url_file` | 空 | Redis URL 文件；为空则使用进程内存 |
| `redis.prefix` | `mikvace:` | Redis key 前缀 |
| `redis.required` | `false` | Redis 不可用时是否拒绝启动 |

**密钥只能用 `*_file` 引用**，配置文件中不出现明文密码、Token、JWT secret、DSN 或证书加密密钥。最小示例：

```bash
sudo install -d -m 700 /etc/mikvace
sudo install -m 600 /dev/null /etc/mikvace/admin.password
printf '%s' '请替换为强密码' | sudo tee /etc/mikvace/admin.password >/dev/null
```

然后在 `panel.yaml` 中设置：

```yaml
admin:
  password_file: "/etc/mikvace/admin.password"
```

Docker 部署时把密钥文件放到 `./config/`。compose 已把整个目录映射到容器的 `/config`，无需再单独挂载：

```bash
sudo install -d -m 700 ./config
sudo install -m 600 /dev/null ./config/admin.password
printf '%s' '请替换为强密码' | sudo tee ./config/admin.password >/dev/null
```

在 `config/panel.yaml` 中写：

```yaml
admin:
  password_file: "/config/admin.password"
```

`jwt_secret_file` 与 `cert_key_file` 一旦使用后不要随意更换：

- 更换 JWT secret 会使所有管理员会话和 2FA 恢复码失效；
- 更换 cert key 会导致已保存的证书私钥、DNS 凭据和 TOTP 密钥无法解密。

两者都会随面板备份归档，恢复数据库时请一起恢复。

### 从 `.env` 迁移

应用不再读取环境变量。旧版 `.env` 与 YAML 的对应关系：

| 旧变量 | 新字段 |
| --- | --- |
| `ADDR` | `server.addr` |
| `DATA_DIR` | `server.data_dir` |
| `PANEL_PUBLIC_URL` | `panel.public_url` |
| `ADMIN_USERNAME` | `admin.username` |
| `ADMIN_PASSWORD` | `admin.password_file`（先写入文件） |
| `JWT_SECRET` | `security.jwt_secret_file`（或留空自动生成） |
| `CERT_ENC_KEY` | `security.cert_key_file`（或留空自动生成） |
| `SESSION_TTL_HOURS` | `session.ttl`（如 `24h`） |
| `CORS_ORIGINS` | `server.cors_origins` |
| `TRUSTED_PROXIES` | `server.trusted_proxies` |
| `LOG_LEVEL` | `server.log_level` |
| `REDIS_URL` | `redis.url_file`（先写入文件） |
| `REDIS_PREFIX` | `redis.prefix` |
| `REDIS_REQUIRED` | `redis.required` |
| `DB_DRIVER` | `database.driver` |
| `DATABASE_URL` | `database.dsn_file`（先写入文件） |
| `DB_PATH` | `database.path` |
| `DB_MAX_OPEN` / `DB_MAX_IDLE` | `database.max_open` / `max_idle` |
| `DB_CONN_LIFETIME` | `database.conn_max_lifetime` |
| `SERVE_WEB` | `server.serve_web` |
| `FRONTEND_URL` | `server.frontend_url` |

节点侧环境变量见 [节点 Agent](node-agent.md#配置)。

## Docker Compose

```yaml
services:
  panel:
    image: ghcr.io/axmishell/mikvacepanel:latest
    container_name: mikvace-panel
    restart: unless-stopped
    command: ["--config", "/config/panel.yaml"]
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - ./data:/data
      - ./config:/config
```

- 只监听 `127.0.0.1:8080`，由反向代理对外提供 HTTPS；
- 需要修改宿主机端口时，直接改 compose 的 `ports`，或用 `compose.override.yaml` 覆盖；不再使用 `.env` 插值；
- 如果面板直接暴露，改为 `8080:8080` 并配置防火墙；
- 数据使用当前目录映射（`./data`），不使用 Docker 命名卷。`./data` 必须持久化，否则重启后用户、节点、证书全部丢失。
- 配置目录使用 `./config` 映射到容器 `/config`。`./config/panel.yaml` 需要可写，`/install` 会把面板名称、公共地址、管理员用户名和数据库配置写回；也可以预先配置 `admin.password_file` 进行无人值守初始化。
- 通过 `/install` 选择 PostgreSQL / MySQL 时，DSN 会写到 `/data/database.dsn`，请确保 `./data` 持久化。
- 使用反向代理时在 `server.trusted_proxies` 中填写代理地址，否则面板会忽略 `X-Forwarded-For`，把所有客户端当成代理 IP，登录限流和审计日志将不准确。

!!! note "为什么挂载 ./config 目录而不是单个 panel.yaml"

    Docker 在宿主机文件不存在时，会把单文件挂载点创建成**目录**。旧版做法 `./panel.yaml:/config/panel.yaml` 如果在启动前没有创建 `panel.yaml`，日志就会反复出现：

    ```text
    ERROR load config error="read /config/panel.yaml: is a directory" path=/config/panel.yaml
    ```

    目录映射则没有这个问题：`./config` 不存在时 Docker 会自动创建，`/install` 直接写入 `/config/panel.yaml`。如果已经遇到该错误，请改成上面的目录映射，然后在 1Panel / Compose 中**重建容器**（`restart` 不会改变挂载类型）：

    ```bash
    docker compose up -d --force-recreate
    ```

    目录映射后，把配置放到 `./config/panel.yaml` 即可；面板检测到 `--config` 指向目录时也会记录 warning 并回退到 `/data/panel.yaml`，不会 crash-loop。

    从旧版单文件挂载升级时，先移动配置再重建容器：

    ```bash
    mkdir -p config
    mv panel.yaml config/panel.yaml
    docker compose up -d --force-recreate
    ```

!!! warning "从命名卷迁移"

    旧版本使用 `panel-data` 命名卷。升级到目录映射前先停止面板，再把卷内容复制到 `./data`：

    ```bash
    docker compose down
    mkdir -p ./data
    docker run --rm \
      -v mikvacepanel_panel-data:/from \
      -v "$PWD/data:/to" \
      alpine sh -c 'cp -a /from/. /to/'
    docker compose up -d
    ```

    卷名取决于 Compose 项目名（默认是目录名 + `_panel-data`），可用 `docker volume ls` 确认。复制完成后建议保留旧卷一段时间，确认面板和备份都正常后再删除。

### 数据库后端

SQLite 是零配置后端，PostgreSQL 18 和 MySQL 8.0 也已可直接使用。`panel.example.yaml` 默认 `database.driver: setup`：首次启动只运行安装器，**不会创建 SQLite 文件**；打开 `/install` 选择后端后才打开数据库：

- 选择 SQLite 后创建 `server.data_dir/panel.db`；
- 选择 PostgreSQL / MySQL 时，向导先测试连接并执行一次幂等迁移，再写入配置；
- `panel.yaml` 写入 `database.driver` 与 `database.dsn_file`；
- DSN 写入 `server.data_dir/database.dsn`（权限 0600），不进入 YAML；
- 保存后面板自动重启一次，随后继续创建管理员。

安装完成后 `/install` 关闭，数据库连接只能通过修改 `panel.yaml` 后重启来调整。以下为手工配置示例：

```yaml
# SQLite
database:
  driver: sqlite
  path: /data/panel.db

# PostgreSQL 18
database:
  driver: postgres
  dsn_file: /run/secrets/database_url
  max_open: 20
  max_idle: 10
  conn_max_lifetime: "30m"

# MySQL 8.0（建议 8.0.13+）
database:
  driver: mysql
  dsn_file: /run/secrets/database_url
  max_open: 10
  max_idle: 5
  conn_max_lifetime: "30m"
```

`dsn_file` 内容示例：

```text
# PostgreSQL
postgres://user:pass@postgres:5432/mikvace?sslmode=require

# MySQL
user:pass@tcp(mysql:3306)/mikvace?charset=utf8mb4&collation=utf8mb4_bin&parseTime=false&loc=UTC
```

注意事项：

- 面板启动时自动创建表、索引和 `schema_migrations`，并通过 advisory lock 防止多实例同时迁移；
- PostgreSQL 使用 `pgx` 驱动，DSN 中的 `sslmode` 按实际环境设置；
- MySQL 表使用 `utf8mb4_bin` 排序规则，Token、哈希等字段区分大小写；建议使用上面的 DSN 参数；
- MySQL 8.0.13+ 才支持 `TEXT` 列的表达式默认值，低版本请先升级；
- 远程库的 SQLite 备份/恢复接口会返回“未实现”，请使用数据库自身的备份工具；
- 切换后端不会复制现有数据，跨库迁移工具将在后续提供；
- 不要把数据库密码写进 `panel.yaml`，只通过 `dsn_file` 引用。

Docker Compose 可选的 PostgreSQL 示例：

```yaml
services:
  postgres:
    image: postgres:18
    restart: unless-stopped
    environment:
      POSTGRES_DB: mikvace
      POSTGRES_USER: mikvace
      POSTGRES_PASSWORD_FILE: /run/secrets/postgres_password
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
    secrets:
      - postgres_password

secrets:
  postgres_password:
    file: ./config/postgres_password
```

MySQL 对应使用 `mysql:8.0` 和 `./data/mysql:/var/lib/mysql`，并在启动参数中指定 `--character-set-server=utf8mb4 --collation-server=utf8mb4_bin`。

## 自定义首页

管理员可以在 **设置 → 首页** 上传 `.html` 或 `.zip`（可含 CSS/JS/图片）。页面通过沙箱 iframe 在 `/` 展示，可以执行 JavaScript，但不能读取管理员登录态，也不参与搜索引擎收录。

- 单 HTML 最大 2 MB；ZIP 最大 25 MB，解压后 50 MB
- 文件保存在 `server.data_dir/homepage/`，仅保留最近 5 个版本
- SQLite 备份会包含当前启用的首页；恢复后下次启动生效
- 反向代理不需要额外配置 `/homepage/*`
- 多实例部署必须共享 `server.data_dir/homepage/`，否则各实例可能显示不同页面

细节见 [自定义首页](homepage.md)。

## HTTPS 反向代理

### Nginx

```nginx
server {
    listen 443 ssl http2;
    server_name panel.example.com;

    ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket: /api/node/ws
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 120s;
        proxy_send_timeout 120s;
    }
}

server {
    listen 80;
    server_name panel.example.com;
    return 301 https://$host$request_uri;
}
```

### Caddy

```caddyfile
panel.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy 会自动处理证书、HTTP/2 和 WebSocket。

!!! warning "WebSocket 必须放行"

    节点 Agent 通过 `wss://panel.example.com/api/node/ws` 长连接。反向代理如果没有转发 `Upgrade` / `Connection` 头，节点会显示离线或反复重连。

## 直接运行二进制

### 预构建二进制

推送到 `v*.*.*` 标签后，`.github/workflows/release.yml` 会自动发布 `mikvace-server` / `mikvace-node` / `mikvace-client` 的跨平台归档和 `checksums.txt` 到 GitHub Releases。面板与客户端支持 Linux / macOS / Windows 的 amd64、arm64；节点 Agent 只发布 Linux amd64 / arm64，且归档不包含 Hysteria/Xray 核心，推荐节点继续使用 GHCR 镜像。

```bash
VERSION=1.2.3
BASE="https://github.com/AXmishell/MikvAcePanel/releases/download/v${VERSION}"
curl -fLO "$BASE/mikvace-server_${VERSION}_linux_amd64.tar.gz"
curl -fLO "$BASE/checksums.txt"
sha256sum -c checksums.txt --ignore-missing
tar -xzf "mikvace-server_${VERSION}_linux_amd64.tar.gz"
sudo install -m 0755 mikvace-server /usr/local/bin/
```

### 从源码构建

```bash
make build
sudo install -m 0755 bin/mikvace-server /usr/local/bin/
sudo install -d -m 700 /etc/mikvace /var/lib/mikvace
sudo cp panel.example.yaml /etc/mikvace/panel.yaml
# 编辑 /etc/mikvace/panel.yaml：server.data_dir、panel.public_url、admin.password_file 等
```

`panel.example.yaml` 默认 `database.driver: setup`：首次启动只运行安装器，**不会创建 SQLite 文件**。打开 `/install` 选择 SQLite / PostgreSQL / MySQL 并保存后，进程自动重启并打开所选数据库。确定使用 SQLite 时也可以直接把 `driver` 改成 `sqlite`，跳过向导第一步。

`/etc/systemd/system/mikvace-panel.service`：

```ini
[Unit]
Description=MikvAce Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/var/lib/mikvace
ExecStart=/usr/local/bin/mikvace-server --config /etc/mikvace/panel.yaml
Restart=always
RestartSec=3
User=root
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now mikvace-panel
sudo systemctl status mikvace-panel
```

## 前后端分离 / CDN（可选）

默认发行方式是单二进制内嵌前端，无需单独部署 SPA。只有确实需要把前端放到 CDN 或独立域名时才使用本节的可选模式；该模式不会随 GHCR 自动发布镜像。

```bash
cd web
VITE_BASE_PATH=/ \
VITE_API_BASE_URL=https://api.example.com/api \
npm run build
```

后端使用 API-only 构建与运行参数，配置示例 `api-panel.yaml`：

```yaml
version: 1
panel:
  public_url: "https://api.example.com"
server:
  addr: "127.0.0.1:8080"
  data_dir: "/var/lib/mikvace"
  serve_web: false
  frontend_url: "https://panel.example.com"
  cors_origins: ["https://panel.example.com"]
  trusted_proxies: ["127.0.0.1/32"]
admin:
  username: "admin"
  password_file: "/etc/mikvace/admin.password"
```

```bash
make server-api     # 使用 -tags noweb，不依赖 web/dist
./bin/mikvace-server-api --config ./api-panel.yaml
```

| 字段 | 说明 |
| --- | --- |
| `server.serve_web` | `false` 时只提供 API，不再返回内嵌前端（`/admin`、`/user` 等） |
| `server.frontend_url` | 前端公网地址，API-only 模式下 `/` 与 `/panel` 跳转使用 |
| `server.cors_origins` | 允许跨域的前端 Origin，必须精确匹配 |
| `server.trusted_proxies` | API 反向代理地址，用于客户端 IP 和登录限流 |
| `panel.public_url` | API 公网地址，用于订阅链接和节点认证 |

API 域名的 Nginx 示例见仓库根目录 `deploy/nginx-api.conf`。注意 `/api/*` 不要被 CDN 缓存，备份恢复需要调大 `client_max_body_size`。

## Redis（可选）

Redis 用于：

- 多实例共享管理员会话；
- 登录 / API Key 限流计数；
- 订阅二维码缓存。

```yaml
redis:
  url_file: "/config/redis.url"   # 文件内容：redis://127.0.0.1:6379/0
  prefix: "mikvace:"
  required: false
```

```bash
install -m 600 /dev/null /etc/mikvace/redis.url
printf '%s' 'redis://127.0.0.1:6379/0' | sudo tee /etc/mikvace/redis.url >/dev/null
```

Compose 内置 profile：

```bash
docker compose --profile redis up -d
```

未启用 Redis 时使用进程内内存；Redis 临时不可用时，已签发的 JWT 仍可继续使用，限流回退到内存。

## 升级

```bash
# 先在面板 设置 → 备份 点击“立即备份”
git pull
docker compose up -d --build
```

数据库迁移是幂等的，启动时自动补齐表与列。升级前建议备份 `server.data_dir`，尤其是：

```text
panel.db
panel.db-wal
panel.db-shm
jwt.secret
cert.key
backups/
```

节点镜像升级：

```bash
docker pull ghcr.io/axmishell/mikvacepanel-node:latest
# 重新执行面板生成的安装命令，或 docker rm -f mikvace-node 后重新运行
```

二进制部署升级时，从 GitHub Releases 下载对应平台归档，校验 `checksums.txt` 后替换 `/usr/local/bin/mikvace-server` 并重启 systemd 服务即可；数据库迁移会随启动自动执行。

## 备份与恢复

面板 **设置 → 备份** 目前只支持 SQLite：

- 立即备份生成 `data/backups/mikvace-*.tar.gz`，包含 SQLite 一致性快照、`meta.json` 和 `cert.key`；
- 自动备份默认 24 小时一次，保留 7 份；
- 上传恢复后需要 **重启面板** 才会替换数据库；
- 原数据库会保留为 `panel.db.pre-restore-<时间>`。

PostgreSQL / MySQL 后端：

- 面板内的备份/恢复接口返回 **501 Not Implemented**；
- 自动备份会跳过，不会每小时写失败日志；
- 请使用 `pg_dump` / `mysqldump` 等数据库原生工具，并同时备份 `server.data_dir/cert.key` 与 `jwt.secret`；
- 可移植 NDJSON 备份/导入工具会在后续版本提供。

手动恢复 SQLite 前请停止面板，避免 WAL 写入：

```bash
docker compose stop panel
cp data/panel.db data/panel.db.bak
cp data/backups/mikvace-xxxx.tar.gz /tmp/
# 通过面板恢复接口上传，或按归档内容手动替换
docker compose start panel
```

## 文档站点（GitHub Pages）

仓库自带 MkDocs Material 文档与 `.github/workflows/docs.yml`：

1. GitHub 仓库 **Settings → Pages → Build and deployment**；
2. Source 选择 **GitHub Actions**；
3. 推送到 `main` 后，工作流会构建 `site/` 并发布；
4. 文档地址为 `https://mikvacepanel.gpcn.cc/`（仓库默认的 `github.io` 地址仍可访问）。

工作流会先读取 Pages 配置：如果尚未启用，或 Source 仍是“Deploy from a branch”，构建步骤会跳过并给出提示；启用 **GitHub Actions** 后重新运行 **Docs** 工作流即可。

本地预览：

```bash
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements-docs.txt
mkdocs serve
```

构建静态站点：

```bash
mkdocs build --strict
# 输出在 site/
```

仓库已包含 `docs/CNAME`（`mikvacepanel.gpcn.cc`）。自定义域名还需要：

1. 在仓库 **Settings → Pages → Custom domain** 填入 `mikvacepanel.gpcn.cc` 并保存；
2. DNS 添加 CNAME：`mikvacepanel` → `axmishell.github.io`；
3. 等待 DNS 生效后勾选 **Enforce HTTPS**。

验证：

```bash
dig +short mikvacepanel.gpcn.cc CNAME
curl -I https://mikvacepanel.gpcn.cc/
```
