# MikvAce Panel 重构方案

> 目标：在不改变现有功能与协议兼容性的前提下，把当前“可运行的大单体”演进为**边界清晰、可测试、可增量交付的模块化单体**。本文是执行方案，不是一次性重写计划。

## 0. 先决条件：处理当前工作区

当前 `main` 上有一批**未提交的大规模变更**（移除 homepage、Redis 会话、安装向导数据库步骤、SQLite dialect/schema、文档与构建调整等），涉及 100+ 文件。

开始结构性重构前必须先做：

1. 将现有变更按主题拆成若干提交/PR 并合并，或在独立分支打基线标签。
2. 清理工作区中的构建产物：根目录 `server`（约 43 MB）、`data/`、`web/dist` 的手工产物不要混入重构提交。
3. 以该基线跑一次完整 CI：`go test -race ./...`、`go vet ./...`、前端构建、证书 e2e。
4. 创建重构分支，例如 `refactor/phase-1-decompose`，后续每个阶段独立 PR。

> 原则：**先固化行为，再移动代码，最后改变结构。** 不要在同一 PR 里同时做“搬文件 + 改逻辑 + 改接口”。

---

## 实施状态（本轮）

| 阶段 | 状态 | 已落地内容 |
| --- | --- | --- |
| P0 基线/护栏 | 部分完成 | `go vet`、全量 Go 测试（214 个）；CI 增加前端 lint + Vitest；新增 10 个前端测试与用户/节点应用服务测试；未替用户提交现有工作区变更 |
| P1 机械拆分 | 完成 | `server.go`、`nodes.go`、`node_hy2.go`、`users.go`、`agent.go`、`runtime.go`、`subscription.go` 拆分；单文件显著缩小 |
| P2 HTTP 边界 | 部分完成 | 路由按 public/admin/static 拆分；`pathID`/`decodeJSON`/`writeJSON` 修复；错误响应增加稳定 `code`；新增 `/api/readyz` |
| P3 应用服务 | 部分完成 | 新增 `internal/ports`、`internal/app/users`、`internal/app/nodes`；用户与节点的 CRUD/校验/令牌/事件/配置推送已迁移；其余领域仍直接调用 store |
| P4 领域/订阅 | 部分完成 | 节点校验、Hy2/ECH/监听地址迁入 `internal/app/nodes`；订阅引入 `Renderer` 注册表；`internal/idgen` 去除服务层对 store 的随机数依赖 |
| P5 Agent | 部分完成 | 显式生命周期状态（init/connecting/connected/running/degraded/backoff/stopped）并写入 hello；runtime 拆分；原子写配置为既有能力 |
| P6 前端 | 部分完成 | 新增 ESLint/Prettier/Vitest；`format.js`、`nodeFormat.js` 抽离并测试；未进行完整 TypeScript 迁移与 NodesView 表单拆分 |
| P7 横切 | 部分完成 | readiness 探针、错误码、架构文档更新；指标/超时/完整安全审计仍待后续 |

后续优先级：把 certs、logs、settings、backup 依次迁入 `internal/app`，再完成 NodesView/SettingsView 的组件与 TS 迁移。

---

## 1. 现状评估

### 1.1 规模数据

| 指标 | 当前值 |
| --- | --- |
| Go 代码 | 约 33,000 行 / 135 个文件 |
| `internal/server` 非测试代码 | 约 9,350 行 / 59 个文件 |
| `internal/store` 非测试代码 | 约 4,330 行，`*Store` 导出方法约 151 个 |
| `internal/agent` 非测试代码 | 约 3,700 行 |
| 最大文件 | `server.go` 1,180 行；`agent.go` 1,225 行；`subscription.go` 1,056 行 |
| 最大前端组件 | `NodesView.vue` 1,895 行（脚本约 1,078 行）；`SettingsView.vue` 1,019 行 |
| 路由 | 约 95 条，集中在 `server.Routes()` 一个函数 |
| 测试覆盖率 | server 59.9%、subscription 79.1%、store 32.1%、agent 24.2%、certmanager 19.7%；前端无测试 |
| 前端工程化 | 无 TypeScript、ESLint、单元测试；仅 Vite 构建 |

### 1.2 主要问题

**A. 文件级“上帝对象”**

