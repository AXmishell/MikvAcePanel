# 服务器与节点测试方案

本文档覆盖面板服务器、节点 Agent、Xray 多协议、同机双核心、订阅输出和真实客户端连通性。

## 测试目标

1. Xray 多协议配置生成正确，并能通过 `xray -test`；
2. 协议启用/禁用只影响对应入站，不影响其他协议；
3. 同机双核心（Hysteria 2 + Xray）互不干扰；
4. 订阅输出与实际配置一致；
5. 流量、在线数、日志统计准确；
6. 配置失败能回滚，Agent 断线能恢复；
7. 旧节点、旧 Agent、旧数据库可兼容。

## 测试拓扑

```text
Panel (8080 API / 5173 UI)
   │
   ├── Node A：Hysteria 2 only
   ├── Node B：Xray only
   │      ├── VLESS + REALITY :443
   │      ├── VMess + WS + TLS :8443
   │      ├── Trojan + gRPC + TLS :8444
   │      └── Shadowsocks 2022 :8445
   └── Node C：Hybrid
          ├── Hysteria 2 UDP :443
          └── Xray TCP :443 / :8443
```

客户端矩阵：

- Xray / v2rayN：VLESS、VMess、Trojan、SS、XHTTP、mKCP；
- sing-box：VLESS/VMess/Trojan/SS、REALITY；
- Clash Meta / Mihomo：订阅 YAML；
- Shadowrocket：Base64 订阅；
- Hysteria 2 客户端：Hy2 链路。

## 分层测试

| 层级 | 内容 | 执行方式 |
| --- | --- | --- |
| L0 静态 | 编译、vet、前端构建 | `go build ./...`、`go vet ./...`、`npm run build` |
| L1 单元/集成 | xrayconfig、subscription、store、server | `go test ./...` |
| L2 API 冒烟 | 健康检查、登录、节点/用户、配置、订阅 | `scripts/smoke.sh` |
| L3 Agent | 真实 xray/hysteria 二进制 | Docker 节点 + 配置下发 |
| L4 端到端 | 真实客户端连通与计费 | 手工/脚本 |
| L5 稳定性/安全 | 长稳、并发、越权、回滚 | 专项 |

## 自动化冒烟

仓库自带 `scripts/smoke.sh`，默认执行 L0、L1，并启动一个临时面板做 API 冒烟测试（不会污染现有数据库）。也可以直接运行 `make smoke`。

```bash
# 默认：L0 + L1 + 临时面板 API 冒烟
./scripts/smoke.sh

# 额外执行前端构建（输出到临时目录，不改动 web/dist）
./scripts/smoke.sh --web

# 只跑 L0/L1
./scripts/smoke.sh --no-api

# 对已运行的面板做 API 冒烟
SMOKE_PANEL_URL=http://127.0.0.1:8080 \
SMOKE_PANEL_TOKEN=<admin-token> \
./scripts/smoke.sh

# 或者使用用户名密码登录
SMOKE_PANEL_URL=http://127.0.0.1:8080 \
SMOKE_PANEL_USERNAME=admin \
SMOKE_PANEL_PASSWORD=<password> \
./scripts/smoke.sh
```

| 环境变量 | 说明 |
| --- | --- |
| `SMOKE_PANEL_URL` | 已运行的面板地址；留空则启动临时面板 |
| `SMOKE_PANEL_TOKEN` | 管理员 token，优先级高于用户名密码 |
| `SMOKE_PANEL_USERNAME` | 登录用户名，默认 `admin` |
| `SMOKE_PANEL_PASSWORD` | 登录密码 |
| `KEEP_TMP=1` | 保留临时数据目录和日志，便于排查 |

脚本覆盖：

- `go build ./...`、`go vet ./...`、`go test ./...`；
- `GET /api/health`、未授权访问、管理员登录；
- 创建 Xray 节点：VLESS 启用、VMess 禁用；
- 检查 `configs.xray` 只包含 VLESS；
- 检查 API 不返回 REALITY 私钥；
- 创建用户并检查 `uri` / `clash` / `singbox` / `xray-json` 四种订阅；
- 全部协议禁用时创建节点返回 400；
- 创建 hybrid 节点并检查同时生成 Hysteria YAML 和 Xray JSON；
- 清理测试用户和节点。

## 安装向导测试

```bash
go test ./internal/server -run 'TestInstall' -count=1
```

覆盖首次管理员创建、重复初始化拒绝、SQLite / 远程数据库配置保存、DSN 写入 0600 文件、状态接口不泄露 DSN，以及安装完成后数据库接口关闭。远程保存路径使用注入的连接检查，不需要真实 PostgreSQL / MySQL。

