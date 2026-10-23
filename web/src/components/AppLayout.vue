<template>
  <div class="shell">
    <div v-if="mobileOpen" class="sidebar-overlay" @click="mobileOpen = false"></div>
    <aside class="sidebar" :class="{ open: mobileOpen }">
      <div class="brand">
        <div class="brand-logo">M</div>
        <div>
          <div class="brand-name">MikvAce</div>
          <div class="brand-sub">Control Plane</div>
        </div>
      </div>
      <div class="nav-label">控制台</div>
      <router-link v-for="item in navMain" :key="item.to" :to="item.to" class="nav-item" :class="{ active: isActive(item.to) }" @click="mobileOpen = false">
        <component :is="item.icon" :size="17" />
        <span>{{ item.label }}</span>
        <span v-if="item.badge" class="nav-badge">{{ item.badge }}</span>
      </router-link>
      <div class="nav-label">系统</div>
      <router-link v-for="item in navSystem" :key="item.to" :to="item.to" class="nav-item" :class="{ active: isActive(item.to) }" @click="mobileOpen = false">
        <component :is="item.icon" :size="17" />
        <span>{{ item.label }}</span>
      </router-link>
      <div class="sidebar-foot">
        <div class="flex"><span class="dot online"></span><span>面板在线</span></div>
        <div class="version">{{ auth.version ? 'v' + auth.version : 'dev build' }}</div>
      </div>
    </aside>

    <div class="main">
      <header class="topbar">
        <button class="icon-btn mobile-toggle" aria-label="打开导航" @click="mobileOpen = true"><Menu :size="19" /></button>
        <div>
          <div class="page-title">{{ title }}</div>
          <div class="page-sub">{{ subtitle }}</div>
        </div>
        <div class="topbar-spacer"></div>
        <button
          class="icon-btn"
          :aria-label="theme.current === 'light' ? '切换为深色主题' : '切换为浅色主题'"
          :title="theme.current === 'light' ? '切换为深色主题' : '切换为浅色主题'"
          @click="toggleTheme"
        >
          <Moon v-if="theme.current === 'light'" :size="17" />
          <Sun v-else :size="17" />
        </button>
        <a class="icon-btn" href="/api/health" target="_blank" title="健康检查" aria-label="健康检查"><Activity :size="17" /></a>
        <div class="user-chip">
          <div class="avatar">{{ initial }}</div>
          <div class="user-meta">
            <div class="user-name">{{ auth.admin?.username || 'admin' }}</div>
            <div class="user-role">管理员</div>
          </div>
          <button class="icon-btn" title="退出登录" @click="logout"><LogOut :size="16" /></button>
        </div>
      </header>

      <main class="content">
        <div v-if="auth.warning" class="banner banner-warn">
          <ShieldCheck :size="16" style="margin-top: 1px" />
          <div class="spacer">{{ auth.warning }} <router-link to="/settings">打开设置</router-link></div>
        </div>
        <router-view v-slot="{ Component }">
          <component :is="Component" />
        </router-view>
      </main>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  Activity, FolderTree, LayoutDashboard, LogOut, Menu, Moon, ScrollText, Server, Settings, ShieldCheck, Sun, Users,
} from 'lucide-vue-next'
import { auth, clearSession } from '../store'
import { theme, toggleTheme } from '../theme'
import { api } from '../api'

const route = useRoute()
const router = useRouter()
const mobileOpen = ref(false)

const navMain = [
  { to: '/dashboard', label: '仪表盘', icon: LayoutDashboard },
  { to: '/users', label: '用户', icon: Users },
  { to: '/nodes', label: '节点', icon: Server },
  { to: '/groups', label: '服务器组', icon: FolderTree },
  { to: '/logs', label: '日志分析', icon: ScrollText },
]
const navSystem = [{ to: '/settings', label: '设置', icon: Settings }]

const title = computed(() => route.meta.title || 'MikvAce Panel')
const subtitle = computed(() => {
  const map = {
    仪表盘: '基础设施实时概览',
    用户: '账号、配额与订阅',
    节点: '节点 Agent 与配置下发',
    服务器组: '将用户映射到节点集合',
    日志分析: '代理日志的安全与稳定性分析',
    设置: '面板、订阅与分析偏好',
  }
  return map[title.value] || '自托管代理控制面板'
})
const initial = computed(() => (auth.admin?.username || 'A').slice(0, 1).toUpperCase())

function isActive(path) {
  return route.path === path || route.path.startsWith(path + '/')
}
async function logout() {
  try {
    await api.logout()
  } catch {
    /* the local session is cleared even if the server is unreachable */
  }
  clearSession()
  router.push('/login')
}
watch(() => route.path, () => (mobileOpen.value = false))
</script>

<style scoped>
.sidebar-overlay { position: fixed; inset: 0; background: var(--scrim); backdrop-filter: blur(2px); z-index: 25; }
.user-chip {
  display: flex; align-items: center; gap: 9px;
  padding: 3px 3px 3px 5px;
  border: 1px solid var(--line); border-radius: 9px;
  background: var(--panel-2);
}
.avatar {
  width: 27px; height: 27px; border-radius: 6px;
  display: grid; place-items: center;
  font-family: var(--mono); font-size: 11px; font-weight: 700;
  color: var(--on-accent); background: var(--accent);
}
.user-meta { line-height: 1.2; padding-right: 8px; }
.user-name { font-size: 11.5px; font-weight: 650; }
.user-role {
  font-family: var(--mono); font-size: 8.5px; color: var(--muted-2);
  letter-spacing: 0.1em; text-transform: uppercase;
}
@media (max-width: 700px) { .user-meta, .topbar > a.icon-btn { display: none; } }
</style>
