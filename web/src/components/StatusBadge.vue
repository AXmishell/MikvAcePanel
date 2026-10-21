<template>
  <span class="badge" :class="statusClass(status)">
    <span class="dot" :class="dotClass(status)"></span>{{ statusLabel(status) }}
  </span>
</template>

<script setup>
import { statusClass, statusLabel } from '../format'
defineProps({ status: String })
function dotClass(s) {
  if (s === 'online') return 'online'
  if (s === 'error') return 'error'
  return 'offline'
}
</script>
