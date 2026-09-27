import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'

// Spec 107 PR-C, T087 (red before T088).
//
// data-model.md §"Frontend surfaces" (`stores/auth.ts`): `principalKind:
// 'tenant'|'admin'|'api_key'` is derived from `/auth/me` role plus the
// presence of an API key, and `tasks.md` T088 gates every non-allowlisted
// core call in App.vue (`/info`, `/routing`, plus servers/connect fetches on
// mount) on `principalKind !== 'tenant'` (FR-041). Today `stores/auth.ts`
// exposes no `principalKind` at all and `App.vue`'s `onMounted` calls
// `fetchInfo`/`fetchRouting`/`fetchServers`/`connectEventSource`
// unconditionally, so both blocks below must fail until T088 lands.

const getProviderMock = vi.fn()
const getSessionStatusMock = vi.fn()
const getMeMock = vi.fn()

vi.mock('@/services/auth-api', () => ({
  authApi: {
    getProvider: (...args: unknown[]) => getProviderMock(...args),
    getSessionStatus: (...args: unknown[]) => getSessionStatusMock(...args),
    getMe: (...args: unknown[]) => getMeMock(...args),
    generateToken: vi.fn(),
    logout: vi.fn(),
    getLoginUrl: vi.fn(() => '/api/v1/auth/login'),
  },
}))

const hasAPIKeyMock = vi.fn(() => false)

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: (...args: unknown[]) => hasAPIKeyMock(...args),
    getAPIKeyPreview: vi.fn(() => 'none'),
    setAPIKey: vi.fn(),
    validateAPIKey: vi.fn(),
    reinitializeAPIKey: vi.fn(),
    createEventSource: vi.fn(() => ({ close: vi.fn(), addEventListener: vi.fn() })),
    addEventListener: vi.fn(() => () => {}),
  },
}))

// System/servers store mocks for the App.vue mount-gating block below. Declared
// at module scope (not per-test `vi.doMock`) because `vi.mock` factories are
// hoisted above every import, and re-mocking an already-imported module via
// `vi.doMock` inside `beforeEach` does not reliably take effect before
// `App.vue` (and its transitive `useSystemStore`/`useServersStore` calls) are
// dynamically imported later in the same file.
const fetchInfoMock = vi.fn()
const fetchRoutingMock = vi.fn()
const fetchScopeFilterFeaturesMock = vi.fn()
const connectEventSourceMock = vi.fn()
const disconnectEventSourceMock = vi.fn()
const fetchServersMock = vi.fn()

vi.mock('@/stores/system', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/system')>()
  return {
    ...actual,
    useSystemStore: () => ({
      sidebarCollapsed: false,
      authEpoch: 0,
      fetchInfo: fetchInfoMock,
      fetchRouting: fetchRoutingMock,
      // Spec 109-k FR-080a: fetched alongside fetchInfo/fetchRouting in
      // App.vue's onMounted, same admin-only gating.
      fetchScopeFilterFeatures: fetchScopeFilterFeaturesMock,
      connectEventSource: connectEventSourceMock,
      disconnectEventSource: disconnectEventSourceMock,
      setAuthRequired: vi.fn(),
      markAuthRecovered: vi.fn(),
    }),
  }
})

vi.mock('@/stores/servers', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/servers')>()
  return {
    ...actual,
    useServersStore: () => ({ fetchServers: fetchServersMock }),
  }
})

