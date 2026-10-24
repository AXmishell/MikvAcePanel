<template>
  <div class="portal-shell">
    <header class="portal-top">
      <div class="portal-brand">
        <div class="brand-logo">{{ initial }}</div>
        <div>
          <div class="brand-name">{{ branding.panelName }}</div>
          <div class="brand-sub">{{ branding.title }}</div>
        </div>
      </div>
      <div class="spacer"></div>
      <a v-if="branding.supportUrl" class="btn btn-sm btn-soft portal-support" :href="branding.supportUrl" target="_blank" rel="noopener">
        <LifeBuoy :size="14" /> 联系支持
      </a>
      <button class="icon-btn" v-tip="theme.current === 'light' ? '切换为深色主题' : '切换为浅色主题'" @click="toggleTheme">
        <Moon v-if="theme.current === 'light'" :size="17" />
        <Sun v-else :size="17" />
      </button>
      <button class="icon-btn" v-tip="'刷新'" :disabled="loading" @click="load"><RefreshCw :size="17" /></button>
      <button class="icon-btn" v-tip="'退出登录'" @click="logout"><LogOut :size="17" /></button>
    </header>

    <main class="portal-main">
      <div v-if="loading && !data" class="card"><div class="empty">正在加载用户中心…</div></div>

      <div v-else-if="error && !data" class="card">
        <div class="empty">
          <div>{{ error }}</div>
          <button class="btn btn-sm" style="margin-top: 12px" @click="load">重试</button>
        </div>
      </div>

      <template v-else-if="data">
        <section class="portal-hero">
          <div class="portal-hero-copy">
            <div class="eyebrow">Account / {{ user.userId }}</div>
            <h1>你好，{{ user.username }}</h1>
            <p>{{ branding.note }}</p>
          </div>
          <div class="portal-hero-side">
            <span class="badge" :class="statusClass">{{ statusLabel }}</span>
            <div class="dim small">{{ statusHint }}</div>
          </div>
        </section>

        <div v-if="user.status !== 'active'" class="banner" :class="bannerClass">
          <ShieldAlert :size="16" style="margin-top: 1px" />
          <div class="spacer">{{ user.statusReason }}。订阅可能暂时无法使用，请联系管理员处理。</div>
        </div>
        <div v-if="error" class="banner banner-danger">{{ error }}</div>

        <section class="stat-grid">
          <div class="stat-card">
            <div class="stat-top"><Database :size="13" /> 已用流量</div>
            <div class="stat-value">{{ fmtBytes(traffic.used) }}</div>
            <div class="stat-foot">{{ traffic.unlimited ? '流量不限' : '上限 ' + fmtBytes(traffic.limit) }}</div>
          </div>
          <div class="stat-card">
            <div class="stat-top"><Gauge :size="13" /> 剩余流量</div>
            <div class="stat-value">{{ traffic.unlimited ? '∞' : fmtBytes(remaining) }}</div>
            <div class="stat-foot">{{ traffic.unlimited ? '不限制用量' : traffic.percent + '% 已使用' }}</div>
          </div>
          <div class="stat-card">
            <div class="stat-top"><CalendarClock :size="13" /> 到期时间</div>
            <div class="stat-value portal-date">{{ user.expireAt ? fmtDate(user.expireAt, false) : '∞' }}</div>
            <div class="stat-foot">{{ expireHint }}</div>
          </div>
          <div class="stat-card">
            <div class="stat-top"><Smartphone :size="13" /> 在线设备</div>
            <div class="stat-value">{{ devices.online }}</div>
            <div class="stat-foot">{{ devices.tracked }} 台已登记 · 上限 {{ devices.limit > 0 ? devices.limit : '∞' }}</div>
          </div>
        </section>

        <div class="grid-2 section-gap">
          <section class="card">
            <div class="card-header">
              <div class="card-title">订阅连接</div>
              <span class="spacer"></span>
              <span class="card-sub">Copy · Import</span>
            </div>
            <div class="card-body">
              <div v-if="!traffic.unlimited" class="portal-usage">
                <div class="portal-usage-top">
                  <span>流量进度</span>
                  <span>{{ fmtBytes(traffic.used) }} / {{ fmtBytes(traffic.limit) }}</span>
                </div>
                <div class="progress" :class="ratioClass(traffic.percent)">
                  <i :style="{ width: traffic.percent + '%' }"></i>
                </div>
              </div>

              <div class="field">
                <label>订阅地址</label>
                <CopyField :value="subscription.url" prefix="订阅" />
              </div>

              <div class="portal-formats">
                <a class="chip" :href="subscription.clashUrl">Clash / Mihomo</a>
                <a class="chip" :href="subscription.singboxUrl">sing-box</a>
                <a class="chip" :href="subscription.uriUrl">URI list</a>
              </div>

              <div v-if="subscription.qr" class="portal-qr">
                <img :src="subscription.qr" alt="订阅二维码" />
                <div class="hint">使用客户端扫码导入订阅。</div>
              </div>

              <p class="portal-note">订阅链接会自动同步节点与账号状态。请妥善保管，不要分享给他人。</p>
            </div>
          </section>

          <section class="card">
            <div class="card-header">
              <div class="card-title">账号信息</div>
              <span class="spacer"></span>
              <span class="card-sub">Profile</span>
            </div>
            <div class="card-body">
              <dl class="kv">
                <dt>用户 ID</dt><dd class="mono">{{ user.userId }}</dd>
                <dt>显示名称</dt><dd>{{ user.username }}</dd>
                <dt>服务器组</dt>
                <dd>
                  <span v-for="g in user.groups" :key="g.id" class="badge badge-muted" style="margin-right: 6px">{{ g.name }}</span>
                  <span v-if="!user.groups.length" class="dim">公开节点</span>
                </dd>
                <dt>可用节点</dt><dd>{{ nodes.length }} 个</dd>
                <dt>最后在线</dt><dd>{{ user.lastSeenAt ? fmtRelative(user.lastSeenAt) : '从未' }}</dd>
                <dt>注册时间</dt><dd>{{ fmtDate(user.createdAt, false) }}</dd>
              </dl>

              <div class="divider"></div>

              <div class="portal-actions">
                <button v-if="capabilities.password" class="btn btn-sm" @click="openPassword"><KeyRound :size="14" /> 修改密码</button>
                <button v-if="capabilities.subscription" class="btn btn-sm btn-danger" @click="resetSubscriptionOpen = true"><RefreshCw :size="14" /> 重置订阅</button>
              </div>

              <div v-if="devices.items.length" class="portal-devices">
                <div class="portal-label">最近设备</div>
                <div v-for="d in devices.items" :key="d.ip" class="portal-device">
                  <span class="mono">{{ d.ip }}</span>
                  <span class="dim">{{ fmtRelative(d.lastSeen) }}</span>
                </div>
              </div>
            </div>
          </section>
        </div>

        <section class="card section-gap">
          <div class="card-header">
            <div class="card-title">节点</div>
            <span class="spacer"></span>
            <span class="card-sub">{{ nodes.length }} 个节点</span>
          </div>
          <div v-if="nodes.length" class="node-grid portal-node-grid">
            <article v-for="n in nodes" :key="n.id" class="node-card">
              <div class="node-head">
                <div class="node-flag">{{ n.flag || '🌐' }}</div>
                <div style="min-width: 0">
                  <div class="node-name">{{ n.name }}</div>
                  <div class="node-host">{{ (n.cores || []).join(' + ') }}</div>
                </div>
                <span class="spacer"></span>
                <span class="badge" :class="n.online ? 'badge-ok' : 'badge-muted'">
                  <span class="dot" :class="n.online ? 'online' : 'offline'"></span>{{ n.online ? '在线' : '离线' }}
                </span>
              </div>
              <div class="portal-configs">
                <div v-for="c in n.configs" :key="c.name" class="portal-cfg">
                  <span class="mono">{{ c.name }}</span>
                  <button class="portal-copy" @click="copy(c.uri)">复制链接</button>
                </div>
                <div v-if="!n.configs.length" class="portal-cfg dim">暂无可用配置</div>
              </div>
            </article>
          </div>
          <div v-else class="empty">暂无可用节点，请联系管理员。</div>
        </section>

        <footer class="portal-foot">
          <span>© {{ year }} {{ branding.panelName }}</span>
          <span>安全连接 · 自动更新</span>
        </footer>
      </template>
    </main>

    <Modal v-model="passwordOpen" title="修改密码" size="sm">
      <div class="tabs" style="margin-bottom: 14px">
        <button class="tab" :class="{ active: passwordMode === 'login' }" @click="switchPasswordMode('login')">控制台登录密码</button>
        <button class="tab" :class="{ active: passwordMode === 'connection' }" @click="switchPasswordMode('connection')">连接密码</button>
      </div>
      <div v-if="passwordError" class="banner banner-danger">{{ passwordError }}</div>
      <p v-if="passwordMode === 'login'" class="dim small" style="margin: 0 0 14px">
        用于登录用户中心，不会改变订阅 URI 中的连接密码。
      </p>
      <p v-else class="dim small" style="margin: 0 0 14px">
        用于 Hysteria 认证和订阅 URI。修改后已有连接不受影响，但新连接和客户端需要更新密码。
      </p>
      <div class="field" style="margin-bottom: 12px">
        <label>当前登录密码</label>
        <input v-model="passwordForm.old" type="password" autocomplete="current-password" />
      </div>
      <div class="field" style="margin-bottom: 12px">
        <label>新{{ passwordMode === 'login' ? '登录' : '连接' }}密码</label>
        <input v-model="passwordForm.next" type="password" autocomplete="new-password" />
        <span class="hint">至少 8 个字符。</span>
      </div>
      <div class="field">
        <label>确认新密码</label>
        <input v-model="passwordForm.confirm" type="password" autocomplete="new-password" />
      </div>
      <template #footer>
        <button class="btn" @click="passwordOpen = false">取消</button>
        <button class="btn btn-primary" :disabled="passwordBusy" @click="savePassword">
          {{ passwordBusy ? '保存中…' : passwordMode === 'login' ? '保存登录密码' : '保存连接密码' }}
        </button>
      </template>
    </Modal>

    <ConfirmDialog
      v-model="resetSubscriptionOpen"
      eyebrow="Subscription Token"
      title="重置订阅链接？"
      message="系统会生成新的订阅 Token，旧链接将立即失效。你需要在每台设备的客户端中重新导入新链接。"
      confirm-text="重置订阅"
      busy-text="正在生成新链接…"
      tone="danger"
      :icon="RefreshCw"
      :busy="resetSubscriptionBusy"
      @confirm="confirmResetSubscription"
    >
      <div class="confirm-warning">
        <ShieldAlert :size="15" />
        <span>此操作不可撤销，请确认已准备好更新所有客户端。</span>
      </div>
    </ConfirmDialog>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  CalendarClock, Database, Gauge, KeyRound, LifeBuoy, LogOut, Moon, RefreshCw,
  ShieldAlert, Smartphone, Sun,
} from 'lucide-vue-next'
import { portalApi } from '../api'
import { clearPortalSession, notify } from '../store'
import { theme, toggleTheme } from '../theme'
import { copyText, fmtBytes, fmtDate, fmtRelative, ratioClass } from '../format'
import CopyField from '../components/CopyField.vue'
import Modal from '../components/Modal.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const router = useRouter()

