# 订阅与客户端

## 订阅地址

每个用户有一个订阅 Token，链接格式：

```text
https://panel.example.com/api/files/<token>
```

Token 可以在用户页查看、重置。重置后旧链接立即失效。

订阅内容取决于：

- 用户启用的节点 / 分组；
- 节点启用的核心（Hysteria 2 / Xray，可同时启用）；
- 节点上的每个已启用 Xray 入站（VLESS / VMess / Trojan / Shadowsocks）；禁用的入站不会出现在订阅中；
- 请求的 User-Agent 或 `format` 参数；
- 面板订阅设置（标题、更新间隔、支持链接、HAPP 路由）。

## 自动识别格式

| User-Agent 特征 | 返回 |
| --- | --- |
| 浏览器 | 带二维码和订阅信息的 HTML 页面 |
| `shadowrocket` | Base64 编码的 URI 列表 |
| `hiddify` / `sing-box` / `nekobox` / `karing` | sing-box JSON |
| `clash` / `stash` / `surge` / `loon` | Clash / Mihomo YAML |
| 其他 | 原始 `hysteria2://` / `vless://` / `vmess://` / `trojan://` / `ss://` 列表 |

强制指定：

```text
?format=clash
?format=singbox
?format=uri
?format=shadowrocket
?format=xray-json
```

## 响应头

```text
Content-Type: text/yaml; charset=utf-8
Profile-Title: base64:<base64 title>
Profile-Update-Interval: 12
Subscription-Userinfo: upload=...; download=...; total=...; expire=...
support-url: https://t.me/...
profile-web-page-url: https://example.com
Cache-Control: no-store, no-cache, must-revalidate, max-age=0
```

- `Subscription-Userinfo` 的 `total=0` 表示不限流量，`expire=0` 表示永不过期；
- `Profile-Title` 按 Clash 约定进行 base64 编码；
- 所有订阅响应都禁用缓存。

## HAPP / Incy 路由

设置 **订阅页面 → HAPP Provider ID / 路由模式 / 路由配置 / 公告** 后：

- HAPP 请求会附加 `providerid` 响应头；
- 路由模式 `off` 返回 `happ://routing/off`；
- 自定义 JSON 路由会以 `happ://routing/onadd/<base64>` 或 `incy://...` 返回；
- URI 格式下路由行会作为正文第一行；
- 公告通过 HAPP 约定响应头返回。

## 用户信息接口

```text
GET /api/files/info/<token>
```

返回：

```json
{
  "enabled": true,
  "groups": [],
  "traffic": {"used": 0, "limit": 0},
  "expire": "2027-01-01T00:00:00Z",
  "servers": 3
}
```

可用于自建订阅页面或客户端诊断。完整字段见 [API](api.md)。

## 客户端导入

### Clash / Mihomo

直接粘贴订阅地址；建议开启自动更新，间隔与面板设置一致。

### sing-box

使用订阅地址作为 remote profile，或在客户端中选择“导入 URL”。

### Shadowrocket

粘贴订阅地址，客户端会自动请求 Base64 URI 列表。

### Hysteria 2 客户端

使用 URI 列表或单条 `hysteria2://` 链接：

```text
hysteria2://userId:password@node.example.com:443?sni=node.example.com&insecure=0&alpn=h3#节点名
```

- `managed` / `acme` 证书：`insecure=0`；
- 自签名证书：`insecure=1`；
- 端口跳跃会附加 `mport` 与 `mportHopInt`；
- obfs 会附加 `obfs` 与 `obfs-password`。

### 订阅入口选择

面板「设置 → 订阅页面」中的 **Hysteria 2 订阅入口** 控制所有订阅下发哪些入口：

| 值 | 行为 |
| --- | --- |
| `both` | 按节点自身的 `includeTls` / `includeHopping` 设置下发，默认两条都发 |
| `tls` | 只下发 TLS 入口，隐藏 Hopping |
| `hopping` | 只下发 Hopping 入口，隐藏 TLS |

这是**订阅输出过滤**，不会修改节点配置：节点的 TLS 监听、端口跳跃和 iptables 规则仍按原配置运行。全局选择 `tls` 或 `hopping` 时，如果某个节点自身关闭了对应入口，则不会强行重新开启它。

### Xray / v2rayN

- 一个 Xray 节点可以有多个入站，订阅会为每个入站生成一条独立的代理；
- 协议：VLESS、VMess、Trojan、Shadowsocks（2022 加密方式）；
- REALITY：使用面板生成的公钥、short id、SNI、fingerprint；
- TLS：`managed` 模式下可校验证书，自签名模式需要允许不安全；
- 传输支持 TCP / WebSocket / gRPC / XHTTP / mKCP；Clash 与 sing-box 没有 mKCP，会跳过对应入站，请使用 Xray/v2rayN 客户端连接。

## 订阅故障排查

| 现象 | 排查 |
| --- | --- |
| 401 / 空订阅 | Token 是否被重置、用户是否启用/过期 |
| 没有节点 | 用户是否绑定节点或分组、节点是否 active |
| 客户端不更新 | 检查 `Profile-Update-Interval` 与客户端缓存 |
| 链接指向 localhost | 修正面板 `public_url` |
| 证书错误 | 确认节点 TLS 模式与订阅 `insecure` 是否匹配 |
