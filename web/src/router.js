import { createRouter, createWebHistory } from 'vue-router'
import { auth, portalAuth } from './store'
import { fetchAdminConfig, setAdminEntry } from './api'

// The administrator console lives under /admin and the self-service portal
// under /user. `/` is a public landing page and must not link to /admin.
const routes = [
  {
    path: '/',
    name: 'home',
    component: () => import('./views/HomeView.vue'),
    meta: { public: true, title: '首页' },
  },
  {
    path: '/user/login',
    name: 'portal-login',
    component: () => import('./views/PortalLoginView.vue'),
    meta: { public: true, portal: true, title: '用户登录' },
  },
  {
    path: '/user',
    name: 'portal',
    component: () => import('./views/PortalView.vue'),
    meta: { public: true, portal: true, title: '用户中心' },
  },
  {
    // The optional entry segment keeps /admin/login working when the security
    // entrance is disabled, and /admin/<entry>/login when it is enabled.
    path: '/admin/:entry?/login',
    name: 'admin-login',
    component: () => import('./views/LoginView.vue'),
    meta: { public: true, admin: true, title: '管理员登录' },
  },
  {
    path: '/admin/:entry?',
    component: () => import('./components/AppLayout.vue'),
    meta: { admin: true },
    children: [
      {
        path: '',
        redirect: (to) => ({ name: 'admin-dashboard', params: { entry: to.params.entry || undefined } }),
      },
      { path: 'dashboard', name: 'admin-dashboard', component: () => import('./views/DashboardView.vue'), meta: { title: '仪表盘' } },
      { path: 'users', name: 'admin-users', component: () => import('./views/UsersView.vue'), meta: { title: '用户' } },
      { path: 'nodes', name: 'admin-nodes', component: () => import('./views/NodesView.vue'), meta: { title: '节点' } },
      { path: 'groups', name: 'admin-groups', component: () => import('./views/GroupsView.vue'), meta: { title: 'Groups' } },
      { path: 'logs', name: 'admin-logs', component: () => import('./views/LogsView.vue'), meta: { title: '日志分析' } },
      { path: 'settings', name: 'admin-settings', component: () => import('./views/SettingsView.vue'), meta: { title: '设置' } },
    ],
  },

  // Legacy URLs from before the admin/user split.
  { path: '/login', redirect: (to) => ({ path: '/admin/login', query: to.query }) },
  { path: '/portal/login', redirect: (to) => ({ path: '/user/login', query: to.query }) },
  { path: '/portal', redirect: '/user' },
  { path: '/dashboard', redirect: '/admin/dashboard' },
  { path: '/users', redirect: '/admin/users' },
  { path: '/nodes', redirect: '/admin/nodes' },
  { path: '/groups', redirect: '/admin/groups' },
  { path: '/logs', redirect: '/admin/logs' },
  { path: '/settings', redirect: '/admin/settings' },

  { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue'), meta: { public: true } },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
  scrollBehavior() {
    return { top: 0 }
  },
})

function notFound(to) {
  return {
    name: 'not-found',
    params: { pathMatch: to.path.split('/').filter(Boolean) },
    query: to.query,
    hash: to.hash,
    replace: true,
  }
}

router.beforeEach(async (to) => {
  if (to.name === 'not-found') return true

  if (to.meta.admin) {
    const entry = typeof to.params.entry === 'string' ? to.params.entry : ''
    setAdminEntry(entry)

    let cfg = { securityEntryEnabled: false }
    try {
      cfg = await fetchAdminConfig()
    } catch {
      // If the probe fails the login request will surface the real error.
    }

    if (cfg.securityEntryEnabled) {
      // Without the secret entrance segment, pretend the admin area does not
      // exist instead of revealing the login page.
      if (!entry) return notFound(to)
    } else if (entry && to.name) {
      // Drop stale entry segments when the entrance has been disabled.
      return {
        name: to.name,
        params: { ...to.params, entry: undefined },
        query: to.query,
        hash: to.hash,
        replace: true,
      }
    }

    if (to.name === 'admin-login') {
      return auth.token ? { name: 'admin-dashboard', params: { entry: entry || undefined } } : true
    }
    if (!auth.token) {
      return { name: 'admin-login', params: { entry: entry || undefined }, query: { redirect: to.fullPath } }
    }
    return true
  }

  if (to.meta.portal) {
    if (to.name === 'portal-login') {
      return portalAuth.token ? { name: 'portal' } : true
    }
    if (!portalAuth.token) {
      return { name: 'portal-login', query: { redirect: to.fullPath } }
    }
    return true
  }

  return true
})

router.afterEach((to) => {
  document.title = (to.meta.title ? to.meta.title + ' · ' : '') + 'MikvAce Panel'
})

export default router