- `internal/server/server.go` 同时承担 Server 构造、路由注册、中间件、SPA 托管、健康检查、JSON 辅助函数。
- `internal/agent/agent.go` 同时承担连接会话、协议分发、配置落盘、运行时生命周期、统计、日志批处理、本地状态。
- `internal/subscription/subscription.go` 把 URI、Clash、sing-box、Xray、REALITY、分享参数全部塞在一个文件。
- 前端 `NodesView.vue` 把列表、卡片、表单、证书、Hy2 调优、Xray 入站、拖拽排序全部放在一个组件。

**B. 缺少应用服务层，HTTP 直接编排持久化**

- 95 条路由的 handler 直接调用 `s.store.*`；`internal/server` 中 `s.store` 调用约 200 处。
- 业务规则散落在 handler、`node_hy2.go`、`subscription.go` 和 store 中。例如 `validateHy2` 单函数 216 行，`nodeFromPayload` 196 行。
- `internal/logs`、`internal/certmanager`、`internal/celerity` 直接依赖具体 `*store.Store`，导致领域/分析/证书逻辑无法脱离 SQLite 单测，依赖方向倒置。

**C. 模型三重身份**

- `internal/model` 同时是领域实体、数据库扫描目标、HTTP 请求/响应 DTO。
- JSON tag 直接挂在持久化结构上，handler 还大量使用 `map[string]any` 拼响应（如节点列表的“扁平化兼容层”）。
- 数据库字段变更会直接改变 API 形状，反之亦然。

**D. Server 聚合了过多进程内状态**

- `Server` 上约有 9 组互斥锁 + 9 个进程内 map：登录限流、2FA 尝试、验证码、API Key 限流、认证日志窗口、配额缓存、配置同步、备份、安装、管理员版本缓存。
- 这些状态与 HTTP handler 生命周期耦合，难以替换为 Redis、难以单测、多实例语义不清晰。

**E. Agent 缺少显式状态机与可靠队列**

- 连接、配置下发、运行时切换、证书申请、日志重传之间靠多个 goroutine 和布尔状态协调。
- 非测试代码中仍有约 32 处 `context.Background()`、16 处裸 `go` 调用；断线重连、日志/统计重传缺少统一的背压与磁盘队列抽象。
- 配置应用路径长（`handleConfig` 140 行、`applyConfigs` 106 行），失败回滚与 revision 语义需要更强的原子性保证。

**F. 前端工程化缺口**

- `api.js` 是手写 fetch 封装，14 个导出函数，视图直接依赖字符串路径和 `localStorage`。
- 没有类型检查、lint、组件测试，1895 行的 `NodesView` 修改风险高。
- 表单校验与后端校验重复，节点协议组合的合法性没有单一事实来源。

**G. 可观测性与错误契约**

- 错误统一是 `{"error": "字符串"}`，没有稳定错误码；不少 handler 直接把 `err.Error()` 返回给客户端。
- `writeJSON` 忽略编码错误；`decodeJSON` 未拒绝未知字段和尾随数据；`pathID` 使用 `fmt.Sscan`，对 `"1abc"` 之类输入不够严格。
- 缺少请求级超时、慢查询日志、Prometheus 指标、readiness/liveness 区分。

---

## 2. 重构目标与非目标

### 目标

1. **边界清晰**：HTTP/WS 适配层 → 应用服务 → 领域/端口 → 存储实现；依赖只向内。
2. **可测试**：核心业务规则不依赖 SQLite、HTTP、真实证书和真实代理进程。
3. **可增量交付**：每个 PR 都能独立合并、独立回滚，功能行为不变。
4. **降低单文件认知负担**：单文件建议 ≤ 400 行，单函数 ≤ 80 行，单组件脚本 ≤ 400 行。
5. **统一契约**：节点协议、Agent 消息、订阅输出、错误码都有明确 schema 与 golden test。
6. **前端可维护**：TypeScript + 类型化 API 客户端 + 组件拆分 + 关键路径测试。

### 非目标

- 不重写 UI 视觉与交互。
- 不改变现有订阅路径、Agent WebSocket 协议、部署方式。
- 不引入微服务、消息队列、Kubernetes 依赖。
- 不在重构期同时增加大量新功能；新功能走独立 PR。
- 控制面仍只使用 SQLite；代理日志可通过 `telemetry.driver: postgres` 选择性外置，见 `docs/telemetry-postgres.md`。