const data = ref(null)
const loading = ref(false)
const error = ref('')

const passwordOpen = ref(false)
const passwordMode = ref('login')
const passwordBusy = ref(false)
const passwordError = ref('')
const passwordForm = reactive({ old: '', next: '', confirm: '' })

const resetSubscriptionOpen = ref(false)
const resetSubscriptionBusy = ref(false)

const user = computed(() => data.value?.user || {})
const traffic = computed(() => data.value?.traffic || {})
const devices = computed(() => data.value?.devices || { online: 0, tracked: 0, limit: 0, items: [] })
const subscription = computed(() => data.value?.subscription || {})
const nodes = computed(() => data.value?.nodes || [])
const capabilities = computed(() => data.value?.capabilities || {})
const branding = computed(() => data.value?.branding || { panelName: 'MikvAce Panel', title: '用户中心', note: '' })
const initial = computed(() => (branding.value.panelName || 'M').slice(0, 1).toUpperCase())
const year = new Date().getFullYear()

const remaining = computed(() => {
  if (traffic.value.unlimited) return 0
  return Math.max(0, (traffic.value.limit || 0) - (traffic.value.used || 0))
})

const statusLabel = computed(() => ({
  active: '正常', quota: '流量已用完', expired: '已过期', disabled: '已停用',
}[user.value.status] || '未知'))

