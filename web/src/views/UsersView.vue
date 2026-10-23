<template>
  <div>
    <div class="toolbar">
      <div>
        <div class="page-title">用户</div>
        <div class="dim small">{{ total }} 个账号 · {{ onlineCount }} 在线</div>
      </div>
      <div class="spacer"></div>
      <div class="flex">
        <input v-model="q" type="search" placeholder="搜索用户、ID 或订阅令牌…" @input="onSearch" />
        <Select v-model="status" :options="STATUS_OPTIONS" size="sm" style="width: 130px" @change="reload" />
        <button class="btn btn-primary" @click="openCreate"><Plus :size="15" /> 新建用户</button>
      </div>
    </div>

    <div v-if="error" class="banner banner-danger">{{ error }}</div>

    <section class="card">
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>用户</th><th>状态</th><th>分组</th><th>流量</th><th>设备</th><th>到期时间</th><th>最后在线</th><th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in users" :key="u.id">
              <td>
                <div class="flex">
                  <div class="avatar-sm">{{ (u.username || u.userId).slice(0, 1).toUpperCase() }}</div>
                  <div style="min-width: 0">
                    <div class="truncate" style="font-weight: 600">{{ u.username || '—' }}</div>
                    <div class="mono dim small truncate">{{ u.userId }}</div>
                  </div>
                </div>
              </td>
              <td>
                <span v-if="!u.enabled" class="badge badge-muted">已禁用</span>
                <span v-else-if="isExpired(u)" class="badge badge-danger">已过期</span>
                <span v-else-if="u.online" class="badge badge-ok"><span class="dot online"></span>在线</span>
                <span v-else class="badge badge-info">已启用</span>
              </td>
              <td>
                <div class="chip-list">
                  <span v-for="g in u.groups" :key="g.id" class="badge badge-muted">{{ g.name }}</span>
                  <span v-if="!u.groups?.length" class="dim small">公开</span>
                </div>
              </td>
              <td style="min-width: 150px">
                <div class="flex small" style="justify-content: space-between">
                  <span>{{ fmtBytes(used(u)) }}</span>
                  <span class="dim">{{ u.trafficLimit > 0 ? fmtBytes(u.trafficLimit) : '∞' }}</span>
                </div>
                <div class="progress" :class="ratioClass(trafficPct(u))" style="margin-top: 5px">
                  <i :style="{ width: (u.trafficLimit > 0 ? trafficPct(u) : 0) + '%' }"></i>
                </div>
              </td>
              <td class="small">
                <span :class="u.online ? 'badge badge-ok' : 'dim'">{{ u.onlineDevices || 0 }}</span>
                <span class="dim"> / {{ u.maxDevices > 0 ? u.maxDevices : '∞' }}</span>
              </td>
              <td class="small nowrap">{{ u.expireAt ? fmtDate(u.expireAt, false) : '∞' }}</td>
              <td class="small nowrap dim">{{ u.lastSeenAt ? fmtRelative(u.lastSeenAt) : 'never' }}</td>
              <td>
                <div class="row-actions">
                  <button class="icon-btn" title="编辑" @click="openEdit(u)"><Pencil :size="15" /></button>
                  <button class="icon-btn" :title="u.enabled ? '禁用' : '启用'" @click="toggle(u)">
                    <Power :size="15" />
                  </button>
                  <button class="icon-btn" title="连接信息" @click="openConnections(u)"><Cable :size="15" /></button>
                  <button class="icon-btn" title="复制订阅链接" @click="copySub(u)"><Copy :size="15" /></button>
                  <button class="icon-btn" title="重置订阅链接" @click="resetSub(u)"><RefreshCw :size="15" /></button>
                  <button class="icon-btn" title="重置设备" @click="openResetDevices(u)"><Smartphone :size="15" /></button>
                  <button class="icon-btn" title="重置流量统计" @click="resetTraffic(u)"><Gauge :size="15" /></button>
                  <button class="icon-btn danger" title="删除" @click="remove(u)"><Trash2 :size="15" /></button>
                </div>
              </td>
            </tr>
            <tr v-if="!users.length && !loading">
              <td colspan="8"><div class="empty"><Users :size="28" /><div>没有符合筛选条件的用户。</div></div></td>
            </tr>
            <tr v-if="loading">
              <td colspan="8"><div class="empty">正在加载用户…</div></td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination :page="page" :page-size="pageSize" :total="total" @change="(p) => { page = p; reload() }" />
    </section>

    <Modal v-model="modalOpen" :title="editing ? '编辑用户' : '新建用户'" size="lg">
      <div v-if="formError" class="banner banner-danger">{{ formError }}</div>
      <div class="form-grid">
        <div class="field">
          <label>用户 ID</label>
          <input v-model="form.userId" type="text" :disabled="!!editing" placeholder="自动生成" />
          <span class="hint">作为代理认证 ID，支持字母、数字、点、下划线和短横线。</span>
        </div>
        <div class="field">
          <label>显示名称</label>
          <input v-model="form.username" type="text" placeholder="Alice" />
        </div>
        <div class="field">
          <label>连接密码</label>
          <input v-model="form.password" type="text" :placeholder="editing ? '留空保持当前连接密码' : '留空自动生成'" />
          <span class="hint">用于 Hysteria 认证和订阅 URI；修改后客户端需同步更新。</span>
        </div>
        <div class="field">
          <label>控制台登录密码</label>
          <input v-model="form.loginPassword" type="text" :placeholder="editing ? '留空保持当前登录密码' : '留空则与连接密码相同'" />
          <span class="hint">用于 /user 用户中心登录，与连接密码相互独立。</span>
        </div>
        <div class="field full">
          <label>订阅 Token</label>
          <input v-model="form.subscriptionToken" type="text" :placeholder="editing ? '' : '留空自动生成'" />
          <span class="hint">订阅 URL 中的 /api/files/&lt;token&gt;；修改后旧链接立即失效。</span>
        </div>
        <div class="field">
          <label>流量上限 (GB)</label>
          <input v-model.number="form.trafficGb" type="number" min="0" step="1" placeholder="0 = 不限制" />
        </div>
        <div class="field">
          <label>最大设备数</label>
          <input v-model.number="form.maxDevices" type="number" min="-1" step="1" placeholder="0 = 继承分组 / 不限制" />
          <span class="hint">-1 不限制，0 继承分组，大于 0 为唯一 IP 上限。</span>
        </div>
        <div class="field">
          <label>到期时间</label>
          <input v-model="form.expireAt" type="datetime-local" />
          <span class="hint">留空表示永不过期。</span>
        </div>
        <div class="field full">
          <label>启用</label>
          <label class="checkbox"><input v-model="form.enabled" type="checkbox" /> 允许该账号认证并获取订阅</label>
        </div>
        <div class="field full">
          <label>服务器组</label>
          <div class="chip-list">
            <button v-for="g in groups" :key="g.id" type="button" class="chip" :class="{ on: form.groupIds.includes(g.id) }" @click="toggleId(form.groupIds, g.id)">
              {{ g.name }} <span class="dim">{{ g.nodeCount }} nodes</span>
            </button>
            <span v-if="!groups.length" class="dim small">暂无分组。</span>
          </div>
          <span class="hint">用户会获得这些分组内的节点；未加入分组的节点对所有用户公开。</span>
        </div>
        <div class="field full">
          <label>直接分配节点（可选）</label>
          <div class="chip-list">
            <button v-for="n in nodes" :key="n.id" type="button" class="chip" :class="{ on: form.nodeIds.includes(n.id) }" @click="toggleId(form.nodeIds, n.id)">
              {{ n.flag }} {{ n.name }}
            </button>
            <span v-if="!nodes.length" class="dim small">暂无节点。</span>
          </div>
          <span class="hint">选择任意节点后，该用户将忽略分组匹配规则。</span>
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="modalOpen = false">取消</button>
        <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存用户' }}</button>
      </template>
    </Modal>

    <Modal v-model="credentialOpen" title="用户凭据" size="sm">
      <div class="banner banner-info">请立即保存密码，每次重置只显示一次。</div>
      <dl class="kv">
        <dt>用户 ID</dt><dd class="mono">{{ credential.userId }}</dd>
        <dt>连接密码</dt><dd class="mono">{{ credential.password || '(未变更)' }}</dd>
        <dt>登录密码</dt><dd class="mono">{{ credential.loginPassword || '(未变更)' }}</dd>
        <dt>订阅</dt><dd class="mono" style="font-size: 11px">{{ credential.subscriptionUrl }}</dd>
      </dl>
      <template #footer>
        <button class="btn" @click="credentialOpen = false">关闭</button>
        <button class="btn btn-primary" @click="copyCredential"><Copy :size="14" /> 复制</button>
      </template>
    </Modal>

    <Modal v-model="connectionsOpen" title="用户连接信息" size="lg">
      <div v-if="connectionsError" class="banner banner-danger">{{ connectionsError }}</div>
      <div v-if="connections" class="stack">
        <div class="flex" style="flex-wrap: wrap">
          <div class="avatar-sm">{{ (connections.user.username || connections.user.userId).slice(0, 1).toUpperCase() }}</div>
          <div>
            <div style="font-weight: 650">{{ connections.user.username }}</div>
            <div class="mono dim small">{{ connections.user.userId }}</div>
          </div>
          <span class="spacer"></span>
          <span class="badge" :class="connectionStatusClass">{{ connectionStatusLabel }}</span>
          <span class="dim small">{{ connections.nodes.length }} 个节点</span>
        </div>

        <div class="field">
          <label>订阅地址</label>
          <CopyField :value="connections.subscription.url" prefix="订阅" />
        </div>

        <div class="flex" style="gap: 7px; flex-wrap: wrap">
          <a class="chip" :href="connections.subscription.clashUrl" target="_blank" rel="noopener">Clash / Mihomo</a>
          <a class="chip" :href="connections.subscription.singboxUrl" target="_blank" rel="noopener">sing-box</a>
          <a class="chip" :href="connections.subscription.uriUrl" target="_blank" rel="noopener">URI list</a>
        </div>

        <div v-if="connections.subscription.qr" class="conn-qr">
          <img :src="connections.subscription.qr" alt="订阅二维码" />
        </div>

        <div class="divider"></div>

        <div v-if="connections.nodes.length" class="conn-nodes">
          <article v-for="n in connections.nodes" :key="n.id" class="conn-node">
            <div class="flex">
              <span style="font-size: 17px">{{ n.flag || '🌐' }}</span>
              <strong style="font-size: 12.5px">{{ n.name }}</strong>
              <span class="badge" :class="n.online ? 'badge-ok' : 'badge-muted'">
                <span class="dot" :class="n.online ? 'online' : 'offline'"></span>{{ n.online ? '在线' : '离线' }}
              </span>
              <span class="spacer"></span>
              <span class="dim small">{{ (n.cores || []).join(' + ') }}</span>
            </div>
            <div v-if="n.configs.length" class="conn-configs">
              <div v-for="c in n.configs" :key="c.name" class="conn-cfg">
                <span class="mono">{{ c.name }}</span>
                <button class="conn-copy" @click="copyConn(c.uri)">复制链接</button>
              </div>
            </div>
            <div v-else class="dim small" style="padding-top: 8px">暂无可用配置</div>
          </article>
        </div>
        <div v-else class="empty">该用户当前没有可用节点。</div>
      </div>
      <div v-else class="empty">正在加载连接信息…</div>
      <template #footer>
        <button class="btn" @click="connectionsOpen = false">关闭</button>
        <button v-if="connections" class="btn btn-primary" @click="copyConn(connections.subscription.url)"><Copy :size="14" /> 复制订阅</button>
      </template>
    </Modal>

    <Modal v-model="deleteOpen" title="删除用户" size="sm">
      <p>删除 <strong>{{ deleteTarget?.username || deleteTarget?.userId }}</strong>? 同时会删除订阅令牌与设备状态。</p>
      <template #footer>
        <button class="btn" @click="deleteOpen = false">取消</button>
        <button class="btn btn-danger" @click="confirmDelete">删除用户</button>
      </template>
    </Modal>

    <ConfirmDialog
      v-model="devicesOpen"
      eyebrow="Device Access"
      title="重置设备状态？"
      :message="`清除 ${devicesTarget?.username || devicesTarget?.userId || '该用户'} 已登记的在线设备 IP 与计数。已建立连接不会立即断开，客户端重连后会重新登记。`"
      confirm-text="确认重置"
      busy-text="正在重置设备…"
      :icon="Smartphone"
      :busy="devicesBusy"
      @confirm="confirmResetDevices"
    >
      <div class="confirm-stats">
        <div class="confirm-stat">
          <span class="confirm-stat-label">在线设备</span>
          <strong>{{ devicesTarget?.onlineDevices || 0 }} 台</strong>
        </div>
        <div class="confirm-stat">
          <span class="confirm-stat-label">设备上限</span>
          <strong>{{ devicesTarget?.maxDevices > 0 ? devicesTarget.maxDevices + ' 台' : '不限' }}</strong>
        </div>
        <div class="confirm-stat">
          <span class="confirm-stat-label">用户 ID</span>
          <strong class="mono">{{ devicesTarget?.userId || '—' }}</strong>
        </div>
      </div>
    </ConfirmDialog>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { Cable, Copy, Gauge, Pencil, Plus, Power, RefreshCw, Smartphone, Trash2, Users } from 'lucide-vue-next'