---

## 3. 目标架构

采用**模块化单体**，保留现有 `internal/` 路径以减少迁移成本，逐步补齐层次：

```
cmd/
  server/                  # 组装依赖、启动、优雅退出
  node/                    # Agent 入口
  client/                  # CLI

internal/
  domain/                  # 纯领域：实体、值对象、校验、领域错误（不依赖 db/http）
    user.go node.go cert.go log.go traffic.go

  app/                     # 应用服务（用例编排）
    auth/                  # 登录、2FA、会话、限流策略
    users/                 # 用户 CRUD、配额、设备、订阅 token
    nodes/                 # 节点 CRUD、校验、配置下发、证书流程
    subscriptions/         # 订阅访问决策与渲染编排
    logs/                  # 日志摄入、分析、导出
    backups/               # 备份、恢复
    install/               # 首装向导

  ports/                   # 消费方定义的接口
    repositories.go        # UserRepository / NodeRepository / ...
    notifier.go            # AgentNotifier / EventSink
    clock.go hasher.go acme.go

  store/                   # SQLite 实现 ports
    sqlite/                # 查询、扫描、事务
    schema/                # 现有 schema 定义

  transport/
    httpapi/               # 路由、中间件、handler、DTO
      router.go
      middleware/
      users/ nodes/ auth/ subscriptions/ logs/ ...
    agentws/               # Agent WebSocket 服务端协议

  agent/                   # 节点端
    session/               # 连接与状态机
    protocol/              # 消息编解码
    runtime/               # process / docker 运行时
    config/                # 配置落盘与原子应用
    state/                 # 本地状态与用户认证快照
    logqueue/              # 磁盘队列 + 重传
    stats/                 # 流量轮询

  subscription/            # 输出渲染
    renderer.go            # Renderer 接口
    clash.go singbox.go xray.go uri.go shadowrocket.go
    endpoint.go            # 统一 Endpoint 模型
    protocol/              # vless/vmess/trojan/ss/hy2 适配

  configgen/               # hy2config + xrayconfig 统一入口
  platform/                # config、logging、version、rediscache
```

> 迁移策略：**不要求第一天就搬成上面的目录**。Phase 1 只在原包内拆文件；Phase 2 起逐步引入 `app` 与 `ports`，store 实现接口；等接口稳定后再决定是否物理搬包。物理搬迁应独立成 PR，避免 diff 被移动噪声淹没。

### 依赖规则（CI 可检查）

- `domain` 不 import `store`、`server`、`net/http`、`database/sql`。
- `app` 只 import `domain`、`ports`，不 import `store/sqlite`、`transport`。
- `transport` 只 import `app`、`domain`，不直接 import `store`。
- `store/sqlite` 实现 `ports`，不 import `transport`。
- `agent` 不 import `store`、`server`。

可用 `go list -deps` 或 `depguard`/`import-boss` 在 CI 中做规则校验。

---

## 4. 分阶段路线图

| 阶段 | 主题 | 预估 | 风险 | 关键产出 |
| --- | --- | --- | --- | --- |
| P0 | 基线与护栏 | 2–3 天 | 低 | 当前变更落盘、CI 增强、黄金测试、基线标签 |
| P1 | 机械拆分，不改行为 | 3–5 天 | 低 | 大文件拆小，路由/中间件/辅助函数归位 |
| P2 | HTTP 边界与 DTO | 1–1.5 周 | 中 | 统一请求/响应/错误、DTO 与 model 分离、路由模块化 |
| P3 | 应用服务与仓储接口 | 2–3 周 | 中高 | `app` + `ports`，handler 变薄，事务边界明确 |
| P4 | 领域模型与配置/订阅 | 1.5–2 周 | 中 | 领域校验单一来源、协议适配器、golden 测试矩阵 |
| P5 | Agent 健壮性 | 1.5–2 周 | 高 | 状态机、原子配置应用、磁盘重传队列、故障注入测试 |
| P6 | 前端工程化与拆分 | 2–3 周 | 中 | TS、lint、类型化 API、Nodes/Settings 拆分、组件测试 |
| P7 | 横切能力 | 1 周 | 低 | 配置校验、指标、超时、安全加固、文档/ADR |

