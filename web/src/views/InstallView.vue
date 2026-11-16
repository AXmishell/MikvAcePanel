<template>
  <div class="login-shell login-shell--centered">
    <main class="login-panel">
      <div class="login-card">
        <div class="login-brand-head login-mobile-head">
          <div class="brand-logo">M</div>
          <div class="brand-word">MikvAce<span>Panel</span></div>
        </div>

        <div class="steps" v-if="!loadingStatus">
          <button
            type="button"
            class="step"
            :class="{ active: step === 'database', done: step === 'admin' }"
            :disabled="restarting"
            @click="step = 'database'"
          >
            <span>01</span> 数据库
          </button>
          <span class="step-line"></span>
          <button
            type="button"
            class="step"
            :class="{ active: step === 'admin' }"
            :disabled="restarting || status.setupMode"
            @click="goAdmin"
          >
            <span>02</span> 管理员
          </button>
        </div>

        <div v-if="loadingStatus" class="hint" style="padding: 8px 0">正在检查安装状态…</div>
        <div v-else-if="!status.configWritable" class="banner banner-danger" style="margin-bottom: 12px">
          配置文件不可写：<span class="mono">{{ status.configPath }}</span>。请把 panel.yaml 以可写方式挂载后重启面板。
        </div>

        <div v-else-if="importDone" class="restart-box">
          <ShieldCheck :size="22" />
          <strong>备份导入完成</strong>
          <span class="hint">{{ importDoneMessage }}</span>
          <div v-if="importWarnings.length" class="banner banner-info import-warnings">
            <span v-for="warning in importWarnings" :key="warning">{{ warning }}</span>
          </div>
          <button class="btn btn-primary" type="button" @click="goLogin">前往登录</button>
        </div>

        <div v-else-if="restarting" class="restart-box">
          <Loader2 :size="22" class="spin" />
          <strong>{{ restartTitle }}</strong>
          <span class="hint">{{ restartHint }}</span>
        </div>

        <form v-else-if="step === 'database'" @submit.prevent="saveDatabase">
          <div v-if="status.setupMode" class="banner banner-info" style="margin-bottom: 12px">
            当前未连接数据库，面板没有创建任何数据库文件。保存 SQLite 路径后会写入配置并重启。
          </div>

          <div class="field">
            <label>SQLite 数据文件</label>
            <input v-model="db.path" type="text" class="mono" placeholder="/data/panel.db" />
            <span class="hint">
              留空使用默认路径 <span class="mono">{{ defaultSQLitePath }}</span>；所在目录必须可写并持久化。
            </span>
          </div>

          <div class="optional-block">
            <label class="checkbox">
              <span class="switch"><input v-model="db.telemetry.enabled" type="checkbox" /><span></span></span>
              <span>外部 PostgreSQL 日志库（可选）</span>
            </label>
            <p class="hint">
              用户、节点、分组、订阅和流量统计仍保存在 SQLite；只有代理日志与日志批次迁移到 PostgreSQL。建议通过 HTTPS 打开安装页；密钥只写入本机 0600 文件。
            </p>
            <div v-if="db.telemetry.enabled" class="optional-fields">
              <div class="field">
                <label>PostgreSQL DSN</label>
                <input
                  v-model="db.telemetry.dsn"
                  type="text"
                  class="mono"
                  autocomplete="off"
                  spellcheck="false"
                  :placeholder="status.telemetryConfig?.configured ? '已配置 — 留空保持不变' : 'postgres://user:pass@host:5432/db?sslmode=disable'"
                />
                <span class="hint">只写入 data 目录下的密钥文件（0600），不会进入 panel.yaml。</span>
                <DsnTemplate :templates="status.dsnTemplates?.postgres || []" @apply="(v) => (db.telemetry.dsn = v)" />
              </div>
              <div class="form-grid">
                <div class="field">
                  <label>Schema</label>
                  <input v-model="db.telemetry.schema" type="text" class="mono" placeholder="public" />
                </div>
                <div class="field">
                  <label>连接失败时</label>
                  <label class="checkbox">
                    <span class="switch"><input v-model="db.telemetry.required" type="checkbox" /><span></span></span>
                    <span>拒绝启动</span>
                  </label>
                  <span class="hint">关闭时回退到 SQLite 日志表，认证与订阅不受影响。</span>
                </div>
              </div>
            </div>
          </div>

          <div class="optional-block">
            <label class="checkbox">
              <span class="switch"><input v-model="db.redis.enabled" type="checkbox" /><span></span></span>
              <span>Redis（可选，启用高级缓存）</span>
            </label>
            <p class="hint">
              启用后：管理员会话跨实例共享，登录验证码与限流共享，日志分析结果进入 Redis 二级缓存。
            </p>
            <div v-if="db.redis.enabled" class="optional-fields">
              <div class="field">
                <label>Redis URL</label>
                <input
                  v-model="db.redis.url"
                  type="text"
                  class="mono"
                  autocomplete="off"
                  spellcheck="false"
                  :placeholder="status.redisConfig?.enabled ? '已配置 — 留空保持不变' : 'redis://:password@host:6379/0'"
                />
                <span class="hint">只写入 data 目录下的密钥文件（0600），不会进入 panel.yaml。</span>
                <DsnTemplate :templates="status.dsnTemplates?.redis || []" @apply="(v) => (db.redis.url = v)" />
              </div>
              <div class="form-grid">
                <div class="field">
                  <label>Key 前缀</label>
                  <input v-model="db.redis.prefix" type="text" class="mono" placeholder="mikvace:" />
                </div>
                <div class="field">
                  <label>连接失败时</label>
                  <label class="checkbox">
                    <span class="switch"><input v-model="db.redis.required" type="checkbox" /><span></span></span>
                    <span>拒绝启动</span>
                  </label>
                  <span class="hint">关闭时会话与缓存自动退回进程内存。</span>
                </div>
              </div>
            </div>
          </div>

          <div v-if="testMessage" class="banner banner-info" style="margin: 4px 0 12px">{{ testMessage }}</div>
          <div v-if="error" class="banner banner-danger" style="margin: 4px 0 12px">{{ error }}</div>

          <div class="btn-row">
            <button type="button" class="btn" :disabled="busy" @click="testConnection">
              <Loader2 v-if="testing" :size="15" class="spin" />
              <PlugZap v-else :size="15" />
              {{ testing ? '测试中…' : '测试路径' }}
            </button>
            <button type="submit" class="btn btn-primary" :disabled="busy">
              <Loader2 v-if="saving" :size="15" class="spin" />
              <ArrowRight v-else :size="15" />
              {{ saving ? '保存中…' : '保存并继续' }}
            </button>
          </div>

          <div class="optional-block import-block">
            <div class="import-head">
              <Upload :size="14" />
              <strong>从备份导入</strong>
            </div>
            <p class="hint">
              支持本面板导出的 <span class="mono">.tar.gz</span> 备份和 CELERITY mongodump 备份。
              档案会写入上方 SQLite 路径（留空则使用默认路径），面板校验并重启后生效；已有管理员时无需再创建管理员。
              外部 PostgreSQL 日志库和 Redis 不受导入影响，可在面板启动后于「设置 → 基础设施」中配置。
            </p>
            <label class="file-pick" :class="{ 'file-pick--disabled': busy }">
              <input type="file" accept=".gz,.tgz,.tar.gz,application/gzip" :disabled="busy" @change="pickBackup" />
              <span class="mono">{{ backupFile ? backupFile.name : '选择备份文件…' }}</span>
            </label>
            <div v-if="backupFile" class="hint mono">{{ formatSize(backupFile.size) }}</div>
            <button type="button" class="btn import-btn" :disabled="busy || !backupFile" @click="importBackup">
              <Loader2 v-if="importing" :size="15" class="spin" />
              <Upload v-else :size="15" />
              {{ importing ? '导入中…' : '导入备份并恢复' }}
            </button>
          </div>

          <button v-if="!status.setupMode" type="button" class="use-current" :disabled="busy" @click="useCurrent">
            使用当前数据库继续
          </button>
        </form>

        <form v-else @submit.prevent="submit">
          <div class="field">
            <label>面板名称</label>
            <input v-model="form.panelName" type="text" maxlength="64" placeholder="MikvAce Panel" />
          </div>
          <div class="field">
            <label>公共访问地址</label>
            <input v-model="form.publicUrl" type="url" placeholder="https://panel.example.com" />
            <span class="hint">用于订阅链接和节点连接；留空则使用当前访问地址。</span>
          </div>
          <div class="field">
            <label>管理员用户名</label>
            <input v-model="form.username" type="text" autocomplete="username" placeholder="admin" autofocus />
            <span class="hint">3–32 位，支持字母、数字、点、下划线和短横线。</span>
          </div>
          <div class="field">
            <label>管理员密码</label>
            <input v-model="form.password" type="password" autocomplete="new-password" placeholder="••••••••••••" />
            <span class="hint">至少 8 个字符。</span>
          </div>
          <div class="field">
            <label>确认密码</label>
            <input v-model="confirmPassword" type="password" autocomplete="new-password" placeholder="••••••••••••" />
          </div>
          <div v-if="error" class="banner banner-danger" style="margin: 4px 0 12px">{{ error }}</div>
          <button class="btn btn-primary" :disabled="submitting" type="submit">
            <Loader2 v-if="submitting" :size="15" class="spin" />
            <ShieldCheck v-else :size="15" />
            {{ submitting ? '初始化中…' : '完成安装' }}
          </button>
          <button type="button" class="use-current" @click="step = 'database'">
            <ArrowLeft :size="13" /> 返回数据库设置
          </button>
        </form>

        <div class="login-foot">{{ status.panelName || 'MikvAce Panel' }} · 首次运行</div>
      </div>
    </main>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  ArrowLeft, ArrowRight, Loader2, PlugZap, ShieldCheck, Upload,
} from 'lucide-vue-next'
import { api, setAdminEntry } from '../api'
import DsnTemplate from '../components/DsnTemplate.vue'
import { notify, setSession } from '../store'

