<template>
  <div class="stat-card">
    <div class="stat-top">
      <span v-if="icon" class="stat-icon"><component :is="icon" :size="15" /></span>
      <span>{{ label }}</span>
    </div>
    <div class="stat-value" :style="tone ? { color: tone } : null">{{ value }}</div>
    <div v-if="foot" class="stat-foot">{{ foot }}</div>
  </div>
</template>

<script setup>
defineProps({
  label: String,
  value: [String, Number],
  foot: String,
  icon: Object,
  tone: String,
})
</script>
