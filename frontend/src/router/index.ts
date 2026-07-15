import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes = [
  {
    path: '/',
    name: 'home',
    component: () => import('@/views/HomeView.vue'),
    meta: { requiresNoAuth: true },
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { requiresIdentity: true, requiresNoAuth: true },
  },
  {
    path: '/setup',
    name: 'setup',
    component: () => import('@/views/SetupView.vue'),
    meta: { requiresNoAuth: true },
  },
  {
    path: '/team',
    name: 'team',
    component: () => import('@/views/TeamView.vue'),
    meta: { requiresAuth: true },
  },
  {
    path: '/chat',
    name: 'chat',
    component: () => import('@/views/ChatView.vue'),
    meta: { requiresAuth: true },
  },
  {
    path: '/files',
    name: 'files',
    component: () => import('@/views/FilesView.vue'),
    meta: { requiresAuth: true },
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { requiresAuth: true },
  },
]

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean
    requiresNoAuth?: boolean
    requiresIdentity?: boolean
  }
}

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to, _from, next) => {
  const authStore = useAuthStore()

  if (to.meta.requiresAuth) {
    if (!authStore.isLoggedIn) {
      next({ name: 'login' })
      return
    }
  }

  if (to.meta.requiresNoAuth) {
    if (authStore.isLoggedIn) {
      next({ name: 'chat' })
      return
    }
  }

  if (to.meta.requiresIdentity && !authStore.hasIdentity) {
    next({ name: 'setup' })
    return
  }

  next()
})

export default router
