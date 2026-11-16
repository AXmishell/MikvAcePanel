# 外部 PostgreSQL 日志库

面板默认把所有数据放在一个 SQLite 文件里，开箱即用。当日志写入开始与控制面争抢 SQLite 写锁，或需要更长的日志留存、更多维度的聚合查询时，可以把**代理日志**迁移到外部 PostgreSQL。

这是一套**双存储**架构，不是把整个面板换成 PostgreSQL：

| 数据 | 位置 | 说明 |
| --- | --- | --- |
| `users`、`nodes`、分组、证书、配置、设置、API Key、管理员 | SQLite | 强一致、事务、单文件备份 |
| `events`、`node_metrics`、`stats_batches` | SQLite | 低频遥测，与用户流量累加同事务 |
| `hy2_logs`、`log_batches` | PostgreSQL | 高写入、只追加、按时间分区 |

因此：

- 认证、订阅、节点下发、流量累计**不依赖** PostgreSQL；
- 日志是最终一致的，允许延迟或丢失，由 Agent 按 batch id 重发；
- 备份分为两部分：控制库仍用面板自带备份，日志库用 `pg_dump`。

---

## 1. 配置

> 需要 PostgreSQL 11+（分区表 + identity）。推荐 PostgreSQL 14 或更高版本。

`config/panel.yaml`：

```yaml
database:
  driver: sqlite
  path: /app/data/panel.db

telemetry:
  driver: postgres
  # DSN 必须放在文件里，不要写进 panel.yaml。
  dsn_file: /app/config/telemetry.pg.url
  schema: mikvace
  max_open: 12
  max_idle: 4
  conn_max_lifetime: "1h"
  # false：PostgreSQL 不可用时回退到本地 SQLite 日志表，面板照常启动；
  # true：启动失败并退出，适合不允许日志落地的严格部署。
  required: false
```

DSN 文件示例（权限 `0600`）：

```text
postgres://panel:CHANGE_ME@postgres:5432/mikvace?sslmode=disable
```

生产环境建议启用 TLS：

```text
postgres://panel:CHANGE_ME@postgres.example.com:5432/mikvace?sslmode=verify-full&sslrootcert=/app/config/pg-ca.pem
```

`telemetry.driver` 支持：

- `sqlite`（默认）：日志留在控制库，行为与旧版本完全一致；
- `postgres` / `postgresql` / `pg`：启用外部日志库。

### 使用安装向导

首次启动打开 `/install`，在“数据库”步骤勾选 **外部 PostgreSQL 日志库**，填写：

- PostgreSQL DSN（不会回显）；
- Schema（默认 `public`）；
- 是否在连接失败时拒绝启动。

点击“测试连接”会同时验证 SQLite、PostgreSQL 和可选的 Redis。保存时 DSN 写入 `server.data_dir/telemetry.pg.url`（权限 0600），`panel.yaml` 只记录文件路径，随后面板自动重启。也可以在安装向导中保持 SQLite 日志库，之后再手工修改配置。

### 安装后修改

管理员登录后打开 **设置 → 存储与缓存**，可以：

- 查看当前日志库、Redis 与缓存命中率；
- 启用/停用外部 PostgreSQL，修改 DSN、Schema、失败策略；
- 启用/停用 Redis，修改 URL、Key 前缀、失败策略；
- 点击“测试连接”验证后端；
- “保存并重启”写入密钥文件与 `panel.yaml`，面板自动重启。

对应 API：

```text
GET  /api/infra
POST /api/infra/test
PUT  /api/infra
```

这三个接口只接受管理员会话，API Key 无法调用。

### DSN 模板

安装向导和 **设置 → 存储与缓存** 都内置可填入的连接模板。点击“填入”会把模板写入输入框，替换其中的大写占位符即可：

PostgreSQL：

| 模板 | 值 |
| --- | --- |
| 本地 / 内网 | `postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=disable` |
| TLS | `postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=require` |
| TLS + 证书校验 | `postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=verify-full&sslrootcert=/path/to/ca.pem` |
| Keyword DSN | `host=HOST port=5432 user=USER password=PASSWORD dbname=DATABASE sslmode=disable` |

Redis：

| 模板 | 值 |
| --- | --- |
| 无密码 | `redis://HOST:6379/0` |
| 密码 | `redis://:PASSWORD@HOST:6379/0` |
| ACL 用户 | `redis://USER:PASSWORD@HOST:6379/0` |
| TLS | `rediss://:PASSWORD@HOST:6379/0` |

