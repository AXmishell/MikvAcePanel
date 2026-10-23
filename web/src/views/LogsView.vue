<template>
  <div>
    <div class="toolbar">
      <div>
        <div class="page-title">日志分析</div>
        <div class="dim small">由 Agent 上报的代理日志，经过解析与规则化检测评分。</div>
      </div>
      <div class="spacer"></div>
      <Select v-model="range" :options="RANGE_OPTIONS" size="sm" style="width: 120px" @change="refreshAll" />
      <Select v-model="nodeId" :options="nodeOptions" size="sm" style="width: 150px" @change="refreshAll" />
      <button class="btn btn-sm" :disabled="loading" @click="refreshAll"><RefreshCw :size="14" :class="{ spin: loading }" /> 刷新</button>
      <label class="checkbox" style="margin-left: 4px"><span class="switch"><input v-model="autoRefresh" type="checkbox" /><span></span></span> 实时</label>
    </div>

    <div v-if="error" class="banner banner-danger">{{ error }}</div>

    <div class="stat-grid">
      <StatCard label="日志事件" :value="compact(summary?.total)" :foot="rangeLabel" :icon="ScrollText" />
      <StatCard label="错误" :value="compact(summary?.errors)" :foot="`${compact(summary?.warnings)} 条警告`" :icon="TriangleAlert" :tone="summary?.errors ? 'var(--danger)' : null" />
      <StatCard label="认证失败" :value="compact(summary?.authFailures)" :foot="`${compact(summary?.authSuccess)} 次成功`" :icon="ShieldAlert" :tone="summary?.authFailures ? 'var(--warn)' : null" />
      <StatCard label="在线用户" :value="summary?.online || 0" foot="活跃会话" :icon="Wifi" />
    </div>

    <div class="grid-2 section-gap">
      <section class="card">
        <div class="card-header">
          <div class="card-title">事件量</div>
          <div class="spacer"></div>
          <div class="legend">
            <span><i style="background: var(--accent)"></i>全部</span>
            <span><i style="background: var(--warn)"></i>错误</span>
            <span><i style="background: var(--danger)"></i>认证失败</span>
          </div>
        </div>
        <div class="card-body">
          <LineChart v-if="summary?.timeline?.length" :data="summary.timeline" :series="timelineSeries" :height="210" :format-y="compact" :format-time="shortTime" />
          <div v-else class="empty">该时间窗口暂无日志。</div>
        </div>
      </section>

      <section class="card">
        <div class="card-header">
          <div class="card-title">发现</div>
          <div class="spacer"></div>
          <span class="dim small">{{ summary?.findings?.length || 0 }} 个问题</span>
        </div>
        <div class="card-body" style="max-height: 286px; overflow-y: auto">
          <div v-if="!summary?.findings?.length" class="finding info">
            <div class="sev"></div>
            <div><div class="finding-title">未检测到问题</div><div class="finding-desc">该时间窗口内认证、错误与节点健康状态正常。</div></div>
          </div>
          <div v-for="f in summary?.findings || []" :key="f.id" class="finding" :class="f.severity">
            <div class="sev"></div>
            <div>
              <div class="finding-title">{{ f.title }}<span v-if="f.nodeName" class="dim"> · {{ f.nodeName }}</span></div>
              <div class="finding-desc">{{ f.description }}</div>
              <div v-if="f.details?.length" class="finding-meta">{{ f.details.join(' · ') }}</div>
              <div class="finding-meta">{{ f.count ? f.count + ' 条观察 · ' : '' }}{{ fmtRelative(f.lastSeen) }}</div>
            </div>
          </div>
        </div>
      </section>
    </div>

    <div class="grid-3 section-gap">
      <section class="card card-pad">
        <div class="card-title" style="margin-bottom: 10px">日志级别</div>
        <div v-for="l in summary?.levels || []" :key="l.name" class="bar-row">
          <span class="log-level" :class="l.name">{{ l.name }}</span>
          <div class="bar-track"><div class="bar-fill" :style="{ width: barWidth(l.count, maxLevel) + '%' }"></div></div>
          <span class="dim" style="text-align: right">{{ compact(l.count) }}</span>
        </div>
        <div v-if="!summary?.levels?.length" class="dim small">暂无数据。</div>
      </section>
      <section class="card card-pad">
        <div class="card-title" style="margin-bottom: 10px">高频事件</div>
        <div v-for="e in (summary?.events || []).slice(0, 6)" :key="e.name" class="bar-row">
          <span class="truncate">{{ eventLabel(e.name) }}</span>
          <div class="bar-track"><div class="bar-fill" :style="{ width: barWidth(e.count, maxEvent) + '%' }"></div></div>
          <span class="dim" style="text-align: right">{{ compact(e.count) }}</span>
        </div>
        <div v-if="!summary?.events?.length" class="dim small">暂无数据。</div>
      </section>
      <section class="card card-pad">
        <div class="card-title" style="margin-bottom: 10px">高频来源 IP</div>
        <div v-for="ip in (summary?.topIps || []).slice(0, 6)" :key="ip.ip" class="ip-row">
          <span class="mono small">{{ ip.ip }}</span>
          <span class="spacer"></span>
          <span v-if="ip.failures" class="badge badge-danger">{{ ip.failures }} fail</span>
          <span class="dim small">{{ compact(ip.count) }}</span>
        </div>
        <div v-if="!summary?.topIps?.length" class="dim small">暂无客户端地址。</div>
      </section>
    </div>

    <section class="card section-gap">
      <div class="card-header" style="flex-wrap: wrap">
        <div class="card-title">日志流</div>
        <div class="spacer"></div>
        <Select v-model="exportFormat" :options="EXPORT_FORMAT_OPTIONS" size="sm" style="width: 128px" />
        <button class="btn btn-sm" @click="downloadLogs"><Download :size="13" /> 下载</button>
        <button class="btn btn-danger btn-sm" @click="clearLogs"><Trash2 :size="13" /> 清空日志</button>
      </div>
      <div class="toolbar" style="padding: 12px 14px 0; margin-bottom: 10px">
        <Select v-model="filterLevel" :options="levelOptions" size="sm" style="width: 120px" @change="reloadLogs" />
        <Select v-model="filterEvent" :options="eventOptions" size="sm" style="width: 170px" @change="reloadLogs" />
        <input v-model="filterQuery" type="search" placeholder="搜索消息、IP、用户…" @keyup.enter="reloadLogs" />
        <button class="btn btn-sm" @click="reloadLogs">应用</button>
        <div class="spacer"></div>
        <span class="dim small">{{ total }} 行</span>
      </div>
      <div class="table-wrap">
        <table class="log-table">
          <thead><tr><th>时间</th><th>节点</th><th>级别</th><th>事件</th><th>用户</th><th>客户端 IP</th><th>消息</th><th></th></tr></thead>
          <tbody>
            <tr v-for="l in logs" :key="l.id" style="cursor: pointer" @click="openDetail(l)">
              <td class="nowrap dim">{{ fmtDate(l.ts) }}</td>
              <td class="nowrap">{{ l.nodeName }}</td>
              <td><span class="log-level" :class="l.level">{{ l.level }}</span></td>
              <td><span class="badge" :class="l.event === 'auth_fail' || l.event === 'error' ? 'badge-danger' : 'badge-muted'">{{ eventLabel(l.event) }}</span></td>
              <td class="mono small">{{ l.userId || '—' }}</td>
              <td class="mono small">{{ l.clientIp || '—' }}</td>
              <td><div class="log-msg">{{ l.message }}</div></td>
              <td><ChevronRight :size="14" class="dim" /></td>
            </tr>
            <tr v-if="!logs.length"><td colspan="8"><div class="empty">没有符合筛选条件的日志。</div></td></tr>
          </tbody>
        </table>
      </div>
      <Pagination :page="page" :page-size="pageSize" :total="total" @change="(p) => { page = p; loadLogs() }" />
    </section>

    <Modal v-model="detailOpen" title="日志详情" size="lg">
      <div v-if="detail">
        <dl class="kv">
          <dt>时间戳</dt><dd>{{ fmtDate(detail.ts) }}</dd>
          <dt>节点</dt><dd>{{ detail.nodeName }} (#{{ detail.nodeId }})</dd>
          <dt>级别 / 事件</dt><dd><span class="log-level" :class="detail.level">{{ detail.level }}</span> · {{ eventLabel(detail.event) }}</dd>
          <dt>用户</dt><dd class="mono">{{ detail.userId || '—' }}</dd>
          <dt>客户端</dt><dd class="mono">{{ detail.clientIp || '—' }}{{ detail.port ? ':' + detail.port : '' }}</dd>
          <dt>目标</dt><dd class="mono">{{ detail.host || '—' }}</dd>
          <dt>流量</dt><dd>{{ detail.bytesTx ? fmtBytes(detail.bytesTx) + ' ↑' : '' }} {{ detail.bytesRx ? fmtBytes(detail.bytesRx) + ' ↓' : '' }}{{ !detail.bytesTx && !detail.bytesRx ? '—' : '' }}</dd>
          <dt>错误</dt><dd style="color: var(--danger)">{{ detail.error || '—' }}</dd>
          <dt>消息</dt><dd>{{ detail.message }}</dd>
        </dl>
        <div class="divider"></div>
        <pre class="code">{{ detail.raw }}</pre>
      </div>
      <template #footer>
        <button class="btn" @click="copy(detail?.raw)"><Copy :size="14" /> 复制原始日志</button>
        <button class="btn btn-primary" @click="detailOpen = false">关闭</button>
      </template>
    </Modal>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  ChevronRight, Copy, Download, RefreshCw, ScrollText, ShieldAlert, Trash2, TriangleAlert, Wifi,
} from 'lucide-vue-next'
import { api } from '../api'
import { notify } from '../store'
import StatCard from '../components/StatCard.vue'
import LineChart from '../components/LineChart.vue'
import Pagination from '../components/Pagination.vue'
import Modal from '../components/Modal.vue'
import Select from '../components/Select.vue'
import { copyText, eventLabel, fmtBytes, fmtDate, fmtRelative } from '../format'

