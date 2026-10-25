import { reactive } from 'vue'

export const auth = reactive({
  token: localStorage.getItem('mikvace_token') || '',
  admin: JSON.parse(localStorage.getItem('mikvace_admin') || 'null'),
  warning: '',
  version: '',
})

export function setSession(token, admin, warning = '', version = '') {
  auth.token = token
  auth.admin = admin
  auth.warning = warning || ''
  if (version) auth.version = version
  localStorage.setItem('mikvace_token', token)
  localStorage.setItem('mikvace_admin', JSON.stringify(admin || null))
}

export function setWarning(w) {
  auth.warning = w || ''
}

export function clearSession() {
  auth.token = ''
  auth.admin = null
  auth.warning = ''
  localStorage.removeItem('mikvace_token')
  localStorage.removeItem('mikvace_admin')
}

// Portal sessions are intentionally separate from administrator sessions.
export const portalAuth = reactive({
  token: localStorage.getItem('mikvace_portal_token') || '',
  user: JSON.parse(localStorage.getItem('mikvace_portal_user') || 'null'),
})

export function setPortalSession(token, user) {
  portalAuth.token = token
  portalAuth.user = user || null
  localStorage.setItem('mikvace_portal_token', token)
  localStorage.setItem('mikvace_portal_user', JSON.stringify(user || null))
}

export function clearPortalSession() {
  portalAuth.token = ''
  portalAuth.user = null
  localStorage.removeItem('mikvace_portal_token')
  localStorage.removeItem('mikvace_portal_user')
}

export const toasts = reactive([])
let toastSeq = 0

export function notify(message, type = 'success', timeout = 3200) {
  const id = ++toastSeq
  toasts.push({ id, message, type })
  setTimeout(() => {
    const i = toasts.findIndex((t) => t.id === id)
    if (i >= 0) toasts.splice(i, 1)
  }, timeout)
}

export function dismissToast(id) {
  const i = toasts.findIndex((t) => t.id === id)
  if (i >= 0) toasts.splice(i, 1)
}
