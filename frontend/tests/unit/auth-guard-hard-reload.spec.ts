import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'

// Hard-reload deep-linking under the server edition. On a full page load two
// callers race for the auth probe: App.vue's onMounted checkAuth() and the
// router guard's own checkAuth() for the initial navigation. The guard must
// decide `isTeamsEdition` / `isAuthenticated` from ONE settled probe (never a
// half-run one) and the pair must not double-issue /status + /auth/me.

const deferred = <T,>() => {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}

const statusSpy = vi.hoisted(() => vi.fn())
const meSpy = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    getStatus: statusSpy,
    hasAPIKey: vi.fn(() => false),
  },
}))

vi.mock('@/services/auth-api', () => ({
  authApi: {
    getMe: meSpy,
    getLoginUrl: vi.fn(() => '/api/v1/auth/login'),
    logout: vi.fn(),
  },
}))

import { useAuthStore } from '@/stores/auth'
import { authGuard } from '@/router'

const tenant = { id: 'u1', email: 'alice@example.com', display_name: 'Alice', role: 'user' }
const admin = { ...tenant, id: 'u2', email: 'dana@example.com', display_name: 'Dana', role: 'admin' }

const stub = { template: '<div />' }

function makeRouter(): Router {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/login', name: 'login', component: stub, meta: { title: 'Sign In', public: true } },
      { path: '/', name: 'dashboard', component: stub, meta: { title: 'Dashboard' } },
      { path: '/servers', name: 'servers', component: stub, meta: { title: 'Servers' } },
      { path: '/activity', name: 'activity', component: stub, meta: { title: 'Activity Log' } },
      { path: '/my/tokens', name: 'user-tokens', component: stub, meta: { title: 'Agent Tokens', requiresAuth: true } },
      { path: '/admin/users', name: 'admin-users', component: stub, meta: { title: 'Users', requiresAuth: true, requiresAdmin: true } },
    ],
  })
  router.beforeEach(authGuard)
  return router
}

function serverEdition(user: typeof tenant | null) {
  const status = deferred<{ data: { edition: string } }>()
  const me = deferred<typeof tenant | null>()
  statusSpy.mockReturnValue(status.promise)
  meSpy.mockReturnValue(me.promise)
  return {
    settle: () => {
      status.resolve({ data: { edition: 'server' } })
      me.resolve(user)
    },
  }
}

describe('auth store: concurrent checkAuth shares one in-flight probe', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    statusSpy.mockReset()
    meSpy.mockReset()
  })

  it('issues /status and /auth/me once for two concurrent callers, both see the settled edition', async () => {
    const rig = serverEdition(tenant)
    const store = useAuthStore()

    const mount = store.checkAuth() // App.vue onMounted
    const guard = store.checkAuth() // router guard, same tick
    rig.settle()
    await Promise.all([mount, guard])

    expect(statusSpy).toHaveBeenCalledTimes(1)
    expect(meSpy).toHaveBeenCalledTimes(1)
    expect(store.isTeamsEdition).toBe(true)
    expect(store.isAuthenticated).toBe(true)
    expect(store.loading).toBe(false)
  })

  it('re-probes once the previous run has settled (reloadAfterAuth relies on a fresh read)', async () => {
    const first = serverEdition(tenant)
    const store = useAuthStore()
    const p = store.checkAuth()
    first.settle()
    await p

    const second = serverEdition(admin)
    const q = store.checkAuth()
    second.settle()
    await q

    expect(statusSpy).toHaveBeenCalledTimes(2)
    expect(store.isAdmin).toBe(true)
  })
})

describe('router guard: hard-reload deep link under the server edition', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    statusSpy.mockReset()
    meSpy.mockReset()
  })

  const deepRoutes: Array<[string, string]> = [
    ['/my/tokens', 'user-tokens'],
    ['/servers', 'servers'],
    ['/activity', 'activity'],
  ]

  for (const [path, name] of deepRoutes) {
    it(`tenant: mount-time checkAuth racing the initial navigation to ${path} lands on ${name}`, async () => {
      const rig = serverEdition(tenant)
      const store = useAuthStore()
      const router = makeRouter()

      // App.vue's onMounted fires first (mount is synchronous), the guard's
      // dynamic import resolves a microtask later — same order as the browser.
      const mount = store.checkAuth()
      const nav = router.push(path)
      rig.settle()
      await Promise.all([mount, nav])

      expect(router.currentRoute.value.name).toBe(name)
      expect(document.title).toBe(`${router.currentRoute.value.meta.title} - MCPProxy Control Panel`)
      expect(statusSpy).toHaveBeenCalledTimes(1)
    })

    it(`admin: mount-time checkAuth racing the initial navigation to ${path} lands on ${name}`, async () => {
      const rig = serverEdition(admin)
      const store = useAuthStore()
      const router = makeRouter()

      const mount = store.checkAuth()
      const nav = router.push(path)
      rig.settle()
      await Promise.all([mount, nav])

      expect(router.currentRoute.value.name).toBe(name)
      expect(statusSpy).toHaveBeenCalledTimes(1)
    })
  }

  it('tenant hard-loading an admin route is sent to the dashboard, not to /login', async () => {
    const rig = serverEdition(tenant)
    const store = useAuthStore()
    const router = makeRouter()

    const mount = store.checkAuth()
    const nav = router.push('/admin/users')
    rig.settle()
    await Promise.all([mount, nav])

    expect(router.currentRoute.value.name).toBe('dashboard')
  })

  it('signed-out hard load of a deep route goes to /login', async () => {
    const rig = serverEdition(null)
    const store = useAuthStore()
    const router = makeRouter()

    const mount = store.checkAuth()
    const nav = router.push('/my/tokens')
    rig.settle()
    await Promise.all([mount, nav])

    expect(router.currentRoute.value.name).toBe('login')
  })

  it('guard alone (no mount call yet) still waits for the probe before deciding', async () => {
    const rig = serverEdition(tenant)
    const router = makeRouter()

    const nav = router.push('/my/tokens')
    await Promise.resolve()
    rig.settle()
    await nav

    expect(router.currentRoute.value.name).toBe('user-tokens')
    expect(statusSpy).toHaveBeenCalledTimes(1)
  })
})