const router = useRouter()
const loadingStatus = ref(true)
const busy = ref(false)
const testing = ref(false)
const saving = ref(false)
const submitting = ref(false)
const restarting = ref(false)
const importing = ref(false)
const backupFile = ref(null)
const importDone = ref(false)
const importResult = ref(null)
const importWarnings = ref([])
const restartTitle = ref('配置已保存，面板正在重启…')
const restartHint = ref('正在等待新的数据库连接就绪，最多约 60 秒。请勿关闭此页面。')
const error = ref('')
const testMessage = ref('')
const confirmPassword = ref('')
const step = ref('database')
const status = reactive({
  panelName: '', publicUrl: '', database: '', installed: false, setupMode: false,
  configPath: '', configWritable: false, startedAt: '',
  databaseConfig: { driver: '', path: '' },
  telemetryConfig: { driver: 'sqlite', schema: 'public', required: false, configured: false },
  redisConfig: { enabled: false, prefix: 'mikvace:', required: false },
  dsnTemplates: { postgres: [], redis: [] },
})
const db = reactive({
  path: '',
  telemetry: { enabled: false, dsn: '', schema: 'public', required: false },
  redis: { enabled: false, url: '', prefix: 'mikvace:', required: false },
})
const form = reactive({ panelName: '', publicUrl: '', username: 'admin', password: '' })

