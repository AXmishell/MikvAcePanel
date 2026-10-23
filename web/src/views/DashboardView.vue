<template>
  <div>
    <div class="toolbar">
      <div>
        <div class="page-title">网络概览</div>
        <div class="dim small">节点、用户与近期安全发现的实时状态。</div>
      </div>
      <div class="spacer"></div>
      <button class="btn btn-sm" :disabled="loading" @click="load">
        <RefreshCw :size="14" :class="{ spin: loading }" /> 刷新
      </button>
      <router-link :to="adminPath('nodes')" class="btn btn-primary btn-sm"><Plus :size="14" /> 添加节点</router-link>
    </div>

    <div v-if="error" class="banner banner-danger">{{ error }}</div>

    <div class="stat-grid">
      <StatCard label="在线用户" :value="stats.usersOnline" :foot="`${stats.devicesOnline || 0} 活跃设备`" :icon="Wifi" />
      <StatCard label="账号" :value="stats.usersTotal" :foot="`${stats.usersActive} 已启用 · ${stats.usersExpired || 0} 已过期`" :icon="Users" />
      <StatCard label="节点" :value="`${stats.nodesOnline}/${stats.nodesTotal}`" foot="个 Agent 已连接" :icon="Server" />
      <StatCard label="今日流量" :value="fmtBytes((stats.todayTx || 0) + (stats.todayRx || 0))" :foot="`↑ ${fmtBytes(stats.todayTx)} · ↓ ${fmtBytes(stats.todayRx)}`" :icon="ArrowUpDown" />
    </div>

    <div class="grid-2 section-gap">
      <section class="card">
        <div class="card-header">
          <div>
            <div class="card-title">流量 · 最近 24 小时</div>
            <div class="card-sub">来自节点心跳聚合</div>
          </div>
          <div class="spacer"></div>
          <div class="legend"><span><i style="background: var(--accent)"></i>上传</span><span><i style="background: var(--accent-2)"></i>下载</span></div>
        </div>
        <div class="card-body">
          <LineChart v-if="trafficSeries.length" :data="trafficSeries" :series="trafficSeriesDef" :height="210" :format-y="fmtBytes" />
          <div v-else class="empty">暂无流量采样，节点首次心跳后会出现数据。</div>
        </div>
      </section>

      <section class="card">
        <div class="card-header">
          <div class="card-title">节点</div>
          <div class="spacer"></div>
          <router-link :to="adminPath('nodes')" class="small">管理 →</router-link>
        </div>
        <div class="card-body" style="padding-top: 8px">
          <div v-if="!nodes.length" class="empty">暂无节点。</div>
          <div v-for="n in nodes.slice(0, 5)" :key="n.id" class="node-row">
            <div class="node-ident">
              <span class="dot" :class="n.connected ? 'online pulse' : n.status === 'error' ? 'error' : 'offline'"></span>
              <div>
                <div class="node-row-name">{{ n.flag }} {{ n.name }}</div>
                <div class="dim small mono">{{ n.domain || n.ip }}:{{ n.port }}</div>
              </div>
            </div>
            <div class="node-row-metrics">
              <span class="small">{{ n.onlineUsers }}{{ n.maxOnline ? ' / ' + n.maxOnline : '' }} 在线</span>
              <span class="badge" :class="statusClass(n.status)">{{ statusLabel(n.status) }}</span>
            </div>
          </div>
          <div v-if="nodes.length > 5" class="dim small" style="padding-top: 8px">+{{ nodes.length - 5 }} more nodes</div>
        </div>
      </section>
    </div>

    <div class="grid-2 section-gap">
      <section class="card">
        <div class="card-header">
          <div class="card-title">安全与健康发现</div>
          <div class="spacer"></div>
          <router-link :to="adminPath('logs')" class="small">分析日志 →</router-link>
        </div>
        <div class="card-body">
          <div v-if="!findings.length" class="finding info">
            <div class="sev"></div>
            <div><div class="finding-title">一切正常</div><div class="finding-desc">最近 24 小时未检测到规则告警。</div></div>
          </div>
          <div v-for="f in findings" :key="f.id" class="finding" :class="f.severity">
            <div class="sev"></div>
            <div>
              <div class="finding-title">{{ f.title }}<span v-if="f.nodeName" class="dim"> · {{ f.nodeName }}</span></div>
              <div class="finding-desc">{{ f.description }}</div>
              <div class="finding-meta">{{ f.count ? f.count + ' events · ' : '' }}{{ fmtRelative(f.lastSeen) }}</div>
            </div>
          </div>
        </div>
      </section>

      <section class="card">
        <div class="card-header">
          <div class="card-title">最近动态</div>
          <div class="spacer"></div>
          <span class="dim small">{{ fmtRelative(data?.system?.now) }}</span>
        </div>
        <div class="card-body" style="padding-top: 8px">
          <div v-if="!events.length" class="empty">暂无动态。</div>
          <div v-for="e in events.slice(0, 6)" :key="e.id" class="event-row">
            <span class="dot" :class="e.severity === 'error' ? 'error' : e.severity === 'warning' ? 'error' : 'online'"></span>
            <div style="min-width: 0">
              <div class="truncate">{{ e.message }}</div>
              <div class="dim small">{{ fmtRelative(e.createdAt) }}</div>
            </div>
          </div>
        </div>
      </section>
    </div>

    <section class="card section-gap">
      <div class="card-header">
        <div class="card-title">最近代理日志</div>
        <div class="spacer"></div>
        <router-link :to="adminPath('logs')" class="small">查看全部 →</router-link>
      </div>
      <div class="table-wrap">
        <table class="log-table">
          <thead><tr><th>时间</th><th>节点</th><th>级别</th><th>事件</th><th>消息</th><th>客户端</th></tr></thead>
          <tbody>
            <tr v-for="l in recentLogs" :key="l.id">
              <td class="nowrap dim">{{ fmtRelative(l.ts) }}</td>
              <td class="nowrap">{{ l.nodeName }}</td>
              <td><span class="log-level" :class="l.level">{{ l.level }}</span></td>
              <td><span class="badge badge-muted">{{ eventLabel(l.event) }}</span></td>
              <td><div class="log-msg">{{ l.message }}</div></td>
              <td class="mono small">{{ l.clientIp || '—' }}</td>
            </tr>
            <tr v-if="!recentLogs.length"><td colspan="6" class="empty">暂无日志上报。</td></tr>
          </tbody>
        </table>
      </div>
    </section>

    <div class="dim small" style="text-align: center; margin-top: 22px">
      {{ data?.system?.version ? 'MikvAce ' + data.system.version : 'MikvAce Panel' }} · 运行时间 {{ uptimeLabel }}
    </div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ArrowUpDown, Plus, RefreshCw, Server, Users, Wifi } from 'lucide-vue-next'
