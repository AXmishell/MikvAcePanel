<template>
  <div ref="root" class="ms-select" :class="[`ms-select--${size}`, { 'is-open': open, 'is-disabled': disabled }]">
    <select
      v-if="useNative"
      :value="modelValue"
      :disabled="disabled"
      @change="onNativeChange"
    >
      <option v-if="placeholder && !hasEmptyOption" value="" disabled>{{ placeholder }}</option>
      <option v-for="o in normalized" :key="String(o.value)" :value="o.value" :disabled="o.disabled">
        {{ o.label }}
      </option>
    </select>

    <button
      v-else
      ref="trigger"
      type="button"
      class="ms-trigger"
      role="combobox"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="listId"
      :aria-activedescendant="open && activeValue !== null ? optionId(activeValue) : undefined"
      :disabled="disabled"
      @click="toggle"
      @keydown="onTriggerKeydown"
    >
      <span class="ms-value" :class="{ 'is-placeholder': !selected }">
        {{ selected ? selected.label : placeholder }}
      </span>
      <ChevronDown :size="14" class="ms-chevron" />
    </button>

    <Teleport to="body">
      <Transition name="ms-pop">
        <div v-if="open" ref="popup" class="ms-popup" :style="popupStyle">
          <div v-if="showSearch" class="ms-search">
            <Search :size="13" />
            <input
              ref="searchInput"
              v-model="query"
              type="text"
              placeholder="搜索…"
              autocomplete="off"
              @keydown="onSearchKeydown"
            />
          </div>
          <div ref="optionsEl" class="ms-options" role="listbox" :id="listId">
            <template v-for="(group, gi) in groupedOptions" :key="group.name || gi">
              <div v-if="group.name" class="ms-group">{{ group.name }}</div>
              <button
                v-for="o in group.items"
                :id="optionId(o.value)"
                :key="String(o.value)"
                type="button"
                class="ms-option"
                :class="{
                  'is-selected': isSelected(o),
                  'is-active': String(o.value) === String(activeValue),
                  'is-disabled': o.disabled,
                }"
                role="option"
                :aria-selected="isSelected(o)"
                :disabled="o.disabled"
                @click="choose(o)"
                @mouseenter="setActive(o)"
              >
                <span class="ms-option-label">{{ o.label }}</span>
                <span v-if="o.description" class="ms-option-desc">{{ o.description }}</span>
                <Check v-if="isSelected(o)" :size="13" class="ms-check" />
              </button>
            </template>
            <div v-if="!filtered.length" class="ms-empty">没有匹配项</div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Check, ChevronDown, Search } from 'lucide-vue-next'

const props = defineProps({
  modelValue: { default: undefined },
  options: { type: Array, default: () => [] },
  placeholder: { type: String, default: '请选择' },
  disabled: { type: Boolean, default: false },
  size: { type: String, default: 'md' },
  searchable: { type: Boolean, default: false },
  native: { type: Boolean, default: false },
})
const emit = defineEmits(['update:modelValue', 'change'])

let selectSeq = 0
const uid = `ms-${++selectSeq}-${Math.random().toString(36).slice(2, 7)}`
const listId = `${uid}-list`

const root = ref(null)
const trigger = ref(null)
const popup = ref(null)
const optionsEl = ref(null)
const searchInput = ref(null)
const open = ref(false)
const query = ref('')
const activeValue = ref(null)
const popupStyle = ref({})
const coarsePointer = ref(false)

const normalized = computed(() =>
  props.options.map((o) => {
    if (typeof o === 'string') return { value: o, label: o, group: '', disabled: false }
    return {
      value: o.value,
      label: o.label ?? String(o.value ?? ''),
      description: o.description || '',
      group: o.group || '',
      disabled: !!o.disabled,
    }
  }),
)

const showSearch = computed(() => props.searchable || normalized.value.length > 10)
const useNative = computed(() => props.native || coarsePointer.value)
const hasEmptyOption = computed(() => normalized.value.some((o) => String(o.value) === ''))

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return normalized.value
  return normalized.value.filter((o) => {
    return String(o.label).toLowerCase().includes(q) ||
      String(o.description).toLowerCase().includes(q) ||
      String(o.value).toLowerCase().includes(q)
  })
})