import { api } from '../api'
import { notify } from '../store'
import CopyField from '../components/CopyField.vue'
import Modal from '../components/Modal.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Pagination from '../components/Pagination.vue'
import Select from '../components/Select.vue'
import {
  bytesToGb, copyText, fmtBytes, fmtDate, fmtRelative, gbToBytes, pct, ratioClass,
} from '../format'

const users = ref([])
const groups = ref([])
const nodes = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const q = ref('')
const status = ref('')
const loading = ref(false)
const error = ref('')
let searchTimer = null

const modalOpen = ref(false)
const editing = ref(null)
const saving = ref(false)
const formError = ref('')
const form = reactive(emptyForm())

const credentialOpen = ref(false)
const credential = reactive({ userId: '', password: '', loginPassword: '', subscriptionUrl: '' })
const connectionsOpen = ref(false)
const connections = ref(null)
const connectionsError = ref('')
const deleteOpen = ref(false)
const deleteTarget = ref(null)
const devicesOpen = ref(false)
const devicesTarget = ref(null)
const devicesBusy = ref(false)

const connectionStatusLabel = computed(() => ({
  active: '正常', quota: '流量已用完', expired: '已过期', disabled: '已停用',
}[connections.value?.user?.status] || '未知'))
const connectionStatusClass = computed(() => ({
  active: 'badge-ok', quota: 'badge-warn', expired: 'badge-danger', disabled: 'badge-danger',
}[connections.value?.user?.status] || 'badge-muted'))

