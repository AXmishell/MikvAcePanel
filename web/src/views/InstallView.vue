<template>
  <div class="login-shell">
    <aside class="login-brand">
      <div class="login-brand-head">
        <div class="brand-logo">M</div>
        <div class="brand-word">MikvAce<span>Panel</span></div>
      </div>

      <div class="login-brand-copy">
        <div class="eyebrow">First-run setup</div>
        <h1>几分钟内<br>完成<span>面板初始化</span>。</h1>
        <p>创建第一个管理员账号，之后节点、用户、订阅与证书都在这一个控制面中管理。</p>
      </div>

      <ul class="login-points">
        <li>
          <span class="num">01</span>
          <div>
            <strong>管理员账号</strong>
            <small>密码使用 bcrypt 存储，登录后可继续开启两步验证。</small>
          </div>
        </li>
        <li>
          <span class="num">02</span>
          <div>
            <strong>公共访问地址</strong>
            <small>用于生成订阅链接和节点 Agent 安装命令，可随时修改。</small>
          </div>
        </li>
        <li>
          <span class="num">03</span>
          <div>
            <strong>数据后端</strong>
            <small>当前使用 {{ status.database || 'sqlite' }}，初始化完成后即可开始添加节点。</small>
          </div>
        </li>
      </ul>

      <div class="login-brand-foot">
        <span class="dot online"></span>
        <span>{{ status.database || 'sqlite' }} · ready</span>
      </div>
    </aside>

    <main class="login-panel">
      <div class="login-card">
        <div class="login-brand-head login-mobile-head">
          <div class="brand-logo">M</div>
          <div class="brand-word">MikvAce<span>Panel</span></div>
        </div>

        <div class="login-head">
          <div class="eyebrow">Install</div>
          <h2>初始化面板</h2>
          <p>设置管理员账号与公共访问地址，完成后将自动登录。面板名称、访问地址和管理员用户名会写入 panel.yaml。</p>
        </div>

        <div v-if="loadingStatus" class="hint" style="padding: 8px 0">正在检查安装状态…</div>
        <div v-else-if="!status.configWritable" class="banner banner-danger" style="margin-bottom: 12px">
          配置文件不可写：<span class="mono">{{ status.configPath }}</span>。请把 panel.yaml 以可写方式挂载后重启面板。
        </div>

        <form v-else @submit.prevent="submit">
          <div class="field">
            <label>面板名称</label>
            <input v-model="form.panelName" type="text" maxlength="64" placeholder="MikvAce Panel" />
          </div>
          <div class="field">
            <label>公共访问地址</label>
            <input v-model="form.publicUrl" type="url" placeholder="https://panel.example.com" />
            <span class="hint">用于订阅链接和节点连接；留空则使用当前访问地址。</span>
          </div>
          <div class="field">
            <label>管理员用户名</label>
            <input v-model="form.username" type="text" autocomplete="username" placeholder="admin" autofocus />
            <span class="hint">3–32 位，支持字母、数字、点、下划线和短横线。</span>
          </div>
          <div class="field">
            <label>管理员密码</label>
            <input v-model="form.password" type="password" autocomplete="new-password" placeholder="••••••••••••" />
            <span class="hint">至少 12 个字符。</span>
          </div>
          <div class="field">
            <label>确认密码</label>
            <input v-model="confirmPassword" type="password" autocomplete="new-password" placeholder="••••••••••••" />
          </div>
          <div v-if="error" class="banner banner-danger" style="margin: 4px 0 12px">{{ error }}</div>
          <button class="btn btn-primary" :disabled="submitting" type="submit">
            <Loader2 v-if="submitting" :size="15" class="spin" />
            <ShieldCheck v-else :size="15" />
            {{ submitting ? '初始化中…' : '完成安装' }}
          </button>
        </form>

        <div class="login-foot">{{ status.panelName || 'MikvAce Panel' }} · 首次运行</div>
      </div>
    </main>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Loader2, ShieldCheck } from 'lucide-vue-next'
import { api } from '../api'
import { notify, setSession } from '../store'

const router = useRouter()
const loadingStatus = ref(true)
const submitting = ref(false)
const error = ref('')
const confirmPassword = ref('')
const status = reactive({ panelName: '', publicUrl: '', database: '', installed: false })
const form = reactive({ panelName: '', publicUrl: '', username: 'admin', password: '' })

onMounted(async () => {
  try {
    const s = await api.installStatus()
    Object.assign(status, s)
    form.panelName = s.panelName || 'MikvAce Panel'
    const saved = String(s.publicUrl || '').trim()
    // A fresh compose deployment defaults to localhost; prefer the address the
    // operator is actually using, while keeping an explicitly configured URL.
    form.publicUrl = !saved || /localhost|127\.0\.0\.1/.test(saved) ? window.location.origin : saved
  } catch (e) {
    error.value = e.message
  } finally {
    loadingStatus.value = false
  }
})

async function submit() {
  error.value = ''
  if (form.password.length < 12) {
    error.value = '管理员密码至少需要 12 个字符。'
    return
  }
  if (form.password !== confirmPassword.value) {
    error.value = '两次输入的密码不一致。'
    return
  }
  submitting.value = true
  try {
    const res = await api.install({
      username: form.username.trim(),
      password: form.password,
      panelName: form.panelName.trim(),
      publicUrl: form.publicUrl.trim(),
    })
    setSession(res.token, res.admin, '')
    notify('面板初始化完成')
    router.replace('/admin/dashboard')
  } catch (e) {
    error.value = e.status === 409 ? '面板已经完成初始化，请直接登录。' : e.message
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
