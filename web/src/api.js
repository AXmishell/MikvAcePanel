// Build-time API origin. Keep the default relative path for single-origin
// deployments; set VITE_API_BASE_URL=https://api.example.com/api for CDN setups.
export const API_BASE = (import.meta.env.VITE_API_BASE_URL || '/api').replace(/\/+$/, '')
const BASE = API_BASE

const ENTRY_KEY = 'mikvace_admin_entry'

export function getAdminEntry() {
  return localStorage.getItem(ENTRY_KEY) || ''
}

export function setAdminEntry(entry) {
  const value = String(entry || '').trim()
  if (value) localStorage.setItem(ENTRY_KEY, value)
  else localStorage.removeItem(ENTRY_KEY)
}

// The public probe tells the SPA whether the optional security entrance is
// enabled. The entrance itself is never returned by the API. Always ask the
// server so a change made in another tab cannot leave the guard with a stale
// answer.
let adminConfigPromise = null
export async function fetchAdminConfig(force = false) {
  if (!force && adminConfigPromise) return adminConfigPromise
  adminConfigPromise = fetch(BASE + '/admin/config', {
    cache: 'no-store',
    headers: { Accept: 'application/json' },
  })
    .then((res) => {
      if (!res.ok) throw new Error('admin config probe failed')
      return res.json()
    })
    .finally(() => {
      adminConfigPromise = null
    })
  return adminConfigPromise
}
export function resetAdminConfig() {
  adminConfigPromise = null
}

// First-run setup. These endpoints are public by design and only succeed while
// the administrators table is empty.
export function fetchInstallStatus() {
  return send('/install/status', {}, '', null, false)
}
export function installPanel(data) {
  return send('/install', { method: 'POST', body: data }, '', null, false)
}
export function testInstallDatabase(data) {
  return send('/install/database/test', { method: 'POST', body: data }, '', null, false)
}
export function saveInstallDatabase(data) {
  return send('/install/database', { method: 'POST', body: data }, '', null, false)
}

// Build an administrator URL that keeps the current security entrance.
export function adminPath(subpath = '') {
  const entry = getAdminEntry()
  const base = entry ? `/admin/${entry}` : '/admin'
  if (!subpath) return base
  return base + '/' + String(subpath).replace(/^\/+/, '')
}


let unauthorizedHandler = null
let portalUnauthorizedHandler = null
export function setUnauthorizedHandler(fn) {
  unauthorizedHandler = fn
}
export function setPortalUnauthorizedHandler(fn) {
  portalUnauthorizedHandler = fn
}

function token() {
  return localStorage.getItem('mikvace_token') || ''
}
function portalToken() {
  return localStorage.getItem('mikvace_portal_token') || ''
}

async function send(path, options = {}, bearer = '', onUnauthorized = null, adminRequest = false) {
  const { method = 'GET', body, query } = options
  let url = BASE + path
  if (query) {
    const qs = new URLSearchParams()
    Object.entries(query).forEach(([k, v]) => {
      if (v !== undefined && v !== null && v !== '') qs.set(k, v)
    })
    const s = qs.toString()
    if (s) url += '?' + s
  }
  const headers = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (bearer) headers.Authorization = 'Bearer ' + bearer
  if (adminRequest) {
    const entry = getAdminEntry()
    if (entry) headers['X-Admin-Entry'] = entry
  }
  let res
  try {
    res = await fetch(url, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  } catch (e) {
    throw new Error('Network error: ' + e.message)
  }
  const text = await res.text()
  let data = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = { error: text }
  }
  if (res.status === 401 && bearer && onUnauthorized) {
    onUnauthorized()
  }
  if (!res.ok) {
    const err = new Error((data && data.error) || `Request failed (${res.status})`)
    err.status = res.status
    err.data = data
    throw err
  }
  return data
}

function request(path, options = {}) {
  return send(path, options, token(), unauthorizedHandler, true)
}

function portalRequest(path, options = {}) {
  return send(path, options, portalToken(), portalUnauthorizedHandler)
}

function adminHeaders(extra = {}) {
  const headers = { ...extra }
  const entry = getAdminEntry()
  if (entry) headers['X-Admin-Entry'] = entry
  return headers
}