const statusClass = computed(() => ({
  active: 'badge-ok', quota: 'badge-warn', expired: 'badge-danger', disabled: 'badge-danger',
}[user.value.status] || 'badge-muted'))

const statusHint = computed(() => ({
  active: '订阅有效，可以正常使用',
  quota: '流量配额已用完',
  expired: '订阅已过期',
  disabled: '账号已被管理员停用',
}[user.value.status] || ''))

const bannerClass = computed(() => (user.value.status === 'quota' ? 'banner-warn' : 'banner-danger'))

const expireHint = computed(() => {
  if (!user.value.expireAt) return '永不过期'
  const days = Math.ceil((new Date(user.value.expireAt).getTime() - Date.now()) / 86400000)
  if (days < 0) return '已过期'
  if (days === 0) return '今天到期'
  return `${days} 天后到期`
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await portalApi.me()
  } catch (e) {
    if (e.status !== 401) error.value = e.message
  } finally {
    loading.value = false
  }
}

async function copy(text) {
  if (!text) return
  await copyText(text)
  notify('已复制到剪贴板')
}

function openPassword() {
  switchPasswordMode('login')
  passwordOpen.value = true
}
function switchPasswordMode(mode) {
  passwordMode.value = mode
  passwordForm.old = ''
  passwordForm.next = ''
  passwordForm.confirm = ''
  passwordError.value = ''
}

