# 日志解析与规则分析

## 数据流

```
Hysteria stdout/stderr
   │  Agent 按 2s / 100 行聚合
   ▼
WS {type:"logs", payload:{batchId, entries:[{ts,level,message,raw}]}}
   │  (未 ack 每 10s 重发 / 重连重发)
   ▼
POST 解析 → hy2_logs 表（按 node_id + batch_id 幂等）
   │
   ├─ /api/logs          分页检索
   ├─ /api/logs/summary  聚合 + 规则 findings
   └─ /api/dashboard     最近日志 + 全部 findings
```

## 解析规则

支持 Hysteria 默认的 zap console 格式与 Xray 的 access/error 日志格式：

```
2026-10-21T15:33:51+08:00	INFO	client connected	{"addr": "1.2.3.4:38499", "id": "alice", "tx": 0}
```

- 时间：RFC3339 / `2006-01-02T15:04:05.000Z0700` / Unix 秒或毫秒；解析失败时用接收时间；
- 级别：INFO / WARN / ERROR / FATAL / DEBUG；
- logger 字段可选（3 段或 4 段 header 都支持）；
- 末尾 JSON 字段解析：`addr`、`id`、`auth`、`reqAddr`、`host`、`tx`、`rx`、`error`、`duration` 等；
- 兼容 `time=... level=... msg=...` logfmt；
- Xray access：`2026/10/21 16:29:05 from 1.2.3.4:1234 accepted tcp:example.com:443 [vless-in >> direct] email: alice`，解析为 `connect` / `error`（rejected）；
- Xray error：`2026/10/21 16:29:05 [Warning] ...`，解析级别并分类；
- Xray `API_INBOUND` 内部统计连接标记为 `stats`，不计入客户端连接。
- 去除 ANSI 控制字符；
- 自动推断 `client_ip`、`port`、`user_id`、`host`、`error`。

事件分类：

| event | 条件 |
| --- | --- |
| `auth_success` | authentication succeeded / auth success |
| `auth_fail` | authentication failed / failed to authenticate / unauthorized |
| `connect` | client connected |
| `disconnect` | client disconnected |
| `start` | server up and running / server started |
| `shutdown` | shutting down / received signal / server stopped / stopping |
| `stats` | traffic stats server |
| `acme` | 包含 acme / certificate |
| `tls` | 包含 tls / handshake / quic |
| `network_error` | TCP error / UDP error |
| `error` | 级别 ERROR/FATAL 或带 error 字段 |
| `warning` | 级别 WARN |
| `log` | 其他 |

面板自身的认证拒绝也会写入日志，`logger=panel-auth`，方便在 Hysteria 不记录失败原因时仍能发现暴力破解。

## 规则 findings

分析窗口由 `range` 决定（`1h/6h/24h/7d/30d`，默认 24h），节点可筛选。

| ID | 严重级别 | 规则 |
| --- | --- | --- |
| `node-error-<id>` | critical | 节点最近配置被 Agent 拒绝 / 应用失败 |
| `node-offline-<id>` | warning | 活跃节点当前不在线 |
| `bruteforce-<ip>` | warning / critical | 单 IP 认证失败 ≥ 阈值；≥3 倍阈值升级 critical |
| `unknown-users` | warning | 认证失败里不存在的 userId 总数 ≥5 |
| `error-burst-<node>` | warning | 单节点 error 事件 ≥ 阈值 |
| `restart-loop-<node>` | warning | 单节点 10 分钟内非计划启动 ≥3 次，或窗口内平均重启 ≥1 次/小时；启动前 2 分钟内有 shutdown 记录视为主动重启并排除 |
| `auth-failure-ratio` | warning | 认证失败率 ≥30%，且样本 ≥20 |
| `tls-errors-<node>` | info | 单节点 TLS 事件 ≥5 |
| `acme-errors-<node>` | info | 单节点 ACME 事件 ≥5 |
| `quota-exceeded` | warning | 有用户达到流量上限 |

阈值：

- `alert_auth_fail_threshold`：默认 10；
- `alert_error_burst_threshold`：默认 20；
- 可在 **Settings → Log analysis** 修改。

## 聚合时间线

`/api/logs/summary` 的 `timeline` 会根据窗口自动选择 bucket：

| 窗口 | bucket |
| --- | --- |
| ≤3h | 5 分钟 |
| ≤12h | 15 分钟 |
| ≤48h | 1 小时 |
| ≤8d | 6 小时 |
| 更长 | 1 天 |

没有数据的 bucket 会补 0，前端据此绘制连续曲线。

## 保留与清理

- `log_retention_days` 默认 14 天；调度器每小时清理 `hy2_logs`、`log_batches`、`events`、`stats_batches`（`stats_batches` 至少保留 7 天，保证 Agent 重传幂等）；
- `metric_retention_days` 默认 30 天，控制 `node_metrics` 心跳与流量采样；
- 设置 → 日志分析 → 数据清理 可查看当前行数、数据库大小与可回收空间，并立即执行一次清理；
- `DELETE` 不会缩小数据库文件，空闲页需要回收（`VACUUM` 或 `PRAGMA incremental_vacuum`）。调度器每小时清理后会自动回收：空闲空间超过 64 MiB 或占文件 25% 以上时执行一次 `VACUUM` 并把数据库切换为增量自动回收模式，其余情况只做增量回收；`DELETE /api/logs` 清空日志后同样会自动回收；
- 勾选“完整压缩数据库”会在清理后强制执行 `VACUUM`（重写整个文件，可能短暂阻塞写入）；
- 管理 API `POST /api/maintenance/cleanup`（可传 `{"vacuum":true,"logRetentionDays":14,"metricRetentionDays":30}`）与 `GET /api/maintenance/usage` 提供同样的能力，后者返回 `liveBytes`/`freeBytes`/`freelistPages`/`autoVacuum`；
- 管理 API `DELETE /api/logs?before=...` 可手动清空全部/部分代理日志；
- 日志采集可在设置中关闭，关闭后 Agent 批次仍会被 ack 但不会落库。

> 配置 `telemetry.driver: postgres` 后，`hy2_logs` 与 `log_batches` 存放在 PostgreSQL 的按天分区表中。保留策略改为优先 `DROP` 完全过期的分区，再对边界分区执行 `DELETE`；SQLite 侧的 `VACUUM`/空闲页回收只针对控制库，不再影响日志存储。详见 [外部 PostgreSQL 日志库](telemetry-postgres.md)。

> 备份是完整的 SQLite 快照，日志和监控采样会一起进入 `tar.gz`。日志外置后，面板备份只包含控制库；缩小备份体积最有效的方式是降低 `metric_retention_days`（采样数据通常占数据库的大部分），必要时再执行一次带 `VACUUM` 的清理。

## 性能考虑

- 单批最多 1000 条（Agent 实际 100 条）；
- `(node_id, ts)`、`(event, ts)`、`(client_ip, ts)`、`(user_id, ts)` 均有索引；
- 时间线按分钟聚合，再在 Go 里合并为更粗粒度：SQLite 使用 `substr(ts,1,16)`，PostgreSQL 使用 `date_trunc('minute', ts AT TIME ZONE 'UTC')`；
- 分页接口最多 500 行/页。
