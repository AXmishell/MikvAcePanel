# 证书签发测试方案

本文覆盖面板 **设置 → 证书签发** 与节点 `managed` 证书下发链路。测试分为四层，日常提交只跑 L1，发版前必须完成 L2 与 L3。

| 层级 | 环境 | 覆盖范围 | 何时执行 |
| --- | --- | --- | --- |
| L1 单元 | `go test` | CSR、域名校验、指纹、设置加密、缓存推送 | 每次提交 |
| L2 集成 | Pebble（本地） | ACME 注册、下单、CSR、存储、推送 | 每次发版 |
| L3 预发 | LiteSSL + 真实 DNS | EAB、DNS 传播、服务商 API、失败重试 | 每次发版 |
| L4 生产 | Let's Encrypt 生产 | 单域名冒烟 | 发布后 |

## L2：Pebble 自动化

### 运行

```bash
make test-cert-e2e
```

默认使用 Docker 镜像 `ghcr.io/letsencrypt/pebble:2.5.2`。如果主机无法拉取镜像，可以指定本地 Pebble 二进制：

```bash
PEBBLE_BIN=/usr/local/bin/pebble make test-cert-e2e
```

脚本会：

1. 生成一次性的 CA 与 `localhost` 服务端证书；
2. 启动 Pebble（`PEBBLE_VA_ALWAYS_VALID=1`，跳过真实 DNS 校验）；
3. 运行 `TestPebbleIssuance`；
4. 清理容器、进程和临时目录。

### 覆盖内容

- 保存 ACME 设置并创建账户密钥；
- 节点 CSR 签发 `node.example.com`；
- 手动 TXT 挑战的生成与确认；
- 证书入库、指纹、SAN 校验；
- `cert.push` 推送与 `cert.ack`；
- 已有有效证书时直接推送，不重复下单。

### 不覆盖内容

- 公共 DNS 的 TXT 传播（测试将 `txtWait` 设为 0）；
- 真实 DNS 服务商 API；
- 续期窗口、面板重启、节点离线重连；
- 私钥是否只存在于节点。

以上由 L3 用例覆盖。

> Pebble 版本说明：v2.10+ 的 finalize 响应不再返回 `Location` 头，而 `golang.org/x/crypto/acme` 依赖该头继续轮询订单。脚本因此固定在 v2.5.2；升级 Pebble 前需要先验证该兼容性。

## L3：LiteSSL 手工用例

### 前置条件

- [ ] 面板可出站访问 `acme.litessl.com:443` 与公共 DNS（53/UDP、53/TCP）；
- [ ] 已在 FreeSSL / LiteSSL 控制台创建 EAB 凭据，并准备好 Key ID 与 HMAC Key；
- [ ] 面板与节点时间已同步（建议启用 NTP）；
- [ ] 测试域名已解析到测试节点，且没有 CAA 限制；
- [ ] 节点 Agent 版本支持 `cert.csr.v1`（`hello.capabilities` 可见）；
- [ ] 已备份 `data/cert.key` 或确认 `security.cert_key_file` 可用；
- [ ] 使用独立的测试 DNS Zone 与最小权限 API Token；
- [ ] 记录当前证书序列号与指纹，便于回滚。

**设置 → 证书签发** 选择：

- ACME 目录：`LiteSSL`；
- EAB Key ID / EAB HMAC Key：填入 LiteSSL 控制台的 EAB 凭据；
- 邮箱：可接收通知的地址；
- DNS 服务商：先用 `手动 TXT` 验证基础链路，再用实际服务商验证 API；
- 提前续期天数：30。

### A. 手动 TXT 链路

| ID | 步骤 | 预期 |
| --- | --- | --- |
| C01 | 保存 LiteSSL + EAB 设置 | 返回成功，刷新后 EAB Key ID 保留、HMAC 不回显 |
| C02 | 将节点 TLS 模式改为 `managed` 并保存 | 节点出现证书申请状态 |
| C03 | 打开节点证书详情 | 显示 `_acme-challenge.<domain>` 与 TXT 值 |
| C04 | 不添加 TXT，直接点击“验证并继续” | 面板等待传播，超时后给出可读错误 |
| C05 | 添加 TXT，等待公共 DNS 生效后确认 | 状态依次变为 `validating` → `issued` |
| C06 | 等待 Agent 回执 | 状态 `active`，Hysteria 重载，客户端证书校验通过 |
| C07 | 查看订阅 | `managed` 节点不再带 `insecure=1` |
| C08 | 再次触发申请 | 复用有效证书直接推送，不产生新订单 |

