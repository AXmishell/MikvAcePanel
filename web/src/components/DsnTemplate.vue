<template>
  <div v-if="templates.length" class="dsn-templates">
    <div class="dsn-template-head">
      <span>连接模板</span>
      <span>点击“填入”后替换大写占位符</span>
    </div>
    <div v-for="tpl in templates" :key="tpl.id" class="dsn-template-row">
      <div class="dsn-template-meta">
        <strong>{{ tpl.label }}</strong>
        <span>{{ tpl.description }}</span>
      </div>
      <code class="dsn-template-value">{{ tpl.value }}</code>
      <button type="button" class="btn btn-sm" @click="$emit('apply', tpl.value)">填入</button>
    </div>
  </div>
</template>

<script setup>
defineProps({
  templates: { type: Array, default: () => [] },
})
defineEmits(['apply'])
</script>

<style scoped>
.dsn-templates {
  margin-top: 8px;
  padding: 10px 12px;
  border: 1px dashed var(--line-strong);
  background: var(--panel-2);
}
.dsn-template-head {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 6px;
  margin-bottom: 6px;
  font-size: 10.5px;
  color: var(--muted-2);
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.dsn-template-row {
  display: grid;
  grid-template-columns: minmax(118px, 0.7fr) minmax(0, 2.4fr) auto;
  gap: 10px;
  align-items: center;
  padding: 7px 0;
  border-top: 1px solid var(--line);
}
.dsn-template-row:first-of-type { border-top: 0; }
.dsn-template-meta { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.dsn-template-meta strong { font-size: 11.5px; font-weight: 600; color: var(--text); }
.dsn-template-meta span { font-size: 10px; line-height: 1.4; color: var(--muted-2); }
.dsn-template-value {
  min-width: 0;
  font-family: var(--mono);
  font-size: 10.5px;
  line-height: 1.5;
  color: var(--muted);
  overflow-wrap: anywhere;
  word-break: break-word;
}
.dsn-template-row .btn { flex: none; }
@media (max-width: 640px) {
  .dsn-template-row { grid-template-columns: 1fr auto; }
  .dsn-template-meta { grid-column: 1 / -1; }
}
</style>
