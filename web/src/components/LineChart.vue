<template>
  <svg class="chart" :viewBox="`0 0 ${width} ${height}`" preserveAspectRatio="xMidYMid meet" role="img">
    <g>
      <line v-for="(g, i) in gridLines" :key="i" :x1="pad.l" :x2="width - pad.r" :y1="g.y" :y2="g.y" class="grid-line" />
      <text v-for="(g, i) in gridLines" :key="'t' + i" :x="pad.l - 7" :y="g.y + 3" text-anchor="end">{{ formatY(g.value) }}</text>
    </g>
    <g v-for="s in series" :key="s.key">
      <path v-if="s.fill" :d="areaPath(s)" :fill="s.color" opacity="0.1" />
      <path :d="linePath(s)" fill="none" :stroke="s.color" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
      <circle v-for="(p, i) in pointsFor(s)" :key="i" :cx="p.x" :cy="p.y" r="1.2" :fill="s.color" opacity="0.8" />
    </g>
    <g>
      <text v-for="(l, i) in xLabels" :key="'x' + i" :x="l.x" :y="height - 5" :text-anchor="l.anchor">{{ l.label }}</text>
    </g>
  </svg>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  data: { type: Array, default: () => [] },
  series: { type: Array, default: () => [] },
  height: { type: Number, default: 180 },
  formatY: { type: Function, default: (v) => String(Math.round(v)) },
  formatTime: { type: Function, default: (t) => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) },
})
const width = 640
const pad = { l: 44, r: 10, t: 12, b: 22 }

const maxValue = computed(() => {
  let max = 0
  for (const s of props.series) {
    for (const row of props.data) {
      const v = Number(row[s.key] || 0)
      if (v > max) max = v
    }
  }
  if (max <= 0) max = 1
  const pow = Math.pow(10, Math.floor(Math.log10(max)))
  const step = pow / 2
  return Math.ceil(max / step) * step || 1
})
const gridLines = computed(() => {
  const out = []
  for (let i = 0; i <= 4; i++) {
    out.push({ y: pad.t + ((props.height - pad.t - pad.b) * i) / 4, value: (maxValue.value * (4 - i)) / 4 })
  }
  return out
})
function xFor(i) {
  const n = Math.max(1, props.data.length - 1)
  return pad.l + ((width - pad.l - pad.r) * i) / n
}
function yFor(v) {
  const h = props.height - pad.t - pad.b
  return pad.t + h - (Math.min(v, maxValue.value) / maxValue.value) * h
}
function pointsFor(s) {
  return props.data.map((row, i) => ({ x: xFor(i), y: yFor(Number(row[s.key] || 0)) }))
}
function linePath(s) {
  const pts = pointsFor(s)
  if (!pts.length) return ''
  return pts.map((p, i) => (i === 0 ? `M${p.x},${p.y}` : `L${p.x},${p.y}`)).join(' ')
}
function areaPath(s) {
  const pts = pointsFor(s)
  if (!pts.length) return ''
  const base = yFor(0)
  return `M${pts[0].x},${base} ` + pts.map((p) => `L${p.x},${p.y}`).join(' ') + ` L${pts[pts.length - 1].x},${base} Z`
}
const xLabels = computed(() => {
  if (!props.data.length) return []
  const idx = [0, Math.floor((props.data.length - 1) / 2), props.data.length - 1]
  return [...new Set(idx)].map((i, k) => ({
    x: xFor(i),
    label: props.formatTime(props.data[i].time),
    anchor: k === 0 ? 'start' : k === 2 ? 'end' : 'middle',
  }))
})
</script>
