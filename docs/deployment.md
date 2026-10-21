# 部署指南

## 部署组成

最简部署只有面板一个进程：

```text
浏览器 ──HTTPS──▶ Nginx / Caddy ──▶ MikvAce Panel :8080
                                           │
节点 Agent ───────WSS /api/node/ws─────────┘
```

面板自身不终止 TLS，生产环境建议使用 Nginx、Caddy 或云负载均衡提供 HTTPS。

## 环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `ADDR` | `:8080` | 面板监听地址 |
| `DATA_DIR` | `./data` | 数据目录，Docker 镜像内为 `/data` |
| `DB_PATH` | `<DATA_DIR>/panel.db` | SQLite 路径 |
| `PANEL_PUBLIC_URL` | `http://localhost:8080` | 对外地址，用于订阅和节点认证回调 |
| `ADMIN_USERNAME` | `admin` | 首次初始化的管理员用户名 |
| `ADMIN_PASSWORD` | `admin123` | 首次初始化的管理员密码 |
| `JWT_SECRET` | 自动生成 | JWT/恢复码签名密钥；未设置时写入 `DATA_DIR/jwt.secret` |
| `CERT_ENC_KEY` | 自动生成 | 证书私钥/DNS 凭据/2FA 密钥的加密密钥；未设置时写入 `DATA_DIR/cert.key` |
| `CORS_ORIGINS` | 空 | 前端开发时的允许来源，逗号分隔 |
| `LOG_LEVEL` | `info` | `debug/info/warn/error` |
| `REDIS_URL` | 空 | 可选 Redis，例如 `redis://127.0.0.1:6379/0` |
| `REDIS_PREFIX` | `mikvace:` | Redis key 前缀 |
| `REDIS_REQUIRED` | `false` | Redis 不可用时是否拒绝启动 |

`JWT_SECRET` 与 `CERT_ENC_KEY` 一旦使用后不要随意更换：

- 更换 JWT secret 会使所有管理员会话和 2FA 恢复码失效；
- 更换 cert key 会导致已保存的证书私钥、DNS 凭据和 TOTP 密钥无法解密。

两者都会随面板备份归档，恢复数据库时请一起恢复。

## Docker Compose

```yaml
services:
  panel:
    image: ghcr.io/axmishell/mikvacepanel:main
    container_name: mikvace-panel
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      PANEL_PUBLIC_URL: "https://panel.example.com"
      ADMIN_USERNAME: "admin"
      ADMIN_PASSWORD: "请替换"
      JWT_SECRET: "请替换为至少32字节随机值"
    volumes:
      - panel-data:/data

volumes:
  panel-data:
```

- 只监听 `127.0.0.1:8080`，由反向代理对外提供 HTTPS；
- 如果面板直接暴露，改为 `8080:8080` 并配置防火墙；
- `panel-data` 必须持久化，否则重启后用户、节点、证书全部丢失。

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
sudo mkdir -p /var/lib/mikvace && sudo chmod 700 /var/lib/mikvace
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
Environment=PANEL_PUBLIC_URL=https://panel.example.com
Environment=ADDR=127.0.0.1:8080
Environment=DATA_DIR=/var/lib/mikvace
ExecStart=/usr/local/bin/mikvace-server
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

## Redis（可选）

Redis 用于：

- 多实例共享管理员会话；
- 登录 / API Key 限流计数；
- 订阅二维码缓存。

```bash
REDIS_URL=redis://127.0.0.1:6379/0
REDIS_PREFIX=mikvace:
REDIS_REQUIRED=false
```

Compose 内置 profile：

```bash
# .env: REDIS_URL=redis://redis:6379/0
docker compose --profile redis up -d
```

未启用 Redis 时使用进程内内存；Redis 临时不可用时，已签发的 JWT 仍可继续使用，限流回退到内存。

## 升级

```bash
# 先在面板 设置 → 备份 点击“立即备份”
git pull
docker compose up -d --build
```

数据库迁移是幂等的，启动时自动补齐表与列。升级前建议备份 `DATA_DIR`，尤其是：

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
docker pull ghcr.io/axmishell/mikvacepanel-node:main
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