const route = useRoute()
const range = ref('24h')
const nodeId = ref(String(route.query.nodeId || ''))
const autoRefresh = ref(true)
const loading = ref(false)
const error = ref('')
const summary = ref(null)
const options = ref({ nodes: [], levels: [], events: [] })

const EXPORT_FORMAT_OPTIONS = [
  { value: 'log', label: '日志 .log' },
  { value: 'csv', label: '表格 .csv' },
  { value: 'jsonl', label: 'JSONL' },
]
const RANGE_OPTIONS = [
  { value: '1h', label: '最近 1 小时' },
  { value: '6h', label: '最近 6 小时' },
  { value: '24h', label: '最近 24 小时' },
  { value: '7d', label: '最近 7 天' },
  { value: '30d', label: '最近 30 天' },
]
const nodeOptions = computed(() => [
  { value: '', label: '全部节点' },
  ...(options.value.nodes || []).map((n) => ({
    value: String(n.id),
    label: `${n.flag || ''} ${n.name || ''}`.trim() || `节点 #${n.id}`,
  })),
])
const levelOptions = computed(() => [
  { value: '', label: '全部级别' },
  ...(options.value.levels || []).map((l) => ({ value: l, label: l })),
])
const eventOptions = computed(() => [
  { value: '', label: '全部事件' },
  ...(options.value.events || []).map((e) => ({ value: e.value, label: e.label })),
])

