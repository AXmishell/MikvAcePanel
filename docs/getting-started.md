# 快速开始

本文从一台全新的 Linux 服务器开始，完成面板部署、管理员登录、节点接入和订阅生成。

## 环境要求

| 组件 | 要求 |
| --- | --- |
| 面板 | Linux / Docker 24+ / Docker Compose v2；也可以直接运行二进制 |
| 节点 | Linux，root 或具备 Docker 权限；Hysteria 使用 UDP |
| 端口 | 面板 `8080/TCP`；节点代理端口（默认 `443/UDP`）；端口跳跃为一段 UDP 端口 |
| 可选 | Redis 7+（会话、限流、二维码缓存）、Nginx / Caddy（HTTPS） |

节点需要能够访问面板的 `PANEL_URL`；面板不需要主动连接节点（SSH 自动安装除外）。

## 1. 部署面板

### Docker Compose

```bash
git clone https://github.com/AXmishell/MikvAcePanel.git
cd MikvAcePanel
cp .env.example .env
```

编辑 `.env`：

```ini
PANEL_PORT=8080
PANEL_PUBLIC_URL=https://panel.example.com
ADMIN_USERNAME=admin
ADMIN_PASSWORD=请替换为强密码
JWT_SECRET=请填入至少32字节的随机值
```

启动：

```bash
docker compose up -d --build
docker compose logs -f panel
```

打开 `http://服务器IP:8080/dashboard`（旧地址 `/panel` 会自动跳转）。

### 使用 GHCR 预构建镜像

```bash
docker run -d --name mikvace-panel --restart unless-stopped \
  -p 8080:8080 \
  -e PANEL_PUBLIC_URL="https://panel.example.com" \
  -e ADMIN_USERNAME="admin" \
  -e ADMIN_PASSWORD="请替换为强密码" \
  -e JWT_SECRET="$(openssl rand -hex 32)" \
  -v panel-data:/data \
  ghcr.io/axmishell/mikvacepanel:main
```

镜像标签：`main`（main 分支）与 `sha-<commit>`；发布 `v*.*.*` 标签时同时提供语义化版本，并更新 `latest`。

## 2. 首次登录

- 用户名：`ADMIN_USERNAME`
- 密码：`ADMIN_PASSWORD`

首次登录后：

1. 进入 **设置 → 安全** 修改密码；
2. 建议启用 **两步验证（TOTP）**，保存一次性恢复码；
3. 在 **设置 → 常规** 确认 `public_url` 与浏览器访问地址一致。

!!! tip "初始密码"

    没有提供 `ADMIN_PASSWORD` 时，面板会随机生成一个强密码，并在首次启动的日志中打印一次。
    请立即保存该密码并在首次登录后修改。管理员只在数据库首次初始化时创建，之后修改环境变量不会覆盖已有密码。

## 3. 添加第一个节点

进入 **节点 → 添加节点**，填写：

- 名称、IP、域名（使用面板签发证书或节点 ACME 时必填）；
- 端口，默认 `443`；
- TLS 模式：
  - `自签名`：无需域名，客户端订阅自动 `insecure=1`；
  - `面板签发（CSR）`：面板完成 ACME，私钥留在节点，推荐；
  - `节点自签 ACME`：Hysteria 自己申请，使用 TLS-ALPN，不占用 80；
- 端口跳跃、obfs、ACL、出站等可选。

保存后点击 **安装**，复制生成的命令到节点执行：

```bash
docker run -d --name mikvace-node --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
  -e PANEL_URL="https://panel.example.com" \
  -e NODE_TOKEN="mtn_xxx" \
  -e HY2_RUNTIME=process \
  -v /var/lib/mikvace:/data \
  ghcr.io/axmishell/mikvacepanel-node:main
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
