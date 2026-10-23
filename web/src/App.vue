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
  if (router.currentRoute.value.name !== 'login') {
    router.push({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } })
  }
})

setPortalUnauthorizedHandler(() => {
  clearPortalSession()
  if (router.currentRoute.value.name !== 'portal-login') {
    router.push({ name: 'portal-login', query: { redirect: router.currentRoute.value.fullPath } })
  }
})

onMounted(async () => {
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