单人串行约 **8–10 周**；后端与前端可并行，P2/P3 是核心路径。若只能投入少量时间，优先完成 **P0 + P1 + P2**，收益/风险比最高。

---

## 5. 各阶段任务与验收

### P0 基线与护栏（必须先做）

**任务**

- 将当前未提交变更按主题拆 PR 合并；打 `pre-refactor-YYYYMMDD` 标签。
- CI 增加：
  - `go test -race ./...`（已有，保留）
  - `go vet ./...`（已有）
  - `staticcheck ./...` 或 `golangci-lint`
  - `govulncheck ./...`
  - 前端 `npm run lint`、`npm run typecheck`、`npm run test`
  - 依赖方向检查（Phase 2 后启用）
- 建立**黄金测试**：
  - 订阅：固定用户 + 节点矩阵，输出 Clash / sing-box / Xray / URI / Shadowrocket 快照。
  - 节点配置：Hy2 / Xray 生成 YAML/JSON 快照。
  - Agent：面板消息 → 落盘配置 → 运行时启动参数快照（用 fake runtime）。
- 记录性能基线：10k 用户订阅生成、10 万日志查询/导出、100 节点心跳。
- 整理 `web/dist` 与根目录 `server` 二进制的提交策略，避免产物漂移。

**验收**

- 任一重构 PR 合入后，黄金测试零 diff（除非显式更新快照）。
- CI 在 10 分钟内完成（证书 e2e 可单独 job）。
- 有可回滚的基线 tag。

---

### P1 机械拆分（行为不变）

**服务端**

- `server.go` 拆为：
  - `server.go`：Server 结构、New、Close、依赖装配
  - `router.go`：路由注册
  - `middleware.go`：recoverer / logger / security headers / CORS / entrance
  - `spa.go`：SPA 与静态资源
  - `respond.go`：writeJSON / writeError / decodeJSON / pathID
  - `health.go`
- `nodes.go` 拆为：
  - `node_handler.go`、`node_payload.go`、`node_public.go`、`node_validate.go`
- `node_hy2.go` 拆为：`hy2_normalize.go`、`hy2_validate.go`、`hy2_ech.go`
- `server_test.go` 按领域拆成多个测试文件，提取 `newTestServer(t)` helper。
- 把 `writeJSON` / `decodeJSON` / `pathID` 的明显缺陷一并修复（见 P2），但保持响应形状兼容。

**Agent**

- `agent.go` 拆为：`agent.go`（生命周期）、`session.go`、`protocol.go`、`config_apply.go`、`state.go`、`log_batcher.go`、`stats.go`。
- `runtime.go` 拆出 process / docker 实现，定义 `Runtime` 接口。

**订阅**

- 同包内按输出格式拆文件：`clash.go`、`singbox.go`、`xray.go`、`uri.go`、`shadowrocket.go`、`endpoint.go`、`share.go`。

**验收**

- `go test ./...` 全绿，黄金测试零 diff。
- 单文件 ≤ 500 行（测试文件可放宽）。
- 无导出 API 变化；前端无需改动。

---

### P2 HTTP 边界与 DTO

**任务**

- 统一请求处理：
  - `decodeJSON` 限制大小、`DisallowUnknownFields`（对兼容性敏感的接口可开关）、拒绝尾随 JSON。
  - `pathID` 改用 `strconv.ParseInt`，统一 400 错误。
  - `writeJSON` 处理编码错误并记录日志。
  - 统一错误结构：`{ "error": { "code": "...", "message": "..." } }`，保留旧 `error` 字符串字段一个版本做兼容。
- 路由模块化：
  - 每个领域一个 `registerXxxRoutes(r chi.Router)`，`routes()` 只做组装。
  - 明确 `/api`、`/api/node`、`/api/files` 的中间件边界。
- DTO 分离：
  - handler 定义 `createNodeRequest` / `nodeResponse` 等，不再直接把 `model.Node` 当响应体。
  - 删除节点列表的 `map[string]any` 扁平化兼容层；若前端仍依赖扁平字段，用显式 DTO 表达。
  - `publicNode` 的安全裁剪逻辑保留在 DTO 映射层，避免遗漏私钥。
