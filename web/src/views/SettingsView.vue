<template>
  <div>
    <div class="toolbar">
      <div>
        <div class="page-title">设置</div>
        <div class="dim small">面板信息、订阅页面、日志分析与安全设置。</div>
      </div>
    </div>

    <div v-if="error" class="banner banner-danger">{{ error }}</div>
    <div v-if="saved" class="banner banner-info">{{ saved }}</div>

    <div class="tabs">
      <button class="tab" :class="{ active: tab === 'general' }" @click="tab = 'general'">常规</button>
      <button class="tab" :class="{ active: tab === 'subscription' }" @click="tab = 'subscription'">订阅页面</button>
      <button class="tab" :class="{ active: tab === 'analysis' }" @click="tab = 'analysis'">日志分析</button>
      <button class="tab" :class="{ active: tab === 'infra' }" @click="tab = 'infra'; loadInfra()">存储与缓存</button>
      <button class="tab" :class="{ active: tab === 'security' }" @click="tab = 'security'">安全</button>
      <button class="tab" :class="{ active: tab === 'certs' }" @click="tab = 'certs'; loadCertSettings()">证书签发</button>
      <button class="tab" :class="{ active: tab === 'backups' }" @click="tab = 'backups'">备份</button>
    </div>

    <div v-if="!settings" class="card"><div class="empty">正在加载设置…</div></div>

    <template v-else>
      <section v-show="tab === 'general'" class="card">
        <div class="card-header"><div class="card-title">面板与公开地址</div></div>
        <div class="card-body">
          <div class="form-grid">
            <div class="field"><label>面板名称</label><input v-model="form.panel_name" type="text" /></div>
            <div class="field">
              <label>公开地址</label>
              <input v-model="form.public_url" type="url" placeholder="https://panel.example.com" @input="syncPreview" />
              <span class="hint">用于订阅链接和代理认证回调，末尾不要带斜杠。</span>
            </div>
            <div class="field full">
              <label>订阅地址预览</label>
              <CopyField :value="subscriptionPreview" prefix="订阅" />
            </div>
          </div>
          <div class="divider"></div>
          <dl class="kv">
            <dt>版本</dt><dd>{{ meta.version }}</dd>
            <dt>平台</dt><dd>{{ meta.platform }}</dd>
            <dt>数据目录</dt><dd class="mono">{{ meta.dataDir }}</dd>
          </dl>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn btn-primary" :disabled="saving" @click="save(['panel_name', 'public_url'])">保存常规设置</button>
        </div>
      </section>

      <section v-show="tab === 'general'" class="card section-gap">
        <div class="card-header">
          <div>
            <div class="card-title">主题外观</div>
            <div class="card-sub">主题偏好保存在当前浏览器，不会影响其他管理员。</div>
          </div>
        </div>
        <div class="card-body">
          <div class="theme-options">
            <button
              v-for="t in themeOptions"
              :key="t.value"
              type="button"
              class="theme-option"
              :class="{ on: theme.current === t.value }"
              @click="setTheme(t.value)"
            >
              <span class="theme-swatch" :class="'theme-swatch--' + t.value"></span>
              <span class="theme-meta">
                <strong>{{ t.label }}</strong>
                <small>{{ t.description }}</small>
              </span>
            </button>
          </div>
        </div>
      </section>

      <section v-show="tab === 'subscription'" class="card">
        <div class="card-header"><div class="card-title">订阅落地页</div></div>
        <div class="card-body">
          <div class="form-grid">
            <div class="field"><label>页面标题</label><input v-model="form.page_title" type="text" /></div>
            <div class="field"><label>配置标题</label><input v-model="form.subscription_title" type="text" />
              <span class="hint">用户没有分组标题时使用的备用标题。</span>
            </div>
            <div class="field full"><label>页面说明</label><input v-model="form.page_note" type="text" /></div>
            <div class="field"><label>新节点默认订阅入口</label>
              <Select v-model="form.subscription_hy2_endpoints" :options="SUBSCRIPTION_HY2_OPTIONS" />
              <span class="hint">仅用于新建节点时初始化订阅入口；已有节点始终以节点设置中的“订阅入口”开关为准，此设置不会覆盖它们。</span>
            </div>
            <div class="field"><label>Logo 地址</label><input v-model="form.logo_url" type="url" placeholder="https://…/logo.png" /></div>
            <div class="field"><label>更新间隔（小时）</label><input v-model="form.subscription_update_interval" type="number" min="1" /></div>
            <div class="field"><label>支持链接</label><input v-model="form.support_url" type="url" placeholder="https://t.me/support" /></div>
            <div class="field"><label>配置网页地址</label><input v-model="form.web_page_url" type="url" placeholder="https://example.com" /></div>
            <div class="field full">
              <label>自定义按钮（JSON）</label>
              <textarea v-model="form.subscription_buttons" rows="4" placeholder='[{"label":"Telegram","url":"https://t.me/example"}]'></textarea>
              <span class="hint">支持占位符：{url}、{url_encoded}、{url_b64}。</span>
            </div>
            <div class="field"><label>HAPP Provider ID</label><input v-model="form.happ_provider_id" type="text" placeholder="可选" /></div>
            <div class="field"><label>HAPP 路由模式</label>
              <Select v-model="form.happ_routing_mode" :options="HAPP_ROUTING_OPTIONS" />
            </div>
            <div class="field full"><label>HAPP 路由配置（JSON）</label>
              <textarea v-model="form.happ_routing_profile" rows="4" placeholder='{"rules":[]}'></textarea>
              <span class="hint">通过 <span class="mono">routing</span> 请求头发送，并前置到 HAPP/Incy 的 URI 订阅。</span>
            </div>
            <div class="field full"><label>HAPP 公告</label><input v-model="form.happ_announce" type="text" placeholder="可选公告" /></div>
          </div>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn btn-primary" :disabled="saving" @click="save(['page_title', 'subscription_title', 'page_note', 'logo_url', 'subscription_update_interval', 'support_url', 'web_page_url', 'subscription_buttons', 'subscription_hy2_endpoints', 'happ_provider_id', 'happ_routing_mode', 'happ_routing_profile', 'happ_announce'])">保存订阅设置</button>
        </div>
      </section>

      <section v-show="tab === 'analysis'" class="card">
        <div class="card-header"><div class="card-title">日志采集与检测</div></div>
        <div class="card-body">
          <div class="form-grid">
            <div class="field full">
              <label class="checkbox">
                <span class="switch"><input v-model="form.log_ingest_enabled" type="checkbox" @change="onIngestToggle" /><span></span></span>
                采集节点 Agent 上报的代理日志
              </label>
              <span class="hint">关闭后 Agent 仍运行，但日志批次会被确认并丢弃。</span>
            </div>
            <div class="field"><label>设备宽限期（分钟）</label><input v-model="form.device_grace_minutes" type="number" min="1" />
              <span class="hint">离线设备 IP 在认证限流中被遗忘前的等待时间。</span>
            </div>
            <div class="field"><label>暴力破解阈值（单 IP 失败次数）</label><input v-model="form.alert_auth_fail_threshold" type="number" min="1" /></div>
            <div class="field"><label>错误爆发阈值（单节点错误数）</label><input v-model="form.alert_error_burst_threshold" type="number" min="1" /></div>
          </div>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn btn-primary" :disabled="saving" @click="save(['log_ingest_enabled', 'device_grace_minutes', 'alert_auth_fail_threshold', 'alert_error_burst_threshold'])">保存分析设置</button>
        </div>
      </section>

      <section v-show="tab === 'analysis'" class="card section-gap">
        <div class="card-header">
          <div>
            <div class="card-title">数据清理</div>
            <div class="card-sub">自动删除超过保留天数的日志、审计事件和监控采样，控制数据库与备份体积。</div>
          </div>
          <div class="spacer"></div>
          <button class="btn btn-sm" :disabled="usageLoading" @click="loadUsage">{{ usageLoading ? '读取中…' : '刷新用量' }}</button>
        </div>
        <div class="card-body">
          <div class="form-grid">
            <div class="field">
              <label>日志保留天数</label>
              <input v-model="form.log_retention_days" type="number" min="1" />
              <span class="hint">代理日志、日志批次和审计事件。</span>
            </div>
            <div class="field">
              <label>监控采样保留天数</label>
              <input v-model="form.metric_retention_days" type="number" min="1" />
              <span class="hint">节点心跳与流量曲线数据；越短备份越小。</span>
            </div>
          </div>
          <div class="divider"></div>
          <dl class="kv">
            <dt>数据库大小</dt><dd>{{ fmtBytes(usage?.databaseBytes || 0) }}<span v-if="usage?.walBytes" class="dim"> · WAL {{ fmtBytes(usage.walBytes) }}</span></dd>
            <dt>可回收空间</dt><dd>
              <span :class="{ dim: !usage?.freeBytes }">{{ fmtBytes(usage?.freeBytes || 0) }}</span>
              <span v-if="usage?.autoVacuum" class="dim"> · 增量回收已启用</span>
            </dd>
            <dt>日志存储</dt><dd>
              <span v-if="usage?.telemetryFallback">SQLite（回退，PostgreSQL 不可用）</span>
              <span v-else>{{ usage?.telemetryDriver === 'postgres' ? 'PostgreSQL' : 'SQLite（控制库）' }}</span>
              <span v-if="usage?.telemetryDriver === 'postgres' && usage?.telemetryDetail" class="dim"> · {{ usage.telemetryDetail }}</span>
              <span v-if="usage?.telemetryDriver === 'postgres' && usage?.telemetryPartitions" class="dim"> · {{ usage.telemetryPartitions }} 个分区</span>
            </dd>
            <template v-if="usage?.telemetryDriver === 'postgres'">
              <dt>日志库大小</dt><dd>{{ fmtBytes(usage?.telemetryBytes || 0) }}<span class="dim"> · 约 {{ fmtRows(usage?.telemetryRows) }} 行</span></dd>
            </template>
            <dt>缓存后端</dt><dd>
              {{ usage?.cache?.backend === 'redis' ? 'Redis（跨实例共享）' : '进程内存' }}
              <span class="dim"> · 命中率 {{ Math.round((usage?.cache?.hitRate || 0) * 100) }}%</span>
              <span v-if="usage?.cache?.entries" class="dim"> · {{ usage.cache.entries }} 项</span>
            </dd>
            <dt>代理日志</dt><dd>{{ fmtRows(usage?.logs) }} 行</dd>
            <dt>审计事件</dt><dd>{{ fmtRows(usage?.events) }} 行</dd>
            <dt>监控采样</dt><dd>{{ fmtRows(usage?.nodeMetrics) }} 行</dd>
            <dt>流量批次</dt><dd>{{ fmtRows(usage?.statsBatches) }} 行</dd>
          </dl>
          <div v-if="usage && usage.telemetryOk === false" class="hint" style="margin-top: 10px; color: var(--danger, #d33)">
            日志存储不可用：{{ usage.telemetryError || '连接失败' }}。控制面、认证与订阅不受影响；日志写入会由节点重试。
          </div>
          <label class="checkbox" style="margin-top: 12px">
            <span class="switch"><input v-model="cleanupVacuum" type="checkbox" /><span></span></span>
            清理后完整压缩数据库（VACUUM，可能短暂阻塞写入）
          </label>
          <span class="hint" style="margin-top: 8px">“按保留期清理”只删除超过保留天数的数据；如需清空全部代理日志，请到「日志分析 → 清空日志」操作。未勾选时仍会自动回收空闲页，只有空闲空间较多或数据库仍处于旧的紧凑模式时才会重写文件。</span>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn btn-danger" :disabled="cleanupBusy" @click="runCleanup">{{ cleanupBusy ? '清理中…' : '按保留期清理' }}</button>
          <button class="btn btn-primary" :disabled="saving" @click="save(['log_retention_days', 'metric_retention_days'])">保存清理策略</button>
        </div>
      </section>

      <section v-show="tab === 'infra'" class="card">
        <div class="card-header">
          <div>
            <div class="card-title">存储与缓存</div>
            <div class="card-sub">主数据库始终是 SQLite；PostgreSQL 只存放代理日志，Redis 提供共享会话、限流与二级缓存。</div>
          </div>
          <div class="spacer"></div>
          <button class="btn btn-sm" :disabled="infraLoading" @click="loadInfra">{{ infraLoading ? '读取中…' : '刷新状态' }}</button>
        </div>
        <div class="card-body">
          <div v-if="infraError" class="banner banner-danger">{{ infraError }}</div>
          <div v-if="infraMessage" class="banner banner-info">{{ infraMessage }}</div>

          <dl v-if="infra" class="kv">
            <dt>控制库</dt>
            <dd>SQLite <span class="dim mono">· {{ meta.dataDir || 'data' }}/panel.db</span></dd>
            <dt>日志库</dt>
            <dd>
              {{ infra.telemetry.activeDriver === 'postgres' ? 'PostgreSQL' : 'SQLite' }}
              <span
                class="badge"
                :class="infra.telemetry.fallback ? 'badge-danger' : (infra.telemetry.ok ? 'badge-ok' : 'badge-warn')"
              >
                {{ infra.telemetry.fallback ? '已回退 SQLite' : (infra.telemetry.ok ? '正常' : '异常') }}
              </span>
              <span v-if="infra.telemetry.error" class="dim"> · {{ infra.telemetry.error }}</span>
            </dd>
            <dt>Redis</dt>
            <dd>
              {{ infra.redis.enabled ? (infra.redis.ok ? '已连接' : '连接异常') : '未启用' }}
              <span class="dim"> · 缓存 {{ infra.cache.backend === 'redis' ? 'Redis' : '进程内存' }}，命中率 {{ Math.round((infra.cache.hitRate || 0) * 100) }}%</span>
              <span v-if="infra.redis.error" class="dim"> · {{ infra.redis.error }}</span>
            </dd>
          </dl>

          <div class="divider"></div>

          <div class="form-grid">
            <div class="field full">
              <label class="checkbox">
                <span class="switch"><input v-model="infraForm.telemetry.enabled" type="checkbox" /><span></span></span>
                <span>使用外部 PostgreSQL 存放代理日志</span>
              </label>
              <span class="hint">关闭时日志保留在 SQLite；用户、节点、订阅和流量统计始终在 SQLite。</span>
            </div>
            <template v-if="infraForm.telemetry.enabled">
              <div class="field full">
                <label>PostgreSQL DSN</label>
                <input
                  v-model="infraForm.telemetry.dsn"
                  type="text"
                  class="mono"
                  autocomplete="off"
                  spellcheck="false"
                  :placeholder="infra?.telemetry?.configured ? '已配置 — 留空保持不变' : 'postgres://user:pass@host:5432/db?sslmode=disable'"
                />
                <span class="hint">保存到 data 目录下的 0600 密钥文件，不写入 panel.yaml。</span>
                <DsnTemplate :templates="infra?.dsnTemplates?.postgres || []" @apply="(v) => (infraForm.telemetry.dsn = v)" />
              </div>
              <div class="field">
                <label>Schema</label>
                <input v-model="infraForm.telemetry.schema" type="text" class="mono" placeholder="public" />
              </div>
              <div class="field">
                <label>连接失败时</label>
                <label class="checkbox">
                  <span class="switch"><input v-model="infraForm.telemetry.required" type="checkbox" /><span></span></span>
                  <span>拒绝启动</span>
                </label>
                <span class="hint">关闭时回退到 SQLite 日志表。</span>
              </div>
            </template>

            <div class="field full">
              <label class="checkbox">
                <span class="switch"><input v-model="infraForm.redis.enabled" type="checkbox" /><span></span></span>
                <span>启用 Redis（共享会话与高级缓存）</span>
              </label>
              <span class="hint">关闭时使用进程内存；启用后日志分析结果可跨实例共享。启用或切换 Redis 后，已登录会话需要重新登录一次。</span>
            </div>
            <template v-if="infraForm.redis.enabled">
              <div class="field full">
                <label>Redis URL</label>
                <input
                  v-model="infraForm.redis.url"
                  type="text"
                  class="mono"
                  autocomplete="off"
                  spellcheck="false"
                  :placeholder="infra?.redis?.configured ? '已配置 — 留空保持不变' : 'redis://:password@host:6379/0'"
                />
                <span class="hint">保存到 data 目录下的 0600 密钥文件，不写入 panel.yaml。</span>
                <DsnTemplate :templates="infra?.dsnTemplates?.redis || []" @apply="(v) => (infraForm.redis.url = v)" />
              </div>
              <div class="field">
                <label>Key 前缀</label>
                <input v-model="infraForm.redis.prefix" type="text" class="mono" placeholder="mikvace:" />
              </div>
              <div class="field">
                <label>连接失败时</label>
                <label class="checkbox">
                  <span class="switch"><input v-model="infraForm.redis.required" type="checkbox" /><span></span></span>
                  <span>拒绝启动</span>
                </label>
                <span class="hint">关闭时自动回退内存会话与缓存。</span>
              </div>
            </template>
          </div>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn" :disabled="infraBusy" @click="testInfra">{{ infraTesting ? '测试中…' : '测试连接' }}</button>
          <button class="btn btn-primary" :disabled="infraBusy" @click="saveInfra">{{ infraSaving ? '保存中…' : '保存并重启' }}</button>
        </div>
      </section>

      <section v-show="tab === 'security'" class="card">
        <div class="card-header">
          <div>
            <div class="card-title">管理员账号</div>
            <div class="card-sub">修改登录用户名；其他已登录会话会在数秒内失效，当前会话自动续签。</div>
          </div>
        </div>
        <div class="card-body">
          <div v-if="accountError" class="banner banner-danger">{{ accountError }}</div>
          <div class="form-grid" style="max-width: 640px">
            <div class="field">
              <label>用户名</label>
              <input v-model="account.username" type="text" autocomplete="username" maxlength="32" />
              <span class="hint">3–32 位，仅允许字母、数字、点、下划线和短横线。</span>
            </div>
            <div class="field"><label>当前密码</label><input v-model="account.password" type="password" autocomplete="current-password" /></div>
            <div v-if="twoFA.enabled" class="field full">
              <label>动态验证码 / 恢复码</label>
              <input v-model="account.code" type="text" class="otp-input" autocomplete="one-time-code" spellcheck="false" placeholder="6 位验证码" />
            </div>
          </div>
          <div class="small dim" style="margin-top: 12px">
            最近登录：{{ auth.admin?.lastLoginAt ? fmtRelative(auth.admin.lastLoginAt) : '—' }}
            <span style="margin: 0 6px">·</span>
            创建时间：{{ auth.admin?.createdAt ? fmtDate(auth.admin.createdAt) : '—' }}
          </div>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn btn-primary" :disabled="account.saving" @click="saveAccount">{{ account.saving ? '保存中…' : '保存账号信息' }}</button>
        </div>
      </section>

      <section v-show="tab === 'security'" class="card section-gap">
        <div class="card-header"><div class="card-title">管理员密码</div></div>
        <div class="card-body">
          <div v-if="auth.warning" class="banner banner-warn">{{ auth.warning }}</div>
          <div class="form-grid" style="max-width: 640px">
            <div class="field full"><label>当前密码</label><input v-model="pw.old" type="password" autocomplete="current-password" /></div>
            <div class="field"><label>新密码</label><input v-model="pw.next" type="password" autocomplete="new-password" /></div>
            <div class="field"><label>确认新密码</label><input v-model="pw.confirm" type="password" autocomplete="new-password" /></div>
            <div v-if="twoFA.enabled" class="field full">
              <label>动态验证码 / 恢复码</label>
              <input v-model="pw.code" type="text" class="otp-input" autocomplete="one-time-code" spellcheck="false" placeholder="6 位验证码" />
            </div>
          </div>
          <div class="divider"></div>
          <div class="small dim">新密码至少 8 个字符。修改密码后其他会话会在数秒内失效，当前会话会自动续签。</div>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn btn-primary" :disabled="pwSaving" @click="changePassword">{{ pwSaving ? '更新中…' : '更新密码' }}</button>
        </div>
      </section>

      <section v-show="tab === 'security'" class="card section-gap">
        <div class="card-header">
          <div>
            <div class="card-title">管理员安全入口</div>
            <div class="card-sub">安全入口强制开启。没有访问入口拿到门禁口令时，普通 /admin 地址一律返回 404。</div>
          </div>
          <div class="spacer"></div>
          <span class="badge badge-ok">强制开启</span>
        </div>
        <div class="card-body">
          <div class="form-grid">
            <div class="field full">
              <label>入口路径</label>
              <div class="flex" style="gap: 8px; align-items: center">
                <input v-model.trim="form.admin_security_path" type="text" class="mono" placeholder="例如 mikvace-8f3a2c" style="max-width: 320px" />
                <button class="btn btn-sm" type="button" @click="generateSecurityPath">随机生成</button>
              </div>
              <span class="hint">4–64 位，仅允许字母、数字、短横线和下划线。修改后旧入口和已有门禁口令立即失效。</span>
            </div>
            <div class="field full">
              <label>完整入口地址</label>
              <CopyField :value="adminEntryPreview" prefix="入口" />
              <span class="hint">访问该地址一次即可获得 HttpOnly 门禁口令，随后 /admin 在当前浏览器可用。忘记时可查看数据目录中的 admin-entry.txt（0600）。</span>
            </div>
          </div>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn btn-primary" :disabled="saving" @click="save(['admin_security_path'])">保存安全入口设置</button>
        </div>
      </section>

      <section v-show="tab === 'security'" class="card section-gap">
        <div class="card-header">
          <div>
            <div class="card-title">两步验证（TOTP）</div>
            <div class="card-sub">使用验证器应用保护面板登录；启用后会生成一次性恢复码。</div>
          </div>
          <div class="spacer"></div>
          <span class="badge" :class="twoFA.enabled ? 'badge-ok' : 'badge-muted'">{{ twoFA.enabled ? '已启用' : '未启用' }}</span>
        </div>
        <div class="card-body">
          <div v-if="twoFA.error" class="banner banner-danger">{{ twoFA.error }}</div>
          <template v-if="twoFA.enabled">
            <div class="banner banner-info">已启用，剩余恢复码 {{ twoFA.recoveryRemaining }} 个。关闭或重置需要当前密码和动态验证码/恢复码。</div>
            <div class="form-grid" style="max-width: 640px">
              <div class="field"><label>当前密码</label><input v-model="twoFA.password" type="password" autocomplete="current-password" /></div>
              <div class="field">
                <label>动态验证码 / 恢复码</label>
                <input v-model="twoFA.code" type="text" class="otp-input" autocomplete="one-time-code" spellcheck="false" autocapitalize="off" placeholder="6 位验证码" />
              </div>
            </div>
            <div class="flex" style="gap: 8px">
              <button class="btn" :disabled="twoFA.busy" @click="regenerateRecovery">重新生成恢复码</button>
              <button class="btn btn-danger" :disabled="twoFA.busy" @click="disable2FA">关闭 2FA</button>
            </div>
            <div v-if="twoFA.newCodes" class="banner banner-warn" style="margin-top: 12px">
              <div>请立即保存新的恢复码（只显示一次）：</div>
              <div class="mono" style="margin-top: 6px; word-break: break-all">{{ twoFA.newCodes.join('  ') }}</div>
              <button class="btn btn-sm" type="button" style="margin-top: 8px" @click="copyCodes(twoFA.newCodes)">复制</button>
            </div>
          </template>
          <template v-else>
            <template v-if="!twoFA.setup">
              <p class="small dim">支持 Google Authenticator、1Password、Bitwarden 等标准 TOTP 应用。</p>
              <button class="btn btn-primary" :disabled="twoFA.busy" @click="start2FA">开始启用 2FA</button>
            </template>
            <template v-else>
              <div class="flex" style="gap: 20px; flex-wrap: wrap">
                <img :src="twoFA.setup.qr" alt="2FA QR" style="width: 220px; height: 220px; border-radius: 0; background: #fff; padding: 8px" />
                <div style="flex: 1; min-width: 260px">
                  <div class="small dim">扫码，或手动输入密钥：</div>
                  <div class="mono" style="word-break: break-all; margin: 6px 0">{{ twoFA.setup.secret }}</div>
                  <div class="small dim">恢复码（只显示一次，请离线保存）：</div>
                  <div class="mono small" style="margin: 6px 0; word-break: break-all">{{ twoFA.setup.recoveryCodes.join('  ') }}</div>
                  <button class="btn btn-sm" type="button" @click="copyCodes(twoFA.setup.recoveryCodes)">复制恢复码</button>
                </div>
              </div>
              <div class="field" style="max-width: 320px; margin-top: 14px">
                <label>输入验证器生成的 6 位验证码</label>
                <input v-model="twoFA.code" type="text" class="otp-input" inputmode="numeric" maxlength="6" autocomplete="one-time-code" spellcheck="false" autocapitalize="off" placeholder="000000" />
              </div>
              <button class="btn btn-primary" :disabled="twoFA.busy" @click="enable2FA">确认启用</button>
            </template>
          </template>
        </div>
      </section>

      <section v-show="tab === 'security'" class="card section-gap">
        <div class="card-header">
          <div>
            <div class="card-title">API 密钥</div>
            <div class="card-sub">按密钥细分 API 资源权限，并可限定可访问的节点与分组</div>
          </div>
          <div class="spacer"></div>
          <button class="btn btn-primary btn-sm" @click="openKeyModal()"><Plus :size="14" /> 新建密钥</button>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>名称</th><th>令牌</th><th>权限</th><th>资源范围</th><th>使用</th><th>状态</th><th></th></tr></thead>
            <tbody>
              <tr v-for="k in apiKeys" :key="k.id">
                <td style="font-weight: 600">
                  {{ k.name }}
                  <div v-if="k.description" class="dim small">{{ k.description }}</div>
                </td>
                <td class="mono small">{{ k.prefix }}…</td>
                <td>
                  <div class="chip-list key-scopes">
                    <span v-for="sc in k.scopes" :key="sc" class="badge" :class="permissionSensitive(sc) ? 'badge-warn' : 'badge-muted'">{{ permissionLabel(sc) }}</span>
                  </div>
                </td>
                <td class="small">
                  <span v-if="!k.nodeIds?.length && !k.groupIds?.length" class="badge badge-muted">全部资源</span>
                  <div v-else class="chip-list key-scopes">
                    <span v-for="id in k.groupIds || []" :key="'g' + id" class="badge badge-info">分组 · {{ groupName(id) }}</span>
                    <span v-for="id in k.nodeIds || []" :key="'n' + id" class="badge badge-info">节点 · {{ nodeName(id) }}</span>
                  </div>
                </td>
                <td class="small dim">
                  <div>{{ k.requestCount || 0 }} 次请求</div>
                  <div>{{ k.rateLimit || 60 }} req/min</div>
                  <div>{{ k.lastUsedAt ? '最后 ' + fmtRelative(k.lastUsedAt) : '从未使用' }}</div>
                  <div v-if="k.lastUsedIp" class="mono" style="font-size: 10.5px">{{ k.lastUsedIp }}</div>
                </td>
                <td>
                  <span v-if="!k.enabled" class="badge badge-muted">已禁用</span>
                  <span v-else-if="k.expiresAt && new Date(k.expiresAt) < new Date()" class="badge badge-danger">已过期</span>
                  <span v-else class="badge badge-ok">正常</span>
                </td>
                <td>
                  <div class="row-actions">
                    <button v-tip="'编辑权限'" class="icon-btn" @click="openKeyModal(k)"><Pencil :size="15" /></button>
                    <button v-tip="'轮换令牌'" class="icon-btn" @click="rotateKey(k)"><RefreshCw :size="15" /></button>
                    <button v-tip="k.enabled ? '禁用' : '启用'" class="icon-btn" @click="toggleKey(k)"><Power :size="15" /></button>
                    <button v-tip="'删除密钥'" class="icon-btn danger" @click="deleteKey(k)"><Trash2 :size="15" /></button>
                  </div>
                </td>
              </tr>
              <tr v-if="!apiKeys.length"><td colspan="7"><div class="empty">暂无 API 密钥。</div></td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <section v-show="tab === 'certs'" class="card">
        <div class="card-header">
          <div>
            <div class="card-title">节点证书签发</div>
            <div class="card-sub">面板通过 ACME DNS-01 申请证书，再通过 Agent 控制通道下发；节点私钥不上传到面板（CSR 模式）。</div>
          </div>
        </div>
        <div class="card-body">
          <div class="form-grid">
            <div class="field">
              <label>ACME 目录</label>
              <Select v-model="certForm.ca" :options="acmeCaOptions" />
            </div>
            <div class="field"><label>ACME 邮箱</label><input v-model="certForm.email" type="email" placeholder="admin@example.com" /></div>
            <template v-if="isLiteSSL">
              <div class="field">
                <label>EAB Key ID</label>
                <input v-model="certForm.eabKid" type="text" placeholder="LiteSSL / FreeSSL 提供的 Key ID" autocomplete="off" />
              </div>
              <div class="field">
                <label>EAB HMAC Key</label>
                <input v-model="certForm.eabHmac" type="password" :placeholder="certForm.eabConfigured ? '已配置 — 留空保持不变' : 'Base64 或 Hex 编码的 HMAC Key'" autocomplete="new-password" />
              </div>
              <div class="field full">
                <span class="hint">
                  LiteSSL 强制要求 EAB。请在 FreeSSL / LiteSSL 控制台创建 EAB 凭据后填写；HMAC Key 会加密保存，不会回显。
                </span>
              </div>
            </template>
            <div class="field">
              <label>DNS 服务商</label>
              <Select v-model="certForm.provider" :options="DNS_PROVIDER_OPTIONS" />
            </div>
            <div class="field"><label>提前续期天数</label><input v-model.number="certForm.renewDays" type="number" min="1" max="90" /></div>
            <template v-if="certForm.provider === 'cloudflare'">
              <div class="field full"><label>Cloudflare API Token</label><input v-model="certForm.apiToken" type="password" :placeholder="certForm.dnsConfigured ? '已配置 — 留空保持不变' : 'Zone.DNS 编辑权限 Token'" /></div>
            </template>
            <template v-else-if="certForm.provider === 'alidns'">
              <div class="field"><label>AccessKey ID</label><input v-model="certForm.accessKeyId" type="text" :placeholder="certForm.dnsConfigured ? '已配置 — 留空保持不变' : 'LTAI…'" /></div>
              <div class="field"><label>AccessKey Secret</label><input v-model="certForm.accessKeySecret" type="password" :placeholder="certForm.dnsConfigured ? '已配置 — 留空保持不变' : ''" /></div>
            </template>
            <template v-else-if="certForm.provider === 'dnspod'">
              <div class="field full"><label>DNSPod Login Token</label><input v-model="certForm.loginToken" type="password" :placeholder="certForm.dnsConfigured ? '已配置 — 留空保持不变' : 'API Token'" /></div>
            </template>
            <div v-else class="field full">
              <span class="hint">手动模式下，申请证书时面板会显示需要添加的 <span class="mono">_acme-challenge</span> TXT 记录，添加后点击“验证并继续”。</span>
            </div>
          </div>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn" :disabled="certSaving" @click="clearCertCredentials">清除 DNS 凭据</button>
          <button v-if="certForm.eabConfigured && !isLiteSSL" class="btn" :disabled="certSaving" @click="clearEabCredentials">清除 EAB 凭据</button>
          <div class="spacer"></div>
          <button class="btn btn-primary" :disabled="certSaving" @click="saveCertSettings">保存证书设置</button>
        </div>
      </section>

      <section v-show="tab === 'backups'" class="card">
        <div class="card-header">
          <div>
            <div class="card-title">本地备份</div>
            <div class="card-sub">存储在面板数据目录中的 SQLite 快照</div>
          </div>
          <div class="spacer"></div>
          <button class="btn btn-primary btn-sm" :disabled="backupBusy" @click="createBackup">{{ backupBusy ? '创建中…' : '立即备份' }}</button>
        </div>
        <div class="card-body" style="padding-bottom: 6px">
          <div class="form-grid">
            <div class="field full">
              <label class="checkbox"><span class="switch"><input v-model="form.backup_enabled" type="checkbox" /></span> 自动备份</label>
            </div>
            <div class="field"><label>间隔（小时）</label><input v-model="form.backup_interval_hours" type="number" min="1" /></div>
            <div class="field"><label>保留数量</label><input v-model="form.backup_keep" type="number" min="1" /></div>
          </div>
          <div class="divider"></div>
          <div class="flex" style="margin-bottom: 10px">
            <span class="dim small">恢复备份：</span>
            <input type="file" accept=".tar.gz,.tgz,application/gzip" @change="restoreBackup" style="max-width: 320px" />
            <span v-if="restoreMessage" class="small" style="color: var(--warn)">{{ restoreMessage }}</span>
          </div>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>归档文件</th><th>大小</th><th>创建时间</th><th></th></tr></thead>
            <tbody>
              <tr v-for="b in backups" :key="b.name">
                <td class="mono small">{{ b.name }}</td>
                <td>{{ fmtBytes(b.size) }}</td>
                <td class="small dim">{{ fmtDate(b.createdAt) }}</td>
                <td><div class="row-actions">
                  <button v-tip="'下载备份'" class="icon-btn" @click="downloadBackup(b)"><Download :size="15" /></button>
                  <button v-tip="'删除备份'" class="icon-btn danger" @click="deleteBackup(b)"><Trash2 :size="15" /></button>
                </div></td>
              </tr>
              <tr v-if="!backups.length"><td colspan="4"><div class="empty">暂无备份。</div></td></tr>
            </tbody>
          </table>
        </div>
        <div class="modal-footer" style="border-radius: 0">
          <button class="btn btn-primary" :disabled="saving" @click="save(['backup_enabled', 'backup_interval_hours', 'backup_keep'])">保存备份设置</button>
        </div>
      </section>
    </template>

    <Modal v-model="keyModal" :title="editingKey ? '编辑 API 密钥' : '创建 API 密钥'" size="lg">
      <div v-if="keyError" class="banner banner-danger">{{ keyError }}</div>
      <div class="form-grid">
        <div class="field">
          <label>名称</label><input v-model="keyForm.name" type="text" placeholder="监控机器人" />
        </div>
        <div class="field">
          <label>说明（可选）</label><input v-model="keyForm.description" type="text" placeholder="仅用于读取节点状态" />
        </div>
      </div>
      <div class="field key-block">
        <label>API 权限</label>
        <div class="perm-groups">
          <div v-for="(items, group) in permissionGroups" :key="group" class="perm-group">
            <div class="perm-head">
              <span>{{ group }}</span>
              <button type="button" class="link-btn" @click="togglePermissionGroup(items)">
                {{ groupFullySelected(items) ? '清空' : '全选' }}
              </button>
            </div>
            <div class="chip-list">
              <button
                v-for="p in items"
                :key="p.value"
                type="button"
                class="chip"
                :class="{ on: keyForm.scopes.includes(p.value) }"
                :title="p.description || p.value"
                @click="toggleScope(p.value)"
              >
                {{ p.label }}
                <span v-if="p.sensitive" class="badge badge-warn">敏感</span>
              </button>
            </div>
          </div>
        </div>
        <span class="hint">{{ selectedScopesSummary }}</span>
      </div>
      <div class="field key-block">
        <label>资源范围（留空表示全部）</label>
        <div class="resource-picker">
          <div>
            <div class="mini-title">分组</div>
            <div class="chip-list scroll-chips">
              <button v-for="g in resources.groups" :key="g.id" type="button" class="chip" :class="{ on: keyForm.groupIds.includes(g.id) }" @click="toggleResource('groupIds', g.id)">{{ g.name }}</button>
              <span v-if="!resources.groups.length" class="dim small">暂无分组</span>
            </div>
          </div>
          <div>
            <div class="mini-title">节点</div>
            <div class="chip-list scroll-chips">
              <button v-for="n in resources.nodes" :key="n.id" type="button" class="chip" :class="{ on: keyForm.nodeIds.includes(n.id) }" @click="toggleResource('nodeIds', n.id)">{{ n.name }}</button>
              <span v-if="!resources.nodes.length" class="dim small">暂无节点</span>
            </div>
          </div>
        </div>
        <span class="hint">限定后，该密钥只能访问选中的分组、节点及其关联用户；仪表盘、设置、备份与全局同步等全局接口会被拒绝。</span>
      </div>
      <div class="form-grid">
        <div class="field"><label>速率限制（请求 / 分钟）</label><input v-model.number="keyForm.rateLimit" type="number" min="1" max="6000" /></div>
        <div class="field"><label>到期时间（可选）</label><input v-model="keyForm.expiresAt" type="datetime-local" /></div>
      </div>
      <template #footer>
        <button class="btn" @click="keyModal = false">取消</button>
        <button class="btn btn-primary" :disabled="keySaving" @click="saveKey">{{ keySaving ? '保存中…' : (editingKey ? '保存修改' : '创建密钥') }}</button>
      </template>
    </Modal>

    <Modal v-model="tokenModal" title="API 密钥令牌" size="sm">
      <div class="banner banner-warn">请立即复制该令牌，之后无法再次查看。旧令牌已失效。</div>
      <CopyField :value="newToken" prefix="Token" />
    </Modal>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Download, Pencil, Plus, Power, RefreshCw, Trash2 } from 'lucide-vue-next'
