# 节点 Agent

节点 Agent 是一个 Go 程序，同时管理 Hysteria 2 与 Xray 节点，负责：

1. 使用节点 Token 建立到面板的 WebSocket 长连接；
2. 接收并落盘 Hysteria 2 YAML 或 Xray JSON；
3. 生成/复用自签名证书；managed 模式向面板提交 CSR 并安装面板签发的证书；
4. 启动、停止、重启 Hysteria；
5. 维护端口跳跃 iptables 规则；
6. 采集 Hysteria 日志并批量上报；
7. 轮询 Hysteria `trafficStats` 或 Xray `StatsService`，上报每用户流量；
8. 每 10 秒上报 CPU / 内存 / 磁盘 / load / uptime；
9. 维护本地用户认证快照，面板故障时继续为 Hysteria 新连接认证。

---

## 部署方式

### 方式 A：单容器（推荐）

面板内 **Nodes → Install** 会生成带 Token 的完整命令。镜像内已经包含 Hysteria：

```bash
docker run -d --name mikvace-node --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
  -e PANEL_URL="https://panel.example.com" \
  -e NODE_TOKEN="mtn_xxx" \
  -e HY2_RUNTIME=process \
  -v /var/lib/mikvace:/data \
  ghcr.io/axmishell/mikvacepanel-node:latest
```

或使用 `docker-compose.node.yml` 与节点模板：

```bash
cp .env.node.example .env
# 填写 PANEL_URL、NODE_TOKEN 等
docker compose -f docker-compose.node.yml up -d
```

### 方式 B：Agent 管理 Hysteria 容器

Agent 自己也在容器里，但通过挂载 Docker socket 启动同机的 Hysteria 容器：

```bash
docker run -d --name mikvace-node --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
  -e PANEL_URL="https://panel.example.com" \
  -e NODE_TOKEN="mtn_xxx" \
  -e HY2_RUNTIME=docker \
  -e HOST_DATA_DIR=/var/lib/mikvace \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/mikvace:/data \
  ghcr.io/axmishell/mikvacepanel-node:latest
```

此时 Hysteria 容器使用 `tobyxdd/hysteria:latest`，挂载宿主机 `/var/lib/mikvace` 到 `/data`。Agent 在容器内看到的 `/data` 并不是宿主机路径，因此必须用 `HOST_DATA_DIR` 告诉它宿主机实际路径。

### 在面板中选择运行方式

节点编辑页的 **Agent 运行方式** 可以让每个节点独立选择：

| 选项 | 说明 |
| --- | --- |
| 进程模式（`process`） | Agent 在自身容器/主机内直接启动 Hysteria 与 Xray 二进制，默认方式 |
| Docker 模式（`docker`） | Agent 通过挂载的 Docker socket 管理独立的代理容器 |

选择 Docker 模式后，面板生成的安装命令、`docker-compose.yml` 和 SSH 自动安装脚本都会自动加入：

- `HY2_RUNTIME=docker`
- `HOST_DATA_DIR=/var/lib/mikvace`
- `-v /var/run/docker.sock:/var/run/docker.sock`

修改运行方式不会远程切换正在运行的 Agent，需要点击 **安装** 重新执行命令或使用 **自动安装** 重建容器。节点卡片会显示所选模式和 Agent 实际回报的运行方式；两者不一致时会标记为需要重新安装。

> 挂载 `/var/run/docker.sock` 等同于给 Agent 宿主机 root 权限，请只在受控节点上启用 Docker 模式。

### 方式 C：二进制 + systemd

```bash
go build -o /usr/local/bin/mikvace-node ./cmd/node
```

`/etc/systemd/system/mikvace-node.service`：

```ini
[Unit]
Description=MikvAce Node Agent
After=network-online.target

[Service]
Type=simple
Environment=PANEL_URL=https://panel.example.com
Environment=NODE_TOKEN=mtn_xxx
Environment=DATA_DIR=/var/lib/mikvace
Environment=HY2_RUNTIME=process
Environment=HYSTERIA_BIN=/usr/local/bin/hysteria
ExecStart=/usr/local/bin/mikvace-node
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

需要以 root 运行（绑定 443、写 iptables、读 `/proc`）。

---

## 环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `PANEL_URL` | — | 面板地址，`http(s)://`；Agent 自动转换成 `ws(s)://.../api/node/ws` |
| `NODE_TOKEN` | — | 节点 Token |
| `DATA_DIR` | `/data` | 配置、证书、状态目录 |
| `HOST_DATA_DIR` | `/var/lib/mikvace` | Docker 模式下的宿主机数据路径 |
| `HY2_RUNTIME` | `process` | `process` 或 `docker` |
| `HYSTERIA_BIN` | `hysteria` | 进程模式 Hysteria 路径 |
| `XRAY_BIN` | `xray` | 进程模式 Xray 路径 |
| `HY2_IMAGE` | `tobyxdd/hysteria:latest` | Hysteria Docker 模式镜像 |
| `XRAY_IMAGE` | `ghcr.io/xtls/xray-core:latest` | Xray Docker 模式镜像 |
| `HY2_CONTAINER` | `mikvace-hysteria` | Docker 模式容器名 |
| `PANEL_INSECURE` | `false` | 跳过面板 TLS 校验（仅自签名面板） |
| `OFFLINE_AUTH` | `true` | 面板故障时使用本地用户快照继续为 Hysteria 认证 |
| `AUTH_LISTEN` | `127.0.0.1:9998` | 本地认证网关地址；只允许回环地址 |
| `LOG_LEVEL` | `info` | `debug/info/warn/error` |