- 把 `Server` 上的进程内状态抽成小对象：`loginLimiter`、`captchaStore`、`apiKeyLimiter`、`authLogWindow`、`quotaCache`、`adminVersionCache`，每个对象独立加锁、可单测、可替换 Redis 实现。
- 请求级超时：为写操作设置 `http.TimeoutHandler` 或 context deadline；为 ACME/SSH 等长任务保留显式异步任务模型。

**验收**

- handler 中不再出现 `map[string]any` 拼装响应（兼容层除外）。
- 所有 4xx/5xx 有稳定错误码；5xx 不返回内部错误详情。
- `Server` 结构字段减少一半以上；限流/验证码/缓存有独立单测。
- 路由注册可按领域独立阅读，`routes()` ≤ 80 行。

---

### P3 应用服务与仓储接口（核心）

**任务**

1. 在 `internal/ports` 定义按聚合拆分的接口，而不是一个 151 方法的 `*store.Store`：

```go
type UserRepository interface {
    Get(ctx context.Context, id int64) (*domain.User, error)
    GetByUserID(ctx context.Context, userID string) (*domain.User, error)
    List(ctx context.Context, f domain.UserFilter) ([]domain.User, int, error)
    Create(ctx context.Context, u *domain.User) (int64, error)
    Update(ctx context.Context, u *domain.User) error
    Delete(ctx context.Context, id int64) error
    SetGroups(ctx context.Context, userID int64, groupIDs []int64) error
    AddTraffic(ctx context.Context, userID string, tx, rx int64) error
}
```

2. 为每个领域建立应用服务，例如 `app/nodes.Service`：

```go
type Service struct {
    repo     ports.NodeRepository
    users    ports.UserRepository
    certs    ports.CertificateService
    notifier ports.AgentNotifier
    clock    ports.Clock
    log      *slog.Logger
}

func (s *Service) Create(ctx context.Context, in CreateNodeInput) (*domain.Node, error)
func (s *Service) Update(ctx context.Context, id int64, in UpdateNodeInput) (*domain.Node, error)
func (s *Service) Sync(ctx context.Context, id int64) error
```

3. handler 只做：解析请求 → 调用 service → 映射响应。禁止 handler 直接调用 `s.store`。
4. 事务边界放在 store 实现层，但由应用服务表达“一个用例一个事务”：
   - 用户创建：user + groups + nodes + 初始订阅 token 必须原子。
   - 节点更新：node + node_configs + 证书关联必须原子。
   - 提供 `store.WithTx(ctx, func(tx ports.Tx) error)`，避免 151 个方法各自拼事务。
5. 逐步替换依赖具体 store 的包：
   - `internal/logs` 改为依赖 `LogRepository` / `UserSnapshotRepository`。
   - `internal/certmanager` 改为依赖 `CertificateRepository` + `NodeRepository` + `Notifier`。
   - `internal/celerity` 改为依赖导入端口，保持“解析归档”和“写库”分离。
6. 单测策略：
   - 应用服务：fake repository + fake notifier，覆盖权限、配额、状态流转。
   - 存储：SQLite 内存库集成测试，覆盖事务回滚、唯一约束、迁移。
   - HTTP：httptest + fake service，覆盖状态码与错误码。

**验收**

- `internal/server` 中 `s.store.` 调用降为 0（仅装配层除外）。
- `logs` / `certmanager` 不再 import `store`。
- 关键用例（用户创建、节点更新、证书申请、流量结算、备份恢复）有事务测试。
- store 包导出方法按聚合拆分，单接口不超过 15 个方法。
- 覆盖率：`app` ≥ 70%，`store` ≥ 60%，`agent` ≥ 50%。

---

### P4 领域模型、配置生成与订阅渲染

**领域模型**

- 将 `internal/model` 拆为 `internal/domain`（先同包分文件，稳定后再物理拆包）：
  - 实体：User、Node、Certificate、Admin、LogEntry、Finding。
  - 值对象：UserID、SubscriptionToken、Port、PortRange、TrafficQuota、TLSMode、CoreSet。
  - 领域错误：`ErrNotFound`、`ErrConflict`、`ErrQuotaExceeded`、`ErrInvalidNode` 等，HTTP 层统一映射。