import { API_BASE, api, resetAdminConfig, setAdminEntry } from '../api'
import { askConfirm, auth, notify, setSession } from '../store'
import CopyField from '../components/CopyField.vue'
import DsnTemplate from '../components/DsnTemplate.vue'
import Modal from '../components/Modal.vue'
import Select from '../components/Select.vue'
import { copyText, fmtBytes, fmtDate, fmtRelative } from '../format'
import { setTheme, theme, themeOptions } from '../theme'

const tab = ref('general')
const route = useRoute()
const router = useRouter()
const settings = ref(null)
const meta = ref({ version: '', platform: '', dataDir: '' })
const form = reactive({})
const error = ref('')
const saved = ref('')
const saving = ref(false)
const pwSaving = ref(false)
const pw = reactive({ old: '', next: '', confirm: '', code: '' })
const account = reactive({ username: auth.admin?.username || '', password: '', code: '', saving: false })
const accountError = ref('')

const subscriptionPreview = ref('')
const adminEntryPreview = computed(() => {
  const pub = (form.public_url || '').replace(/\/+$/, '') || window.location.origin
  const p = String(form.admin_security_path || '').trim()
  return p ? `${pub}/admin/${p}` : `${pub}/admin`
})
const apiKeys = ref([])
const permissions = ref([])
const resources = ref({ nodes: [], groups: [] })
const keyModal = ref(false)
const keySaving = ref(false)
const keyError = ref('')
const editingKey = ref(null)
const keyForm = reactive({ name: '', description: '', scopes: [], nodeIds: [], groupIds: [], rateLimit: 60, expiresAt: '' })
const tokenModal = ref(false)
const newToken = ref('')
const backups = ref([])
const backupBusy = ref(false)
const restoreMessage = ref('')
const usage = ref(null)
const usageLoading = ref(false)
const cleanupBusy = ref(false)
const cleanupVacuum = ref(false)
const infra = ref(null)
const infraLoading = ref(false)
const infraTesting = ref(false)
const infraSaving = ref(false)
const infraError = ref('')
const infraMessage = ref('')
const infraForm = reactive({
  telemetry: { enabled: false, dsn: '', schema: 'public', required: false },
  redis: { enabled: false, url: '', prefix: 'mikvace:', required: false },
})
const infraBusy = computed(() => infraLoading.value || infraTesting.value || infraSaving.value)
const twoFA = reactive({ enabled: false, recoveryRemaining: 0, password: '', code: '', busy: false, setup: null, newCodes: null, error: '' })
const certForm = reactive({
  ca: 'https://acme-v02.api.letsencrypt.org/directory', email: '', provider: 'manual',
  renewDays: 30, apiToken: '', accessKeyId: '', accessKeySecret: '', loginToken: '',
  dnsConfigured: false, eabKid: '', eabHmac: '', eabConfigured: false,
})
const certSaving = ref(false)
const isLiteSSL = computed(() => String(certForm.ca || '').includes('litessl.com'))