const onlineCount = computed(() => users.value.filter((u) => u.online).length)

const STATUS_OPTIONS = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '正常' },
  { value: 'disabled', label: '已禁用' },
  { value: 'expired', label: '已过期' },
]

function emptyForm() {
  return { userId: '', username: '', password: '', loginPassword: '', subscriptionToken: '', enabled: true, trafficGb: 0, maxDevices: 0, expireAt: '', groupIds: [], nodeIds: [] }
}
function resetForm() {
  Object.assign(form, emptyForm())
}
function used(u) { return (u.trafficTx || 0) + (u.trafficRx || 0) }
function trafficPct(u) { return pct(used(u), u.trafficLimit) }
function isExpired(u) { return u.expireAt && new Date(u.expireAt).getTime() < Date.now() }
function toggleId(list, id) {
  const i = list.indexOf(id)
  if (i >= 0) list.splice(i, 1)
  else list.push(id)
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await api.users({ page: page.value, pageSize, q: q.value, status: status.value })
    users.value = res.items
    total.value = res.total
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
function reload() { page.value = 1; load() }
function onSearch() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(reload, 300)
}

async function loadMeta() {
  try {
    const [g, n] = await Promise.all([api.groups(), api.nodes()])
    groups.value = g.items
    nodes.value = n.items
  } catch (e) {
    error.value = e.message
  }
}

