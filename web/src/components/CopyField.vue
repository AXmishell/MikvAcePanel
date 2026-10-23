<template>
  <div class="copy-field" :class="{ 'is-copied': copied, 'is-empty': !value }">
    <span v-if="prefix" class="copy-field-prefix">{{ prefix }}</span>
    <input
      ref="inputEl"
      :value="value"
      readonly
      spellcheck="false"
      :title="value"
      @focus="selectAll"
      @click="selectAll"
    />
    <button type="button" class="copy-field-btn" :disabled="!value" @click="doCopy">
      <Check v-if="copied" :size="14" />
      <Copy v-else :size="14" />
      <span>{{ copied ? '已复制' : '复制' }}</span>
    </button>
  </div>
</template>

<script setup>
import { onBeforeUnmount, ref } from 'vue'
import { Check, Copy } from 'lucide-vue-next'
import { copyText } from '../format'

const props = defineProps({
  value: { type: String, default: '' },
  prefix: { type: String, default: '' },
})

const inputEl = ref(null)
const copied = ref(false)
let timer = null

function selectAll() {
  inputEl.value?.select()
}

async function doCopy() {
  if (!props.value) return
  try {
    await copyText(props.value)
    copied.value = true
    clearTimeout(timer)
    timer = setTimeout(() => { copied.value = false }, 1500)
  } catch {
    // Clipboard failures are rare (insecure context / denied permission).
    // Selecting the value still lets the operator copy it manually.
    selectAll()
  }
}

onBeforeUnmount(() => clearTimeout(timer))
</script>

<style scoped>
.copy-field {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 3px 3px 3px 11px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--input-bg);
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.copy-field:hover { border-color: var(--line-strong); }
.copy-field:focus-within {
  border-color: var(--accent-line);
  box-shadow: 0 0 0 3px var(--accent-soft);
}
.copy-field.is-empty { opacity: 0.6; }

.copy-field-prefix {
  flex: 0 0 auto;
  padding-right: 9px;
  border-right: 1px solid var(--line-soft);
  font-size: 9.5px;
  font-weight: 650;
  letter-spacing: 0.09em;
  text-transform: uppercase;
  color: var(--muted-2);
}

.copy-field input {
  flex: 1 1 auto;
  min-width: 0;
  width: auto;
  padding: 6px 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  color: var(--text);
  font-family: var(--mono);
  font-size: 11.5px;
  line-height: 1.4;
  text-overflow: ellipsis;
  outline: none;
}
.copy-field input:focus,
.copy-field input:focus-visible {
  border: 0;
  box-shadow: none;
  outline: none;
}

.copy-field-btn {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 28px;
  padding: 0 9px;
  border: 1px solid transparent;
  border-radius: 7px;
  background: var(--panel-3);
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1;
  cursor: pointer;
  transition: color 0.15s ease, background 0.15s ease, border-color 0.15s ease;
}
.copy-field-btn:hover:not(:disabled) {
  color: var(--text);
  border-color: var(--line-strong);
}
.copy-field-btn:disabled { cursor: default; opacity: 0.55; }
.copy-field.is-copied .copy-field-btn {
  color: var(--ok);
  border-color: rgba(134, 201, 155, 0.35);
  background: var(--ok-soft);
}

@media (max-width: 560px) {
  .copy-field-btn span { display: none; }
  .copy-field-btn { width: 30px; justify-content: center; padding: 0; }
}
</style>
