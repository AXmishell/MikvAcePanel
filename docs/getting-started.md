# 快速开始

本文从一台全新的 Linux 服务器开始，完成面板部署、管理员登录、节点接入和订阅生成。

## 环境要求

| 组件 | 要求 |
| --- | --- |
| 面板 | Linux / Docker 24+ / Docker Compose v2；也可以直接运行二进制 |
| 节点 | Linux，root 或具备 Docker 权限；Hysteria 使用 UDP |
| 端口 | 面板 `8080/TCP`；节点代理端口（默认 `443/UDP`）；端口跳跃为一段 UDP 端口 |
| 可选 | Redis 7+（会话、限流、二维码缓存）、Nginx / Caddy（HTTPS） |

节点需要能够访问面板的 `panel.url`；面板不需要主动连接节点（SSH 自动安装除外）。

## 1. 部署面板

### Docker Compose

```bash
git clone https://github.com/AXmishell/MikvAcePanel.git
cd MikvAcePanel
cp panel.example.yaml panel.yaml
```

编辑 `panel.yaml`：

```yaml
panel:
  public_url: "https://panel.example.com"
server:
  data_dir: "/data"
admin:
  username: "admin"
  password_file: ""      # 留空则使用 /install
```

启动：

```bash
docker compose up -d --build
docker compose logs -f panel
```

打开 `http://服务器IP:8080/` 查看公开首页。首次启动且 `admin.password_file` 为空时，打开 `/install`：先选择 SQLite / PostgreSQL / MySQL，再创建第一个管理员；管理员控制台位于 `/admin`（旧地址 `/panel` 会自动跳转到 `/admin/dashboard`）。

### 使用 GHCR 预构建镜像

```bash
cp panel.example.yaml panel.yaml
# 编辑 panel.yaml 后：
docker run -d --name mikvace-panel --restart unless-stopped \
  -p 8080:8080 \
  -v "$PWD/panel.yaml:/config/panel.yaml" \
  -v "$PWD/data:/data" \
  ghcr.io/axmishell/mikvacepanel:latest --config /config/panel.yaml
```

使用目录映射而非命名卷：数据会写入当前目录的 `./data`，直接打包该目录即可完成备份。`panel.yaml` 需要可写，`/install` 会把面板名称、公共地址、管理员用户名和数据库配置写回该文件；远程数据库的 DSN 单独写入 `./data/database.dsn`，不会进入 YAML。

镜像标签：`main`、`latest`（main 分支）与 `sha-<commit>`；发布 `v*.*.*` 标签时同时提供语义化版本，并更新 `latest`。`latest` 指向最后一次成功构建的镜像，需要固定版本请使用语义化版本标签。

## 2. 首次登录

应用配置只从 `panel.yaml` 读取，不读取环境变量，也不使用 `.env`。`panel.example.yaml` 默认 `database.driver: setup`：首次启动进入**安装器模式**，只提供 `/install` 和数据库配置接口，**不会打开或创建 SQLite 文件**；保存后端后进程重启，才进入完整面板。

首次启动有两种初始化方式：

- **`admin.password_file` 为空**：访问 `http://服务器IP:8080/install`。第一步选择数据后端：SQLite 保存后才创建 `data/panel.db`；PostgreSQL / MySQL 需要填写 DSN，向导会先测试连接，再写入配置并自动重启面板。第二步设置管理员用户名、密码、面板名称和公共访问地址。提交后会自动登录，并把非敏感字段写回 `panel.yaml`。
- **配置了 `admin.password_file`**：启动时读取密钥文件并自动创建 `admin.username` 对应的管理员，直接打开 `/admin` 登录即可。文件内容只保存密码，权限保持 0600。

!!! note "数据库配置的写入位置"

    `panel.yaml` 只保存 `database.driver`、SQLite 的 `database.path` 和 `database.dsn_file` 路径。PostgreSQL / MySQL 的 DSN 写入 `server.data_dir/database.dsn`（权限 0600），与配置文件分离。切换后端不会迁移旧库数据；初始化向导只在创建第一个管理员之前可用，完成后不能再通过 `/install` 改数据库。

    如果希望跳过安装器模式、直接用 SQLite，把 `database.driver` 改为 `sqlite` 后启动即可；已有配置文件缺少 `database` 段时也按 SQLite 处理。

!!! note "安装入口只在首次运行开放"

    创建管理员后，`/install` 会返回 404 并渲染 404 页面，不会暴露安装向导。需要重新初始化时，请手动清空 `admins` 表，或恢复一个没有管理员的数据库备份。

首次登录后：

1. 进入 **设置 → 安全** 修改密码；
2. 建议启用 **两步验证（TOTP）**，保存一次性恢复码；
3. 在 **设置 → 常规** 确认 `public_url` 与浏览器访问地址一致。

!!! tip "纯 API 部署"

    `server.serve_web: false` 时没有 `/install` 页面。请通过 `admin.password_file` 提供初始密码，或直接调用 `POST /api/install`。管理员只在数据库首次初始化时创建，之后修改配置文件不会覆盖已有密码。

## 3. 添加第一个节点

进入 **节点 → 添加节点**，填写：

- 名称、IP、域名（使用面板签发证书或节点 ACME 时必填）；
- 端口，默认 `443`；
- TLS 模式：
  - `自签名`：无需域名，客户端订阅自动 `insecure=1`；
  - `面板签发（CSR）`：面板完成 ACME，私钥留在节点，推荐；
  - `节点自签 ACME`：Hysteria 自己申请，使用 TLS-ALPN，不占用 80；
- 端口跳跃、obfs、ACL、出站等可选。

保存后点击 **安装**，复制生成的命令到节点执行。命令会创建 `/etc/mikvace/node.yaml` 和 `/etc/mikvace/node.token`，并通过 `--config` 启动 Agent：

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

如果使用 Compose 部署节点：

```bash
cp node.example.yaml node.yaml
printf '%s' 'mtn_xxx' > node.token && chmod 600 node.token
# 编辑 node.yaml：panel.url、agent.host_data_dir 等
docker compose -f docker-compose.node.yml up -d
```

等待面板显示 **Online**，配置 revision 变为已应用。详细参数和 Docker 运行时模式见 [节点 Agent](node-agent.md)。

!!! warning "网络模式"

    Hysteria 使用 UDP，端口跳跃需要修改宿主机 iptables，因此节点容器使用 `--network host` 和 `--cap-add NET_ADMIN`。
    `runtime.type: docker` 还会挂载 `/var/run/docker.sock`，这等同于宿主机 root 权限，请只在受控节点使用。

## 4. 创建用户与订阅

进入 **用户** 页面创建账号，设置：

- 用户 ID / 密码（Hysteria 认证使用）；
- 流量上限、到期时间、设备数；
- 允许访问的节点或分组。

保存后复制订阅链接：

```text
https://panel.example.com/api/files/<token>
```

订阅会根据 User-Agent 返回 Clash、sing-box、Shadowrocket 或 URI 列表，也可以追加 `?format=clash|singbox|uri|shadowrocket|xray-json`。详见 [订阅与客户端](subscriptions.md)。

## 5. 下一步

- 配置 HTTPS 反向代理：[部署指南](deployment.md)
- 启用面板证书签发：[证书签发](certificates.md)
- 启用管理员 2FA：[两步验证](2fa.md)
- 排查连接问题：[常见问题](faq.md)
