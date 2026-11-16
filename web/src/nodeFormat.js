// Presentation helpers shared by the node list, card and detail views.

export function coreLabel(n) {
  const cores = n.cores && n.cores.length ? n.cores : [n.type || 'hysteria']
  return cores.map((c) => (c === 'xray' ? 'Xray' : 'Hy2')).join(' + ')
}

export function runtimeMode(n) {
  return n?.runtimeMode === 'docker' ? 'docker' : 'process'
}

export function runtimeLabel(mode) {
  return mode === 'docker' ? 'Docker 模式' : '进程模式'
}

export function versionLabel(n) {
  const cores = n.cores && n.cores.length ? n.cores : [n.type || 'hysteria']
  const parts = []
  if (n.agentVersion) parts.push('Agent ' + n.agentVersion)
  if (cores.includes('hysteria')) parts.push('Hysteria ' + (n.hy2Version || 'unknown'))
  if (cores.includes('xray')) parts.push('Xray ' + (n.xrayVersion || 'unknown'))
  return parts.join(' · ') || 'unknown'
}

export function agentNeedsUpgrade(n, panelVersion) {
  const agent = String(n?.agentVersion || '').trim()
  const panel = String(panelVersion || '').trim()
  if (!agent || !panel) return false
  return agent !== panel
}

export function memPct(n) {
  const m = n.metrics
  if (!m || !m.memTotal) return 0
  return Math.min(100, Math.round((m.memUsed / m.memTotal) * 100))
}

export function diskPct(n) {
  const m = n.metrics
  if (!m || !m.diskTotal) return 0
  return Math.min(100, Math.round((m.diskUsed / m.diskTotal) * 100))
}

export function rankingLabel(value) {
  const n = Number(value)
  if (!Number.isFinite(n)) return '1'
  return Number.isInteger(n) ? String(n) : String(Number(n.toFixed(2)))
}
