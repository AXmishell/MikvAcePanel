<template>
  <div class="login-shell">
    <aside class="login-brand">
      <div class="login-brand-head">
        <div class="brand-logo">M</div>
        <div class="brand-word">MikvAce<span>Panel</span></div>
      </div>

      <div class="login-brand-copy">
        <div class="eyebrow">First-run setup</div>
        <h1>几分钟内<br>完成<span>面板初始化</span>。</h1>
        <p>先选择数据后端，再创建第一个管理员。之后节点、用户、订阅与证书都在这一个控制面中管理。</p>
      </div>

      <ul class="login-points">
        <li>
          <span class="num">01</span>
          <div>
            <strong>数据后端</strong>
            <small>SQLite 开箱即用；也可以连接 PostgreSQL 或 MySQL。切换后端后需要重启一次面板。</small>
          </div>
        </li>
        <li>
          <span class="num">02</span>
          <div>
            <strong>管理员账号</strong>
            <small>密码使用 bcrypt 存储，登录后可继续开启两步验证。</small>
          </div>
        </li>
        <li>
          <span class="num">03</span>
          <div>
            <strong>公共访问地址</strong>
            <small>用于生成订阅链接和节点 Agent 安装命令，可随时修改。</small>
          </div>
        </li>
      </ul>

      <div class="login-brand-foot">
        <span class="dot online"></span>
        <span>{{ selectedLabel }} · {{ status.installed ? 'installed' : 'ready' }}</span>
      </div>
    </aside>

    <main class="login-panel">
      <div class="login-card">
        <div class="login-brand-head login-mobile-head">
          <div class="brand-logo">M</div>
          <div class="brand-word">MikvAce<span>Panel</span></div>
        </div>

        <div class="login-head">
          <div class="eyebrow">Install · {{ step === 'database' ? '01 / 02' : '02 / 02' }}</div>
          <h2>{{ step === 'database' ? '选择数据后端' : '创建管理员' }}</h2>
          <p v-if="step === 'database'">
            选择面板使用的数据库。SQLite 无需额外配置；远程数据库会先测试连接，再写入配置并重启面板。
          </p>
          <p v-else>
            设置管理员账号、面板名称与公共访问地址，完成后将自动登录。
          </p>
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

        <div v-else-if="restarting" class="restart-box">
          <Loader2 :size="22" class="spin" />
          <strong>配置已保存，面板正在重启…</strong>
          <span class="hint">正在等待新的数据库连接就绪，最多约 60 秒。请勿关闭此页面。</span>
        </div>

        <form v-else-if="step === 'database'" @submit.prevent="saveDatabase">
          <div v-if="status.setupMode" class="banner banner-info" style="margin-bottom: 12px">
            当前未连接数据库，面板没有创建任何数据库文件。选择后端并保存后会写入配置并重启。
          </div>
          <div class="db-grid">
            <button
              v-for="opt in drivers"
              :key="opt.id"
              type="button"
              class="db-card"
              :class="{ selected: db.driver === opt.id }"
              :disabled="busy"
              @click="selectDriver(opt.id)"
            >
              <component :is="opt.icon" :size="18" />
              <strong>{{ opt.label }}</strong>
              <small>{{ opt.hint }}</small>
            </button>
          </div>

          <div v-if="db.driver === 'sqlite'" class="field">
            <label>SQLite 数据文件</label>
            <input v-model="db.path" type="text" class="mono" placeholder="/data/panel.db" />
            <span class="hint">
              留空使用默认路径 <span class="mono">{{ defaultSQLitePath }}</span>；所在目录必须可写并持久化。
            </span>
          </div>

          <div v-else class="field">
            <label>{{ db.driver === 'postgres' ? 'PostgreSQL' : 'MySQL' }} DSN</label>
            <textarea v-model="db.dsn" rows="3" class="mono" :placeholder="dsnPlaceholder" spellcheck="false"></textarea>
            <span class="hint">
              DSN 会写入 <span class="mono">{{ dsnFilePath }}</span>（权限 0600），不会写进 panel.yaml。
            </span>
            <span v-if="canReuseDSN" class="hint">
              当前 {{ db.driver }} 连接已配置，留空表示继续使用现有 DSN。
            </span>
          </div>

          <div v-if="testMessage" class="banner banner-info" style="margin: 4px 0 12px">{{ testMessage }}</div>
          <div v-if="error" class="banner banner-danger" style="margin: 4px 0 12px">{{ error }}</div>

          <div class="btn-row">
            <button type="button" class="btn" :disabled="busy" @click="testConnection">
              <Loader2 v-if="testing" :size="15" class="spin" />
              <PlugZap v-else :size="15" />
              {{ testing ? '测试中…' : '测试连接' }}
            </button>
            <button type="submit" class="btn btn-primary" :disabled="busy">
              <Loader2 v-if="saving" :size="15" class="spin" />
              <ArrowRight v-else :size="15" />
              {{ saving ? '保存中…' : '保存并继续' }}
            </button>
          </div>
          <button v-if="!status.setupMode" type="button" class="use-current" :disabled="busy" @click="useCurrent">
            使用当前数据库继续（{{ status.database || 'sqlite' }}）
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
            <span class="hint">至少 12 个字符。</span>
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
  ArrowLeft, ArrowRight, HardDrive, Layers, Loader2, PlugZap, Server, ShieldCheck,
} from 'lucide-vue-next'
import { api } from '../api'
import { notify, setSession } from '../store'

