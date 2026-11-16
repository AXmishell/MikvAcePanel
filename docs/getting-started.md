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
mkdir -p config
cp panel.example.yaml config/panel.yaml
```

编辑 `config/panel.yaml`：

```yaml
panel:
  public_url: "https://panel.example.com"
server:
  data_dir: "/app/data"
admin:
  username: "admin"
  password_file: ""      # 留空则使用 /install
```

启动：

```bash
docker compose up -d --build
docker compose logs -f panel
```

打开 `http://服务器IP:8080/install` 初始化面板。首次启动且 `admin.password_file` 为空时，打开 `/install`：先确认 SQLite 数据文件，可同时启用外部 PostgreSQL 日志库和 Redis 高级缓存，再创建第一个管理员；管理员控制台只通过 `/admin/<入口>` 访问（直接访问 `/admin` 返回 404）；入口在首次启动时随机生成并写入 `data_dir/admin-entry.txt`，安装完成后向导会自动跳转到该地址（旧地址 `/panel` 不再跳转，直接返回 404）。站点根路径 `/` 不提供公开首页，初始化完成后直接返回 404；尚未创建管理员时会跳转到 `/install`。

### 使用 GHCR 预构建镜像

```bash
mkdir -p config
cp panel.example.yaml config/panel.yaml
# 编辑 config/panel.yaml 后：
docker run -d --name mikvace-panel --restart unless-stopped \
  -p 8080:8080 \
  -v "$PWD/config:/app/config" \
  -v "$PWD/data:/app/data" \
  ghcr.io/axmishell/mikvacepanel:latest --config /app/config/panel.yaml
```

使用目录映射而非命名卷：配置写入 `./config/panel.yaml`，数据写入 `./data`，直接打包这两个目录即可完成备份。`/install` 会把面板名称、公共地址、管理员用户名和 SQLite 路径写回 `config/panel.yaml`。

> **注意**：请挂载 `./config` 目录，不要挂载单个 `./panel.yaml`。宿主机文件不存在时，Docker / 1Panel 会把单文件挂载点创建成目录，日志出现 `read /app/config/panel.yaml: is a directory`。新版面板检测到目录时会自动回退到 `/app/data/panel.yaml` 并继续启动安装器。

镜像标签：`main`、`latest`（main 分支）与 `sha-<commit>`；发布 `v*.*.*` 标签时同时提供语义化版本，并更新 `latest`。`latest` 指向最后一次成功构建的镜像，需要固定版本请使用语义化版本标签。

## 2. 首次登录

应用配置只从 YAML 读取（Docker 部署为 `config/panel.yaml`），不读取环境变量，也不使用 `.env`。`panel.example.yaml` 默认 `database.driver: setup`：首次启动进入**安装器模式**，只提供 `/install` 和数据库配置接口，**不会打开或创建 SQLite 文件**；保存 SQLite 路径后进程重启，才进入完整面板。

首次启动有两种初始化方式：

- **`admin.password_file` 为空**：访问 `http://服务器IP:8080/install`。第一步确认 SQLite 数据文件路径，保存后才创建 `data/panel.db`；同一页可以勾选外部 PostgreSQL 日志库和 Redis（均可测试连接），保存时 DSN / URL 写入 `data` 目录下的 0600 密钥文件，`panel.yaml` 只记录路径，然后自动重启面板。第二步设置管理员用户名、密码、面板名称和公共访问地址。提交后会自动登录并跳转到安全入口，把非敏感字段写回 `panel.yaml`；入口同时保存在 `data_dir/admin-entry.txt`（0600）。**如果已有备份，也可以在第一步直接上传本面板 `.tar.gz` 或 CELERITY mongodump 备份**：面板会校验、暂存数据库并重启，备份中已有管理员时直接使用原账号登录，没有管理员时继续创建管理员。
- **配置了 `admin.password_file`**：启动时读取密钥文件并自动创建 `admin.username` 对应的管理员；从 `data_dir/admin-entry.txt` 读取安全入口，打开 `/admin/<入口>` 登录即可。文件内容只保存密码，权限保持 0600。

!!! note "数据库配置的写入位置"

    `panel.yaml` 保存 `database.driver` 和 SQLite 的 `database.path`。初始化向导只在创建第一个管理员之前可用，完成后不能再通过 `/install` 改数据库路径。

    如果希望跳过安装器模式，把 `database.driver` 改为 `sqlite` 后启动即可；已有配置文件缺少 `database` 段时也按 SQLite 处理。

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

保存后点击 **安装**，复制生成的命令到节点执行。令牌通过环境变量传入，不需要创建 `node.yaml` 或 `node.token`：

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

如果使用 Compose 部署节点，同样只需要一个文件，令牌写在 `environment` 中：

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

等待面板显示 **Online**，配置 revision 变为已应用。详细参数和 Docker 运行时模式见 [节点 Agent](node-agent.md)。

!!! warning "网络模式"

    Hysteria 使用 UDP，端口跳跃需要修改宿主机 iptables，因此节点容器使用 `--network host` 和 `--cap-add NET_ADMIN`。
    `HY2_RUNTIME=docker` 还会挂载 `/var/run/docker.sock`，这等同于宿主机 root 权限，请只在受控节点使用。

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