async function savePassword() {
  passwordError.value = ''
  if (passwordForm.next.length < 8) {
    passwordError.value = '新密码至少需要 8 个字符'
    return
  }
  if (passwordForm.next !== passwordForm.confirm) {
    passwordError.value = '两次输入的新密码不一致'
    return
  }
  passwordBusy.value = true
  try {
    if (passwordMode.value === 'login') {
      await portalApi.changePassword(passwordForm.old, passwordForm.next)
      notify('登录密码已更新')
    } else {
      await portalApi.changeConnectionPassword(passwordForm.old, passwordForm.next)
      notify('连接密码已更新')
    }
    passwordOpen.value = false
    await load()
  } catch (e) {
    passwordError.value = e.message
  } finally {
    passwordBusy.value = false
  }
}

async function confirmResetSubscription() {
  resetSubscriptionBusy.value = true
  try {
    const res = await portalApi.resetSubscription()
    if (data.value) data.value.subscription = res
    resetSubscriptionOpen.value = false
    notify('订阅链接已重置')
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    resetSubscriptionBusy.value = false
  }
}

async function logout() {
  try {
    await portalApi.logout()
  } catch {
    /* clear the local session even if the server is unreachable */
  }
  clearPortalSession()
  router.push('/user/login')
}

onMounted(load)
</script>