const defaultSQLitePath = computed(() => status.databaseConfig?.path || '<data_dir>/panel.db')
const importDoneMessage = computed(() => {
  const summary = importResult.value?.summary
  if (!summary) return '备份已生效，请使用备份中的管理员账号登录。'
  return `已恢复 ${summary.users ?? 0} 用户 / ${summary.nodes ?? 0} 节点 / ${summary.groups ?? 0} 分组。请使用备份中的管理员账号登录。`
})

function applyStatus(s) {
  Object.assign(status, s)
  status.databaseConfig = { driver: '', path: '', ...(s.databaseConfig || {}) }
  form.panelName = s.panelName || 'MikvAce Panel'
  const saved = String(s.publicUrl || '').trim()
  form.publicUrl = !saved || /localhost|127\.0\.0\.1/.test(saved) ? window.location.origin : saved
  status.telemetryConfig = { driver: 'sqlite', schema: 'public', required: false, configured: false, ...(s.telemetryConfig || {}) }
  status.redisConfig = { enabled: false, prefix: 'mikvace:', required: false, ...(s.redisConfig || {}) }
  // Setup mode has no active store; default the path but require an explicit
  // save before any SQLite file is created.
  db.path = status.databaseConfig.path || ''
  // Secrets are never sent back to the browser. Keep the fields empty; an
  // empty value means "reuse the configured secret" on save.
  db.telemetry.enabled = status.telemetryConfig.driver === 'postgres'
  db.telemetry.dsn = ''
  db.telemetry.schema = status.telemetryConfig.schema || 'public'
  db.telemetry.required = !!status.telemetryConfig.required
  db.redis.enabled = !!status.redisConfig.enabled
  db.redis.url = ''
  db.redis.prefix = status.redisConfig.prefix || 'mikvace:'
  db.redis.required = !!status.redisConfig.required
}

onMounted(async () => {
  try {
    const s = await api.installStatus()
    applyStatus(s)
  } catch (e) {
    error.value = e.message
  } finally {
    loadingStatus.value = false
  }
})

function goAdmin() {
  if (status.setupMode) {
    error.value = '请先保存数据库配置，面板重启后才能创建管理员。'
    return
  }
  error.value = ''
  testMessage.value = ''
  step.value = 'admin'
}