对应命令行参数：`--panel`、`--token`、`--data-dir`、`--host-data-dir`、`--runtime`、`--hysteria-bin`、`--hysteria-image`、`--container`、`--insecure`、`--log-level`、`--offline-auth`、`--auth-listen`。

---

## 数据目录

```
/data/
├── config/config.yaml     # 面板下发的 Hysteria 配置
├── config/config.json     # 面板下发的 Xray 配置
├── log/access.log         # Xray access log（Agent 自动 tail）
├── certs/cert.pem         # 自签名证书（selfsigned 模式）
├── certs/key.pem
├── certs/ech.pem          # 可选的 ECH 私钥 + 客户端配置（面板下发）
├── certs/client_ca.pem    # 可选的 mTLS 客户端 CA（面板下发）
├── state/meta.json        # revision / protocol / port hopping 元信息
├── state/users.json       # 离线认证快照（0600，包含用户连接密码）
└── state/auth.key         # 本地认证网关密钥（0600）
```

进程模式下，Agent 会把配置中的 `/data/` 前缀改写为实际 `DATA_DIR`；Docker 模式下保持不变，因为 Hysteria 容器也把数据目录挂载到 `/data`。

`ech.pem` 与 `client_ca.pem` 随 `config` 消息的 `files` 字段下发：`ech` 对应 `/data/certs/ech.pem`（0600），`clientCA` 对应 `/data/certs/client_ca.pem`（0644）。面板不会把 ECH 私钥返回给浏览器，只保留从 `ECH CONFIGS` 块提取的客户端配置列表。

---

## 面板故障时的离线认证

Hysteria 2 默认把认证请求回调到面板 `/api/auth`。面板进程故障、数据库不可用或网络中断时，新连接会全部认证失败。节点 Agent 现在在本地维护一个认证网关：

```text
客户端 ── QUIC ──▶ Hysteria 2 ── HTTP auth ──▶ 127.0.0.1:9998（Agent 本地网关）
                                                      │
                                   面板可达 ──────────┼──▶ 面板 /api/auth（权威认证）
                                   面板不可达 ────────┴──▶ state/users.json（最后一次快照）
```

### 工作方式

1. 面板在 Agent 连接后（`hello`）立即推送 `user.snapshot`，之后每 5 分钟由 Agent 请求一次；
2. 用户、分组、节点可见性、密码、启用状态、到期时间或流量配额变化时，面板会主动补推；
3. Agent 把快照原子写入 `state/users.json`（0600），并只对 `hysteria` 节点生效；
4. Agent 把下发配置里的 `auth.http.url` 改写为 `http://127.0.0.1:9998/api/auth?...`，无需修改面板模板；
5. 面板可达时，网关原样代理请求，面板的设备限制、在线状态、认证日志和配额判断都不变；
6. 控制通道断开或面板认证接口连续失败时，网关切换到快照认证，并保留 15 秒重试间隔。

Xray 不需要这套机制：用户 UUID/密码本来就写在 `config.json` 中，面板故障时 Xray 继续使用本地配置，Agent 重启也会从本地配置启动两个核心。

### 离线判断规则

| 条件 | 来源 |
| --- | --- |
| 用户存在且密码匹配 | 快照 |
| `enabled` | 快照（面板只下发启用用户） |
| 到期时间 | 快照中的 `expire_at`，离线期间按本机时间判断 |
| 流量上限 | 快照中的最后一次 `traffic_tx/rx` |
| 设备数 | 快照中的有效设备上限 + 本机 IP 宽限期计数 |

快照是“最后一次已知状态”，因此面板故障期间：