const HAPP_ROUTING_OPTIONS = [
  { value: 'off', label: '关闭（清除 HAPP 路由）' },
  { value: 'custom', label: '自定义路由配置' },
]
const SUBSCRIPTION_HY2_OPTIONS = [
  { value: 'both', label: 'TLS 和 Hopping 都开启' },
  { value: 'tls', label: '仅开启 TLS 入口' },
  { value: 'hopping', label: '仅开启 Hopping 入口' },
]
const ACME_CA_OPTIONS = [
  { value: 'https://acme-v02.api.letsencrypt.org/directory', label: "Let's Encrypt" },
  { value: 'https://acme.litessl.com/acme/v2/directory', label: 'LiteSSL' },
]
// Keep a previously saved directory visible even after it is removed from the
// built-in list (for example the old Let's Encrypt Staging endpoint), so the
// operator can see which CA is actually in use.
const acmeCaOptions = computed(() => {
  const ca = String(certForm.ca || '')
  if (!ca || ACME_CA_OPTIONS.some((o) => o.value === ca)) return ACME_CA_OPTIONS
  const label = ca.includes('acme-staging-v02.api.letsencrypt.org')
    ? "Let's Encrypt Staging（已弃用）"
    : ca
  return [...ACME_CA_OPTIONS, { value: ca, label }]
})
const DNS_PROVIDER_OPTIONS = [
  { value: 'manual', label: '手动 TXT（无 API）' },
  { value: 'cloudflare', label: 'Cloudflare' },
  { value: 'alidns', label: '阿里云 DNS' },
  { value: 'dnspod', label: 'DNSPod' },
]

