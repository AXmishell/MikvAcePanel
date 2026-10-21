<template>
  <div>
    <div class="toolbar">
      <div>
        <div class="page-title">服务器组</div>
        <div class="dim small">用户会获得所在分组内的全部节点，以及未分组（公开）节点。</div>
      </div>
      <div class="spacer"></div>
      <button class="btn btn-primary btn-sm" @click="openCreate"><Plus :size="14" /> 新建分组</button>
    </div>

    <div v-if="error" class="banner banner-danger">{{ error }}</div>

    <div class="group-grid">
      <article v-for="g in groups" :key="g.id" class="card group-card">
        <div class="group-head">
          <span class="group-color" :style="{ background: g.color }"></span>
          <div style="min-width: 0">
            <div class="group-name">{{ g.name }}</div>
            <div class="dim small">{{ g.subscriptionTitle || g.name }}</div>
          </div>
          <div class="spacer"></div>
          <button class="icon-btn" title="编辑" @click="openEdit(g)"><Pencil :size="15" /></button>
          <button class="icon-btn danger" title="删除" @click="askDelete(g)"><Trash2 :size="15" /></button>
        </div>
        <p class="group-desc">{{ g.description || '暂无描述' }}</p>
        <div class="group-stats">
          <div><span class="k">节点</span><span class="v">{{ g.nodeCount }}</span></div>
          <div><span class="k">用户</span><span class="v">{{ g.userCount }}</span></div>
          <div><span class="k">设备</span><span class="v">{{ g.maxDevices > 0 ? g.maxDevices : '∞' }}</span></div>
        </div>
      </article>
      <div v-if="!groups.length" class="card"><div class="empty"><FolderTree :size="30" /><div>暂无分组。创建分组以便为不同客户群体组织节点。</div></div></div>
    </div>

    <Modal v-model="modalOpen" :title="editing ? '编辑 group' : '新建分组'" size="sm">
      <div v-if="formError" class="banner banner-danger">{{ formError }}</div>
      <div class="form-grid">
        <div class="field full"><label>名称</label><input v-model="form.name" type="text" placeholder="Europe" /></div>
        <div class="field full"><label>描述</label><input v-model="form.description" type="text" placeholder="欧洲优质节点" /></div>
        <div class="field"><label>颜色</label><input v-model="form.color" type="color" style="height: 38px; padding: 3px" /></div>
        <div class="field"><label>最大设备数</label><input v-model.number="form.maxDevices" type="number" min="0" placeholder="0 = 不限制" /></div>
        <div class="field full"><label>订阅标题</label><input v-model="form.subscriptionTitle" type="text" placeholder="显示在客户端配置中" /></div>
      </div>
      <template #footer>
        <button class="btn" @click="modalOpen = false">取消</button>
        <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存分组' }}</button>
      </template>
    </Modal>

    <Modal v-model="deleteOpen" title="删除分组" size="sm">
      <p>删除 <strong>{{ deleteTarget?.name }}</strong>? 用户与节点不会被删除，只会移除该分组成员关系。</p>
      <template #footer>
        <button class="btn" @click="deleteOpen = false">取消</button>
        <button class="btn btn-danger" @click="confirmDelete">删除分组</button>
      </template>
    </Modal>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { FolderTree, Pencil, Plus, Trash2 } from 'lucide-vue-next'
import { api } from '../api'
import { notify } from '../store'
import Modal from '../components/Modal.vue'

const groups = ref([])
const error = ref('')
const modalOpen = ref(false)
const editing = ref(null)
const saving = ref(false)
const formError = ref('')
const form = reactive({ name: '', description: '', color: '#38bdf8', subscriptionTitle: '', maxDevices: 0 })
const deleteOpen = ref(false)
const deleteTarget = ref(null)

async function load() {
  try {
    groups.value = (await api.groups()).items
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}
function openCreate() {
  editing.value = null
  Object.assign(form, { name: '', description: '', color: '#38bdf8', subscriptionTitle: '', maxDevices: 0 })
  formError.value = ''
  modalOpen.value = true
}
function openEdit(g) {
  editing.value = g
  Object.assign(form, {
    name: g.name, description: g.description, color: g.color || '#38bdf8',
    subscriptionTitle: g.subscriptionTitle || '', maxDevices: g.maxDevices || 0,
  })
  formError.value = ''
  modalOpen.value = true
}
async function save() {
  formError.value = ''
  saving.value = true
  try {
    if (editing.value) await api.updateGroup(editing.value.id, { ...form })
    else await api.createGroup({ ...form })
    modalOpen.value = false
    notify(editing.value ? 'Group updated' : 'Group created')
    load()
  } catch (e) {
    formError.value = e.message
  } finally {
    saving.value = false
  }
}
function askDelete(g) {
  deleteTarget.value = g
  deleteOpen.value = true
}
async function confirmDelete() {
  try {
    await api.deleteGroup(deleteTarget.value.id)
    deleteOpen.value = false
    notify('Group deleted')
    load()
  } catch (e) {
    notify(e.message, 'error')
  }
}
onMounted(load)
</script>

<style scoped>
.group-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 16px; }
.group-card { padding: 17px; }
.group-head { display: flex; align-items: center; gap: 11px; }
.group-color { width: 12px; height: 34px; border-radius: 6px; flex: none; }
.group-name { font-weight: 700; font-size: 14px; }
.group-desc { color: var(--muted); font-size: 12px; min-height: 36px; margin: 12px 0; }
.group-stats { display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px; border-top: 1px solid var(--line-soft); padding-top: 12px; }
.group-stats .k { display: block; font-size: 9.5px; text-transform: uppercase; letter-spacing: 0.8px; color: var(--muted-2); }
.group-stats .v { font-size: 15px; font-weight: 700; }
</style>