describe('stores/auth principalKind (Spec 107 T087/T088, data-model.md "Frontend surfaces")', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getProviderMock.mockReset()
    getSessionStatusMock.mockReset().mockResolvedValue({ authenticated: true })
    getMeMock.mockReset()
    hasAPIKeyMock.mockReset().mockReturnValue(false)
  })

  it('is "tenant" for a session user with no local API key', async () => {
    getProviderMock.mockResolvedValue({ display_name: 'Example Corp' })
    getMeMock.mockResolvedValue({
      id: 'u1',
      email: 'alice@example.com',
      display_name: 'Alice',
      role: 'user',
      provider: 'oidc',
      created_at: '',
      last_login_at: '',
    })
    hasAPIKeyMock.mockReturnValue(false)

    const { useAuthStore } = await import('@/stores/auth')
    const store = useAuthStore()
    await store.checkAuth()

    // @ts-expect-error - principalKind does not exist on the store yet (T088)
    expect(store.principalKind).toBe('tenant')
  })

  it('is "admin" for a session admin with no local API key', async () => {
    getProviderMock.mockResolvedValue({ display_name: 'Example Corp' })
    getMeMock.mockResolvedValue({
      id: 'u2',
      email: 'root@example.com',
      display_name: 'Root',
      role: 'admin',
      provider: 'oidc',
      created_at: '',
      last_login_at: '',
    })
    hasAPIKeyMock.mockReturnValue(false)

    const { useAuthStore } = await import('@/stores/auth')
    const store = useAuthStore()
    await store.checkAuth()

    // @ts-expect-error - principalKind does not exist on the store yet (T088)
    expect(store.principalKind).toBe('admin')
  })

  it('is "api_key" whenever a local API key is present, regardless of session role', async () => {
    getProviderMock.mockResolvedValue({ display_name: 'Example Corp' })
    getMeMock.mockResolvedValue({
      id: 'u3',
      email: 'scripts@example.com',
      display_name: 'Scripts',
      role: 'user',
      provider: 'oidc',
      created_at: '',
      last_login_at: '',
    })
    hasAPIKeyMock.mockReturnValue(true)

    const { useAuthStore } = await import('@/stores/auth')
    const store = useAuthStore()
    await store.checkAuth()

    // @ts-expect-error - principalKind does not exist on the store yet (T088)
    expect(store.principalKind).toBe('api_key')
  })

  it('is "api_key" on the personal edition (no session, API key present)', async () => {
    getProviderMock.mockResolvedValue(null)
    hasAPIKeyMock.mockReturnValue(true)

    const { useAuthStore } = await import('@/stores/auth')
    const store = useAuthStore()
    await store.checkAuth()

    // @ts-expect-error - principalKind does not exist on the store yet (T088)
    expect(store.principalKind).toBe('api_key')
  })
})