const logs = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const filterLevel = ref('')
const filterEvent = ref('')
const filterQuery = ref('')
const exportFormat = ref('log')

const detailOpen = ref(false)
const detail = ref(null)
let timer = null

const rangeLabel = computed(() => ({ '1h': '最近 1 小时', '6h': '最近 6 小时', '24h': '最近 24 小时', '7d': '最近 7 天', '30d': '最近 30 天' }[range.value]))
const timelineSeries = [
  { key: 'total', color: '#ff6b3d', fill: true },
  { key: 'errors', color: '#e6b64c', fill: false },
  { key: 'authFailures', color: '#ff6f6f', fill: false },
]
const maxLevel = computed(() => Math.max(1, ...(summary.value?.levels || []).map((l) => l.count)))
const maxEvent = computed(() => Math.max(1, ...(summary.value?.events || []).map((l) => l.count)))

function rangeMs() {
  const n = parseInt(range.value, 10)
  if (range.value.endsWith('d')) return n * 86400000
  return n * 3600000
}
function windowFrom() {
  return new Date(Date.now() - rangeMs()).toISOString()
}
function compact(n) {
  n = Number(n || 0)
  if (n >= 1e9) return (n / 1e9).toFixed(1) + 'B'
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k'
  return String(n)
}
function shortTime(t) {
  const d = new Date(t)
  if (rangeMs() > 2 * 86400000) return d.toLocaleDateString([], { month: '2-digit', day: '2-digit' })
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
function barWidth(v, max) {
  return Math.max(2, Math.round((Number(v || 0) / max) * 100))
}

async function loadSummary() {
  try {
    summary.value = await api.logSummary({ range: range.value, nodeId: nodeId.value })
  } catch (e) {
    error.value = e.message
  }
}
async function loadLogs() {
  loading.value = true
  error.value = ''
  try {
    const res = await api.logs({
      page: page.value, pageSize, nodeId: nodeId.value, level: filterLevel.value,
      event: filterEvent.value, q: filterQuery.value, from: windowFrom(),
    })
    logs.value = res.items
    total.value = res.total
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
function refreshAll() {
  page.value = 1
  loadSummary()
  loadLogs()
}
function reloadLogs() {
  page.value = 1
  loadLogs()
}
async function loadOptions() {
  try {
    options.value = await api.logOptions()
  } catch (e) {
    error.value = e.message
  }
}
function openDetail(l) {
  detail.value = l
  detailOpen.value = true
}
async function downloadLogs() {
  try {
    const res = await api.downloadLogs({
      format: exportFormat.value,
      nodeId: nodeId.value,
      level: filterLevel.value,
      event: filterEvent.value,
      q: filterQuery.value,
      from: windowFrom(),
    })
    const url = URL.createObjectURL(res.blob)
    const a = document.createElement('a')
    a.href = url
    a.download = res.filename || `mikvace-logs.${exportFormat.value}`
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
    notify(res.truncated ? '日志已下载（超过 10 万行，已截断）' : '日志已下载')
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function clearLogs() {
  if (!confirm('删除所有已存储的代理日志？分析历史将丢失。')) return
  try {
    const res = await api.clearLogs(new Date().toISOString())
    notify(`已删除 ${res.deleted} 条日志`)
    refreshAll()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function copy(text) {
  if (!text) return
  await copyText(text)
  notify('已复制')
}

onMounted(async () => {
  await loadOptions()
  refreshAll()
  timer = setInterval(() => {
    if (autoRefresh.value) {
      loadSummary()
      if (page.value === 1) loadLogs()
    }
  }, 12000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<style scoped>
.ip-row { display: flex; align-items: center; gap: 8px; padding: 6px 0; border-bottom: 1px solid var(--line-soft); }
.ip-row:last-child { border-bottom: 0; }
.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