export const api = {
  installStatus: () => fetchInstallStatus(),
  install: (data) => installPanel(data),
  installDatabaseTest: (data) => testInstallDatabase(data),
  installDatabase: (data) => saveInstallDatabase(data),
  login: (username, password, captchaId, captchaAnswer) =>
    request('/login', { method: 'POST', body: { username, password, captchaId, captchaAnswer } }),
  loginCaptcha: () => request('/login/captcha'),
  login2FA: (challenge, code) => request('/login/2fa', { method: 'POST', body: { challenge, code } }),
  twoFactorStatus: () => request('/2fa'),
  setupTwoFactor: () => request('/2fa/setup', { method: 'POST' }),
  enableTwoFactor: (code) => request('/2fa/enable', { method: 'POST', body: { code } }),
  disableTwoFactor: (password, code) => request('/2fa/disable', { method: 'POST', body: { password, code } }),
  regenerateRecovery: (password, code) => request('/2fa/recovery', { method: 'POST', body: { password, code } }),
  logout: () => request('/logout', { method: 'POST' }),
  me: () => request('/me'),
  updateAccount: (username, currentPassword, code) =>
    request('/admin/account', { method: 'PUT', body: { username, currentPassword, code } }),
  changePassword: (oldPassword, newPassword, code) =>
    request('/change-password', { method: 'POST', body: { oldPassword, newPassword, code } }),

  dashboard: () => request('/dashboard'),
  events: (query) => request('/events', { query }),

  users: (query) => request('/users', { query }),
  user: (id) => request(`/users/${id}`),
  userConnections: (id) => request(`/users/${id}/connections`),
  createUser: (data) => request('/users', { method: 'POST', body: data }),
  updateUser: (id, data) => request(`/users/${id}`, { method: 'PUT', body: data }),
  deleteUser: (id) => request(`/users/${id}`, { method: 'DELETE' }),
  enableUser: (id) => request(`/users/${id}/enable`, { method: 'POST' }),
  disableUser: (id) => request(`/users/${id}/disable`, { method: 'POST' }),
  resetSubscription: (id) => request(`/users/${id}/reset-subscription`, { method: 'POST' }),
  resetUserPassword: (id, password) =>
    request(`/users/${id}/reset-password`, { method: 'POST', body: { password: password || '' } }),
  resetDevices: (id) => request(`/users/${id}/reset-devices`, { method: 'POST' }),
  resetTraffic: (id) => request(`/users/${id}/reset-traffic`, { method: 'POST' }),

  groups: () => request('/groups'),
  createGroup: (data) => request('/groups', { method: 'POST', body: data }),
  updateGroup: (id, data) => request(`/groups/${id}`, { method: 'PUT', body: data }),
  deleteGroup: (id) => request(`/groups/${id}`, { method: 'DELETE' }),

  nodes: () => request('/nodes'),
  node: (id) => request(`/nodes/${id}`),
  createNode: (data) => request('/nodes', { method: 'POST', body: data }),
  reorderNodes: (ids) => request('/nodes/reorder', { method: 'POST', body: { ids } }),
  updateNode: (id, data) => request(`/nodes/${id}`, { method: 'PUT', body: data }),
  deleteNode: (id) => request(`/nodes/${id}`, { method: 'DELETE' }),
  resetNodeToken: (id) => request(`/nodes/${id}/token`, { method: 'POST' }),
  syncNode: (id) => request(`/nodes/${id}/sync`, { method: 'POST' }),
  nodeCommand: (id, action) => request(`/nodes/${id}/command`, { method: 'POST', body: { action } }),
  nodeSSHInstall: (id) => request(`/nodes/${id}/ssh/install`, { method: 'POST' }),
  nodeConfig: (id) => request(`/nodes/${id}/config`),
  nodeInstall: (id) => request(`/nodes/${id}/install`),
  nodeCert: (id) => request(`/nodes/${id}/cert`),
  requestNodeCert: (id) => request(`/nodes/${id}/cert/request`, { method: 'POST' }),
  uploadNodeCert: (id, data) => request(`/nodes/${id}/cert/upload`, { method: 'POST', body: data }),
  confirmNodeCert: (id) => request(`/nodes/${id}/cert/confirm`, { method: 'POST' }),
  cancelNodeCert: (id) => request(`/nodes/${id}/cert/cancel`, { method: 'POST' }),
  certSettings: () => request('/cert/settings'),
  updateCertSettings: (data) => request('/cert/settings', { method: 'PUT', body: data }),

  logs: (query) => request('/logs', { query }),
  logSummary: (query) => request('/logs/summary', { query }),
  logOptions: () => request('/logs/options'),
  clearLogs: (before) => request('/logs', { method: 'DELETE', query: { before } }),
  async downloadLogs(query) {
    const params = new URLSearchParams()
    Object.entries(query || {}).forEach(([k, v]) => {
      if (v !== undefined && v !== null && v !== '') params.set(k, v)
    })
    const res = await fetch(`${BASE}/logs/export?${params.toString()}`, {
      headers: adminHeaders({ Authorization: 'Bearer ' + token() }),
    })
    if (res.status === 401 && unauthorizedHandler) unauthorizedHandler()
    if (!res.ok) {
      const text = await res.text().catch(() => '')
      let msg = text
      try { msg = JSON.parse(text).error || text } catch {}
      throw new Error(msg || `Download failed (${res.status})`)
    }
    const blob = await res.blob()
    const cd = res.headers.get('Content-Disposition') || ''
    const m = /filename="?([^"]+)"?/.exec(cd)
    return {
      blob,
      filename: m ? m[1] : '',
      truncated: res.headers.get('X-Log-Export-Truncated') === 'true',
    }
  },

  settings: () => request('/settings'),
  updateSettings: (data) => request('/settings', { method: 'PUT', body: data }),

  syncAll: () => request('/sync', { method: 'POST' }),
  backups: () => request('/backups'),
  createBackup: () => request('/backups', { method: 'POST' }),
  deleteBackup: (name) => request(`/backups/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  async downloadBackup(name) {
    const res = await fetch(`${BASE}/backups/${encodeURIComponent(name)}`, {
      headers: adminHeaders({ Authorization: 'Bearer ' + token() }),
    })
    if (!res.ok) throw new Error('Download failed (' + res.status + ')')
    return res.blob()
  },
  async restoreBackup(file) {
    const fd = new FormData()
    fd.append('file', file)
    const res = await fetch(`${BASE}/backups/restore`, {
      method: 'POST',
      headers: adminHeaders({ Authorization: 'Bearer ' + token() }),
      body: fd,
    })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(data.error || 'Restore failed')
    return data
  },
  async uploadHomepage(file) {
    const fd = new FormData()
    fd.append('file', file)
    const res = await fetch(`${BASE}/homepage/upload`, {
      method: 'POST',
      headers: adminHeaders({ Authorization: 'Bearer ' + token() }),
      body: fd,
    })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(data.error || 'Upload failed')
    return data
  },
  homepage: () => request('/homepage'),
  activateHomepage: (version) => request('/homepage/activate', { method: 'POST', body: { version } }),
  setHomepageMode: (mode) => request('/homepage/mode', { method: 'PUT', body: { mode } }),
  deleteHomepageVersion: (version) => request(`/homepage/versions/${encodeURIComponent(version)}`, { method: 'DELETE' }),
  previewHomepage: (version) => request('/homepage/preview', { method: 'POST', body: { version } }),
  resetHomepage: () => request('/homepage', { method: 'DELETE' }),

  apiKeys: () => request('/api-keys'),
  createAPIKey: (data) => request('/api-keys', { method: 'POST', body: data }),
  deleteAPIKey: (id) => request(`/api-keys/${id}`, { method: 'DELETE' }),
  enableAPIKey: (id) => request(`/api-keys/${id}/enable`, { method: 'POST' }),
  disableAPIKey: (id) => request(`/api-keys/${id}/disable`, { method: 'POST' }),
}

// User self-service portal. Uses its own token so an administrator session and
// a portal session can coexist in the same browser.
export const portalApi = {
  config: () => portalRequest('/portal/config'),
  login: (userId, password) =>
    portalRequest('/portal/login', { method: 'POST', body: { userId, password } }),
  logout: () => portalRequest('/portal/logout', { method: 'POST' }),
  me: () => portalRequest('/portal/me'),
  changePassword: (oldPassword, newPassword) =>
    portalRequest('/portal/password', { method: 'POST', body: { oldPassword, newPassword } }),
  changeConnectionPassword: (currentPassword, newPassword) =>
    portalRequest('/portal/connection-password', { method: 'POST', body: { currentPassword, newPassword } }),
  resetSubscription: () => portalRequest('/portal/subscription/reset', { method: 'POST' }),
}
