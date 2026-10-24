# 常见问题

## 面板故障后客户端还能连接吗？

可以，取决于使用的核心：

- **Xray**：用户 UUID/密码本来就写在节点本地 `config.json` 中，面板故障不会影响正在运行的 Xray；Agent 重启也会从本地配置启动。
- **Hysteria 2**：新版 Agent 会把认证地址改写为 `127.0.0.1:9998` 的本地网关。面板可达时网关代理到面板，行为不变；面板不可达时使用 `DATA_DIR/state/users.json` 的最后一次用户快照。需要保持 `OFFLINE_AUTH=true`（默认）。

离线期间的新密码、禁用、配额和设备数判断都基于最后一次快照，无法实时同步；面板恢复后 Agent 会自动刷新快照。详见 [节点 Agent](node-agent.md#面板故障时的离线认证)。

## 节点一直离线
1. 在节点上查看 Agent 日志：

   ```bash
   docker logs -f mikvace-node
   ```

2. 确认 `PANEL_URL` 能从节点访问，且协议为 `https://`（或内网 `http://`）；
3. 确认反向代理支持 WebSocket Upgrade（`/api/node/ws`）；
4. 确认节点 Token 未被重置：重置后需要重新执行安装命令；
5. 面板使用自签名 HTTPS 时，设置 `PANEL_INSECURE=true`。

## Agent 返回 401 / invalid node token

- Token 与面板数据库不匹配，通常是因为重置了 Token 或更换了数据库；
- 在面板节点页重新复制安装命令；
- 如果恢复过数据库，确认恢复的是包含该节点的库。

## 配置被拒绝

查看面板 **日志** 或节点日志，常见原因：

- 端口被占用；
- 证书路径不存在或私钥不匹配；
- ACME 域名不可达 / 443 被占用；
- `HY2_RUNTIME=docker` 时 `HOST_DATA_DIR` 与 Agent 数据目录不一致；
- Xray 配置字段非法；
- REALITY 只支持 TCP / XHTTP / gRPC 传输；
- Shadowsocks 多用户只支持 `2022-blake3-aes-128-gcm` / `2022-blake3-aes-256-gcm`；
- 双核心节点需要 Agent 具备 `multi-core.v1` 能力（升级节点镜像即可）。

Agent 会尝试回滚旧配置；修复后点击“同步”或重启节点。

## Docker 运行时证书/配置不可见

Agent 容器内的 `/data` 和兄弟代理容器的 `/data` 必须指向宿主机同一目录：

```text
agent:   -v /var/lib/mikvace:/data
env:     HOST_DATA_DIR=/var/lib/mikvace
sibling: -v /var/lib/mikvace:/data
```

- `HOST_DATA_DIR` 必须是绝对路径且存在；
- 使用 Docker 命名卷时，不能把命名卷名当作 `HOST_DATA_DIR`；
- 如果 Agent 的 `/data` 是命名卷，兄弟容器看不到 Agent 生成的证书。

## 端口跳跃不生效

- 需要 `--network host` 与 `--cap-add NET_ADMIN`；
- 节点防火墙放行整个 UDP 端口段；
- 检查 iptables：

  ```bash
  iptables -t nat -L PREROUTING -n -v
  ```

- 规则由 Agent 执行；Agent 容器使用 `--network host` 时直接作用于宿主机 netns。
- 目前仅 IPv4。

## 证书签发失败

### 面板签发（managed）

- 节点必须有域名，并解析到节点；
- 面板需要能访问 ACME 与 DNS 服务商 API；
- Cloudflare / 阿里云 / DNSPod 凭据权限是否正确；
- 手动 TXT 模式需要在 DNS 添加面板显示的记录，并等待生效后点击“验证并继续”；
- 查看 **设置 → 证书签发** 与节点证书状态中的 `lastError`。

### 节点自签 ACME

- 只能使用 TLS-ALPN，需要 443/TCP 可用；
- 443/UDP 由 Hysteria 使用，两者不冲突；
- 域名必须解析到节点；
- ACME 缓存位于 `/data/acme`，不要删除。

## 订阅链接指向 localhost

修改 **设置 → 常规 → 公开地址** 为实际访问地址，例如：

```text
https://panel.example.com
```

保存后节点会在下一次配置同步时更新认证 URL；订阅链接立即生效。

## 2FA 无法登录

1. 使用保存的恢复码（每个只能用一次）；
2. 恢复码也丢失时，停止面板并重置数据库字段：

   ```sql
   UPDATE admins
   SET totp_enabled = 0, totp_secret = '', totp_pending = '',
       totp_recovery = '[]', totp_pending_recovery = '[]'
   WHERE username = 'admin';
   ```

3. 重启面板后用密码登录，重新启用 2FA。

如果 `data/cert.key` 丢失但 `data/jwt.secret` 仍在，恢复码仍然可用。

## 恢复备份后数据没变化

上传备份只是暂存为 `panel.db.restore`，需要 **重启面板** 才会应用。重启前的数据库会保留为 `panel.db.pre-restore-<时间>`。

如果重启后看到的是旧数据，检查 `data/` 下是否存在多个 `panel.db.*` 文件，确认启动参数 `-data` 指向正确目录。

## 面板 502 / WebSocket 断线

- 反向代理转发 `Upgrade` / `Connection`；
- 增加 `proxy_read_timeout`；
- 云负载均衡需要开启 WebSocket；
- 检查面板日志与 `docker logs`。

## 时间不同步导致 2FA / 证书异常

TOTP 和 ACME 都依赖系统时间。请启用 NTP：

```bash
timedatectl set-ntp true
timedatectl status
```

## 日志不显示

- **设置 → 日志分析** 中日志采集是否开启；
- Agent 是否能连接面板；
- 节点日志是否已经产生；
- 保留天数是否过短；
- Xray 需要 access log 路径可写（`/data/log/access.log`）。

## 如何完全重置面板

停止面板并备份 `data/`，然后删除或改名：

```text
data/panel.db
data/panel.db-wal
data/panel.db-shm
data/jwt.secret
data/cert.key
```

重新启动会创建空数据库和新密钥。节点需要重新添加并重新安装 Agent。

## 如何备份

优先使用面板 **设置 → 备份 → 立即备份**。归档包含数据库、`meta.json` 和 `cert.key`。手动备份时至少保存：

```text
data/panel.db
data/panel.db-wal
data/panel.db-shm
data/jwt.secret
data/cert.key
data/backups/
```