const groupedOptions = computed(() => {
  const groups = new Map()
  for (const o of filtered.value) {
    const name = o.group || ''
    if (!groups.has(name)) groups.set(name, [])
    groups.get(name).push(o)
  }
  return [...groups.entries()].map(([name, items]) => ({ name, items }))
})

const selected = computed(() => {
  if (props.modelValue === undefined || props.modelValue === null) return null
  return normalized.value.find((o) => String(o.value) === String(props.modelValue)) || null
})

function isSelected(o) {
  return String(o.value) === String(props.modelValue)
}

function optionId(value) {
  return `${uid}-opt-${String(value).replace(/[^a-zA-Z0-9_-]/g, '_')}`
}

function firstEnabled() {
  return filtered.value.find((o) => !o.disabled) || null
}

function setActive(o) {
  if (!o || o.disabled) return
  activeValue.value = o.value
}

async function openMenu() {
  if (props.disabled || open.value) return
  updatePosition()
  open.value = true
  query.value = ''
  activeValue.value = selected.value ? selected.value.value : (firstEnabled()?.value ?? null)
  await nextTick()
  updatePosition()
  if (showSearch.value) searchInput.value?.focus()
  else trigger.value?.focus()
  scrollActiveIntoView()
}

function closeMenu(focusTrigger = true) {
  if (!open.value) return
  open.value = false
  query.value = ''
  if (focusTrigger) nextTick(() => trigger.value?.focus())
}

function toggle() {
  if (open.value) closeMenu()
  else openMenu()
}

function choose(o) {
  if (!o || o.disabled) return
  emit('update:modelValue', o.value)
  emit('change', o.value)
  closeMenu()
}

function chooseActive() {
  const o = filtered.value.find((item) => String(item.value) === String(activeValue.value) && !item.disabled)
  if (o) choose(o)
}

function moveActive(delta) {
  const items = filtered.value.filter((o) => !o.disabled)
  if (!items.length) return
  let idx = items.findIndex((o) => String(o.value) === String(activeValue.value))
  if (idx < 0) idx = delta > 0 ? -1 : 0
  let next = idx + delta
  if (next < 0) next = items.length - 1
  if (next >= items.length) next = 0
  activeValue.value = items[next].value
  nextTick(scrollActiveIntoView)
}

function onTriggerKeydown(e) {
  if (props.disabled) return
  if (!open.value) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      openMenu()
    }
    return
  }
  switch (e.key) {
    case 'ArrowDown': e.preventDefault(); moveActive(1); break
    case 'ArrowUp': e.preventDefault(); moveActive(-1); break
    case 'Enter': e.preventDefault(); chooseActive(); break
    case 'Escape': e.preventDefault(); closeMenu(); break
    case 'Tab': closeMenu(false); break
  }
}

function onSearchKeydown(e) {
  switch (e.key) {
    case 'ArrowDown': e.preventDefault(); moveActive(1); break
    case 'ArrowUp': e.preventDefault(); moveActive(-1); break
    case 'Enter': e.preventDefault(); chooseActive(); break
    case 'Escape': e.preventDefault(); closeMenu(); break
    case 'Tab': closeMenu(false); break
  }
}

function onNativeChange(e) {
  const raw = e.target.value
  const match = normalized.value.find((o) => String(o.value) === String(raw))
  const value = match ? match.value : raw
  emit('update:modelValue', value)
  emit('change', value)
}

function updatePosition() {
  if (!root.value) return
  const rect = root.value.getBoundingClientRect()
  const viewport = window.innerHeight || document.documentElement.clientHeight
  const gap = 5
  const margin = 12
  const below = viewport - rect.bottom - margin
  const above = rect.top - margin
  const openUp = below < 180 && above > below
  const maxHeight = Math.max(160, Math.min(300, openUp ? above : below))
  const width = Math.max(rect.width, 160)
  const viewportWidth = window.innerWidth || document.documentElement.clientWidth
  const left = Math.min(Math.max(8, rect.left), Math.max(8, viewportWidth - width - 8))
  const style = {
    position: 'fixed',
    left: `${left}px`,
    width: `${width}px`,
    maxHeight: `${maxHeight}px`,
  }
  if (openUp) {
    style.bottom = `${viewport - rect.top + gap}px`
    style.top = 'auto'
  } else {
    style.top = `${rect.bottom + gap}px`
    style.bottom = 'auto'
  }
  popupStyle.value = style
}

