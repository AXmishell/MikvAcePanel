import { createRouter, createWebHistory } from 'vue-router'
import { auth, portalAuth } from './store'

const routes = [
  {
    path: '/',
    name: 'home',
    component: () => import('./views/HomeView.vue'),
    meta: { public: true, title: '首页' },
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('./views/LoginView.vue'),
    meta: { public: true, title: '登录' },
  },
  {
    path: '/portal/login',
    name: 'portal-login',
    component: () => import('./views/PortalLoginView.vue'),
    meta: { public: true, portal: true, title: '用户登录' },
  },
  {
    path: '/portal',
    name: 'portal',
    component: () => import('./views/PortalView.vue'),
    meta: { public: true, portal: true, title: '用户中心' },
  },
  {
    path: '/',
    component: () => import('./components/AppLayout.vue'),
    children: [
      { path: 'dashboard', name: 'dashboard', component: () => import('./views/DashboardView.vue'), meta: { title: '仪表盘' } },
      { path: 'users', name: 'users', component: () => import('./views/UsersView.vue'), meta: { title: '用户' } },
      { path: 'nodes', name: 'nodes', component: () => import('./views/NodesView.vue'), meta: { title: '节点' } },
      { path: 'groups', name: 'groups', component: () => import('./views/GroupsView.vue'), meta: { title: 'Groups' } },
      { path: 'logs', name: 'logs', component: () => import('./views/LogsView.vue'), meta: { title: '日志分析' } },
      { path: 'settings', name: 'settings', component: () => import('./views/SettingsView.vue'), meta: { title: '设置' } },
    ],
  },
  { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue'), meta: { public: true } },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
  scrollBehavior() {
    return { top: 0 }
  },
})

router.beforeEach((to) => {
  if (to.meta.portal) {
    if (to.name === 'portal-login') {
      return portalAuth.token ? { name: 'portal' } : true
    }
    if (!portalAuth.token) {
      return { name: 'portal-login', query: { redirect: to.fullPath } }
    }
    return true
  }
  if (to.meta.public) {
    if (to.name === 'login' && auth.token) return { name: 'dashboard' }
    return true
  }
  if (!auth.token) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  return true
})

router.afterEach((to) => {
  document.title = (to.meta.title ? to.meta.title + ' · ' : '') + 'MikvAce Panel'
})

export default router
