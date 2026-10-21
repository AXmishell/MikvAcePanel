<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="login-logo">M</div>
      <h1>欢迎回来</h1>
      <p>登录以管理你的代理网络。</p>
      <form @submit.prevent="submit">
        <div class="field">
          <label>用户名</label>
          <input v-model="username" type="text" autocomplete="username" placeholder="admin" autofocus />
        </div>
        <div class="field">
          <label>密码</label>
          <input v-model="password" type="password" autocomplete="current-password" placeholder="••••••••" />
        </div>
        <div v-if="error" class="banner banner-danger" style="margin: 4px 0 12px">{{ error }}</div>
        <button class="btn btn-primary" :disabled="loading" type="submit">
          <Loader2 v-if="loading" :size="15" class="spin" />
          <LogIn v-else :size="15" />
          {{ loading ? '登录中…' : '登录' }}
        </button>
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
import { auth, notify, setSession } from '../store'

const username = ref('admin')
const password = ref('')
const loading = ref(false)
const error = ref('')
const router = useRouter()
const route = useRoute()

async function submit() {
  error.value = ''
  loading.value = true
  try {
    const res = await api.login(username.value, password.value)
    setSession(res.token, res.admin, res.warning)
    notify('登录成功')
    const target = typeof route.query.redirect === 'string' ? route.query.redirect : '/dashboard'
    router.push(target)
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
