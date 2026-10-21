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
      <button class="tab" :class="{ active: tab === 'security' }" @click="tab = 'security'">安全</button>
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
              <div class="copy-row"><input :value="subscriptionPreview" readonly /><button class="btn btn-sm" @click="copy(subscriptionPreview)"><Copy :size="13" /> 复制</button></div>
            </div>
          </div>
          <div class="divider"></div>
          <dl class="kv">
            <dt>版本</dt><dd>{{ meta.version }}</dd>
            <dt>平台</dt><dd>{{ meta.platform }}</dd>
            <dt>数据目录</dt><dd class="mono">{{ meta.dataDir }}</dd>
          </dl>
        </div>
        <div class="modal-footer" style="border-radius: 0 0 16px 16px">
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
              <select v-model="form.happ_routing_mode">
                <option value="off">关闭（清除 HAPP 路由）</option>
                <option value="custom">自定义路由配置</option>
              </select>
            </div>
            <div class="field full"><label>HAPP 路由配置（JSON）</label>
              <textarea v-model="form.happ_routing_profile" rows="4" placeholder='{"rules":[]}'></textarea>
              <span class="hint">通过 <span class="mono">routing</span> 请求头发送，并前置到 HAPP/Incy 的 URI 订阅。</span>
            </div>
            <div class="field full"><label>HAPP 公告</label><input v-model="form.happ_announce" type="text" placeholder="可选公告" /></div>
          </div>
        </div>
        <div class="modal-footer" style="border-radius: 0 0 16px 16px">
          <button class="btn btn-primary" :disabled="saving" @click="save(['page_title', 'subscription_title', 'page_note', 'logo_url', 'subscription_update_interval', 'support_url', 'web_page_url', 'subscription_buttons', 'happ_provider_id', 'happ_routing_mode', 'happ_routing_profile', 'happ_announce'])">保存订阅设置</button>
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
            <div class="field"><label>保留天数</label><input v-model="form.log_retention_days" type="number" min="1" /></div>
            <div class="field"><label>设备宽限期（分钟）</label><input v-model="form.device_grace_minutes" type="number" min="1" />
              <span class="hint">离线设备 IP 在认证限流中被遗忘前的等待时间。</span>
            </div>
            <div class="field"><label>暴力破解阈值（单 IP 失败次数）</label><input v-model="form.alert_auth_fail_threshold" type="number" min="1" /></div>
            <div class="field"><label>错误爆发阈值（单节点错误数）</label><input v-model="form.alert_error_burst_threshold" type="number" min="1" /></div>
          </div>
        </div>
        <div class="modal-footer" style="border-radius: 0 0 16px 16px">
          <button class="btn btn-primary" :disabled="saving" @click="save(['log_ingest_enabled', 'log_retention_days', 'device_grace_minutes', 'alert_auth_fail_threshold', 'alert_error_burst_threshold'])">保存分析设置</button>
        </div>
      </section>

      <section v-show="tab === 'security'" class="card">
        <div class="card-header"><div class="card-title">管理员密码</div></div>
        <div class="card-body">
          <div v-if="auth.warning" class="banner banner-warn">{{ auth.warning }}</div>
          <div class="form-grid" style="max-width: 640px">
            <div class="field full"><label>当前密码</label><input v-model="pw.old" type="password" autocomplete="current-password" /></div>
            <div class="field"><label>新密码</label><input v-model="pw.next" type="password" autocomplete="new-password" /></div>
            <div class="field"><label>确认新密码</label><input v-model="pw.confirm" type="password" autocomplete="new-password" /></div>
          </div>
          <div class="divider"></div>
          <div class="small dim">会话使用服务端密钥签名，密钥位于 <span class="mono">{{ meta.dataDir }}/jwt.secret</span>. 修改密码不会使现有会话失效；如需强制下线请轮换该密钥。</div>
        </div>
        <div class="modal-footer" style="border-radius: 0 0 16px 16px">
          <button class="btn btn-primary" :disabled="pwSaving" @click="changePassword">{{ pwSaving ? '更新中…' : '更新密码' }}</button>
        </div>
      </section>

      <section v-show="tab === 'security'" class="card section-gap">
        <div class="card-header">
          <div>
            <div class="card-title">API 密钥</div>
            <div class="card-sub">用于自动化与 REST API 的作用域凭据</div>
          </div>
          <div class="spacer"></div>
          <button class="btn btn-primary btn-sm" @click="openKeyModal"><Plus :size="14" /> 新建密钥</button>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>名称</th><th>令牌</th><th>作用域</th><th>最后使用</th><th>状态</th><th></th></tr></thead>
            <tbody>
              <tr v-for="k in apiKeys" :key="k.id">
                <td style="font-weight: 600">{{ k.name }}<div class="dim small">{{ k.rateLimit || 60 }} req/min</div></td>
                <td class="mono small">{{ k.prefix }}…</td>
                <td><div class="chip-list"><span v-for="sc in k.scopes" :key="sc" class="badge badge-muted">{{ sc }}</span></div></td>
                <td class="small dim">{{ k.lastUsedAt ? fmtRelative(k.lastUsedAt) : 'never' }}</td>
                <td>
                  <span v-if="!k.enabled" class="badge badge-muted">已禁用</span>
                  <span v-else-if="k.expiresAt && new Date(k.expiresAt) < new Date()" class="badge badge-danger">已过期</span>
                  <span v-else class="badge badge-ok">正常</span>
                </td>
                <td>
                  <div class="row-actions">
                    <button class="icon-btn" :title="k.enabled ? '禁用' : '启用'" @click="toggleKey(k)"><Power :size="15" /></button>
                    <button class="icon-btn danger" title="删除" @click="deleteKey(k)"><Trash2 :size="15" /></button>
                  </div>
                </td>
              </tr>
              <tr v-if="!apiKeys.length"><td colspan="6"><div class="empty">暂无 API 密钥。</div></td></tr>
            </tbody>
          </table>
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
                  <button class="icon-btn" title="Download" @click="downloadBackup(b)"><Download :size="15" /></button>
                  <button class="icon-btn danger" title="删除" @click="deleteBackup(b)"><Trash2 :size="15" /></button>
                </div></td>
              </tr>
              <tr v-if="!backups.length"><td colspan="4"><div class="empty">暂无备份。</div></td></tr>
            </tbody>
          </table>
        </div>
        <div class="modal-footer" style="border-radius: 0 0 16px 16px">
          <button class="btn btn-primary" :disabled="saving" @click="save(['backup_enabled', 'backup_interval_hours', 'backup_keep'])">保存备份设置</button>
        </div>
      </section>
    </template>

    <Modal v-model="keyModal" title="创建 API 密钥" size="sm">
      <div v-if="keyError" class="banner banner-danger">{{ keyError }}</div>
      <div class="field" style="margin-bottom: 13px">
        <label>名称</label><input v-model="keyForm.name" type="text" placeholder="监控机器人" />
      </div>
      <div class="field" style="margin-bottom: 13px">
        <label>作用域</label>
        <div class="chip-list">
          <button v-for="sc in scopes" :key="sc.value" type="button" class="chip" :class="{ on: keyForm.scopes.includes(sc.value) }" @click="toggleScope(sc.value)">{{ sc.value }}</button>
        </div>
        <span class="hint">{{ scopes.find(s => keyForm.scopes.includes(s.value))?.label || '至少选择一个作用域' }}</span>
      </div>
      <div class="field" style="margin-bottom: 13px"><label>速率限制（请求 / 分钟）</label><input v-model.number="keyForm.rateLimit" type="number" min="1" /></div>
      <div class="field"><label>到期时间（可选）</label><input v-model="keyForm.expiresAt" type="datetime-local" /></div>
      <template #footer>
        <button class="btn" @click="keyModal = false">取消</button>
        <button class="btn btn-primary" :disabled="keySaving" @click="createKey">{{ keySaving ? '创建中…' : '创建密钥' }}</button>
      </template>
    </Modal>

    <Modal v-model="tokenModal" title="API 密钥已创建" size="sm">
      <div class="banner banner-warn">请立即复制该令牌，之后无法再次查看。</div>
      <div class="copy-row">
        <input :value="newToken" readonly @focus="$event.target.select()" />
        <button class="btn btn-primary" @click="copy(newToken)"><Copy :size="14" /> 复制</button>
      </div>
    </Modal>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { Copy, Download, Plus, Power, Trash2 } from 'lucide-vue-next'
