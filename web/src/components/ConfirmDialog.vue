<template>
  <Teleport to="body">
    <Transition name="confirm">
      <div
        v-if="modelValue"
        class="confirm-backdrop"
        @mousedown.self="onBackdrop"
      >
        <div
          ref="card"
          class="confirm-card"
          :class="'tone-' + tone"
          role="dialog"
          aria-modal="true"
          :aria-label="title"
          tabindex="-1"
        >
          <div class="confirm-head">
            <div class="confirm-icon">
              <component :is="iconComponent" :size="21" />
            </div>
            <div class="confirm-heading">
              <div v-if="eyebrow" class="confirm-eyebrow">{{ eyebrow }}</div>
              <h2 class="confirm-title">{{ title }}</h2>
            </div>
            <button class="icon-btn confirm-close" type="button" aria-label="关闭" :disabled="busy" @click="close">
              <X :size="16" />
            </button>
          </div>

          <div class="confirm-body">
            <p class="confirm-message">{{ message }}</p>
            <slot />

            <div v-if="busy" class="confirm-busy" role="status" aria-live="polite">
              <span class="confirm-spinner" aria-hidden="true"></span>
              <span>{{ busyText || '正在处理…' }}</span>
            </div>
          </div>

          <div class="confirm-actions">
            <button ref="cancelBtn" class="btn" type="button" :disabled="busy" @click="close">
              {{ cancelText }}
            </button>
            <button
              class="btn"
              :class="tone === 'danger' ? 'btn-danger' : 'btn-primary'"
              type="button"
              :disabled="busy"
              @click="confirm"
            >
              <span v-if="busy" class="confirm-spinner confirm-spinner--button" aria-hidden="true"></span>
              <component :is="confirmIcon" v-else-if="confirmIcon" :size="14" />
              {{ confirmText }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { Check, ShieldAlert, X } from 'lucide-vue-next'

const props = defineProps({
  modelValue: Boolean,
  title: { type: String, default: '确认操作' },
  message: { type: String, default: '' },
  eyebrow: { type: String, default: '' },
  confirmText: { type: String, default: '确定' },
  cancelText: { type: String, default: '取消' },
  busyText: { type: String, default: '正在处理…' },
  tone: { type: String, default: 'accent' }, // accent | danger | warn
  icon: { type: [Object, Function], default: null, skipFactory: true },
  // The default is a render function, not a factory. Without skipFactory Vue
  // would invoke the Lucide component during prop resolution and crash because
  // it is called without its component context (slots/attrs).
  confirmIcon: { type: [Object, Function], default: Check, skipFactory: true },
  closeOnBackdrop: { type: Boolean, default: true },
  busy: Boolean,
})
const emit = defineEmits(['update:modelValue', 'confirm', 'cancel'])

const card = ref(null)
const cancelBtn = ref(null)
const iconComponent = computed(() => props.icon || ShieldAlert)

let previousFocus = null
let previousOverflow = ''
let scrollLocked = false

function close() {
  if (props.busy) return
  emit('update:modelValue', false)
  emit('cancel')
}
function confirm() {
  if (props.busy) return
  emit('confirm')
}
function onBackdrop() {
  if (props.closeOnBackdrop) close()
}
function onKeydown(e) {
  if (!props.modelValue) return
  if (e.key === 'Escape') {
    e.preventDefault()
    close()
    return
  }
  // Keep focus inside the dialog while it is open.
  if (e.key === 'Tab' && card.value) {
    const focusable = card.value.querySelectorAll(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    )
    if (!focusable.length) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }
}

function lockScroll() {
  if (scrollLocked) return
  scrollLocked = true
  previousFocus = document.activeElement
  previousOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  nextTick(() => cancelBtn.value?.focus())
}
function unlockScroll() {
  if (!scrollLocked) return
  scrollLocked = false
  document.body.style.overflow = previousOverflow
  if (previousFocus && typeof previousFocus.focus === 'function') previousFocus.focus()
  previousFocus = null
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      lockScroll()
      window.addEventListener('keydown', onKeydown)
    } else {
      window.removeEventListener('keydown', onKeydown)
      unlockScroll()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  unlockScroll()
})
</script>

<style scoped>
.confirm-backdrop {
  position: fixed; inset: 0; z-index: 220;
  display: grid; place-items: center;
  padding: 22px;
  background: var(--scrim);
  backdrop-filter: blur(10px) saturate(0.9);
}

