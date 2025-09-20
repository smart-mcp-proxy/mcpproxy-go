import { createRouter, createWebHistory } from 'vue-router'
import Dashboard from '@/views/Dashboard.vue'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/',
      name: 'dashboard',
      component: Dashboard,
      meta: {
        title: 'Dashboard',
      },
    },
    {
      path: '/servers',
      name: 'servers',
      component: () => import('@/views/Servers.vue'),
      meta: {
        title: 'Servers',
      },
    },
    {
      path: '/servers/:serverName',
      name: 'server-detail',
      component: () => import('@/views/ServerDetail.vue'),
      props: true,
      meta: {
        title: 'Server Details',
      },
    },
    {
      path: '/tools',
      name: 'tools',
      component: () => import('@/views/Tools.vue'),
      meta: {
        title: 'Tools',
      },
    },
    {
      path: '/search',
      name: 'search',
      component: () => import('@/views/Search.vue'),
      meta: {
        title: 'Search Tools',
      },
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('@/views/Settings.vue'),
      meta: {
        title: 'Settings',
      },
    },
    {
      path: '/secrets',
      name: 'secrets',
      component: () => import('@/views/Secrets.vue'),
      meta: {
        title: 'Secrets',
      },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFound.vue'),
      meta: {
        title: 'Page Not Found',
      },
    },
  ],
})

// Update document title based on route
router.beforeEach((to) => {
  const title = to.meta.title as string
  if (title) {
    document.title = `${title} - MCPProxy Control Panel`
  }
})

export default router