模板只用于填充，不会自动保存；保存前仍会校验格式并测试真实连接。占位符请全部替换，尤其是 `PASSWORD`、`HOST`、`DATABASE`。

`telemetry.schema` 默认为 `public`。面板会在首次启动时创建 schema、表和迁移记录。若数据库账号无权建 schema，请让 DBA 预创建并授权：

```sql
CREATE SCHEMA IF NOT EXISTS mikvace AUTHORIZATION panel;
GRANT USAGE, CREATE ON SCHEMA mikvace TO panel;
```

### Docker Compose 示例

```yaml
services:
  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    profiles: ["postgres"]
    environment:
      POSTGRES_DB: mikvace
      POSTGRES_USER: panel
      POSTGRES_PASSWORD_FILE: /run/secrets/pg_password
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
      - ./config/pg_password:/run/secrets/pg_password:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U panel -d mikvace"]
      interval: 10s
      timeout: 3s
      retries: 5
```

把 `postgres://panel:<密码>@postgres:5432/mikvace?sslmode=disable` 写入 `./config/telemetry.pg.url`（权限 `0600`），然后启用 compose profile：

```bash
docker compose --profile postgres up -d
```

---

## 2. 数据库与权限

最小权限示例：

```sql
CREATE ROLE panel LOGIN PASSWORD 'CHANGE_ME';
CREATE DATABASE mikvace OWNER panel;

\c mikvace
CREATE SCHEMA IF NOT EXISTS mikvace AUTHORIZATION panel;
GRANT USAGE, CREATE ON SCHEMA mikvace TO panel;
```

面板运行角色需要对 `telemetry.schema` 有 `CREATE`（自动建分区）和对自己表的 `SELECT/INSERT/UPDATE/DELETE`。如果希望运行角色完全无 DDL 权限，可以：

1. 用迁移角色先启动一次面板，完成建表和初始分区；
2. 之后回收 `CREATE`，并由 DBA 定时预建未来分区（见第 5 节）。

> 日志表不包含用户密码或私钥。`user_id`、客户端 IP、访问域名等仍属于敏感数据，请按日志数据级别保护 PostgreSQL。

---

## 3. 启动、健康检查与回退

启动日志：

```text
level=INFO msg="database ready" driver=sqlite
level=INFO msg="telemetry ready" driver=postgres schema=mikvace
```

`GET /api/health` 新增：

```json
{
  "ok": true,
  "database": { "driver": "sqlite" },
  "telemetry": { "driver": "postgres", "ok": true, "required": false }
}
```

- `required: false` 且 PostgreSQL 连接失败：面板回退到 SQLite 日志表，`/api/health` 返回 200，但 `telemetry.ok=false`，并带有 `error`；
- `required: true` 且连接失败：进程退出，由容器编排重启；
- `GET /api/readyz` 在 `required: true` 时同样检查日志库。

回退期间新日志写回 SQLite。恢复 PostgreSQL 后需要**重启面板**才会切回。回退期间产生的 SQLite 日志不会自动搬过去；如需保留，使用第 4 节的迁移工具。

---

## 4. 从 SQLite 迁移已有日志

切换配置前，可以把旧 `hy2_logs` 导入 PostgreSQL：

```bash
# 先确认 panel.yaml 已写入 telemetry 配置，且 PostgreSQL 可连接
mikvace-telemetry-migrate --config /app/config/panel.yaml --dry-run

# 正式导入（可重复执行，不会重复插入）
mikvace-telemetry-migrate --config /app/config/panel.yaml --batch 1000

# 只导入日志，不复制 Agent 幂等批次标记
mikvace-telemetry-migrate --config /app/config/panel.yaml --skip-receipts

# 从某个 SQLite 行号之后继续（可选）
mikvace-telemetry-migrate --config /app/config/panel.yaml --from-id 123456
```

迁移工具会：

- 按 `id` 顺序批量读取 SQLite 的 `hy2_logs`；
- 为历史时间戳创建对应的日分区，不会把旧日志改成当前时间；
- 用由行号派生的 batch id 保证重复执行幂等；
- 复制 `log_batches`，避免 Agent 重发旧批次时重复写入。

建议在**停止面板后**执行迁移，避免迁移过程中新日志写入造成边界批次不完整。`--from-id` 会向下对齐到 `--batch` 的整数倍；恢复时请使用相同的 `--batch`。

迁移完成后重启面板。旧的 SQLite 日志表会被保留，面板的保留策略会继续让它们按天数过期，方便随时回退。

