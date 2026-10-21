# 节点 Agent

节点 Agent 是一个 Go 程序，同时管理 Hysteria 2 与 Xray 节点，负责：

1. 使用节点 Token 建立到面板的 WebSocket 长连接；
2. 接收并落盘 Hysteria 2 YAML 或 Xray JSON；
3. 生成/复用自签名证书（ACME 模式由 Hysteria 自己签发）；
4. 启动、停止、重启 Hysteria；
5. 维护端口跳跃 iptables 规则；
6. 采集 Hysteria 日志并批量上报；
7. 轮询 Hysteria `trafficStats` 或 Xray `StatsService`，上报每用户流量；
8. 每 10 秒上报 CPU / 内存 / 磁盘 / load / uptime。

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
  mikvacepanel/node:latest
```

或使用 `docker-compose.node.yml`：

```bash
PANEL_URL=https://panel.example.com NODE_TOKEN=mtn_xxx \
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
  mikvacepanel/node:latest
```

此时 Hysteria 容器使用 `tobyxdd/hysteria:latest`，挂载宿主机 `/var/lib/mikvace` 到 `/data`。Agent 在容器内看到的 `/data` 并不是宿主机路径，因此必须用 `HOST_DATA_DIR` 告诉它宿主机实际路径。

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
| `LOG_LEVEL` | `info` | `debug/info/warn/error` |

对应命令行参数：`--panel`、`--token`、`--data-dir`、`--host-data-dir`、`--runtime`、`--hysteria-bin`、`--hysteria-image`、`--container`、`--insecure`、`--log-level`。

---

## 数据目录

```
/data/
├── config/config.yaml     # 面板下发的 Hysteria 配置
├── config/config.json     # 面板下发的 Xray 配置
├── log/access.log         # Xray access log（Agent 自动 tail）
├── certs/cert.pem         # 自签名证书（selfsigned 模式）
├── certs/key.pem
└── state/meta.json        # revision / protocol / port hopping 元信息
```

进程模式下，Agent 会把配置中的 `/data/` 前缀改写为实际 `DATA_DIR`；Docker 模式下保持不变，因为 Hysteria 容器也把数据目录挂载到 `/data`。

---

## Xray / VLESS / REALITY

节点设置中的 `xray` 对象：

| 字段 | 说明 |
| --- | --- |
| `transport` | `tcp` / `ws` / `grpc` |
| `security` | `reality` / `tls` / `none` |
| `flow` | TCP + REALITY/TLS 使用 `xtls-rprx-vision` |
| `fingerprint` | uTLS 指纹，默认 `chrome` |
| `alpn` | TLS ALPN，默认 `h2,http/1.1` |
| `realityDest` | REALITY 伪装目标，默认 `www.cloudflare.com:443` |
| `realitySni` | REALITY serverNames 数组 |
| `realityPrivateKey` / `realityPublicKey` | 面板生成/保存的 x25519 密钥 |
| `realityShortIds` | REALITY short id 数组 |
| `wsPath` / `wsHost` | WebSocket 传输参数 |
| `grpcServiceName` | gRPC 服务名 |
| `apiPort` | Xray StatsService 本地端口，默认 61000 |
| `inboundTag` | VLESS 入站标签，默认 `vless-in` |



节点类型选择 Xray 后，面板生成包含全部授权用户的 `config.json`，Agent 运行：

```bash
xray run -c /data/config/config.json
```

- 支持 TCP、WebSocket、gRPC；
- 支持 REALITY、TLS（使用 Agent 生成的自签名证书）和无加密；
- REALITY 的 x25519 密钥与 short id 由面板生成；
- Xray access log 写入 `/data/log/access.log`，Agent 自动 tail 并上报；
- 用户流量通过 `xray api statsquery` 获取，按 email（userId）聚合；
- 新增/修改/禁用用户后，面板会重新渲染并下发所有 Xray 节点配置。

## SSH 自动安装

节点编辑页可保存 SSH 端口、用户、密码或私钥。点击 **Auto setup** 后，面板通过 SSH 执行安装脚本，创建/重建 `mikvace-node` 容器。要求：

- 面板服务器能访问节点 SSH；
- 使用 root 或具备 Docker 权限的用户；
- 默认不验证 SSH host key（使用 `InsecureIgnoreHostKey`），请限制面板出口 IP 并使用专用凭据；
- 可选自动执行 `get.docker.com` 安装 Docker；
- 私钥暂不支持 passphrase。

## 证书

- `tlsMode=selfsigned`：Agent 生成 ECDSA P-256 证书，10 年有效，包含节点 `domain` 或 `ip` 的 SAN；如果现有证书不含当前主机名或即将过期（7 天内）会重新生成。
- `tlsMode=acme`：面板生成 `acme.domains` 配置，由 Hysteria 内置 ACME 处理。节点域名必须解析到该节点，443/TCP 和 443/UDP 可用。

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