- 持久化行结构与领域结构分离：`store/sqlite` 内部使用 `userRow` 扫描，再转 `domain.User`，避免 schema 变化污染 API。
- 校验单一来源：
  - `domain/node.Validate()` 覆盖端口、域名、监听地址、协议组合、REALITY/TLS/ECH 约束。
  - handler 只做 JSON 解码，不再重复实现 `validateHy2` / Xray 入站校验。
  - 前端校验规则从同一份 schema 生成或至少共享测试用例，避免两套规则漂移。

**配置生成**

- 将 `hy2config` 与 `xrayconfig` 收敛到 `configgen`：
  - 输入 `domain.Node` + 用户/证书上下文，输出带类型的配置结构。
  - 每个生成器提供 `Validate()`，生成后立即校验。
  - 统一 TLS / REALITY / ECH / mTLS 的共享语义，避免 Hy2 与 Xray 各写一套。
- 增加 golden 测试：每个协议 × 传输 × 安全模式的最小矩阵。

**订阅渲染**

- 引入统一 `Endpoint` 模型（节点、协议、传输、安全、标签、认证）。
- 每个客户端格式实现 `Renderer` 接口：

```go
type Renderer interface {
    Format() string
    ContentType() string
    Render(ctx context.Context, u *domain.User, endpoints []Endpoint) ([]byte, error)
}
```

- 协议适配器负责 URI / Clash proxy / sing-box outbound / Xray outbound 的字段映射，消除 `clashXrayProxy`、`singboxXrayOutbound`、`xrayJSONOutbound` 之间的重复分支。
- 用 `yaml.v3` 结构体或有序 map 输出 YAML，避免 `map[string]any` 导致字段顺序和类型不稳定。
- 增加订阅快照测试：固定节点矩阵 × 5 种输出 × 有无 TLS/REALITY/ hopping，任何字段变化都必须显式更新快照。

**验收**

- 节点合法性规则只在 `domain` 中实现一次。
- 新增一种协议/传输只需改协议适配器 + 渲染器，不修改 handler。
- 订阅输出快照覆盖全部 5 种格式，CI 零 diff。
- 生成配置在写入前 100% 经过 `Validate()`。

---

### P5 Agent 健壮性

**任务**

1. 显式状态机：
   `disconnected → connecting → handshaking → syncing → running → degraded → reconnecting`
   - 状态迁移集中在一个 goroutine，所有事件（心跳、配置、命令、断线）通过 channel 进入。
   - 禁止在多个 goroutine 中直接修改运行时状态。
2. 配置应用原子化：
   - 写临时文件 → 语法校验（`hy2 -c` / `xray -test`）→ 原子 rename → 启动/重载 → 成功后才提交 revision。
   - 失败回滚到上一份配置并上报 `config_error`，保留错误上下文。
   - 同一 revision 幂等；重复下发不重复重启。
3. 可靠上报：
   - 日志与流量批次进入**有界磁盘队列**（带 segment/压缩/清理策略），按 batch id 幂等重传。
   - 面板不可用时继续采集，恢复后按序补传；队列满时按策略丢弃最旧并记录指标。
4. 运行时抽象：
   - `Runtime` 接口覆盖 process 与 docker；统一 Start/Stop/Reload/Logs/Stats/Version。
   - 证书安装、端口跳跃 iptables 规则作为独立副作用组件，可失败重试。
5. 上下文与 goroutine 生命周期：
   - 去掉裸 `context.Background()`；所有后台任务从 `Run(ctx)` 派生，退出时统一 cancel + wait。
   - 用 `errgroup` 管理 session/heartbeat/stats/log 循环；禁止无界 `go`。
6. 故障注入测试：
   - 面板断线、半开连接、慢消费者、配置非法、运行时启动失败、磁盘满、证书过期。
   - `go test -race` 下运行。

**验收**

- Agent 只有一个状态写者；状态迁移有表驱动测试。
- 配置失败可回滚，revision 不回退、不跳变。
- 断线 10 分钟后恢复，日志/流量不丢批次（在队列容量内）。
- agent 覆盖率 ≥ 50%，关键路径有 race 测试。

---

### P6 前端工程化与拆分

**工程化**

