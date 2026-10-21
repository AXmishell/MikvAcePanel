<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="login-logo">M</div>
      <h1>欢迎回来</h1>
      <p>{{ need2FA ? '请输入身份验证器中的 6 位验证码。' : '登录以管理你的代理网络。' }}</p>
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
      <div class="login-foot">MikvAce 面板 · 代理控制平面</div>
    </div>
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

function finishLogin(res) {
  setSession(res.token, res.admin, res.warning)
  notify('登录成功')
  const target = typeof route.query.redirect === 'string' ? route.query.redirect : '/dashboard'
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
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