- 面板上刚修改的密码/禁用操作，如果快照尚未送达，可能仍会短暂生效；
- 用户离线期间继续消耗的流量无法实时写回面板，可能超过快照中的上限；
- 设备数由节点本地重新计数，不与面板内存中的设备表共享；
- 节点重启后仍可离线认证，快照和密钥都在 `DATA_DIR/state/`。

面板恢复后会自动重连并刷新快照；建议定期备份 `DATA_DIR/state/users.json` 与 `auth.key`，或直接备份整个数据目录。

### 部署要求

- **必须是 host 网络**：官方 Compose 使用 `network_mode: host`。Docker 模式下 Hysteria 是兄弟容器，只有 host 网络才能访问 Agent 的 `127.0.0.1:9998`。
- 同一台宿主机运行多个节点时，用 `AUTH_LISTEN=127.0.0.1:9997` 等为每个节点分配不同端口。
- 网关只绑定回环地址，并校验 `state/auth.key`；不要把 `AUTH_LISTEN` 改成 `0.0.0.0`（Agent 会拒绝启动）。
- 快照包含 Hysteria 连接密码，`state/` 目录和 `users.json` 必须保持仅 root 可读；面板与 Agent 之间应使用 WSS/HTTPS。
- 不需要离线认证时可设置 `OFFLINE_AUTH=false`，Agent 会恢复为直接回调面板。

---

## Xray：多协议 / 多入站

节点设置中的 `xray` 对象：

```json
{
  "apiPort": 61000,
  "inbounds": [
    {
      "protocol": "vless",
      "tag": "vless-in",
      "port": 443,
      "transport": "tcp",
      "security": "reality",
      "realityDest": "www.cloudflare.com:443",
      "realitySni": ["www.cloudflare.com"],
      "realityPublicKey": "...",
      "realityShortIds": ["ab12cd34"]
    },
    {
      "protocol": "vmess",
      "tag": "vmess-ws",
      "port": 8443,
      "transport": "ws",
      "security": "tls",
      "wsPath": "/vm"
    }
  ]
}
```

`inbounds[]` 每个条目的主要字段：

| 字段 | 说明 |
| --- | --- |
| `protocol` | `vless` / `vmess` / `trojan` / `shadowsocks` |
| `enabled` | 是否启用；缺省为 `true`，禁用后保留端口/密钥但不渲染、不下发订阅 |
| `port` / `listen` | 入站端口和监听地址；端口留空使用节点端口 |
| `transport` | `tcp` / `ws` / `grpc` / `xhttp` / `mkcp` |
| `security` | `reality` / `tls` / `none`；Shadowsocks 固定 `none` |
| `flow` | VLESS + TCP 使用 `xtls-rprx-vision` |
| `fingerprint` | uTLS 指纹，默认 `chrome` |
| `alpn` | TLS ALPN，默认 `h2,http/1.1` |
| `realityDest` / `realitySni` | REALITY 伪装目标与 serverNames |
| `realityPrivateKey` / `realityPublicKey` | x25519 密钥；面板生成，私钥不回传浏览器 |
| `realityShortIds` / `realitySpiderX` | REALITY short id 与 spiderX |
| `wsPath` / `wsHost` | WebSocket 参数 |
| `grpcServiceName` | gRPC 服务名 |
| `xhttpPath` / `xhttpHost` / `xhttpMode` | XHTTP 参数 |
| `kcpSeed` | mKCP seed |
| `ssMethod` | Shadowsocks 2022 加密方式（支持多用户与按用户统计） |

`apiPort` 是 Xray StatsService 的本地端口，默认 `61000`，只监听 `127.0.0.1`。

面板还支持三个全局 Xray 选项，保存在节点 `xray` 对象中：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `logLevel` | `warning` | 生成配置的 `log.loglevel` |
| `routeStrategy` | `IPIfNonMatch` | 生成配置的 `routing.domainStrategy` |
| `sniffing` | 启用，`http/tls/quic`，`routeOnly=true` | 每个入站的 `sniffing` 块；可整体关闭 |

旧节点没有这些字段时自动使用默认值；新的「Xray 设置」页签可修改。

节点启用 Xray 后，面板生成包含全部授权用户的 `config.json`，Agent 运行：

```bash
xray run -c /data/config/config.json
```

- 客户端凭据：VLESS/VMess 使用用户 UUID，Trojan 使用用户密码，Shadowsocks 使用由 UUID 派生的 2022 密钥；
- Xray access log 写入 `/data/log/access.log`，Agent 自动 tail 并上报；
- 用户流量通过 `xray api statsquery` 获取，按 email（userId）聚合；Docker 模式通过 `docker exec` 调用；
- 配置下发前 Agent 会执行 `xray run -test` 预检，失败时恢复上一份配置；
- 新增/修改/禁用用户后，面板会重新渲染并下发所有 Xray 节点配置。