- 引入 TypeScript（先 `allowJs`，逐文件迁移），`vue-tsc --noEmit` 进 CI。
- ESLint + Prettier + `eslint-plugin-vue`；禁止裸 `fetch`、禁止在组件内直接访问 `localStorage`。
- Vitest + Vue Test Utils；Playwright 覆盖登录、用户 CRUD、节点创建、订阅下载、设置保存。
- API 客户端按领域拆分：
  ```
  web/src/api/client.ts      # fetch、错误、鉴权、401
  web/src/api/users.ts
  web/src/api/nodes.ts
  web/src/api/subscriptions.ts
  web/src/api/settings.ts
  web/src/types/             # 与后端 DTO 对齐的类型
  ```
- 统一错误模型与 toast；去掉各视图重复的 try/catch 模板。

**组件拆分**

- `NodesView.vue` 拆为：
  - `NodeList` / `NodeCard` / `NodeTable`
  - `NodeForm`（`BasicTab`、`Hy2Tab`、`XrayTab`、`CertTab`、`AdvancedTab`）
  - `useNodeForm()`、`useNodeFilters()`、`useNodeReorder()`
- `SettingsView.vue` 拆为 `PanelSettings`、`SecuritySettings`、`SubscriptionSettings`、`BackupSettings`、`RedisSettings`。
- `Select.vue`、`Modal.vue` 等基础组件补齐 props/emits 类型与可访问性。
- 全局 CSS 拆为 design tokens + 组件样式；避免 828 行 `main.css` 继续膨胀。

**验收**

- 单文件脚本 ≤ 400 行；`NodesView` 主文件 ≤ 150 行。
- `vue-tsc`、ESLint、Vitest 全绿。
- 关键流程 Playwright 通过；无 `any` 泛滥（新增代码禁止隐式 any）。
- 前后端类型/DTO 变更能在编译期发现。

---

### P7 横切能力

- **配置**：集中校验（端口、URL、密钥文件、Redis、数据库路径），启动失败快速报错；环境变量覆盖规则文档化。
- **可观测性**：
  - `/healthz`（liveness）与 `/readyz`（DB/Redis/证书依赖）。
  - Prometheus `/metrics`：HTTP 延迟、Agent 连接数、配置下发失败、日志队列深度、流量批次延迟。
  - 请求 ID 注入日志与错误响应；慢查询（>200ms）日志。
- **安全**：
  - 统一 5xx 不泄露内部错误；敏感字段（密码、Token、私钥、SSH 凭据）日志脱敏。
  - JWT 密钥轮换与会话失效策略；API Key 作用域与限流测试。
  - 依赖漏洞扫描（govulncheck / npm audit）进 CI。
  - 备份文件权限、恢复流程、路径穿越测试。
- **数据库**：
  - 明确 SQLite 单后端；`dialect` 保留但文档说明“仅为未来扩展预留”，避免过度抽象。
  - 迁移只增不改；每个 release 提供“旧版本 DB → 新版本”升级测试。
  - 定期 `VACUUM`/WAL checkpoint 策略与备份一致性测试。
- **文档**：
  - 增加 ADR（架构决策记录）：为什么 SQLite、为什么模块化单体、Agent 协议版本策略。
  - API 文档与 DTO 同步；订阅输出格式变更需更新 golden 快照与文档。

---

## 6. PR 拆分建议

每个 PR 控制在一个可审查的范围（建议 ≤ 800 行有效 diff）：

1. `chore/ci-guardrails`：CI、lint、golden 测试、基线标签。
2. `refactor/server-split`：仅移动代码 + 文件头注释。
3. `refactor/agent-split`：仅移动代码。
4. `refactor/subscription-split`：仅移动代码。
5. `fix/http-helpers`：decode/pathID/writeJSON/错误码。
6. `refactor/server-state-objects`：限流、验证码、缓存对象化。
7. `feat/ports-repositories`：接口 + store 适配（不切 handler）。
8. `refactor/users-service`：用户领域切换。
9. `refactor/nodes-service`：节点领域切换。
10. `refactor/certs-logs-ports`：证书/日志去 store 依赖。
11. `refactor/domain-validation`：校验单一来源。
12. `refactor/subscription-renderers`：渲染器与协议适配。
13. `refactor/agent-state-machine`：状态机与原子配置。
14. `refactor/agent-reliable-queue`：磁盘队列。
15. `refactor/web-ts-api`：TS + API 客户端。
16. `refactor/web-nodes-view`：NodesView 拆分。
17. `refactor/web-settings-view`：SettingsView 拆分。
18. `chore/observability-security`：指标、超时、脱敏、漏洞扫描。