describe('App.vue gated mount-time fetches (Spec 107 FR-041, T087/T088)', () => {
  const fetchInfo = fetchInfoMock
  const fetchRouting = fetchRoutingMock
  const fetchScopeFilterFeatures = fetchScopeFilterFeaturesMock
  const fetchServers = fetchServersMock
  const connectEventSource = connectEventSourceMock

  beforeEach(() => {
    setActivePinia(createPinia())
    getProviderMock.mockReset().mockResolvedValue({ display_name: 'Example Corp' })
    getSessionStatusMock.mockReset().mockResolvedValue({ authenticated: true })
    getMeMock.mockReset()
    hasAPIKeyMock.mockReset().mockReturnValue(false)
    fetchInfo.mockClear()
    fetchRouting.mockClear()
    fetchScopeFilterFeatures.mockClear()
    fetchServers.mockClear()
    connectEventSource.mockClear()
    disconnectEventSourceMock.mockClear()
  })

  async function mountAppWith(role: 'user' | 'admin') {
    getMeMock.mockResolvedValue({
      id: 'u1',
      email: 'p@example.com',
      display_name: 'P',
      role,
      provider: 'oidc',
      created_at: '',
      last_login_at: '',
    })

    const { default: App } = await import('@/App.vue')
    const wrapper = mount(App, {
      global: {
        stubs: [
          'SidebarNav',
          'TopHeader',
          'AppFooter',
          'ToastContainer',
          'ConnectionStatus',
          'AuthErrorModal',
          'router-view',
        ],
      },
    })
    // Flush the async onMounted (checkAuth + gated fetches).
    await flushPromises()
    return wrapper
  }

  it('skips /info, /routing, servers and the event source for a tenant principal', async () => {
    await mountAppWith('user')

    expect(fetchInfo).not.toHaveBeenCalled()
    expect(fetchRouting).not.toHaveBeenCalled()
    expect(fetchScopeFilterFeatures).not.toHaveBeenCalled()
    expect(fetchServers).not.toHaveBeenCalled()
    expect(connectEventSource).not.toHaveBeenCalled()
  })

  it('still issues them for an admin principal', async () => {
    await mountAppWith('admin')

    expect(fetchInfo).toHaveBeenCalled()
    expect(fetchRouting).toHaveBeenCalled()
    expect(fetchScopeFilterFeatures).toHaveBeenCalled()
    expect(fetchServers).toHaveBeenCalled()
    expect(connectEventSource).toHaveBeenCalled()
  })

  it.each([
    ['an admin cookie session', () => {
      getProviderMock.mockResolvedValue({ display_name: 'Example Corp' })
      getSessionStatusMock.mockResolvedValue({ authenticated: true })
      getMeMock.mockResolvedValue({ id: 'admin', email: 'root@example.test', display_name: 'Root', role: 'admin', provider: 'oidc', created_at: '', last_login_at: '' })
    }],
    ['the personal edition', () => {
      getProviderMock.mockResolvedValue(null)
    }],
    ['a repaired local API key', () => {
      hasAPIKeyMock.mockReturnValue(true)
      getProviderMock.mockResolvedValue({ display_name: 'Example Corp' })
      getSessionStatusMock.mockResolvedValue({ authenticated: false })
    }],
  ])('loads each core door once after a deferred failed bootstrap recovers to %s', async (_case, recover) => {
    const deferred = <T,>() => {
      let resolve!: (value: T) => void
      return { promise: new Promise<T>((r) => { resolve = r }), resolve }
    }
    const failedProbe = deferred<undefined>()
    getProviderMock.mockReturnValue(failedProbe.promise)
    const { default: App } = await import('@/App.vue')
    mount(App, { global: { stubs: ['SidebarNav', 'TopHeader', 'AppFooter', 'ToastContainer', 'ConnectionStatus', 'AuthErrorModal', 'router-view'] } })
    await Promise.resolve()
    expect(fetchInfo).not.toHaveBeenCalled()

    failedProbe.resolve(undefined)
    await flushPromises()
    expect(fetchInfo).not.toHaveBeenCalled()

    recover()
    const { useAuthStore } = await import('@/stores/auth')
    await useAuthStore().checkAuth({ fresh: true })
    await flushPromises()

    for (const fetch of [fetchInfo, fetchRouting, fetchScopeFilterFeatures, fetchServers, connectEventSource]) {
      expect(fetch).toHaveBeenCalledTimes(1)
    }
  })

  it('sends a failed fresh key repair to Login with the safe internal route', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login', name: 'login', component: { template: '<div />' } },
        { path: '/my/activity', name: 'activity', component: { template: '<div />' } },
        { path: '/', name: 'dashboard', component: { template: '<div />' } },
      ],
    })
    await router.push('/my/activity?view=mine')
    await router.isReady()

    getProviderMock.mockResolvedValue(undefined)
    const { default: App } = await import('@/App.vue')
    const wrapper = mount(App, {
      global: {
        plugins: [router],
        stubs: {
          SidebarNav: { template: '<div />' }, TopHeader: { template: '<div />' }, AppFooter: true, ToastContainer: true, ConnectionStatus: true, 'router-view': true,
          AuthErrorModal: { template: '<button data-test="repair" @click="$emit(\'authenticated\')" />' },
        },
      },
    })
    await flushPromises()

    // The modal has validated a repaired key, but the fresh server probe
    // fails. App must expose Login's Retry rather than leave this protected
    // route with no shell and no recovery UI.
    await wrapper.get('[data-test="repair"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/login')
    expect(router.currentRoute.value.query.redirect).toBe('/my/activity?view=mine')
    expect(fetchInfo).not.toHaveBeenCalled()
  })

  it('disconnects an inherited admin SSE stream as a fresh probe becomes pending, then makes no tenant core calls', async () => {
    await mountAppWith('admin')
    const { useAuthStore } = await import('@/stores/auth')
    const auth = useAuthStore()
    const deferred = <T,>() => {
      let resolve!: (value: T) => void
      return { promise: new Promise<T>((r) => { resolve = r }), resolve }
    }
    const provider = deferred<{ display_name: string }>()
    getProviderMock.mockReturnValue(provider.promise)
    const fresh = auth.checkAuth({ fresh: true })
    await nextTick()
    expect(disconnectEventSourceMock).toHaveBeenCalledTimes(1)

    provider.resolve({ display_name: 'Example Corp' })
    getSessionStatusMock.mockResolvedValue({ authenticated: true })
    getMeMock.mockResolvedValue({ id: 'tenant', email: 'tenant@example.test', display_name: 'Tenant', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' })
    await fresh
    await flushPromises()
    for (const fetch of [fetchInfo, fetchRouting, fetchScopeFilterFeatures, fetchServers, connectEventSource]) {
      expect(fetch).toHaveBeenCalledTimes(1)
    }
  })

  it('disconnects an open core SSE stream on logout', async () => {
    await mountAppWith('admin')
    const { useAuthStore } = await import('@/stores/auth')
    await useAuthStore().logout()
    await nextTick()
    expect(disconnectEventSourceMock).toHaveBeenCalledTimes(1)
  })

  it('does not mount the shell or issue core calls while provider, session, and /me settle', async () => {
    const deferred = <T,>() => {
      let resolve!: (value: T) => void
      return { promise: new Promise<T>((r) => { resolve = r }), resolve }
    }
    const provider = deferred<{ display_name: string }>()
    const session = deferred<{ authenticated: boolean }>()
    const me = deferred<{ id: string; email: string; display_name: string; role: 'user'; provider: string; created_at: string; last_login_at: string }>()
    getProviderMock.mockReturnValue(provider.promise)
    getSessionStatusMock.mockReturnValue(session.promise)
    getMeMock.mockReturnValue(me.promise)

    const { default: App } = await import('@/App.vue')
    const wrapper = mount(App, {
      global: {
        stubs: {
          TopHeader: { name: 'TopHeader', template: '<div data-test="header" />' },
          SidebarNav: { name: 'SidebarNav', template: '<div data-test="sidebar" />' },
          AppFooter: true, ToastContainer: true, ConnectionStatus: true, AuthErrorModal: true, 'router-view': true,
        },
      },
    })
    await Promise.resolve()
    expect(wrapper.find('[data-test="header"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="sidebar"]').exists()).toBe(false)
    expect(fetchInfo).not.toHaveBeenCalled()
    expect(fetchServers).not.toHaveBeenCalled()

    provider.resolve({ display_name: 'Example Corp' })
    await Promise.resolve()
    expect(getSessionStatusMock).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="header"]').exists()).toBe(false)

    session.resolve({ authenticated: true })
    await Promise.resolve()
    expect(getMeMock).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="sidebar"]').exists()).toBe(false)

    me.resolve({ id: 'tenant', email: 'tenant@example.test', display_name: 'Tenant', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' })
    await flushPromises()
    // A settled authenticated tenant gets the permitted shell so they retain
    // navigation and the sign-out control. Its admin/core requests remain
    // independently gated by canLoadCore.
    expect(wrapper.find('[data-test="header"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="sidebar"]').exists()).toBe(true)
    expect(fetchInfo).not.toHaveBeenCalled()
    expect(fetchServers).not.toHaveBeenCalled()
  })

  it('fails closed without session or core calls when the provider probe is unavailable', async () => {
    getProviderMock.mockResolvedValue(undefined)
    const { default: App } = await import('@/App.vue')
    mount(App, { global: { stubs: ['SidebarNav', 'TopHeader', 'AppFooter', 'ToastContainer', 'ConnectionStatus', 'AuthErrorModal', 'router-view'] } })
    await flushPromises()

    const { useAuthStore } = await import('@/stores/auth')
    const auth = useAuthStore()
    expect(auth.authResolvedSuccessfully).toBe(false)
    expect(auth.canShowShell).toBe(false)
    expect(auth.canLoadCore).toBe(false)
    expect(getSessionStatusMock).not.toHaveBeenCalled()
    expect(getMeMock).not.toHaveBeenCalled()
    expect(fetchInfo).not.toHaveBeenCalled()
    expect(fetchRouting).not.toHaveBeenCalled()
    expect(fetchScopeFilterFeatures).not.toHaveBeenCalled()
    expect(fetchServers).not.toHaveBeenCalled()
    expect(connectEventSource).not.toHaveBeenCalled()
  })

  it.each([
    ['a session 500', () => Promise.reject(new Error('HTTP 500'))],
    ['a malformed session status', () => Promise.resolve({ authenticated: 'yes' })],
  ])('fails closed with no protected calls after %s', async (_case, sessionResult) => {
    getSessionStatusMock.mockImplementation(sessionResult)
    const { default: App } = await import('@/App.vue')
    mount(App, { global: { stubs: ['SidebarNav', 'TopHeader', 'AppFooter', 'ToastContainer', 'ConnectionStatus', 'AuthErrorModal', 'router-view'] } })
    await flushPromises()

    const { useAuthStore } = await import('@/stores/auth')
    const auth = useAuthStore()
    expect(auth.authResolvedSuccessfully).toBe(false)
    expect(auth.canShowShell).toBe(false)
    expect(auth.canLoadCore).toBe(false)
    expect(getMeMock).not.toHaveBeenCalled()
    expect(fetchInfo).not.toHaveBeenCalled()
    expect(fetchRouting).not.toHaveBeenCalled()
    expect(fetchScopeFilterFeatures).not.toHaveBeenCalled()
    expect(fetchServers).not.toHaveBeenCalled()
    expect(connectEventSource).not.toHaveBeenCalled()
  })
})
