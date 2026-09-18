import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

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
const getMeMock = vi.fn()

vi.mock('@/services/auth-api', () => ({
  authApi: {
    getProvider: (...args: unknown[]) => getProviderMock(...args),
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
const connectEventSourceMock = vi.fn()
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
      connectEventSource: connectEventSourceMock,
      disconnectEventSource: vi.fn(),
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
  const fetchServers = fetchServersMock
  const connectEventSource = connectEventSourceMock

  beforeEach(() => {
    setActivePinia(createPinia())
    getProviderMock.mockReset().mockResolvedValue({ display_name: 'Example Corp' })
    getMeMock.mockReset()
    hasAPIKeyMock.mockReset().mockReturnValue(false)
    fetchInfo.mockClear()
    fetchRouting.mockClear()
    fetchServers.mockClear()
    connectEventSource.mockClear()
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
    expect(fetchServers).not.toHaveBeenCalled()
    expect(connectEventSource).not.toHaveBeenCalled()
  })

  it('still issues them for an admin principal', async () => {
    await mountAppWith('admin')

    expect(fetchInfo).toHaveBeenCalled()
    expect(fetchRouting).toHaveBeenCalled()
    expect(fetchServers).toHaveBeenCalled()
    expect(connectEventSource).toHaveBeenCalled()
  })
})