function useCurrent() {
  goAdmin()
}

function payload() {
  return {
    driver: 'sqlite',
    path: db.path.trim(),
    telemetry: {
      enabled: !!db.telemetry.enabled,
      dsn: db.telemetry.dsn.trim(),
      schema: db.telemetry.schema.trim() || 'public',
      required: !!db.telemetry.required,
    },
    redis: {
      enabled: !!db.redis.enabled,
      url: db.redis.url.trim(),
      prefix: db.redis.prefix.trim() || 'mikvace:',
      required: !!db.redis.required,
    },
  }
}

function validate() {
  if (db.telemetry.enabled && !db.telemetry.dsn.trim() && !status.telemetryConfig.configured) {
    error.value = '请填写 PostgreSQL DSN，或关闭外部日志库。'
    return false
  }
  if (db.redis.enabled && !db.redis.url.trim() && !status.redisConfig.enabled) {
    error.value = '请填写 Redis URL，或关闭 Redis。'
    return false
  }
  return true
}

async function testConnection() {
  error.value = ''
  testMessage.value = ''
  if (!validate()) return
  testing.value = true
  busy.value = true
  try {
    const res = await api.installDatabaseTest(payload())
    const parts = [`SQLite 正常（${res.latencyMs ?? 0} ms）`]
    if (res.telemetry?.enabled) parts.push('PostgreSQL 正常')
    if (res.redis?.enabled) parts.push('Redis 正常')
    testMessage.value = parts.join(' · ')
  } catch (e) {
    error.value = e.message
  } finally {
    testing.value = false
    busy.value = false
  }
}

async function saveDatabase() {
  error.value = ''
  testMessage.value = ''
  if (!validate()) return
  saving.value = true
  busy.value = true
  const previousStartedAt = status.startedAt
  try {
    const res = await api.installDatabase(payload())
    if (!res.restartRequired) {
      step.value = 'admin'
      notify('配置已就绪')
      return
    }
    restartTitle.value = '配置已保存，面板正在重启…'
    restartHint.value = '正在等待新的数据库连接就绪，最多约 60 秒。请勿关闭此页面。'
    restarting.value = true
    const s = await waitForRestart(previousStartedAt)
    restarting.value = false
    if (!s) return
    step.value = 'admin'
    notify('安装配置已保存，请继续创建管理员')
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
    busy.value = false
  }
}

function pickBackup(event) {
  backupFile.value = event.target.files?.[0] || null
  error.value = ''
  testMessage.value = ''
}

