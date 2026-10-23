<template>
  <div class="login-shell">
    <aside class="login-brand">
      <div class="login-brand-head">
        <div class="brand-logo">{{ initial }}</div>
        <div class="brand-word">{{ panelName }}<span>Portal</span></div>
      </div>

      <div class="login-brand-copy">
        <div class="eyebrow">Self-service portal</div>
        <h1>你的订阅，<br>随时<span>可用</span>。</h1>
        <p>{{ note }}</p>
      </div>

      <ul class="login-points">
        <li>
          <span class="num">01</span>
          <div>
            <strong>订阅同步</strong>
            <small>订阅链接与节点变更自动更新，无需重新导入。</small>
          </div>
        </li>
        <li>
          <span class="num">02</span>
          <div>
            <strong>用量透明</strong>
            <small>随时查看已用流量、到期时间和在线设备。</small>
          </div>
        </li>
        <li>
          <span class="num">03</span>
          <div>
            <strong>账号安全</strong>
            <small>自助修改密码、重置设备状态与订阅链接。</small>
          </div>
        </li>
      </ul>

      <div class="login-brand-foot">
        <span class="dot" :class="enabled ? 'online' : 'offline'"></span>
        <span>{{ enabled ? 'Portal online' : 'Portal closed' }}</span>
      </div>
    </aside>

    <main class="login-panel">
      <div class="login-card">
        <div class="login-brand-head login-mobile-head">
          <div class="brand-logo">{{ initial }}</div>
          <div class="brand-word">{{ panelName }}<span>Portal</span></div>
        </div>

        <template v-if="configLoading">
          <div class="empty">正在加载用户中心…</div>
        </template>

        <template v-else-if="!enabled">
          <div class="login-head">
            <div class="eyebrow">Unavailable</div>
            <h2>用户中心未开放</h2>
            <p>管理员尚未启用自助用户中心。你仍然可以使用已有的订阅链接，或联系管理员获取帮助。</p>
          </div>
          <a v-if="supportUrl" class="btn btn-soft" :href="supportUrl" target="_blank" rel="noopener">联系支持</a>
          <div class="login-foot"><router-link to="/login">管理员登录 →</router-link></div>
        </template>

        <template v-else>
          <div class="login-head">
            <div class="eyebrow">Account access</div>
            <h2>{{ title }}</h2>
            <p>{{ note }}</p>
          </div>

          <form @submit.prevent="submit">
            <div class="field">
              <label>用户 ID</label>
              <input v-model="userId" type="text" autocomplete="username" placeholder="你的用户 ID" autofocus />
            </div>
            <div class="field">
              <label>密码</label>
              <input v-model="password" type="password" autocomplete="current-password" placeholder="••••••••" />
            </div>
            <div v-if="error" class="banner banner-danger" style="margin: 4px 0 12px">{{ error }}</div>
            <button class="btn btn-primary" :disabled="loading" type="submit">
              <Loader2 v-if="loading" :size="15" class="spin" />
              <LogIn v-else :size="15" />
              {{ loading ? '登录中…' : '登录用户中心' }}
            </button>
          </form>

          <div class="login-foot">
            <router-link to="/login">管理员登录 →</router-link>
          </div>
        </template>
      </div>
    </main>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Loader2, LogIn } from 'lucide-vue-next'
import { portalApi } from '../api'
import { setPortalSession } from '../store'

const router = useRouter()
const route = useRoute()

const config = ref(null)
const configLoading = ref(true)
const userId = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')

const enabled = computed(() => config.value ? !!config.value.enabled : true)
const panelName = computed(() => config.value?.panelName || 'MikvAce Panel')
const title = computed(() => config.value?.title || '用户中心')
const note = computed(() => config.value?.note || '登录后查看你的订阅、流量与账号状态。')
const supportUrl = computed(() => config.value?.supportUrl || '')
const initial = computed(() => (panelName.value || 'M').slice(0, 1).toUpperCase())

async function loadConfig() {
  try {
    config.value = await portalApi.config()
  } catch {
    // Keep the login form usable if the config probe fails.
    config.value = { enabled: true, panelName: 'MikvAce Panel', title: '用户中心' }
  } finally {
    configLoading.value = false
  }
}

async function submit() {
  error.value = ''
  if (!userId.value.trim() || !password.value) {
    error.value = '请输入用户 ID 和密码'
    return
  }
  loading.value = true
  try {
    const res = await portalApi.login(userId.value.trim(), password.value)
    setPortalSession(res.token, res.user)
    const target = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/portal')
      ? route.query.redirect
      : '/portal'
    router.push(target)
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(loadConfig)
</script>

<style scoped>
.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