---

## 5. 保留策略与分区

日志表按 UTC 天分区：`hy2_logs_YYYYMMDD`，父表 `hy2_logs` 按 `ts` RANGE 分区。插入时自动创建当天分区；启动时确保当天分区存在。

面板每小时执行一次清理：

1. 删除完全过期的日分区（`DROP TABLE`，不产生 bloat）；
2. 对跨越保留边界的最后一个分区执行 `DELETE ... WHERE ts < cutoff`；
3. 删除过期的 `log_batches`。

保留天数仍由 **设置 → 日志分析 → 日志保留天数** 控制。注意分区按天，因此整体误差不超过 24 小时；跨边界的部分会由第 2 步补齐。

手工预建分区（无 DDL 权限时）：

```sql
CREATE TABLE IF NOT EXISTS mikvace.hy2_logs_20261117
  PARTITION OF mikvace.hy2_logs
  FOR VALUES FROM ('2026-11-17 00:00:00+00')
               TO ('2026-11-18 00:00:00+00');
```

---

## 6. 备份与恢复

面板的“立即备份/自动备份”仍然只打包 **SQLite 控制库**（`VACUUM INTO` + `meta.json` + 证书私钥）。`meta.json` 会记录：

```json
{
  "telemetry": { "driver": "postgres", "included": false }
}
```

PostgreSQL 日志请单独备份：

```bash
pg_dump --format=custom --no-owner \
  --schema=mikvace \
  --file=/backup/mikvace-logs-$(date +%F).dump \
  postgres://panel:CHANGE_ME@postgres:5432/mikvace
```

恢复时：

- 先恢复 SQLite 控制库（面板自带流程）；
- 再按需恢复 PostgreSQL 日志；
- 两者时间点**允许不一致**，日志不是控制面恢复目标；
- 若控制库回滚到较早时间点，历史日志中的 `node_id` 可能与新库不一致；这些日志会按保留期自然过期，必要时可在恢复后手动清空日志。

如果使用云数据库，建议开启自动备份 + PITR；日志库通常可以接受比控制库更短的保留窗口。

---

## 7. 运维与排障

| 现象 | 检查 |
| --- | --- |
| 启动日志出现 `postgres telemetry unavailable` | DSN 文件权限、网络、`psql` 手工连接、`required` 配置 |
| `/api/health` 中 `telemetry.ok=false` | `telemetry.error` 字段；PostgreSQL 连接数、磁盘、慢查询 |
| 日志页为空但节点在线 | 确认 `telemetry.driver`；查看面板日志中的 `store logs` 警告；Agent 会按 batch 重发。Agent 最多保留 200 个未确认批次，长时间故障会丢弃最旧批次 |
| 日志表持续变大 | 保留天数、分区是否被删除；`log_batches` 也会随保留期清理 |
| 迁移后出现重复日志 | 迁移时使用了不一致的 `--batch`；重新导入会因 batch id 不同而重复，建议清空目标表后按固定参数重跑 |

常用 SQL：

```sql
-- 分区与大小
SELECT c.relname,
       pg_size_pretty(pg_total_relation_size(c.oid)) AS size,
       c.reltuples::bigint AS estimated_rows
FROM pg_inherits i
JOIN pg_class c ON c.oid = i.inhrelid
JOIN pg_class p ON p.oid = i.inhparent
JOIN pg_namespace n ON n.oid = p.relnamespace
WHERE n.nspname = 'mikvace' AND p.relname = 'hy2_logs'
ORDER BY c.relname;

-- 最近的日志
SELECT ts, node_id, level, event, user_id, client_ip, message
FROM mikvace.hy2_logs
ORDER BY ts DESC
LIMIT 50;
```

---

## 8. 已知边界

- **跨库 JOIN 不存在**。节点名称、用户是否存在由面板分两步查询：PostgreSQL 聚合出 id，再回 SQLite 解析名称/判断用户是否存在。
- **外键不跨库**。删除节点不会级联删除 PostgreSQL 日志；日志按保留期自然过期。
- **`stats_batches` 仍在 SQLite**，因为流量累加与用户更新必须在同一事务里。
- **搜索使用 `ILIKE '%q%'`**，不建 `pg_trgm` 索引。日志量特别大时，可自行安装扩展并添加 GIN 索引，或缩短保留期。
- **回退不是自动的**。PostgreSQL 恢复后需要重启面板才会重新启用外部日志库。