function formatSize(bytes) {
  const n = Number(bytes) || 0
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

async function importBackup() {
  if (!backupFile.value) return
  error.value = ''
  testMessage.value = ''
  const target = db.path.trim() || defaultSQLitePath.value
  if (!window.confirm(`导入备份会覆盖 ${target}，并重启面板。确定继续吗？`)) return
  importing.value = true
  busy.value = true
  const previousStartedAt = status.startedAt
  restartTitle.value = '备份已写入，面板正在重启…'
  restartHint.value = '正在校验导入的数据库并等待新进程就绪，最多约 60 秒。请勿关闭此页面。'
  try {
    const res = await api.installImport(backupFile.value, db.path.trim())
    importResult.value = res
    importWarnings.value = Array.isArray(res.warnings) ? res.warnings : []
    if (res.adminEntry) setAdminEntry(res.adminEntry)
    if (!res.restartRequired) {
      finishImport(res)
      return
    }
    restarting.value = true
    const s = await waitForRestart(previousStartedAt)
    restarting.value = false
    if (!s) return
    finishImport(res, s)
  } catch (e) {
    error.value = e.message
  } finally {
    importing.value = false
    busy.value = false
  }
}

function finishImport(res, after) {
  const installed = after ? !!after.installed : !!res.hasAdmin
  if (installed) {
    importDone.value = true
    notify('备份导入完成')
    return
  }
  step.value = 'admin'
  notify('备份已导入，请继续创建管理员')
}

function goLogin() {
  const entry = typeof importResult.value?.adminEntry === 'string' ? importResult.value.adminEntry : ''
  if (entry) setAdminEntry(entry)
  router.replace(entry ? `/admin/${entry}/login` : '/admin/login')
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

async function waitForRestart(previousStartedAt) {
  const deadline = Date.now() + 60000
  // The server replies before re-exec; give the socket time to go away.
  await sleep(1400)
  while (Date.now() < deadline) {
    try {
      const s = await api.installStatus()
      if (s.startedAt && s.startedAt !== previousStartedAt) {
        applyStatus(s)
        return s
      }
    } catch {
      // The process is re-executing; keep polling.
    }
    await sleep(1200)
  }
  error.value = '等待面板重启超时。请手动重启面板后刷新页面。'
  return null
}

async function submit() {
  error.value = ''
  if (form.password.length < 8) {
    error.value = '管理员密码至少需要 8 个字符。'
    return
  }
  if (form.password !== confirmPassword.value) {
    error.value = '两次输入的密码不一致。'
    return
  }
  submitting.value = true
  try {
    const res = await api.install({
      username: form.username.trim(),
      password: form.password,
      panelName: form.panelName.trim(),
      publicUrl: form.publicUrl.trim(),
    })
    setSession(res.token, res.admin, '', res.version)
    const entry = typeof res.adminEntry === 'string' ? res.adminEntry : ''
    setAdminEntry(entry)
    notify('面板初始化完成，请保存安全入口地址')
    router.replace(entry ? `/admin/${entry}/dashboard` : '/admin/dashboard')
  } catch (e) {
    error.value = e.status === 409 ? '面板已经完成初始化，请直接登录。' : e.message
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

.steps {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0 0 20px;
}
.step {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 6px 10px;
  border: 1px solid var(--line);
  border-radius: 0;
  background: transparent;
  color: var(--muted-2);
  font-size: 11px;
  font-weight: 600;
  cursor: pointer;
}
.step span { font-family: var(--mono); font-size: 9.5px; color: var(--muted-2); }
.step.active { color: var(--text); border-color: var(--accent-line); background: var(--accent-soft); }
.step.active span { color: var(--accent); }
.step.done { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 30%, transparent); }
.step:disabled { opacity: 0.55; cursor: default; }
.step-line { flex: 1; height: 1px; background: var(--line); }

.db-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 16px;
}
.db-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 5px;
  padding: 11px 12px;
  border: 1px solid var(--line);
  border-radius: 0;
  background: var(--panel-2);
  color: var(--muted);
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;
}
.db-card:hover { border-color: var(--line-strong); color: var(--text); }
.db-card.selected { border-color: var(--accent); background: var(--accent-soft); color: var(--text); }
.db-card strong { font-size: 12.5px; font-weight: 600; }
.db-card small { font-size: 10px; line-height: 1.35; color: var(--muted-2); }
.db-card.selected small { color: var(--muted); }
.db-card:disabled { opacity: 0.6; cursor: default; }

.btn-row { display: flex; gap: 8px; margin-top: 6px; }
.btn-row .btn { flex: 1; width: auto; margin-top: 0; }

.login-card .use-current {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  width: 100%;
  margin-top: 12px;
  padding: 4px;
  border: 0;
  background: none;
  color: var(--muted-2);
  font-size: 11px;
  cursor: pointer;
}
.login-card .use-current:hover { color: var(--text); }
.login-card .use-current:disabled { opacity: 0.5; cursor: default; }

.restart-box {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 28px 16px;
  border: 1px solid var(--line);
  border-radius: 0;
  background: var(--panel-2);
  text-align: center;
}
.restart-box strong { font-size: 13px; }
.restart-box .hint { max-width: 34ch; line-height: 1.5; }

.optional-block {
  margin: 14px 0;
  padding: 12px;
  border: 1px solid var(--line);
  background: var(--panel-2);
}
.optional-block > .hint {
  margin: 6px 0 0 44px;
  line-height: 1.5;
}
.optional-fields {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--line);
}
.optional-fields .field { margin-bottom: 10px; }
.optional-fields .field:last-child { margin-bottom: 0; }

.import-block { margin-top: 16px; }
.import-block > .hint { margin: 6px 0 0; line-height: 1.5; }
.import-head {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-bottom: 4px;
  color: var(--text);
  font-size: 12px;
}
.file-pick {
  display: flex;
  align-items: center;
  margin-top: 10px;
  padding: 9px 11px;
  border: 1px dashed var(--line-strong);
  background: var(--panel);
  color: var(--muted);
  font-size: 11px;
  cursor: pointer;
}
.file-pick:hover { border-color: var(--accent-line); color: var(--text); }
.file-pick--disabled { opacity: 0.55; cursor: default; }
.file-pick input { display: none; }
.import-btn { width: 100%; margin-top: 10px; }
.import-warnings {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
  margin-top: 4px;
  line-height: 1.45;
  text-align: left;
}
.restart-box .btn { margin-top: 4px; }

@media (max-width: 520px) {
  .db-grid { grid-template-columns: 1fr; }
}
</style>
