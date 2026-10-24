---
hide:
  - navigation
  - toc
---

<div class="hero" markdown>

# MikvAce Panel

面向 **Hysteria 2** 与 **Xray VLESS / REALITY** 的自托管代理管理面板。Go 后端、Vue 3 前端、单二进制部署，节点通过 WebSocket 接受配置并回传日志与流量。

<div class="badge-row" markdown>
`Go 1.26+` `Vue 3` `SQLite` `Docker` `WebSocket` `MIT`
</div>

</div>

## 核心能力

<div class="grid cards" markdown>

-   :material-server-network:{ .lg .middle } **双协议节点**

    ---

    Hysteria 2（QUIC/UDP）与 Xray 多协议入站（TCP / WebSocket / gRPC / XHTTP / mKCP，VLESS / VMess / Trojan / Shadowsocks）。
    同一节点可同时运行两个核心，也可只启用其中一个。

-   :material-certificate:{ .lg .middle } **证书统一签发**

    ---

    面板通过 ACME DNS-01 申请证书，节点提交 CSR 后接收证书链，私钥不出节点。
    节点自签 ACME 仅使用 TLS-ALPN，不占用 80 端口。

-   :material-shield-lock:{ .lg .middle } **安全控制面**

    ---

    管理员 TOTP 两步验证、一次性恢复码、JWT 会话、登录限流与 API Key 作用域。

-   :material-chart-timeline-variant:{ .lg .middle } **日志与流量**

    ---

    节点日志批量上报，按用户/客户端/事件分析；流量统计幂等入库，支持配额与在线数。

-   :material-rocket-launch:{ .lg .middle } **快速部署**

    ---

    Docker Compose、GHCR 预构建镜像、SSH 自动安装、单二进制嵌入前端。

-   :material-swap-horizontal:{ .lg .middle } **订阅兼容**

    ---

    Clash / sing-box / Shadowrocket / URI / Xray JSON，按 User-Agent 自动识别。

</div>

## 架构一览

```text
                     ┌─────────────────────────────────────┐
  浏览器 / 客户端 ───▶│  MikvAce Panel (:8080)               │
                     │  Vue SPA + REST API + WebSocket Hub  │
                     └───────────────┬─────────────────────┘
                                     │  WSS /api/node/ws
                     ┌───────────────▼─────────────────────┐
                     │  Node Agent (mikvace-node)           │
                     │  config / cert / logs / stats        │
                     └───────────────┬─────────────────────┘
                                     │
                     ┌───────────────▼─────────────────────┐
                     │  Hysteria 2 / Xray                   │
                     │  QUIC / TCP + iptables port hopping  │
                     └─────────────────────────────────────┘
```

面板只保存配置、证书元数据和统计数据；代理核心运行在节点上。节点通过 Token 认证后建立长连接，断线自动重连并补推未确认配置。

## 快速开始

```bash
git clone https://github.com/AXmishell/MikvAcePanel.git
cd MikvAcePanel
cp panel.example.yaml panel.yaml
# 修改 panel.public_url；admin.password_file 留空则走 /install
docker compose up -d --build
```

访问 `http://服务器IP:8080/` 查看公开首页。未配置 `admin.password_file` 时先打开 `/install` 创建管理员；管理员控制台是 `/admin`。登录后：

1. 修改默认密码，建议同时启用两步验证；
2. 在 **节点** 页面添加节点，复制生成的安装命令；
3. 在节点服务器执行命令，等待 Agent 上线；
4. 在 **用户** 页面创建账号并复制订阅链接。

## 文档导航

<div class="grid cards" markdown>

-   [:material-rocket-launch: 快速开始](getting-started.md)

    从零部署面板、添加第一个节点并生成订阅。

-   [:material-server: 部署指南](deployment.md)

    反向代理、HTTPS、数据持久化、Redis、升级与 GHCR 镜像。

-   [:material-book-open-variant: 面板功能](panel.md)

    用户、分组、节点、日志、设置、备份与 API Key。

-   [:material-link-variant: 订阅与客户端](subscriptions.md)

    订阅格式、响应头、HAPP 路由与客户端导入。

-   [:material-certificate: 证书签发](certificates.md)

    面板 CSR 签发、DNS-01、自签名与端口占用说明。

-   [:material-shield-check: 两步验证](2fa.md)

    TOTP 启用、恢复码与无法登录时的处理。

-   [:material-api: API 参考](api.md)

    公共接口、管理接口、Agent 控制协议。

-   [:material-help-circle: 常见问题](faq.md)

    节点离线、401、配置失败、证书、端口跳跃与恢复。

</div>

!!! warning "安全提示"

    面板必须通过 HTTPS 暴露；`public_url` 会影响订阅链接和 Hysteria 认证回调。请修改默认管理员密码，并限制 `/data` 与节点 Token 的访问权限。
