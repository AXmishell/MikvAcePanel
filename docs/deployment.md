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

应用配置**只从 YAML 读取**，不读取任何环境变量，也不使用 `.env`。面板模板为 `panel.example.yaml`，节点模板为 `node.example.yaml`；复制为 `panel.yaml` / `node.yaml` 后按需修改。

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

Docker 部署时把密钥文件放到 `./config/`，并在 compose 中追加只读挂载：

```yaml
    volumes:
      - ./panel.yaml:/config/panel.yaml
      - ./config/admin.password:/config/admin.password:ro
```

配置中写 `/config/admin.password` 即可。

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

节点侧对应关系见 [节点 Agent](node-agent.md#配置文件)。

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
      - ./panel.yaml:/config/panel.yaml
```

- 只监听 `127.0.0.1:8080`，由反向代理对外提供 HTTPS；
- 需要修改宿主机端口时，直接改 compose 的 `ports`，或用 `compose.override.yaml` 覆盖；不再使用 `.env` 插值；
- 如果面板直接暴露，改为 `8080:8080` 并配置防火墙；
- 数据使用当前目录映射（`./data`），不使用 Docker 命名卷。`./data` 必须持久化，否则重启后用户、节点、证书全部丢失。
- `panel.yaml` 需要可写，`/install` 会把面板名称、公共地址和管理员用户名写回；也可以预先配置 `admin.password_file` 进行无人值守初始化。
- 使用反向代理时在 `server.trusted_proxies` 中填写代理地址，否则面板会忽略 `X-Forwarded-For`，把所有客户端当成代理 IP，登录限流和审计日志将不准确。

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

默认使用 SQLite，数据文件为 `./data/panel.db`。代码已经引入方言层（`internal/store/dialect`）和统一的 `DB` 包装层，PostgreSQL 18 与 MySQL 8.0 作为可选后端分阶段接入：

```yaml
# SQLite（默认）
database:
  driver: sqlite
  path: /data/panel.db

# PostgreSQL（待启用）
database:
  driver: postgres
  dsn_file: /run/secrets/database_url

# MySQL 8.0（待启用）
database:
  driver: mysql
  dsn_file: /run/secrets/database_url
```

`dsn_file` 内容示例：

```text
# PostgreSQL
postgres://user:pass@postgres:5432/mikvace?sslmode=require

# MySQL
user:pass@tcp(mysql:3306)/mikvace?charset=utf8mb4&collation=utf8mb4_bin&parseTime=false&loc=UTC
```

当前版本若选择 `postgres` 或 `mysql` 会在启动时明确报错，不会半途写入远程库。切换到远程库前请先在 **设置 → 备份** 创建备份；跨库迁移工具会在后续阶段提供。

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

```bash
make build
sudo install -m 0755 bin/mikvace-server /usr/local/bin/
sudo install -d -m 700 /etc/mikvace /var/lib/mikvace
sudo cp panel.example.yaml /etc/mikvace/panel.yaml
# 编辑 /etc/mikvace/panel.yaml：server.data_dir、panel.public_url、admin.password_file 等
```

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

## 备份与恢复

面板 **设置 → 备份**：

- 立即备份生成 `data/backups/mikvace-*.tar.gz`，包含 SQLite 一致性快照、`meta.json` 和 `cert.key`；
- 自动备份默认 24 小时一次，保留 7 份；
- 上传恢复后需要 **重启面板** 才会替换数据库；
- 原数据库会保留为 `panel.db.pre-restore-<时间>`。

手动恢复前请停止面板，避免 WAL 写入：

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