import { api } from '../api'
import { auth, notify } from '../store'
import Modal from '../components/Modal.vue'
import { copyText, fmtBytes, fmtDate, fmtRelative } from '../format'
import { setTheme, theme, themeOptions } from '../theme'

const tab = ref('general')
const settings = ref(null)
const meta = ref({ version: '', platform: '', dataDir: '' })
const form = reactive({})
const error = ref('')
const saved = ref('')
const saving = ref(false)
const pwSaving = ref(false)
const pw = reactive({ old: '', next: '', confirm: '' })

const subscriptionPreview = ref('')
const apiKeys = ref([])
const scopes = ref([])
const keyModal = ref(false)
const keySaving = ref(false)
const keyError = ref('')
const keyForm = reactive({ name: '', scopes: [], rateLimit: 60, expiresAt: '' })
const tokenModal = ref(false)
const newToken = ref('')
const backups = ref([])
const backupBusy = ref(false)
const restoreMessage = ref('')

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
  if (!confirm(`删除备份 ${b.name}?`)) return
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
  if (!confirm('用该备份替换面板数据库？必须重启面板后才会生效。')) return
  try {
    const res = await api.restoreBackup(file)
    restoreMessage.value = res.message || '重启面板后恢复生效。'
    notify('备份校验通过 — 重启面板后生效')
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function loadKeys() {
  try {
    const res = await api.apiKeys()
    apiKeys.value = res.items || []
    scopes.value = res.scopes || []
  } catch (e) {
    error.value = e.message
  }
}
function openKeyModal() {
  keyForm.name = ''
  keyForm.scopes = ['stats:read']
  keyForm.rateLimit = 60
  keyForm.expiresAt = ''
  keyError.value = ''
  keyModal.value = true
}
function toggleScope(v) {
  const i = keyForm.scopes.indexOf(v)
  if (i >= 0) keyForm.scopes.splice(i, 1)
  else keyForm.scopes.push(v)
}
async function createKey() {
  keyError.value = ''
  keySaving.value = true
  try {
    const res = await api.createAPIKey({
      name: keyForm.name,
      scopes: keyForm.scopes,
      rateLimit: keyForm.rateLimit,
      expiresAt: keyForm.expiresAt ? new Date(keyForm.expiresAt).toISOString() : null,
    })
    keyModal.value = false
    newToken.value = res.token
    tokenModal.value = true
    notify('API 密钥已创建')
    loadKeys()
  } catch (e) {
    keyError.value = e.message
  } finally {
    keySaving.value = false
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
  if (!confirm(`删除 API 密钥 "${k.name}"？使用它的自动化将停止工作。`)) return
  try {
    await api.deleteAPIKey(k.id)
    notify('API key deleted')
    loadKeys()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function load() {
  try {
    const res = await api.settings()
    settings.value = res.settings
    meta.value = { version: res.version, platform: res.platform, dataDir: res.dataDir }
    Object.assign(form, res.settings)
    form.log_ingest_enabled = form.log_ingest_enabled === 'true' || form.log_ingest_enabled === true
    syncPreview()
  } catch (e) {
    error.value = e.message
  }
}
function onIngestToggle() {
  form.log_ingest_enabled = !!form.log_ingest_enabled
}
async function save(keys) {
  saving.value = true
  error.value = ''
  saved.value = ''
  try {
    const payload = {}
    for (const k of keys) {
      payload[k] = form[k]
    }
    if (keys.includes('log_ingest_enabled')) payload.log_ingest_enabled = form.log_ingest_enabled ? 'true' : 'false'
    const res = await api.updateSettings(payload)
    settings.value = res.settings
    Object.assign(form, res.settings)
    form.log_ingest_enabled = form.log_ingest_enabled === 'true' || form.log_ingest_enabled === true
    syncPreview()
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
async function changePassword() {
  if (pw.next.length < 8) return notify('新密码至少需要 8 个字符', 'error')
  if (pw.next !== pw.confirm) return notify('两次输入的密码不一致', 'error')
  pwSaving.value = true
  try {
    await api.changePassword(pw.old, pw.next)
    pw.old = ''
    pw.next = ''
    pw.confirm = ''
    auth.warning = ''
    notify('密码已更新')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    pwSaving.value = false
  }
}
async function copy(text) {
  await copyText(text)
  notify('已复制')
}
onMounted(() => { load(); loadKeys(); loadBackups() })
</script>

<style scoped>
.theme-options { display: flex; gap: 12px; flex-wrap: wrap; }
.theme-option {
  display: flex; align-items: center; gap: 12px; width: 236px; padding: 12px;
  border: 1px solid var(--line); border-radius: 12px; background: var(--panel-2);
  color: var(--text); text-align: left; transition: 0.16s ease;
}
.theme-option:hover { border-color: var(--muted-2); }
.theme-option.on { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
.theme-swatch { width: 44px; height: 34px; border-radius: 8px; border: 1px solid var(--line); flex: none; }
.theme-swatch--dark { background: linear-gradient(135deg, #060a12, #1d2b42); }
.theme-swatch--light { background: linear-gradient(135deg, #ffffff, #e8eef6); }
.theme-meta { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.theme-meta strong { font-size: 12.5px; }
.theme-meta small { font-size: 11px; color: var(--muted-2); }
</style>