.confirm-card {
  position: relative; overflow: hidden;
  width: min(452px, 100%);
  border: 1px solid var(--line-strong);
  border-radius: 18px;
  background:
    radial-gradient(120% 90% at 50% 0%, var(--accent-soft), transparent 58%),
    var(--panel);
  box-shadow: 0 32px 84px rgba(0, 0, 0, 0.58), inset 0 1px 0 rgba(255, 255, 255, 0.035);
  transform-origin: 50% 20%;
  transition: transform 0.18s ease, opacity 0.18s ease;
}
.confirm-card::before {
  content: ""; position: absolute; inset: 0 0 auto;
  height: 1px;
  background: linear-gradient(90deg, transparent, var(--accent-line), transparent);
}
.confirm-card.tone-danger {
  background:
    radial-gradient(120% 90% at 50% 0%, var(--danger-soft), transparent 58%),
    var(--panel);
}
.confirm-card.tone-danger::before { background: linear-gradient(90deg, transparent, rgba(255, 111, 111, 0.45), transparent); }
.confirm-card.tone-warn {
  background:
    radial-gradient(120% 90% at 50% 0%, var(--warn-soft), transparent 58%),
    var(--panel);
}
.confirm-card.tone-warn::before { background: linear-gradient(90deg, transparent, rgba(230, 182, 76, 0.45), transparent); }

.confirm-head { display: flex; align-items: flex-start; gap: 14px; padding: 22px 20px 0; }
.confirm-icon {
  flex: none;
  width: 43px; height: 43px;
  display: grid; place-items: center;
  border: 1px solid var(--accent-line);
  border-radius: 12px;
  background: var(--accent-soft);
  color: var(--accent);
  box-shadow: 0 10px 28px -16px var(--accent);
}
.tone-danger .confirm-icon {
  border-color: rgba(255, 111, 111, 0.34);
  background: var(--danger-soft);
  color: var(--danger);
  box-shadow: 0 10px 28px -16px var(--danger);
}
.tone-warn .confirm-icon {
  border-color: rgba(230, 182, 76, 0.34);
  background: var(--warn-soft);
  color: var(--warn);
  box-shadow: 0 10px 28px -16px var(--warn);
}
.confirm-heading { flex: 1; min-width: 0; padding-top: 1px; }
.confirm-eyebrow {
  font-family: var(--mono); font-size: 9px; letter-spacing: 0.18em;
  text-transform: uppercase; color: var(--muted-2);
}
.confirm-title {
  margin: 4px 0 0;
  font-size: 17px; line-height: 1.25; font-weight: 650;
  letter-spacing: -0.025em; color: var(--text);
}
.confirm-close { margin-left: auto; flex: none; }

.confirm-body { padding: 13px 20px 20px; }
.confirm-message {
  margin: 0;
  color: var(--muted);
  font-size: 12.5px;
  line-height: 1.72;
}

.confirm-busy {
  display: flex; align-items: center; gap: 9px;
  margin-top: 15px; padding: 10px 12px;
  border: 1px solid var(--accent-line);
  border-radius: 10px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 11.5px;
}
.tone-danger .confirm-busy {
  border-color: rgba(255, 111, 111, 0.3);
  background: var(--danger-soft);
  color: var(--danger);
}
.tone-warn .confirm-busy {
  border-color: rgba(230, 182, 76, 0.3);
  background: var(--warn-soft);
  color: var(--warn);
}

.confirm-spinner {
  flex: none;
  width: 15px; height: 15px;
  border: 2px solid currentColor;
  border-right-color: transparent;
  border-radius: 50%;
  opacity: 0.9;
  animation: confirmSpin 0.75s linear infinite;
}
.confirm-spinner--button { width: 13px; height: 13px; border-width: 1.5px; }
@keyframes confirmSpin { to { transform: rotate(360deg); } }

.confirm-actions {
  display: flex; justify-content: flex-end; gap: 8px;
  padding: 14px 18px;
  border-top: 1px solid var(--line-soft);
  background: rgba(255, 255, 255, 0.014);
}

.confirm-enter-active, .confirm-leave-active { transition: opacity 0.18s ease; }
.confirm-enter-from, .confirm-leave-to { opacity: 0; }
.confirm-enter-from .confirm-card { opacity: 0; transform: translateY(10px) scale(0.975); }
.confirm-leave-to .confirm-card { opacity: 0; transform: translateY(4px) scale(0.99); }

@media (max-width: 520px) {
  .confirm-backdrop { padding: 14px; }
  .confirm-head { padding: 18px 16px 0; }
  .confirm-body { padding: 12px 16px 18px; }
  .confirm-actions { padding: 12px 14px; }
  .confirm-actions .btn { flex: 1; justify-content: center; }
}
</style>