## 双核心节点

节点保存 `cores: ["hysteria", "xray"]` 时，面板生成一份 Hysteria YAML 和一份 Xray JSON，并通过同一条 `config` 消息的 `configs` 映射下发：

```json
{
  "revision": 12,
  "cores": ["hysteria", "xray"],
  "configs": { "hysteria": "...", "xray": "..." },
  "tlsMode": "managed",
  "domain": "node.example.com",
  "host": "node.example.com",
  "port": 443,
  "portRange": ""
}
```

- Agent 为每个核心维护独立 runtime、配置文件和统计轮询器；
- Docker 模式下两个核心分别运行在 `mikvace-hysteria` 与 `mikvace-hysteria-xray` 容器；
- 端口跳跃只作用于 Hysteria（UDP），Xray 入站使用自己的 TCP 端口；
- 在线数与流量为两个核心之和；日志统一上报；
- Agent 通过 `multi-core.v1` capability 协商，旧 Agent 只接收主核心配置。

## SSH 自动安装

节点编辑页可保存 SSH 端口、用户、密码或私钥。点击 **Auto setup** 后，面板通过 SSH 执行安装脚本，创建/重建 `mikvace-node` 容器。要求：

- 面板服务器能访问节点 SSH；
- 使用 root 或具备 Docker 权限的用户；
- 首次连接会以 TOFU（首次信任）方式固定节点 SSH host key，后续安装会严格校验；也可以在节点 API/表单中预先填写 `sshHostKey`（authorized_keys 格式）来避免首次信任风险；
- 可选自动执行 `get.docker.com` 安装 Docker；
- 自动安装会使用节点所选的 **Agent 运行方式**；Docker 模式会额外检查并挂载 `/var/run/docker.sock`；
- 私钥暂不支持 passphrase。

## 证书

- `tlsMode=selfsigned`：Agent 生成 ECDSA P-256 证书，10 年有效，包含节点 `domain` 或 `ip` 的 SAN；如果现有证书不含当前主机名或即将过期（7 天内）会重新生成。
- `tlsMode=managed`：Agent 生成私钥与 CSR，面板通过 ACME DNS-01 签发后通过 WebSocket 下发证书链；私钥不出节点。详见 [certificates.md](certificates.md)。
- `tlsMode=acme`（旧模式）：面板生成 `acme.domains` 配置，由 Hysteria 内置 ACME 处理。已强制使用 TLS-ALPN（`disableHTTP: true`），**不占用 80 端口**；需要 443/TCP 与 443/UDP 可用，且 ACME 缓存写入 `/data/acme`。

自签名模式下，订阅中会带 `insecure=1`，客户端跳过证书校验。

---

## 端口跳跃

节点设置 `portRange` 后：

- 订阅生成 `mport` 与 `mportHopInt`；
- Agent 在节点上执行等价规则：

```bash
iptables -t nat -A PREROUTING -p udp --dport 20000:50000 \
  -j REDIRECT --to-ports 443
```

- 配置变化时会删除旧 range 规则并安装新规则；
- 需要 `--cap-add NET_ADMIN` 与 `--network host`；
- IPv6 端口跳跃暂未实现。

---

## 日志上报细节

- 进程模式：读取子进程 stdout/stderr；
- Docker 模式：`docker logs -f --tail 0`；
- 每 2 秒或累计 100 行发送一个批次；
- 每批带随机 batch id，未收到 `log.ack` 时每 10 秒重发；
- 内存中最多保留 200 个未确认批次，超出后丢弃最旧的批次；
- 面板关闭日志采集时仍会 ack，Agent 不会无限重试。

---

## 流量统计

Agent 每 15 秒请求：

```
GET http://127.0.0.1:<statsPort>/traffic?clear=1
GET http://127.0.0.1:<statsPort>/online
Authorization: <statsSecret>
```

把用户维度增量发给面板；面板用 `(node_id, batch_id)` 唯一索引保证只应用一次。在线用户列表为空时也会发送，以便面板清除过期的在线状态。

---

## 故障排查

```bash
docker logs -f mikvace-node
```

- `invalid node token`：在面板重置 Token 并更新启动命令。
- `port hopping rules: ...`：容器缺少 NET_ADMIN、iptables 或使用了非 host 网络。
- `hysteria exited after applying the configuration`：查看同批次 Hysteria 日志（面板 Logs 页面或容器日志），常见原因是端口被占用、证书路径错误、ACME 域名不可达。
- 面板显示节点离线：检查面板 URL 是否可从节点访问、反代是否支持 WebSocket Upgrade、`PANEL_INSECURE` 是否正确。
