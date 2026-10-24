# 节点证书：面板签发与下发

面板统一管理节点 TLS 证书。证书私钥默认**只存在于节点**：

```
节点 Agent                         面板                         ACME
   │  生成 EC P-256 私钥              │                            │
   │  ── cert.request (CSR) ────────▶ │                            │
   │                                  │ ── DNS-01 下单 ──────────▶ │
   │                                  │ ◀── fullchain ─────────── │
   │  ◀── cert.push (仅证书链) ────── │                            │
   │  校验 → 原子安装 → 重启 Hysteria  │                            │
   │  ── cert.ack ──────────────────▶ │                            │
```

## TLS 模式

| `tlsMode` | 证书来源 | 端口占用 | 说明 |
| --- | --- | --- | --- |
| `selfsigned` | Agent 本地生成 | 无额外端口 | 默认；订阅 `insecure=1` |
| `managed` | 面板 ACME + 下发 | DNS-01，仅出站 | 推荐；订阅按域名校验证书 |
| `acme` | 节点 Hysteria 自签 | TLS-ALPN 443/TCP | 兼容旧模式，已禁用 HTTP-01，不再占用 80 |

`managed` 模式需要节点开启域名，且 Agent 具备 `cert.csr.v1` 能力（本次升级后的 Agent）。

## 控制通道协议

| 消息 | 方向 | 说明 |
| --- | --- | --- |
| `hello.capabilities` | Agent → 面板 | 声明 `cert.csr.v1` |
| `cert.request` | Agent → 面板 | CSR、域名、私钥公钥指纹 |
| `cert.push` | 面板 → Agent | fullchain、指纹、有效期；上传模式还带私钥 |
| `cert.ack` | Agent → 面板 | 安装结果 |
| `cert.error` | 面板 → Agent | 签发失败原因 |
| `cert.renew` | 面板 → Agent | 管理员手动触发申请 |

Agent 在以下时机自动申请/续期：

- 首次切换到 `managed` 且本地没有可用证书；
- 证书剩余有效期不足 30 天；
- 每 5 分钟检查一次（断线、面板重启、签发失败后自愈）。

## 面板设置

**设置 → 证书签发**：

- ACME 目录：Let's Encrypt / LiteSSL；
- ACME 邮箱；
- LiteSSL 需要额外填写 EAB Key ID 与 EAB HMAC Key，HMAC 会加密保存且不回显；
- DNS 服务商：手动 TXT、Cloudflare、阿里云 DNS、DNSPod；
- 提前续期天数（默认 30）；
- DNS 凭据加密保存在 `panel.db`，加密密钥为 `data/cert.key`（0600）。也可以通过 `security.cert_key_file` 指向 32 字节密钥文件；`data/cert.key` 会随备份归档一起保存。

DNS-01 要求面板能访问 DNS 服务商 API 与 ACME 目录，**不需要节点开放 80 或 443**。

### 手动 TXT

1. Agent 发起 CSR 后，节点编辑页显示 `_acme-challenge.<domain>` 的 TXT 记录；
2. 管理员在 DNS 添加记录；
3. 点击“验证并继续”，面板等待 TXT 生效后完成签发；
4. 30 分钟内未确认会自动失败，可重新发起。

泛域名与主域名同时申请时会出现同一名称的多条 TXT 记录，需要全部添加。证书签发完成后请自行删除这些 TXT 记录。

## 节点端文件

```
/data/certs/cert.pem        # fullchain（managed 由面板下发）
/data/certs/key.pem         # 私钥（CSR 模式永不上传面板）
/data/certs/previous-*.pem  # 上一次安装的备份，用于回滚
/data/state/meta.json       # 证书指纹 / 到期时间 / TLS 模式
```

安装流程会校验私钥配对、域名 SAN、有效期，再写入临时文件并重命名；相同指纹不会重复重启 Hysteria。

## 限制

- 面板侧 DNS-01 需要 DNS 服务商 API 或手动操作；
- CSR 模式下面板离线时无法续期，但旧证书到期前仍可正常服务；
- 同一注册域名下大量节点首次签发可能触发 Let's Encrypt 频率限制，建议分批上线；
- 上传证书由管理员提供私钥，面板会加密保存并通过控制通道下发；
- 不支持 IP 证书（ACME 不签发 IP SAN），纯 IP 节点继续使用自签名；
- 当前 CSR 模式按节点单域名签发，泛域名共享证书尚未开放。

测试流程（Pebble 自动化与 LiteSSL 手工用例）见 [证书签发测试](certificates-testing.md)。