每个 PR 的合并条件：

- 行为不变（黄金测试零 diff）或变更点有明确测试。
- `go test -race ./...`、`go vet`、前端 typecheck/lint/test 全绿。
- 不引入新的 `context.Background()`、裸 `go`、直接 `s.store` 调用（按阶段逐步收紧）。
- 更新对应文档/ADR。

---

## 7. 风险与回滚

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| 当前未提交变更与重构冲突 | 高 | 先落盘、打 tag、独立分支；不要直接在大 diff 上继续改 |
| 机械拆文件导致 merge 冲突 | 中 | 短周期合并；拆文件 PR 不做逻辑修改；通知协作者 |
| 应用服务层引入后行为漂移 | 高 | 黄金测试 + 双跑对比（旧 handler vs 新 service 输出） |
| SQLite 事务/并发回归 | 高 | 内存库事务测试 + `-race` + 备份恢复演练 |
| Agent 状态机改造导致节点掉线 | 高 | 分阶段：先抽接口与状态观测，再切状态机；保留旧路径 feature flag |
| 前端 TS 迁移拖长 | 中 | 先 `allowJs`，只对新增/改动文件强制类型；不追求一次性全量 |
| 订阅输出字段变化导致客户端不可用 | 高 | 快照测试 + 真实客户端冒烟（Clash/sing-box/Shadowrocket） |
| 过度抽象 | 中 | 只抽有第二个实现的边界；不为单实现造接口；ADR 记录取舍 |

**回滚策略**

- 每个阶段独立 tag；数据库迁移只增不改，回滚二进制不破坏旧数据。
- Agent 改造保留旧协议兼容与双配置路径，按节点灰度。
- 前端按路由/组件灰度，保留旧组件直到新组件通过 e2e。

---

## 8. 验收指标

**结构**

- `internal/server` 非测试代码 ≤ 6,000 行；单文件 ≤ 500 行；单函数 ≤ 80 行。
- `NodesView.vue` ≤ 150 行，脚本 ≤ 400 行的组件占比 100%。
- handler 直接 `s.store` 调用 = 0；`logs`/`certmanager` 不 import `store`。

**质量**

- 覆盖率：`app` ≥ 70%、`store` ≥ 60%、`agent` ≥ 50%、整体 ≥ 55%。
- 前端 typecheck/lint/unit/e2e 全绿。
- `go test -race ./...` 稳定通过；无已知数据竞争。
- 黄金测试覆盖 5 种订阅格式 × 主要协议组合。

**运行**

- 100 节点、10k 用户场景下：心跳/配置下发 P99 < 500ms；订阅生成 P99 < 300ms；日志查询 P99 < 1s。
- Agent 断线 10 分钟恢复后日志/流量批次不丢（队列容量内）。
- 首装、升级、备份恢复、证书签发四条路径 e2e 通过。

---

## 9. 立即可做的 10 件事

1. 把当前未提交变更拆成主题 PR 并打基线 tag。
2. 删除根目录 `server` 二进制与临时 `data/` 产物，确认 `.gitignore` 生效。
3. 给 `internal/server`、`internal/agent`、`internal/subscription` 加文件级职责注释。
4. 拆 `server.go` 为 router/middleware/spa/respond/health。
5. 拆 `agent.go` 为 session/protocol/config/state/log/stats。
6. 拆 `subscription.go` 为 clash/singbox/xray/uri/shadowrocket/endpoint。
7. 修复 `pathID`、`decodeJSON`、`writeJSON` 的三个小缺陷。
8. 为订阅输出建立第一组 golden 快照。
9. 引入 ESLint + Prettier + Vitest（先不强制 TS）。
10. 写 ADR-001：为什么采用模块化单体与 SQLite 单后端。

---

## 10. 建议的起步顺序

如果只能做一件事：**先做 P0，把当前变更稳定下来**。

如果能投入 1–2 周：**P0 + P1**，先降低文件级认知负担，风险最低、review 最快。

如果能投入 1 个月：**P0 + P1 + P2 + P3 的用户/节点两个领域**，这是收益最大的路径；证书、日志、备份等领域可在接口稳定后按同样模式复制。

前端可与后端并行，从 **ESLint/Prettier/Vitest + 类型化 API 客户端 + NodesView 拆分** 开始，不必等待后端分层完成。
