// Build-time API origin. Keep the default relative path for single-origin
// deployments; set VITE_API_BASE_URL=https://api.example.com/api for CDN setups.
export const API_BASE = (import.meta.env.VITE_API_BASE_URL || '/api').replace(/\/+$/, '')
const BASE = API_BASE

let unauthorizedHandler = null
export function setUnauthorizedHandler(fn) {
  unauthorizedHandler = fn
}

function token() {
  return localStorage.getItem('mikvace_token') || ''
}

async function request(path, options = {}) {
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
  const t = token()
  if (t) headers.Authorization = 'Bearer ' + t
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
  if (res.status === 401 && token() && unauthorizedHandler) {
    unauthorizedHandler()
  }
  if (!res.ok) {
    const err = new Error((data && data.error) || `Request failed (${res.status})`)
    err.status = res.status
    err.data = data
    throw err
  }
  return data
}

export const api = {
  login: (username, password) => request('/login', { method: 'POST', body: { username, password } }),
  login2FA: (challenge, code) => request('/login/2fa', { method: 'POST', body: { challenge, code } }),
  twoFactorStatus: () => request('/2fa'),
  setupTwoFactor: () => request('/2fa/setup', { method: 'POST' }),
  enableTwoFactor: (code) => request('/2fa/enable', { method: 'POST', body: { code } }),
  disableTwoFactor: (password, code) => request('/2fa/disable', { method: 'POST', body: { password, code } }),
  regenerateRecovery: (password, code) => request('/2fa/recovery', { method: 'POST', body: { password, code } }),
  logout: () => request('/logout', { method: 'POST' }),
  me: () => request('/me'),
  changePassword: (oldPassword, newPassword) =>
    request('/change-password', { method: 'POST', body: { oldPassword, newPassword } }),

  dashboard: () => request('/dashboard'),
  events: (query) => request('/events', { query }),

  users: (query) => request('/users', { query }),
  user: (id) => request(`/users/${id}`),
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

  settings: () => request('/settings'),
  updateSettings: (data) => request('/settings', { method: 'PUT', body: data }),

  syncAll: () => request('/sync', { method: 'POST' }),
  backups: () => request('/backups'),
  createBackup: () => request('/backups', { method: 'POST' }),
  deleteBackup: (name) => request(`/backups/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  async downloadBackup(name) {
    const res = await fetch(`${BASE}/backups/${encodeURIComponent(name)}`, {
      headers: { Authorization: 'Bearer ' + token() },
    })
    if (!res.ok) throw new Error('Download failed (' + res.status + ')')
    return res.blob()
  },
  async restoreBackup(file) {
    const fd = new FormData()
    fd.append('file', file)
    const res = await fetch(`${BASE}/backups/restore`, {
      method: 'POST',
      headers: { Authorization: 'Bearer ' + token() },
      body: fd,
    })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(data.error || 'Restore failed')
    return data
  },

  apiKeys: () => request('/api-keys'),
  createAPIKey: (data) => request('/api-keys', { method: 'POST', body: data }),
  deleteAPIKey: (id) => request(`/api-keys/${id}`, { method: 'DELETE' }),
  enableAPIKey: (id) => request(`/api-keys/${id}/enable`, { method: 'POST' }),
  disableAPIKey: (id) => request(`/api-keys/${id}/disable`, { method: 'POST' }),
}