// Settings are stored as strings, so checkbox-bound keys must be normalized
// when loading and stringified when saving.
const BOOL_KEYS = [
  'log_ingest_enabled',
]

function applySettings(src) {
  Object.assign(form, src)
  if (!form.subscription_hy2_endpoints) form.subscription_hy2_endpoints = 'both'
  if (!form.metric_retention_days) form.metric_retention_days = '30'
  for (const k of BOOL_KEYS) form[k] = form[k] === true || form[k] === 'true'
}

function fmtRows(n) {
  return n === undefined || n === null ? '—' : Number(n).toLocaleString('zh-CN')
}

function syncPreview() {
  const pub = (form.public_url || '').replace(/\/+$/, '')
  subscriptionPreview.value = pub ? `${pub}/api/files/<token>` : '/api/files/<token>'
}
async function loadBackups() {
  try {
    const res = await api.backups()
    backups.value = res.items || []
  } catch (e) {
    error.value = e.message
  }
}
async function loadUsage() {
  usageLoading.value = true
  try {
    usage.value = await api.maintenanceUsage()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    usageLoading.value = false
  }
}
async function runCleanup() {
  const ok = await askConfirm('立即删除超过保留天数的日志和监控数据？此操作不可撤销。', {
    title: '按保留期清理',
    confirmText: '开始清理',
    tone: 'danger',
  })
  if (!ok) return
  cleanupBusy.value = true
  try {
    const res = await api.cleanupData({
      vacuum: cleanupVacuum.value,
      logRetentionDays: Number(form.log_retention_days) || 0,
      metricRetentionDays: Number(form.metric_retention_days) || 0,
    })
    const d = res.deleted || {}
    let msg = `清理完成：日志 ${fmtRows(d.logs)}、日志批次 ${fmtRows(d.logBatches)}、事件 ${fmtRows(d.events)}、流量批次 ${fmtRows(d.statsBatches)}、监控采样 ${fmtRows(d.nodeMetrics)}`
    if (res.vacuumed) msg += '，数据库已压缩'
    else if (res.reclaimedPages > 0) msg += `，已回收 ${fmtBytes((res.reclaimedPages || 0) * (usage.value?.pageSize || 4096))}`
    else if (res.vacuumError) msg += `；压缩失败：${res.vacuumError}`
    if (res.freeBytes > 0) msg += `（仍有 ${fmtBytes(res.freeBytes)} 可回收空间）`
    if (!d.logs) msg += '（没有超过保留期的代理日志；如需全部清空请到「日志分析」页）'
    notify(msg, res.totalDeleted ? 'success' : 'info', 8000)
    await loadUsage()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    cleanupBusy.value = false
  }
}
async function createBackup() {
  backupBusy.value = true
  try {
    await api.createBackup()
    notify('备份创建成功')
    loadBackups()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    backupBusy.value = false
  }
}
async function downloadBackup(b) {
  try {
    const blob = await api.downloadBackup(b.name)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = b.name
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function deleteBackup(b) {
  const ok = await askConfirm(`删除备份 ${b.name}?`, {
    title: '删除备份',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!ok) return
  try {
    await api.deleteBackup(b.name)
    notify('备份删除成功')
    loadBackups()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function restoreBackup(ev) {
  const file = ev.target.files?.[0]
  ev.target.value = ''
  if (!file) return
  const ok = await askConfirm('用该备份替换面板数据库？支持 MikvAce 与 CELERITY 备份，必须重启面板后才会生效。', {
    title: '恢复面板数据库',
    confirmText: '继续恢复',
    tone: 'danger',
  })
  if (!ok) return
  try {
    const res = await api.restoreBackup(file)
    const warnings = Array.isArray(res.warnings) ? res.warnings : []
    restoreMessage.value = [res.message || '重启面板后恢复生效。', ...warnings].join(' ')
    if (res.source === 'celerity') {
      const s = res.summary || {}
      notify(`CELERITY 备份已导入：${s.users || 0} 用户 / ${s.nodes || 0} 节点 / ${s.groups || 0} 分组 / ${s.apiKeys || 0} API 密钥 — 重启面板后生效`)
    } else {
      notify('备份校验通过 — 重启面板后生效')
    }
  } catch (e) {
    notify(e.message, 'error')
  }
}
const permissionGroups = computed(() => {
  const out = {}
  for (const p of permissions.value) {
    if (!out[p.group]) out[p.group] = []
    out[p.group].push(p)
  }
  return out
})
const selectedScopesSummary = computed(() => {
  if (!keyForm.scopes.length) return '尚未选择任何权限，该密钥将无法访问管理 API。'
  return `已选择 ${keyForm.scopes.length} 项权限`
})
function permissionLabel(value) {
  return permissions.value.find((p) => p.value === value)?.label || value
}
function permissionSensitive(value) {
  return !!permissions.value.find((p) => p.value === value)?.sensitive
}
function nodeName(id) {
  return resources.value.nodes.find((n) => n.id === id)?.name || `#${id}`
}
function groupName(id) {
  return resources.value.groups.find((g) => g.id === id)?.name || `#${id}`
}
function groupFullySelected(items) {
  return items.length > 0 && items.every((p) => keyForm.scopes.includes(p.value))
}
function togglePermissionGroup(items) {
  if (groupFullySelected(items)) {
    keyForm.scopes = keyForm.scopes.filter((v) => !items.some((p) => p.value === v))
    return
  }
  for (const p of items) {
    if (!keyForm.scopes.includes(p.value)) keyForm.scopes.push(p.value)
  }
}
function toggleResource(field, id) {
  const i = keyForm[field].indexOf(id)
  if (i >= 0) keyForm[field].splice(i, 1)
  else keyForm[field].push(id)
}
function toLocalInput(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}
async function loadKeys() {
  try {
    const res = await api.apiKeys()
    apiKeys.value = res.items || []
    permissions.value = res.permissions || res.scopes || []
    resources.value = res.resources || { nodes: [], groups: [] }
  } catch (e) {
    error.value = e.message
  }
}
function openKeyModal(k = null) {
  editingKey.value = k
  keyForm.name = k?.name || ''
  keyForm.description = k?.description || ''
  keyForm.scopes = k ? [...(k.scopes || [])] : ['dashboard:read']
  keyForm.nodeIds = k ? [...(k.nodeIds || [])] : []
  keyForm.groupIds = k ? [...(k.groupIds || [])] : []
  keyForm.rateLimit = k?.rateLimit || 60
  keyForm.expiresAt = k?.expiresAt ? toLocalInput(k.expiresAt) : ''
  keyError.value = ''
  keyModal.value = true
}
function toggleScope(v) {
  const i = keyForm.scopes.indexOf(v)
  if (i >= 0) keyForm.scopes.splice(i, 1)
  else keyForm.scopes.push(v)
}
async function saveKey() {
  keyError.value = ''
  keySaving.value = true
  try {
    const payload = {
      name: keyForm.name,
      description: keyForm.description,
      scopes: keyForm.scopes,
      nodeIds: keyForm.nodeIds,
      groupIds: keyForm.groupIds,
      rateLimit: keyForm.rateLimit,
      expiresAt: keyForm.expiresAt ? new Date(keyForm.expiresAt).toISOString() : null,
    }
    if (editingKey.value) {
      await api.updateAPIKey(editingKey.value.id, payload)
      keyModal.value = false
      notify('API 密钥已更新')
    } else {
      const res = await api.createAPIKey(payload)
      keyModal.value = false
      newToken.value = res.token
      tokenModal.value = true
      notify('API 密钥已创建')
    }
    loadKeys()
  } catch (e) {
    keyError.value = e.message
  } finally {
    keySaving.value = false
  }
}
async function rotateKey(k) {
  const ok = await askConfirm(`轮换 API 密钥 "${k.name}" 的令牌？旧令牌会立即失效。`, {
    title: '轮换 API 令牌',
    confirmText: '轮换',
    tone: 'danger',
  })
  if (!ok) return
  try {
    const res = await api.rotateAPIKey(k.id)
    newToken.value = res.token
    tokenModal.value = true
    notify('令牌已轮换')
    loadKeys()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function toggleKey(k) {
  try {
    if (k.enabled) await api.disableAPIKey(k.id)
    else await api.enableAPIKey(k.id)
    notify(k.enabled ? 'API key disabled' : 'API key enabled')
    loadKeys()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function deleteKey(k) {
  const ok = await askConfirm(`删除 API 密钥 "${k.name}"？使用它的自动化将停止工作。`, {
    title: '删除 API 密钥',
    confirmText: '删除',
    tone: 'danger',
  })
  if (!ok) return
  try {
    await api.deleteAPIKey(k.id)
    notify('API key deleted')
    loadKeys()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function load2FA() {
  try {
    const r = await api.twoFactorStatus()
    twoFA.enabled = !!r.enabled
    twoFA.recoveryRemaining = r.recoveryRemaining || 0
  } catch {
    // Ignore; the security tab shows the error when an action is attempted.
  }
}
async function start2FA() {
  twoFA.error = ''
  twoFA.busy = true
  try {
    twoFA.setup = await api.setupTwoFactor()
    twoFA.code = ''
  } catch (e) {
    twoFA.error = e.message
  } finally {
    twoFA.busy = false
  }
}
async function enable2FA() {
  twoFA.error = ''
  twoFA.busy = true
  try {
    await api.enableTwoFactor(twoFA.code)
    twoFA.enabled = true
    twoFA.setup = null
    twoFA.code = ''
    notify('两步验证已启用')
    await load2FA()
  } catch (e) {
    twoFA.error = e.message
  } finally {
    twoFA.busy = false
  }
}
async function disable2FA() {
  const ok = await askConfirm('关闭两步验证？之后仅使用密码即可登录。', {
    title: '关闭两步验证',
    confirmText: '关闭',
    tone: 'danger',
  })
  if (!ok) return
  twoFA.error = ''
  twoFA.busy = true
  try {
    await api.disableTwoFactor(twoFA.password, twoFA.code)
    twoFA.enabled = false
    twoFA.password = ''
    twoFA.code = ''
    notify('两步验证已关闭')
    await load2FA()
  } catch (e) {
    twoFA.error = e.message
  } finally {
    twoFA.busy = false
  }
}
async function regenerateRecovery() {
  twoFA.error = ''
  twoFA.busy = true
  try {
    const r = await api.regenerateRecovery(twoFA.password, twoFA.code)
    twoFA.newCodes = r.recoveryCodes || []
    twoFA.password = ''
    twoFA.code = ''
    notify('恢复码已重新生成')
    await load2FA()
  } catch (e) {
    twoFA.error = e.message
  } finally {
    twoFA.busy = false
  }
}
function copyCodes(codes) {
  copyText(codes.join('\n'))
  notify('恢复码已复制')
}
async function loadCertSettings() {
  try {
    const r = await api.certSettings()
    certForm.ca = r.ca || certForm.ca
    certForm.email = r.email || ''
    certForm.provider = r.provider || 'manual'
    certForm.renewDays = r.renewDays || 30
    certForm.dnsConfigured = !!r.dnsConfigured
    certForm.eabKid = r.eabKid || ''
    certForm.eabConfigured = !!r.eabConfigured
    certForm.eabHmac = ''
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function saveCertSettings() {
  const eabKid = String(certForm.eabKid || '').trim()
  const eabHmac = String(certForm.eabHmac || '').trim()
  if (isLiteSSL.value && (!eabKid || (!eabHmac && !certForm.eabConfigured))) {
    notify('LiteSSL 需要 EAB Key ID 和 HMAC Key', 'error')
    return
  }
  certSaving.value = true
  try {
    const payload = { ca: certForm.ca, email: certForm.email, provider: certForm.provider, renewDays: Number(certForm.renewDays) || 30 }
    if (isLiteSSL.value) {
      if (eabKid) payload.eabKid = eabKid
      if (eabHmac) payload.eabHmac = eabHmac
    }
    const dns = {}
    if (certForm.provider === 'cloudflare' && certForm.apiToken) dns.apiToken = certForm.apiToken
    if (certForm.provider === 'alidns') {
      if (certForm.accessKeyId) dns.accessKeyId = certForm.accessKeyId
      if (certForm.accessKeySecret) dns.accessKeySecret = certForm.accessKeySecret
    }
    if (certForm.provider === 'dnspod' && certForm.loginToken) dns.loginToken = certForm.loginToken
    if (certForm.provider === 'manual') payload.dnsConfig = {}
    else if (Object.keys(dns).length) payload.dnsConfig = dns
    await api.updateCertSettings(payload)
    certForm.apiToken = ''
    certForm.accessKeyId = ''
    certForm.accessKeySecret = ''
    certForm.loginToken = ''
    certForm.eabHmac = ''
    notify('证书设置已保存')
    await loadCertSettings()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    certSaving.value = false
  }
}
async function clearEabCredentials() {
  const ok = await askConfirm('清除已保存的 EAB 凭据？LiteSSL 需要重新填写才能签发。', {
    title: '清除 EAB 凭据',
    confirmText: '清除',
    tone: 'danger',
  })
  if (!ok) return
  certSaving.value = true
  try {
    await api.updateCertSettings({
      ca: certForm.ca, email: certForm.email, provider: certForm.provider,
      renewDays: Number(certForm.renewDays) || 30, clearEab: true,
    })
    certForm.eabKid = ''
    certForm.eabHmac = ''
    certForm.eabConfigured = false
    notify('EAB 凭据已清除')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    certSaving.value = false
  }
}
async function clearCertCredentials() {
  const ok = await askConfirm('清除已保存的 DNS 服务商凭据？', {
    title: '清除 DNS 凭据',
    confirmText: '清除',
    tone: 'danger',
  })
  if (!ok) return
  certSaving.value = true
  try {
    await api.updateCertSettings({ ca: certForm.ca, email: certForm.email, provider: certForm.provider, renewDays: Number(certForm.renewDays) || 30, dnsConfig: {} })
    certForm.dnsConfigured = false
    notify('DNS 凭据已清除')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    certSaving.value = false
  }
}
function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function applyInfraStatus(s) {
  infra.value = s
  infraForm.telemetry.enabled = s.telemetry?.driver === 'postgres'
  infraForm.telemetry.dsn = ''
  infraForm.telemetry.schema = s.telemetry?.schema || 'public'
  infraForm.telemetry.required = !!s.telemetry?.required
  infraForm.redis.enabled = !!s.redis?.enabled
  infraForm.redis.url = ''
  infraForm.redis.prefix = s.redis?.prefix || 'mikvace:'
  infraForm.redis.required = !!s.redis?.required
}

async function loadInfra() {
  infraLoading.value = true
  infraError.value = ''
  try {
    applyInfraStatus(await api.infraStatus())
  } catch (e) {
    infraError.value = e.message
  } finally {
    infraLoading.value = false
  }
}

function infraPayload() {
  return {
    telemetry: {
      enabled: !!infraForm.telemetry.enabled,
      dsn: infraForm.telemetry.dsn.trim(),
      schema: infraForm.telemetry.schema.trim() || 'public',
      required: !!infraForm.telemetry.required,
    },
    redis: {
      enabled: !!infraForm.redis.enabled,
      url: infraForm.redis.url.trim(),
      prefix: infraForm.redis.prefix.trim() || 'mikvace:',
      required: !!infraForm.redis.required,
    },
  }
}

function validateInfra() {
  infraError.value = ''
  infraMessage.value = ''
  if (infraForm.telemetry.enabled && !infraForm.telemetry.dsn.trim() && !infra.value?.telemetry?.configured) {
    infraError.value = '请填写 PostgreSQL DSN，或关闭外部日志库。'
    return false
  }
  if (infraForm.redis.enabled && !infraForm.redis.url.trim() && !infra.value?.redis?.configured) {
    infraError.value = '请填写 Redis URL，或关闭 Redis。'
    return false
  }
  return true
}

async function testInfra() {
  if (!validateInfra()) return
  infraTesting.value = true
  try {
    const res = await api.testInfra(infraPayload())
    const parts = [`连接正常（${res.latencyMs ?? 0} ms）`]
    if (res.telemetry?.enabled) parts.push('PostgreSQL 正常')
    if (res.redis?.enabled) parts.push('Redis 正常')
    infraMessage.value = parts.join(' · ')
  } catch (e) {
    infraError.value = e.message
  } finally {
    infraTesting.value = false
  }
}

async function waitForPanelRestart() {
  await sleep(1800)
  const deadline = Date.now() + 60000
  let healthy = 0
  while (Date.now() < deadline) {
    try {
      const res = await fetch(API_BASE + '/health', { cache: 'no-store' })
      // Two consecutive healthy checks avoid returning while the old process
      // is still flushing its final response before exec.
      healthy = res.ok ? healthy + 1 : 0
      if (healthy >= 2) return
    } catch {
      // The process is re-executing.
      healthy = 0
    }
    await sleep(1000)
  }
  throw new Error('等待面板重启超时，请手动刷新页面')
}

async function saveInfra() {
  if (!validateInfra()) return
  const ok = await askConfirm('保存存储与缓存配置后需要重启面板才能生效。确定继续？', {
    title: '保存并重启',
    confirmText: '保存并重启',
  })
  if (!ok) return
  infraSaving.value = true
  infraError.value = ''
  infraMessage.value = ''
  try {
    const res = await api.saveInfra(infraPayload())
    if (!res.restartRequired) {
      infraMessage.value = '配置已保存，无需重启'
      notify('配置已保存')
      await loadInfra()
      return
    }
    infraMessage.value = '配置已保存，面板正在重启…'
    await waitForPanelRestart()
    await loadInfra()
    infraMessage.value = '配置已生效'
    notify('存储与缓存配置已生效')
  } catch (e) {
    infraError.value = e.message
  } finally {
    infraSaving.value = false
  }
}

async function load() {
  try {
    const res = await api.settings()
    settings.value = res.settings
    meta.value = { version: res.version, platform: res.platform, dataDir: res.dataDir }
    applySettings(res.settings)
    syncPreview()
    if (auth.admin?.username) account.username = auth.admin.username
    loadCertSettings()
    load2FA()
  } catch (e) {
    error.value = e.message
  }
}
function onIngestToggle() {
  form.log_ingest_enabled = !!form.log_ingest_enabled
}
function generateSecurityPath() {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789'
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  form.admin_security_path = Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('')
}
async function save(keys) {
  const securityChanged = keys.includes('admin_security_path')
  const securityPath = String(form.admin_security_path || '').trim()
  if (securityChanged) {
    if (!/^[A-Za-z0-9_-]{4,64}$/.test(securityPath)) {
      notify('入口路径需要 4–64 位，仅允许字母、数字、短横线和下划线', 'error')
      return
    }
    if (['admin', 'api', 'assets', 'panel', 'user', 'login', 'logout', 'dashboard', 'users', 'nodes', 'groups', 'logs', 'settings'].includes(securityPath.toLowerCase())) {
      notify('该入口路径是保留字，请换一个', 'error')
      return
    }
  }
  saving.value = true
  error.value = ''
  saved.value = ''
  try {
    const payload = {}
    for (const k of keys) {
      payload[k] = form[k]
    }
    for (const k of BOOL_KEYS) {
      if (keys.includes(k)) payload[k] = form[k] ? 'true' : 'false'
    }
    const res = await api.updateSettings(payload)
    settings.value = res.settings
    applySettings(res.settings)
    syncPreview()
    if (securityChanged) {
      const path = String(form.admin_security_path || '').trim()
      setAdminEntry(path)
      resetAdminConfig()
      if (route.path.startsWith('/admin')) {
        router.replace(`/admin/${path}/settings`)
      }
    }
    saved.value = '设置已保存。'
    notify('设置已保存')
    setTimeout(() => (saved.value = ''), 3000)
  } catch (e) {
    error.value = e.message
    notify(e.message, 'error')
  } finally {
    saving.value = false
  }
}
async function saveAccount() {
  accountError.value = ''
  const username = account.username.trim()
  if (!/^[A-Za-z0-9._-]{3,32}$/.test(username)) {
    return notify('用户名需要 3–32 位，仅允许字母、数字、点、下划线和短横线', 'error')
  }
  if (!account.password) return notify('请输入当前密码', 'error')
  if (twoFA.enabled && !account.code.trim()) return notify('请输入动态验证码或恢复码', 'error')
  account.saving = true
  try {
    const res = await api.updateAccount(username, account.password, account.code)
    const merged = { ...auth.admin, ...res.admin }
    if (res.token) setSession(res.token, merged, auth.warning)
    else setSession(auth.token, merged, auth.warning)
    account.username = username
    account.password = ''
    account.code = ''
    notify('账号信息已更新')
  } catch (e) {
    accountError.value = e.message
    notify(e.message, 'error')
  } finally {
    account.saving = false
  }
}

async function changePassword() {
  if (pw.next.length < 8) return notify('新密码至少需要 8 个字符', 'error')
  if (pw.next !== pw.confirm) return notify('两次输入的密码不一致', 'error')
  if (pw.next === pw.old) return notify('新密码不能与当前密码相同', 'error')
  if (twoFA.enabled && !pw.code.trim()) return notify('请输入动态验证码或恢复码', 'error')
  pwSaving.value = true
  try {
    const res = await api.changePassword(pw.old, pw.next, pw.code)
    const merged = { ...auth.admin, ...res.admin }
    if (res.token) setSession(res.token, merged, '')
    else setSession(auth.token, merged, '')
    pw.old = ''
    pw.next = ''
    pw.confirm = ''
    pw.code = ''
    notify('密码已更新')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    pwSaving.value = false
  }
}
onMounted(() => { load(); loadKeys(); loadBackups(); loadUsage() })
</script>

<style scoped>
.key-scopes { max-width: 330px; }
.key-block { margin-top: 14px; }
.perm-groups {
  display: grid; grid-template-columns: repeat(auto-fit, minmax(230px, 1fr));
  gap: 12px; margin: 4px 0 8px;
}
.perm-group { border: 1px solid var(--line); padding: 10px 12px; background: var(--panel-2); }
.perm-head {
  display: flex; align-items: center; justify-content: space-between;
  margin-bottom: 8px; font-size: 12px; font-weight: 600;
}
.link-btn {
  border: 0; background: none; padding: 0; cursor: pointer;
  color: var(--accent); font-size: 11px;
}
.resource-picker { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin: 4px 0 8px; }
.mini-title {
  font-family: var(--mono); font-size: 9.5px; letter-spacing: 0.1em;
  text-transform: uppercase; color: var(--muted-2); margin-bottom: 6px;
}
.scroll-chips { max-height: 132px; overflow-y: auto; padding-right: 4px; }
@media (max-width: 760px) { .resource-picker { grid-template-columns: 1fr; } }

.theme-options { display: flex; gap: 12px; flex-wrap: wrap; }
.theme-option {
  display: flex; align-items: center; gap: 12px; width: 236px; padding: 12px;
  border: 1px solid var(--line); border-radius: 0; background: var(--panel-2);
  color: var(--text); text-align: left; transition: 0.16s ease;
}
.theme-option:hover { border-color: var(--line-strong); }
.theme-option.on { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
.theme-swatch { width: 44px; height: 34px; border-radius: 0; border: 1px solid var(--line); flex: none; }
.theme-swatch--dark { background: linear-gradient(135deg, #0d0d0c 55%, #ff6b3d 55%); }
.theme-swatch--light { background: linear-gradient(135deg, #f3efe6 55%, #d9481f 55%); }
.theme-meta { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.theme-meta strong { font-size: 12.5px; }
.theme-meta small { font-size: 11px; color: var(--muted-2); }
</style>
