<template>
  <div class="login-shell">
    <aside class="login-brand">
      <div class="login-brand-head">
        <div class="brand-logo">M</div>
        <div class="brand-word">MikvAce<span>Panel</span></div>
      </div>

      <div class="login-brand-copy">
        <div class="eyebrow">Self-hosted proxy control plane</div>
        <h1>把代理网络<br>当作<span>基础设施</span>运营。</h1>
        <p>节点、用户、订阅、证书与日志分析，集中在一个轻量控制面中。</p>
      </div>

      <ul class="login-points">
        <li>
          <span class="num">01</span>
          <div>
            <strong>多协议节点</strong>
            <small>Hysteria 2 与 Xray VLESS 统一编排、配置下发。</small>
          </div>
        </li>
        <li>
          <span class="num">02</span>
          <div>
            <strong>可观测性内置</strong>
            <small>心跳、流量、在线设备与代理日志分析开箱即用。</small>
          </div>
        </li>
        <li>
          <span class="num">03</span>
          <div>
            <strong>证书自动化</strong>
            <small>面板签发、ACME 续期与 Agent 安全下发。</small>
          </div>
        </li>
      </ul>

      <div class="login-brand-foot">
        <span class="dot online"></span>
        <span>Panel online</span>
      </div>
    </aside>

    <main class="login-panel">
      <div class="login-card">
        <div class="login-brand-head login-mobile-head">
          <div class="brand-logo">M</div>
          <div class="brand-word">MikvAce<span>Panel</span></div>
        </div>

        <div class="login-head">
          <div class="eyebrow">{{ need2FA ? 'Second factor' : 'Admin access' }}</div>
          <h2>{{ need2FA ? '两步验证' : '登录控制台' }}</h2>
          <p>{{ need2FA ? '请输入身份验证器中的 6 位验证码。' : '登录以管理你的代理网络。' }}</p>
        </div>

        <form @submit.prevent="submit">
          <div class="field">
            <label>用户名</label>
            <input v-model="username" type="text" autocomplete="username" placeholder="admin" :disabled="need2FA" autofocus />
          </div>
          <div class="field">
            <label>密码</label>
            <input v-model="password" type="password" autocomplete="current-password" placeholder="••••••••" :disabled="need2FA" />
          </div>
          <div v-if="need2FA" class="field">
            <label>动态验证码 / 恢复码</label>
            <input v-model="code" type="text" inputmode="numeric" autocomplete="one-time-code" placeholder="6 位验证码" autofocus />
            <span class="hint">验证器不可用时，可以输入一次性恢复码。</span>
          </div>
          <div v-if="error" class="banner banner-danger" style="margin: 4px 0 12px">{{ error }}</div>
          <button class="btn btn-primary" :disabled="loading" type="submit">
            <Loader2 v-if="loading" :size="15" class="spin" />
            <LogIn v-else :size="15" />
            {{ loading ? '验证中…' : need2FA ? '验证并登录' : '登录' }}
          </button>
          <button v-if="need2FA" class="btn" type="button" style="margin-top: 8px" @click="resetTwoFactor">返回重新登录</button>
        </form>

        <div class="login-foot">MikvAce Panel · 代理控制平面</div>
      </div>
    </main>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Loader2, LogIn } from 'lucide-vue-next'
import { api } from '../api'
import { notify, setSession } from '../store'

const username = ref('admin')
const password = ref('')
const code = ref('')
const challenge = ref('')
const need2FA = ref(false)
const loading = ref(false)
const error = ref('')
const router = useRouter()
const route = useRoute()

function adminBase() {
  const entry = typeof route.params.entry === 'string' ? route.params.entry : ''
  return entry ? `/admin/${entry}` : '/admin'
}

function finishLogin(res) {
  setSession(res.token, res.admin, res.warning, res.version)
  notify('登录成功')
  const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : ''
  const target = redirect.startsWith('/admin') ? redirect : adminBase() + '/dashboard'
  router.push(target)
}

function resetTwoFactor() {
  need2FA.value = false
  challenge.value = ''
  code.value = ''
  error.value = ''
}

async function submit() {
  error.value = ''
  loading.value = true
  try {
    if (need2FA.value) {
      const res = await api.login2FA(challenge.value, code.value)
      finishLogin(res)
      return
    }
    const res = await api.login(username.value, password.value)
    if (res.twoFactorRequired) {
      need2FA.value = true
      challenge.value = res.challenge
      code.value = ''
      return
    }
    finishLogin(res)
  } catch (e) {
    error.value = e.status === 404 ? '安全入口无效或已变更，请使用最新的管理员入口地址。' : e.message
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
