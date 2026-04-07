import { createRouter, createWebHistory } from 'vue-router'
import Dashboard from '@/views/Dashboard.vue'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  scrollBehavior() {
    // Scroll main content area to top on every navigation
    const main = document.querySelector('main.overflow-y-auto')
    if (main) main.scrollTop = 0
    return { top: 0 }
  },
  routes: [
    // Server edition auth routes
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/teams/Login.vue'),
      meta: { title: 'Sign In', public: true },
    },
    // Existing routes (admin/personal)
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
      path: '/repositories',
      name: 'repositories',
      component: () => import('@/views/Repositories.vue'),
      meta: {
        title: 'Repositories',
      },
    },
    {
      path: '/search',
      name: 'search',
      component: () => import('@/views/Search.vue'),
      meta: {
        title: 'Search',
      },
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('@/views/Settings.vue'),
      meta: {
        title: 'Configuration',
      },
    },
    {
      path: '/feedback',
      name: 'feedback',
      component: () => import('@/views/Feedback.vue'),
      meta: {
        title: 'Send Feedback',
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
      path: '/sessions',
      name: 'sessions',
      component: () => import('@/views/Sessions.vue'),
      meta: {
        title: 'MCP Sessions',
      },
    },
    {
      path: '/activity',
      name: 'activity',
      component: () => import('@/views/Activity.vue'),
      meta: {
        title: 'Activity Log',
      },
    },
    {
      path: '/security',
      name: 'security',
      component: () => import('@/views/Security.vue'),
      meta: {
        title: 'Security',
      },
    },
    {
      path: '/security/scans/:jobId',
      name: 'scan-report',
      component: () => import('@/views/ScanReport.vue'),
      props: true,
      meta: {
        title: 'Scan Report',
      },
    },
    {
      path: '/tokens',
      name: 'tokens',
      component: () => import('@/views/AgentTokens.vue'),
      meta: {
        title: 'Agent Tokens',
      },
    },
    // Server edition user routes
    {
      path: '/my/servers',
      name: 'user-servers',
      component: () => import('@/views/teams/UserServers.vue'),
      meta: { title: 'My Servers', requiresAuth: true },
    },
    {
      path: '/my/activity',
      name: 'user-activity',
      component: () => import('@/views/teams/UserActivity.vue'),
      meta: { title: 'My Activity', requiresAuth: true },
    },
    {
      path: '/my/diagnostics',
      name: 'user-diagnostics',
      component: () => import('@/views/teams/UserDiagnostics.vue'),
      meta: { title: 'Diagnostics', requiresAuth: true },
    },
    {
      path: '/my/tokens',
      name: 'user-tokens',
      component: () => import('@/views/teams/UserTokens.vue'),
      meta: { title: 'Agent Tokens', requiresAuth: true },
    },
    // Server edition admin routes
    {
      path: '/admin/dashboard',
      name: 'admin-dashboard',
      component: () => import('@/views/teams/AdminDashboard.vue'),
      meta: { title: 'Admin Dashboard', requiresAuth: true, requiresAdmin: true },
    },
    {
      path: '/admin/users',
      name: 'admin-users',
      component: () => import('@/views/teams/AdminUsers.vue'),
      meta: { title: 'Users', requiresAuth: true, requiresAdmin: true },
    },
    {
      path: '/admin/servers',
      name: 'admin-servers',
      component: () => import('@/views/teams/AdminServers.vue'),
      meta: { title: 'Servers', requiresAuth: true, requiresAdmin: true },
    },
    // 404 - keep at end
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

// Auth guard
router.beforeEach(async (to) => {
  const { useAuthStore } = await import('@/stores/auth')
  const authStore = useAuthStore()

  // Initialize auth state on first navigation
  if (authStore.loading) {
    await authStore.checkAuth()
  }

  // Skip auth checks for personal edition
  if (!authStore.isTeamsEdition) {
    // Don't show server routes in personal edition
    if (to.path === '/login' || to.path.startsWith('/my/') || to.path.startsWith('/admin/')) {
      return { name: 'dashboard' }
    }
    // Update title for personal edition
    const title = to.meta.title as string
    if (title) {
      document.title = `${title} - MCPProxy Control Panel`
    }
    return
  }

  // Public routes (login) - redirect to dashboard if already authenticated
  if (to.meta.public) {
    if (authStore.isAuthenticated) {
      return { name: 'dashboard' }
    }
    return
  }

  // Require authentication for server edition
  if (!authStore.isAuthenticated) {
    return { name: 'login' }
  }

  // Admin-only routes
  if (to.meta.requiresAdmin && !authStore.isAdmin) {
    return { name: 'dashboard' }
  }

  // Update title
  const title = to.meta.title as string
  if (title) {
    document.title = `${title} - MCPProxy Control Panel`
  }
})

export default router
