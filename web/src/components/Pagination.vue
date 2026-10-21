<template>
  <div v-if="total > 0" class="pagination">
    <span class="dim">{{ from }}–{{ to }} of {{ total }}</span>
    <button class="page-btn" :disabled="page <= 1" @click="$emit('change', page - 1)">‹</button>
    <button
      v-for="p in pages"
      :key="p"
      class="page-btn"
      :class="{ active: p === page }"
      @click="$emit('change', p)"
    >{{ p }}</button>
    <button class="page-btn" :disabled="page >= lastPage" @click="$emit('change', page + 1)">›</button>
  </div>
</template>

<script setup>
import { computed } from 'vue'
const props = defineProps({
  page: { type: Number, default: 1 },
  pageSize: { type: Number, default: 20 },
  total: { type: Number, default: 0 },
})
defineEmits(['change'])
const lastPage = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
const from = computed(() => (props.total === 0 ? 0 : (props.page - 1) * props.pageSize + 1))
const to = computed(() => Math.min(props.total, props.page * props.pageSize))
const pages = computed(() => {
  const out = []
  const last = lastPage.value
  let start = Math.max(1, props.page - 2)
  let end = Math.min(last, start + 4)
  start = Math.max(1, end - 4)
  for (let i = start; i <= end; i++) out.push(i)
  return out
})
</script>