function openCreate() {
  editing.value = null
  resetForm()
  formError.value = ''
  modalOpen.value = true
}
function openEdit(u) {
  editing.value = u
  Object.assign(form, {
    userId: u.userId, username: u.username || '', password: '', loginPassword: '',
    subscriptionToken: u.subscriptionToken || '', enabled: u.enabled,
    trafficGb: bytesToGb(u.trafficLimit), maxDevices: u.maxDevices || 0,
    expireAt: u.expireAt ? toLocalInput(u.expireAt) : '',
    groupIds: (u.groups || []).map((g) => g.id), nodeIds: [...(u.nodeIds || [])],
  })
  formError.value = ''
  modalOpen.value = true
}
function toLocalInput(v) {
  const d = new Date(v)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

async function save() {
  formError.value = ''
  saving.value = true
  try {
    const payload = {
      userId: form.userId.trim(), username: form.username.trim(), enabled: form.enabled,
      trafficLimitGb: Number(form.trafficGb) || 0, maxDevices: Number(form.maxDevices) || 0,
      expireAt: form.expireAt ? new Date(form.expireAt).toISOString() : null,
      groupIds: form.groupIds, nodeIds: form.nodeIds,
    }
    if (form.password) payload.password = form.password
    if (form.loginPassword) payload.loginPassword = form.loginPassword
    if (form.subscriptionToken.trim()) payload.subscriptionToken = form.subscriptionToken.trim()
    let res
    if (editing.value) res = await api.updateUser(editing.value.id, payload)
    else res = await api.createUser(payload)
    modalOpen.value = false
    notify(editing.value ? '用户已更新' : '用户已创建')
    if (res.password || res.loginPassword || !editing.value) {
      credential.userId = res.userId
      credential.password = res.password || ''
      credential.loginPassword = res.loginPassword || ''
      credential.subscriptionUrl = res.subscriptionUrl
      credentialOpen.value = true
    }
    load()
  } catch (e) {
    formError.value = e.message
  } finally {
    saving.value = false
  }
}

async function toggle(u) {
  try {
    if (u.enabled) await api.disableUser(u.id)
    else await api.enableUser(u.id)
    notify(u.enabled ? '用户已禁用' : '用户已启用')
    load()
  } catch (e) {
    notify(e.message, 'error')
  }
}
function openConnections(u) {
  connectionsOpen.value = true
  connections.value = null
  connectionsError.value = ''
  api.userConnections(u.id)
    .then((res) => { connections.value = res })
    .catch((e) => { connectionsError.value = e.message })
}
async function copyConn(text) {
  if (!text) return
  await copyText(text)
  notify('已复制到剪贴板')
}
async function copySub(u) {
  let url = u.subscriptionUrl
  if (!url) {
    const res = await api.resetSubscription(u.id)
    url = res.subscriptionUrl
    notify('New subscription link generated')
    load()
  }
  if (url) {
    await copyText(url)
    notify('订阅链接已复制')
  }
}
async function resetSub(u) {
  if (!confirm(`Generate a new subscription link for ${u.userId}? The old link stops working.`)) return
  try {
    const res = await api.resetSubscription(u.id)
    await copyText(res.subscriptionUrl)
    notify('New subscription link copied')
    load()
  } catch (e) {
    notify(e.message, 'error')
  }
}
function openResetDevices(u) {
  devicesTarget.value = u
  devicesOpen.value = true
}
async function confirmResetDevices() {
  if (!devicesTarget.value) return
  devicesBusy.value = true
  try {
    await api.resetDevices(devicesTarget.value.id)
    devicesOpen.value = false
    notify('Device state cleared')
    load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    devicesBusy.value = false
  }
}
async function resetTraffic(u) {
  if (!confirm(`重置 ${u.userId} 的流量统计？此操作不可撤销。`)) return
  try {
    await api.resetTraffic(u.id)
    notify('流量统计已重置')
    load()
  } catch (e) {
    notify(e.message, 'error')
  }
}
function remove(u) {
  deleteTarget.value = u
  deleteOpen.value = true
}
async function confirmDelete() {
  try {
    await api.deleteUser(deleteTarget.value.id)
    deleteOpen.value = false
    notify('用户已删除')
    load()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function copyCredential() {
  await copyText(`用户 ID: ${credential.userId}\n连接密码: ${credential.password}\n登录密码: ${credential.loginPassword}\n订阅: ${credential.subscriptionUrl}`)
  notify('Credentials copied')
}

onMounted(() => { loadMeta(); load() })
</script>

<style scoped>
.avatar-sm { width: 30px; height: 30px; border-radius: 7px; flex: none; display: grid; place-items: center; font-family: var(--mono); font-size: 12px; font-weight: 700; color: var(--on-accent); background: var(--accent); }
.stack { display: flex; flex-direction: column; gap: 14px; }
.conn-qr { display: flex; justify-content: center; padding: 2px 0; }
.conn-qr img { width: 150px; height: auto; aspect-ratio: 1; border-radius: 8px; background: #fff; padding: 7px; border: 1px solid var(--line); }
.conn-nodes { display: flex; flex-direction: column; gap: 10px; }
.conn-node { border: 1px solid var(--line-soft); border-radius: 10px; background: var(--panel-2); padding: 12px; }
.conn-configs { margin-top: 9px; border-top: 1px solid var(--line-soft); }
.conn-cfg { display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 6px 0; border-top: 1px solid var(--line-soft); font-size: 11.5px; color: var(--muted); }
.conn-cfg:first-child { border-top: 0; }
.conn-copy { border: 0; background: none; color: var(--accent); font-size: 10.5px; padding: 2px 0; white-space: nowrap; }
.conn-copy:hover { color: var(--accent-hover); }
</style>