### B. DNS 服务商 API

| ID | 步骤 | 预期 |
| --- | --- | --- |
| C09 | 选择 Cloudflare，填入仅限测试 Zone 的 Token | 保存成功，`dnsConfigured=true` |
| C10 | 刷新页面并读取设置 | 不回显 Token |
| C11 | 触发签发 | 自动写入并清理 TXT，签发成功 |
| C12 | 使用无权限 Token | 签发失败，`lastError` 指出服务商错误 |
| C13 | 清空凭据后再签发 | 提示未配置 DNS 凭据，不进入 ACME 下单 |
| C14 | 阿里云 / DNSPod（可选） | 行为与 Cloudflare 一致 |

### C. 续期与异常恢复

| ID | 步骤 | 预期 |
| --- | --- | --- |
| C15 | 将节点证书有效期视为不足 30 天（或临时调大续期天数） | Agent 自动发起续期 |
| C16 | 手动点击续期 | 产生新序列号，旧证书保留为 `previous-*.pem` |
| C17 | 签发过程中断开节点 | 状态保持 `pushing`，重连后自动补推 |
| C18 | 签发过程中重启面板 | 任务丢失；Agent 在 5 分钟内重试并成功 |
| C19 | 上传证书：私钥与证书不匹配 | 拒绝并给出错误 |
| C20 | 上传证书成功后查看 `GET /api/certs` | 不返回私钥 |
| C21 | 多节点同时签发 | 每节点仅一个任务，无重复下单 |

### D. 边界与负向

| ID | 步骤 | 预期 |
| --- | --- | --- |
| C22 | CSR 域名与节点域名不一致 | 直接拒绝 |
| C23 | 旧 Agent（无 `cert.csr.v1`） | 提示升级 Agent，不进入签发 |
| C24 | 纯 IP 节点 | 明确不支持 |
| C25 | 泛域名节点 | 当前按单域名处理，不签发泛域名 |
| C26 | 短时间批量签发 | 观察 LiteSSL 频率限制，必要时分批 |

## 验收标准

- [ ] L1、L2 全部通过；
- [ ] LiteSSL 至少完成一次手动 TXT 与一次服务商 API 签发；
- [ ] 续期、节点离线重连、面板重启三种恢复路径均有记录；
- [ ] 失败场景均有可读 `lastError` 与事件日志；
- [ ] `GET /api/certs`、节点详情、订阅均不泄露私钥；
- [ ] 生产冒烟只针对单个测试域名，未触发速率限制。

## 清理

1. 删除 `_acme-challenge` 测试 TXT 记录；
2. 将 ACME 目录切回 Let's Encrypt 或继续使用 LiteSSL（切回 Let's Encrypt 会自动清除 EAB 凭据）；
3. 删除测试节点上不再使用的 `previous-*.pem`；
4. 轮换或删除测试 DNS API Token；
5. 恢复节点 TLS 模式与测试前一致。

## 故障定位

| 现象 | 排查方向 |
| --- | --- |
| `awaiting_txt` 长时间不变 | TXT 是否写入正确的 Zone、公共 DNS 是否可见 |
| `validating` 超时 | 防火墙/出站代理、DNS 解析、CAA 记录 |
| `ACME order/account` 失败 | 目录 URL、邮箱、面板时间、EAB 凭据、CA 限额 |
| `cert.push` 后无回执 | Agent 是否在线、能力是否包含 `cert.csr.v1` |
| Agent 拒绝证书 | 私钥不匹配、SAN 不含节点域名、系统时间偏差 |
| 面板重启后无进展 | Agent 周期检查会在 5 分钟内重新发起 |