另覆盖 `database.driver: setup` 安装器模式：启动后不创建 SQLite 文件、`setupMode` 状态正确、创建管理员被拒绝、保存首个数据库后触发重启。

## 自定义首页测试

```bash
go test ./internal/server -run 'TestHomepage' -count=1
```

覆盖 HTML 上传、ZIP 资源与顶层目录归一化、启用前不替换 `/`、沙箱 CSP / `X-Frame-Options`、预览 token、ZIP 路径穿越拒绝、未认证上传拒绝、恢复内置首页，以及自定义首页随 SQLite 备份创建和恢复。

## 远程数据库测试

SQLite 测试默认运行。PostgreSQL 18 / MySQL 8.0 使用同一套 store 契约测试，按环境变量启用：

```bash
TEST_POSTGRES_DSN='postgres://test:test@127.0.0.1:55432/mikvace?sslmode=disable' \
TEST_MYSQL_DSN='test:test@tcp(127.0.0.1:53306)/mikvace?charset=utf8mb4&collation=utf8mb4_bin&parseTime=false&loc=UTC' \
go test ./internal/store -run 'TestRemote' -v
```

覆盖：

- `schema` 元数据生成三方言 DDL；
- 迁移版本表、重复启动幂等；
- 用户、分组、节点、配置 revision、证书、日志、指标、API Key、设置、批次幂等；
- `INSERT IGNORE` / `UPSERT` / `substr` 时间分桶 / `ONLY_FULL_GROUP_BY` 兼容；
- 旧 SQLite 库的 `groups`/`settings.key` 自动重命名。

测试容器示例：

```bash
docker run -d --name mikvace-pg \
  -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=mikvace \
  -p 55432:5432 postgres:18

docker run -d --name mikvace-mysql \
  -e MYSQL_ROOT_PASSWORD=test -e MYSQL_DATABASE=mikvace \
  -e MYSQL_USER=test -e MYSQL_PASSWORD=test \
  -p 53306:3306 mysql:8.0 \
  --character-set-server=utf8mb4 --collation-server=utf8mb4_bin
```

## 服务器测试用例

| ID | 用例 | 步骤 | 预期 |
| --- | --- | --- | --- |
| S01 | 健康检查 | `GET /api/health` | 200，`ok=true` |
| S02 | 登录鉴权 | 错误密码 / 正确密码 | 401 / token |
| S03 | 创建 Hy2 节点 | 只启用 hysteria | 生成 YAML，订阅有 `hysteria2://` |
| S04 | 创建 Xray 节点 | 只启用 xray + VLESS | 生成 JSON，`xray -test` 通过 |
| S05 | 协议开关 | 启用 VLESS，禁用 VMess | 配置和订阅只有 VLESS |
| S06 | 全协议 | 勾选 4 个协议 | 生成 4 个 inbound |
| S07 | 全部禁用 | 取消所有协议后保存 | 400 / 前端拦截 |
| S08 | 预设 | 点击“全协议” | 自动补齐 4 个入站 |
| S09 | 端口避让 | 连续添加协议 | 端口不重复，避开 API 端口 |
| S10 | 双核心 | `cores=[hysteria,xray]` | `configs` 同时包含 YAML 和 JSON |
| S11 | 订阅矩阵 | `?format=uri/clash/singbox/xray-json` | 各格式协议数量一致 |
| S12 | 禁用协议订阅 | 禁用 VMess 后拉订阅 | 不出现 `vmess://` |
| S13 | 用户过期/超流量 | 设置过期或配额 | 从 Xray clients 移除；Hy2 认证失败 |
| S14 | 流量幂等 | 重放同一 stats batch | 只计一次 |
| S15 | 在线数 | 客户端连接后等待心跳 | 2 分钟内 online +1 |
| S16 | 证书签发 | managed 模式申请 | 证书下发，Xray TLS 可用；详见 [证书签发测试](certificates-testing.md) |
| S17 | 日志分析 | 产生 access/error 日志 | 面板可检索、规则告警 |
| S18 | 备份恢复 | 备份后恢复 | 节点、用户、配置版本一致 |
| S19 | 旧数据兼容 | 使用旧 DB 启动 | 迁移成功，旧节点可渲染 |
| S20 | 私钥保护 | `GET /api/nodes` | 不返回 REALITY 私钥 |

## 节点测试用例