<style scoped>
.portal-shell { min-height: 100vh; }
.portal-top {
  position: sticky; top: 0; z-index: 20;
  display: flex; align-items: center; gap: 10px;
  height: 62px; padding: 0 26px;
  border-bottom: 1px solid var(--line);
  background: var(--topbar);
  backdrop-filter: blur(16px);
}
.portal-brand { display: flex; align-items: center; gap: 12px; min-width: 0; }
.portal-main { max-width: 1180px; margin: 0 auto; padding: 30px 26px 64px; }

.portal-hero {
  display: flex; align-items: flex-end; justify-content: space-between; gap: 24px;
  margin-bottom: 22px;
}
.portal-hero h1 {
  margin: 12px 0 8px;
  font-size: clamp(28px, 4vw, 42px); line-height: 1.05;
  letter-spacing: -0.045em; font-weight: 650;
}
.portal-hero p { margin: 0; color: var(--muted); font-size: 13px; max-width: 56ch; }
.portal-hero-side { text-align: right; display: flex; flex-direction: column; align-items: flex-end; gap: 7px; }
.portal-date { font-size: 23px !important; letter-spacing: -0.03em !important; }

.portal-usage { margin-bottom: 18px; }
.portal-usage-top {
  display: flex; justify-content: space-between; gap: 10px;
  font-family: var(--mono); font-size: 10px; color: var(--muted-2);
  margin-bottom: 7px;
}
.portal-formats { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 14px; }
.portal-qr { display: flex; flex-direction: column; align-items: center; margin-top: 18px; }
.portal-qr img {
  width: min(190px, 60vw); height: auto; aspect-ratio: 1;
  border-radius: 8px; background: #fff; padding: 8px;
  border: 1px solid var(--line);
}
.portal-qr .hint { margin-top: 8px; color: var(--muted-2); font-size: 11px; }
.portal-note {
  margin: 16px 0 0; padding-top: 14px;
  border-top: 1px solid var(--line-soft);
  color: var(--muted-2); font-size: 11px; line-height: 1.65;
}
.portal-actions { display: flex; flex-wrap: wrap; gap: 7px; }
.portal-label {
  font-family: var(--mono); font-size: 9px; letter-spacing: 0.12em;
  text-transform: uppercase; color: var(--muted-2); margin-bottom: 7px;
}
.portal-devices { margin-top: 16px; padding-top: 14px; border-top: 1px solid var(--line-soft); }
.portal-device {
  display: flex; align-items: center; justify-content: space-between; gap: 10px;
  padding: 6px 0; font-size: 11.5px;
  border-top: 1px solid var(--line-soft);
}
.portal-device:first-of-type { border-top: 0; }
.portal-node-grid { padding: 16px; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); }
.portal-configs { display: flex; flex-direction: column; gap: 2px; }
.portal-cfg {
  display: flex; align-items: center; justify-content: space-between; gap: 10px;
  padding: 7px 0; font-size: 11.5px; color: var(--muted);
  border-top: 1px solid var(--line-soft);
}
.portal-cfg:first-child { border-top: 0; }
.portal-copy { border: 0; background: none; color: var(--accent); font-size: 10.5px; white-space: nowrap; }
.portal-copy:hover { color: var(--accent-hover); }
.portal-foot {
  display: flex; justify-content: space-between; gap: 12px; flex-wrap: wrap;
  margin-top: 26px; padding-top: 20px;
  border-top: 1px solid var(--line-soft);
  font-family: var(--mono); font-size: 9.5px; letter-spacing: 0.08em;
  text-transform: uppercase; color: var(--muted-2);
}

@media (max-width: 900px) {
  .portal-main { padding: 22px 16px 52px; }
  .portal-hero { flex-direction: column; align-items: flex-start; }
  .portal-hero-side { align-items: flex-start; }
}
@media (max-width: 620px) {
  .portal-top { padding: 0 14px; }
  .portal-support { display: none; }
  .portal-brand .brand-sub { display: none; }
  .portal-node-grid { grid-template-columns: 1fr; padding: 12px; }
}
</style>
