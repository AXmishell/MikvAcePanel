// Lightweight hover/focus tooltip. Teleports a single shared bubble to <body>
// so tooltips are never clipped by table wrappers or cards with overflow hidden.

const SHOW_DELAY = 320
const OFFSET = 9
const MARGIN = 8

let tip = null
let active = null
let showTimer = 0

function textOf(binding) {
  const value = binding.value
  if (typeof value === 'string') return value.trim()
  if (value && typeof value === 'object') return String(value.text || '').trim()
  return ''
}

function ensureTip() {
  if (tip) return tip
  tip = document.createElement('div')
  tip.className = 'app-tooltip'
  tip.setAttribute('role', 'tooltip')
  tip.setAttribute('aria-hidden', 'true')
  document.body.appendChild(tip)
  // A tooltip anchored to a stale position is worse than no tooltip.
  window.addEventListener('scroll', hideNow, true)
  window.addEventListener('resize', hideNow)
  return tip
}

function place(el) {
  const elTip = ensureTip()
  const rect = el.getBoundingClientRect()
  const vw = window.innerWidth
  const vh = window.innerHeight

  elTip.style.visibility = 'hidden'
  elTip.style.left = '0px'
  elTip.style.top = '0px'
  const w = elTip.offsetWidth
  const h = elTip.offsetHeight

  let placement = 'top'
  let top = rect.top - h - OFFSET
  if (top < MARGIN) {
    placement = 'bottom'
    top = rect.bottom + OFFSET
  }
  top = Math.max(MARGIN, Math.min(top, vh - h - MARGIN))

  let left = rect.left + rect.width / 2 - w / 2
  left = Math.max(MARGIN, Math.min(left, vw - w - MARGIN))

  elTip.dataset.placement = placement
  elTip.style.left = Math.round(left) + 'px'
  elTip.style.top = Math.round(top) + 'px'
  elTip.style.setProperty('--tip-arrow-x', Math.round(rect.left + rect.width / 2 - left) + 'px')
  elTip.style.visibility = ''
}

function showNow(el) {
  const text = el.__tipText
  if (!text) return
  active = el
  const elTip = ensureTip()
  elTip.textContent = text
  elTip.dataset.tone = el.classList.contains('danger') ? 'danger' : ''
  elTip.classList.remove('is-visible')
  place(el)
  // Force a style flush so switching between adjacent buttons re-runs the
  // enter transition instead of jumping.
  void elTip.offsetWidth
  elTip.classList.add('is-visible')
}

function show(el, delay = SHOW_DELAY) {
  window.clearTimeout(showTimer)
  if (active && active !== el) hideNow()
  showTimer = window.setTimeout(() => showNow(el), delay)
}

function hideNow() {
  window.clearTimeout(showTimer)
  active = null
  if (tip) tip.classList.remove('is-visible')
}

function onEnter() {
  show(this.el)
}

function onLeave() {
  if (active === this.el) hideNow()
  else window.clearTimeout(showTimer)
}

function onFocus() {
  show(this.el, 0)
}

function onBlur() {
  if (active === this.el) hideNow()
}

function onPress() {
  hideNow()
}

function onKeydown(e) {
  if (e.key === 'Escape') hideNow()
}

export const tooltip = {
  mounted(el, binding) {
    el.__tipText = textOf(binding)
    const nativeTitle = el.getAttribute('title')
    if (!el.__tipText && nativeTitle) el.__tipText = nativeTitle
    if (nativeTitle) el.removeAttribute('title')
    if (el.__tipText && !el.getAttribute('aria-label')) {
      el.setAttribute('aria-label', el.__tipText)
      el.__tipAutoAria = true
    }

    const handlers = { onEnter, onLeave, onFocus, onBlur, onPress, onKeydown }
    el.__tipHandlers = {}
    for (const [name, fn] of Object.entries(handlers)) {
      const bound = fn.bind({ el })
      el.__tipHandlers[name] = bound
      const event = {
        onEnter: 'mouseenter', onLeave: 'mouseleave', onFocus: 'focus',
        onBlur: 'blur', onPress: 'pointerdown', onKeydown: 'keydown',
      }[name]
      el.addEventListener(event, bound)
    }
  },
  updated(el, binding) {
    el.__tipText = textOf(binding)
    if (el.__tipAutoAria) el.setAttribute('aria-label', el.__tipText)
    if (active === el) {
      if (!el.__tipText) hideNow()
      else showNow(el)
    }
  },
  beforeUnmount(el) {
    window.clearTimeout(showTimer)
    if (active === el) hideNow()
    const handlers = el.__tipHandlers || {}
    const events = {
      onEnter: 'mouseenter', onLeave: 'mouseleave', onFocus: 'focus',
      onBlur: 'blur', onPress: 'pointerdown', onKeydown: 'keydown',
    }
    for (const [name, event] of Object.entries(events)) {
      if (handlers[name]) el.removeEventListener(event, handlers[name])
    }
    delete el.__tipHandlers
  },
}
