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

// confirmDialog backs a single app-wide ConfirmDialog so views can replace the
// native window.confirm() popup with an in-page div dialog.
export const confirmDialog = reactive({
  open: false,
  title: '确认操作',
  message: '',
  eyebrow: '',
  confirmText: '确定',
  cancelText: '取消',
  tone: 'accent',
})
let confirmResolve = null

export function askConfirm(message, options = {}) {
  // A second confirm replaces (and declines) one that is still open.
  if (confirmResolve) {
    confirmResolve(false)
    confirmResolve = null
  }
  Object.assign(confirmDialog, {
    open: true,
    title: options.title || '确认操作',
    message,
    eyebrow: options.eyebrow || '',
    confirmText: options.confirmText || '确定',
    cancelText: options.cancelText || '取消',
    tone: options.tone || 'accent',
  })
  return new Promise((resolve) => {
    confirmResolve = resolve
  })
}

export function resolveConfirm(value) {
  confirmDialog.open = false
  const resolve = confirmResolve
  confirmResolve = null
  if (resolve) resolve(Boolean(value))
}