const router = useRouter()
const loadingStatus = ref(true)
const busy = ref(false)
const testing = ref(false)
const saving = ref(false)
const submitting = ref(false)
const restarting = ref(false)
const error = ref('')
const testMessage = ref('')
const confirmPassword = ref('')
const step = ref('database')
const status = reactive({
  panelName: '', publicUrl: '', database: '', installed: false, setupMode: false,
  configPath: '', configWritable: false, startedAt: '',
  databaseConfig: { driver: '', path: '', dsnFile: '', dsnConfigured: false },
})
const db = reactive({ driver: 'sqlite', path: '', dsn: '' })
const form = reactive({ panelName: '', publicUrl: '', username: 'admin', password: '' })

const drivers = [
  { id: 'sqlite', label: 'SQLite', hint: '默认，单文件，零配置', icon: HardDrive },
  { id: 'postgres', label: 'PostgreSQL', hint: '适合多实例与生产环境', icon: Server },
  { id: 'mysql', label: 'MySQL', hint: '兼容 MySQL 8 / MariaDB', icon: Layers },
]

const selectedLabel = computed(() => drivers.find((d) => d.id === db.driver)?.label || 'SQLite')
const defaultSQLitePath = computed(() => status.databaseConfig?.path || '<data_dir>/panel.db')
const dsnFilePath = computed(() => status.databaseConfig?.dsnFile || '<data_dir>/database.dsn')
const canReuseDSN = computed(() =>
  !status.setupMode &&
  db.driver !== 'sqlite' &&
  status.databaseConfig?.driver === db.driver &&
  status.databaseConfig?.dsnConfigured,
)
const dsnPlaceholder = computed(() => db.driver === 'postgres'
  ? 'postgres://user:pass@postgres:5432/mikvace?sslmode=disable'
  : 'user:pass@tcp(mysql:3306)/mikvace?charset=utf8mb4&collation=utf8mb4_bin&parseTime=false&loc=UTC')

function applyStatus(s) {
  Object.assign(status, s)
  status.databaseConfig = { driver: '', path: '', dsnFile: '', dsnConfigured: false, ...(s.databaseConfig || {}) }
  form.panelName = s.panelName || 'MikvAce Panel'
  const saved = String(s.publicUrl || '').trim()
  form.publicUrl = !saved || /localhost|127\.0\.0\.1/.test(saved) ? window.location.origin : saved
  // Setup mode has no active store; default the picker to SQLite but require
  // an explicit save before any SQLite file is created.
  const current = s.setupMode ? 'sqlite' : (status.databaseConfig.driver || s.database || 'sqlite')
  db.driver = current
  db.path = status.databaseConfig.path || ''
  db.dsn = ''
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

function selectDriver(id) {
  db.driver = id
  error.value = ''
  testMessage.value = ''
}

function goAdmin() {
  if (status.setupMode) {
    error.value = '请先选择并保存数据库，面板重启后才能创建管理员。'
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
    driver: db.driver,
    path: db.driver === 'sqlite' ? db.path.trim() : '',
    dsn: db.driver === 'sqlite' ? '' : db.dsn.trim(),
  }
}

function validate() {
  if (db.driver !== 'sqlite' && !db.dsn.trim() && !canReuseDSN.value) {
    error.value = '请填写数据库 DSN。'
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
    testMessage.value = `连接成功：${res.driver}（${res.latencyMs} ms）`
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
      notify('数据后端已就绪')
      return
    }
    restarting.value = true
    await waitForRestart(previousStartedAt)
  } catch (e) {
    error.value = e.message
  } finally {
    saving.value = false
    busy.value = false
  }
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
        restarting.value = false
        step.value = 'admin'
        notify('数据后端已切换，请继续创建管理员')
        return
      }
    } catch {
      // The process is re-executing; keep polling.
    }
    await sleep(1200)
  }
  restarting.value = false
  error.value = '等待面板重启超时。请手动重启面板后刷新页面。'
}

async function submit() {
  error.value = ''
  if (form.password.length < 12) {
    error.value = '管理员密码至少需要 12 个字符。'
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
    notify('面板初始化完成')
    router.replace('/admin/dashboard')
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
  border-radius: 999px;
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
  border-radius: var(--radius-sm);
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
  border-radius: var(--radius-sm);
  background: var(--panel-2);
  text-align: center;
}
.restart-box strong { font-size: 13px; }
.restart-box .hint { max-width: 34ch; line-height: 1.5; }

@media (max-width: 520px) {
  .db-grid { grid-template-columns: 1fr; }
}
</style>