function scrollActiveIntoView() {
  if (!optionsEl.value) return
  const el = optionsEl.value.querySelector('.ms-option.is-active')
  el?.scrollIntoView({ block: 'nearest' })
}

function onDocumentPointer(e) {
  if (!open.value) return
  if (root.value?.contains(e.target) || popup.value?.contains(e.target)) return
  closeMenu(false)
}

function onViewportChange() {
  if (open.value) updatePosition()
}

watch(() => props.disabled, (disabled) => {
  if (disabled) closeMenu(false)
})
watch(() => props.modelValue, (value) => {
  if (open.value && value === undefined) activeValue.value = firstEnabled()?.value ?? null
})

onMounted(() => {
  coarsePointer.value = !!window.matchMedia?.('(pointer: coarse)').matches
  document.addEventListener('pointerdown', onDocumentPointer, true)
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange, true)
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocumentPointer, true)
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange, true)
})
</script>

<style scoped>
.ms-select { position: relative; width: 100%; min-width: 0; }
.ms-select > select { width: 100%; }

.ms-trigger {
  display: flex; align-items: center; gap: 9px;
  width: 100%; min-height: 37px;
  padding: 9px 11px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--input-bg);
  color: var(--text);
  font-size: 13px;
  text-align: left;
  transition: border-color 0.15s ease, box-shadow 0.15s ease, background 0.15s ease;
}
.ms-trigger:hover:not(:disabled) { border-color: var(--line-strong); }
.ms-trigger:focus-visible { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
.ms-select.is-open .ms-trigger { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
.ms-select.is-disabled .ms-trigger { opacity: 0.5; cursor: not-allowed; }

.ms-value { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ms-value.is-placeholder { color: var(--muted-2); }
.ms-chevron { flex: none; color: var(--muted-2); transition: transform 0.16s ease, color 0.16s ease; }
.ms-select.is-open .ms-chevron { transform: rotate(180deg); color: var(--accent); }

.ms-select--sm .ms-trigger { min-height: 31px; padding: 6px 9px; font-size: 11.5px; border-radius: 7px; }
.ms-select--sm .ms-chevron { width: 13px; height: 13px; }

.ms-popup {
  z-index: 1200;
  display: flex; flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--panel);
  box-shadow: var(--shadow);
}
.ms-options { flex: 1; overflow-y: auto; padding: 4px; min-height: 0; }
.ms-group {
  padding: 7px 9px 4px;
  font-family: var(--mono); font-size: 9px; letter-spacing: 0.12em;
  text-transform: uppercase; color: var(--muted-2);
}
.ms-group:first-child { padding-top: 3px; }

.ms-option {
  display: flex; align-items: center; gap: 8px;
  width: 100%; padding: 7px 9px;
  border: 0; border-radius: 6px;
  background: transparent; color: var(--muted);
  font-size: 12.5px; text-align: left;
  cursor: pointer;
  transition: background 0.12s ease, color 0.12s ease;
}
.ms-option:hover, .ms-option.is-active { background: var(--hover); color: var(--text); }
.ms-option.is-selected { background: var(--accent-soft); color: var(--accent); }
.ms-option.is-disabled { opacity: 0.38; cursor: not-allowed; }
.ms-option-label { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ms-option-desc { max-width: 45%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--muted-2); font-size: 10.5px; }
.ms-option.is-selected .ms-option-desc { color: var(--accent); opacity: 0.75; }
.ms-check { flex: none; color: var(--accent); }

.ms-search {
  display: flex; align-items: center; gap: 7px;
  padding: 8px 10px;
  border-bottom: 1px solid var(--line-soft);
  color: var(--muted-2);
}
.ms-search input {
  flex: 1; min-width: 0;
  padding: 0; border: 0; background: transparent;
  box-shadow: none; font-size: 12px; color: var(--text);
}
.ms-search input:focus { border: 0; box-shadow: none; }
.ms-empty { padding: 18px 10px; text-align: center; color: var(--muted-2); font-size: 11.5px; }

.ms-pop-enter-active, .ms-pop-leave-active { transition: opacity 0.12s ease, transform 0.12s ease; }
.ms-pop-enter-from, .ms-pop-leave-to { opacity: 0; transform: translateY(-4px); }

@media (prefers-reduced-motion: reduce) {
  .ms-trigger, .ms-chevron, .ms-option, .ms-pop-enter-active, .ms-pop-leave-active { transition: none; }
}
</style>