| ID | 用例 | 步骤 | 预期 |
| --- | --- | --- | --- |
| N01 | Agent 连接 | 启动节点容器 | hello 上报版本、能力 |
| N02 | 单核心 Hy2 | 下发 YAML | 进程启动，端口监听 |
| N03 | 单核心 Xray | 下发 JSON | `xray` 启动，`xray -test` 通过 |
| N04 | 多协议 Xray | 4 个 inbound | 4 个端口监听，日志正常 |
| N05 | 禁用入站 | 下发带 `enabled=false` 的配置 | 对应端口不监听 |
| N06 | 双核心 | 同时下发 YAML+JSON | 两个进程同时运行 |
| N07 | Docker 隔离 | `runtime.type: docker` | `mikvace-hysteria` 与 `mikvace-hysteria-xray` 两个容器 |
| N08 | 配置回滚 | 下发非法 Xray 配置 | 旧配置继续运行，ack 报错 |
| N09 | 重启/停止 | 面板命令 | 两个核心按命令执行 |
| N10 | 断线重连 | 重启 Agent | 自动重连，补发未确认批次 |
| N11 | Xray 流量 | `xray api statsquery` | 按用户增量上报 |
| N12 | Hy2 流量 | `trafficStats` | 按用户增量上报 |
| N13 | 双核心流量 | 两个核心同时产生流量 | 节点总流量为两者之和 |
| N14 | 在线数 | 连接后看 access log | Xray 在线数正确 |
| N15 | 端口跳跃 | Hy2 配置 portRange | iptables 规则生效，Xray 不受影响 |
| N16 | 证书更新 | managed 证书续期 | 两个核心都使用新证书 |
| N17 | 版本检测 | 启动后看 hello | Hy2/Xray 版本分别正确 |

## 端到端场景

### E2E-1：Xray 单协议

1. 创建 Xray 节点，只启用 VLESS + REALITY；
2. 用 v2rayN 导入订阅；
3. 访问 `https://ifconfig.me`；
4. 检查面板用户流量、在线数、access log。

预期：连接成功，流量计入对应用户，日志有 `email: <userId>`。

### E2E-2：多协议选择性启用

1. 启用 VLESS + Trojan，禁用 VMess + SS；
2. 分别导入 Xray、sing-box、Clash；
3. 确认客户端只看到 2 条链路；
4. 两条链路都能连通；
5. 重新启用 VMess，等待配置下发；
6. 订阅出现 VMess，且原 VLESS/Trojan 配置不丢失。

### E2E-3：同机双核心

1. 创建 hybrid 节点：Hy2 UDP 443 + Xray TCP 443；
2. 同时下发；
3. Hysteria 客户端走 UDP 443，Xray 客户端走 TCP 443；
4. 两个核心各自产生流量；
5. 面板在线数为两者之和，流量累计正确。

### E2E-4：故障恢复

1. 正常下发后，手工破坏 Xray 配置并触发同步；
2. Agent 执行 `xray -test` 失败；
3. 旧进程保持运行；
4. 面板显示 config error；
5. 修复后同步成功。

### E2E-5：配额与到期

1. 用户流量配额设为 1 MB；
2. 产生超过 1 MB 流量；
3. Hy2 认证被拒绝；
4. Xray 用户在下一轮 reconcile 后从配置移除；
5. 重置流量后恢复。

## 回归与兼容

必须单独跑：

- 旧节点 JSON 没有 `enabled` 字段 → 默认启用；
- 旧 Agent 没有 `multi-core.v1` → 只下发主核心，hybrid 节点提示升级；
- 旧数据库直接启动新二进制 → 自动迁移；
- CELERITY 备份导入 → 节点、用户、订阅正常；
- 禁用协议后重新启用 → 端口、REALITY 密钥不丢失。

## 稳定性与安全

- 500 用户 × 4 入站配置生成耗时；
- 50 节点并发同步；
- Agent 反复断线重连 100 次；
- 24 小时长稳，观察内存、goroutine、日志量；
- API Key 越权、登录限流、CORS；
- REALITY 私钥不出面板；
- 节点 Token 轮换后旧连接断开。

## 建议执行顺序

1. L0/L1 自动化；
2. 重建 `mikvace-node:test`；
3. Node B 单核心 Xray：VLESS → VMess → Trojan → SS；
4. 协议开关与订阅矩阵；
5. Node C 双核心；
6. 客户端连通与计费；
7. 故障恢复、兼容、长稳。

## 测试产物

- 测试报告（通过/失败用例、环境版本）；
- Xray/Hysteria 生成配置；
- 四种格式的订阅输出；
- Agent 日志与 access log；
- 面板截图（节点表单、协议开关、订阅页）；
- 流量/在线数统计截图。
