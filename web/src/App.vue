<template>
  <router-view />
  <div class="toast-stack">
    <div
      v-for="t in toasts"
      :key="t.id"
      class="toast"
      :class="'toast--' + t.type"
      @click="dismissToast(t.id)"
    >
      <span class="toast-dot"></span>
      <span>{{ t.message }}</span>
    </div>
  </div>
</template>

<script setup>
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api, setPortalUnauthorizedHandler, setUnauthorizedHandler } from './api'
import { auth, clearPortalSession, clearSession, setWarning, toasts, dismissToast } from './store'

const router = useRouter()

setUnauthorizedHandler(() => {
  clearSession()
  const current = router.currentRoute.value
  // Never pull a portal visitor into the administrator login flow.
  if (!current.path.startsWith('/admin') || current.name === 'admin-login') return
  const entry = typeof current.params.entry === 'string' ? current.params.entry : ''
  router.push({ name: 'admin-login', params: { entry: entry || undefined }, query: { redirect: current.fullPath } })
})

setPortalUnauthorizedHandler(() => {
  clearPortalSession()
  if (router.currentRoute.value.name !== 'portal-login') {
    router.push({ name: 'portal-login', query: { redirect: router.currentRoute.value.fullPath } })
  }
})

onMounted(async () => {
  // Let the initial navigation finish so the router guard has stored the
  // optional admin entrance before the first authenticated API call.
  await router.isReady()
  if (!auth.token) return
  try {
    const me = await api.me()
    auth.admin = me.admin
    auth.version = me.version
    setWarning(me.warning)
  } catch {
    /* handled by the unauthorized hook */
  }
})
</script>