import { adminPath, api } from '../api'
import StatCard from '../components/StatCard.vue'
import LineChart from '../components/LineChart.vue'
import { eventLabel, fmtBytes, fmtRelative, statusClass, statusLabel } from '../format'

const data = ref(null)
const loading = ref(false)
const error = ref('')
let timer = null

const stats = computed(() => data.value?.stats || {})
const nodes = computed(() => data.value?.nodes || [])
const findings = computed(() => data.value?.findings || [])
const events = computed(() => data.value?.events || [])
const recentLogs = computed(() => data.value?.recentLogs || [])
const trafficSeries = computed(() => (data.value?.trafficSeries || []).map((p) => ({ ...p, total: (p.tx || 0) + (p.rx || 0) })))
const trafficSeriesDef = [
  { key: 'tx', color: '#ff6b3d', fill: true },
  { key: 'rx', color: '#93b8a5', fill: false },
]
const uptimeLabel = computed(() => {
  const s = data.value?.system?.uptime || 0
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
  return `${Math.floor(s / 86400)}d`
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await api.dashboard()
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 30000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<style scoped>
.node-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 0; border-bottom: 1px solid var(--line-soft); }
.node-row:last-child { border-bottom: 0; }
.node-ident { display: flex; align-items: center; gap: 10px; min-width: 0; }
.node-row-name { font-weight: 600; font-size: 12.5px; }
.node-row-metrics { display: flex; align-items: center; gap: 9px; color: var(--muted); }
.event-row { display: flex; gap: 10px; padding: 9px 0; border-bottom: 1px solid var(--line-soft); font-size: 12px; }
.event-row:last-child { border-bottom: 0; }
.event-row .dot { margin-top: 6px; }
.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
