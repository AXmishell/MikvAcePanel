export function fmtBytes(n, decimals = 1) {
  n = Number(n || 0)
  if (n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : decimals)} ${units[i]}`
}

export function fmtTraffic(n) {
  return fmtBytes(n, 2)
}

export function gbToBytes(gb) {
  const n = Number(gb)
  if (!Number.isFinite(n) || n <= 0) return 0
  return Math.round(n * 1024 * 1024 * 1024)
}

export function bytesToGb(bytes) {
  const n = Number(bytes || 0)
  if (!n) return 0
  return Math.round((n / (1024 * 1024 * 1024)) * 100) / 100
}

export function fmtDate(v, withTime = true) {
  if (!v) return '—'
  const d = new Date(v)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 1970) return '—'
  const opts = withTime
    ? { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }
    : { year: 'numeric', month: '2-digit', day: '2-digit' }
  return d.toLocaleString(undefined, opts)
}

export function fmtRelative(v) {
  if (!v) return '从未'
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return '从未'
  const sec = Math.round((Date.now() - d.getTime()) / 1000)
  if (sec < 0) {
    const future = -sec
    if (future < 60) return '即将'
    if (future < 3600) return `${Math.round(future / 60)} 分钟后`
    if (future < 86400) return `${Math.round(future / 3600)} 小时后`
    return `${Math.round(future / 86400)} 天后`
  }
  if (sec < 10) return '刚刚'
  if (sec < 60) return `${sec} 秒前`
  if (sec < 3600) return `${Math.round(sec / 60)} 分钟前`
  if (sec < 86400) return `${Math.round(sec / 3600)} 小时前`
  if (sec < 86400 * 30) return `${Math.round(sec / 86400)} 天前`
  return fmtDate(v, false)
}

export function pct(used, limit) {
  if (!limit || limit <= 0) return 0
  return Math.min(100, Math.round((used / limit) * 100))
}

export function ratioClass(p) {
  if (p >= 90) return 'danger'
  if (p >= 70) return 'warn'
  return ''
}

export function statusLabel(status) {
  switch (status) {
    case 'online': return '在线'
    case 'offline': return '离线'
    case 'error': return '错误'
    case 'syncing': return '同步中'
    default: return status || '未知'
  }
}

export function statusClass(status) {
  switch (status) {
    case 'online': return 'badge-ok'
    case 'error': return 'badge-danger'
    case 'syncing': return 'badge-info'
    default: return 'badge-muted'
  }
}

export function eventLabel(event) {
  const map = {
    auth_success: '认证成功',
    auth_fail: '认证失败',
    connect: '连接',
    disconnect: '断开',
    start: '启动',
    shutdown: '关闭',
    error: '错误',
    warning: '警告',
    network_error: '网络错误',
    tls: 'TLS',
    acme: 'ACME',
    stats: '统计',
    log: '日志',
  }
  return map[event] || event || '—'
}

export async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return true
  }
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  try {
    document.execCommand('copy')
    return true
  } finally {
    ta.remove()
  }
}

export function shortToken(v, n = 10) {
  if (!v) return '—'
  return v.length > n * 2 ? v.slice(0, n) + '…' + v.slice(-4) : v
}
