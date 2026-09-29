// #1401: repairing a failed API key runs a fresh auth probe. The shell
// (TopHeader + SidebarNav) used to unmount for the whole probe and remount on
// recovery, wiping the header search text / open menus, and the remounted
// SidebarNav loaded on mount AND again from its authEpoch watcher.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

const calls: Record<string, number> = {}
let authErrorListener: ((e: unknown) => void) | null = null
const getProviderMock = vi.fn()
const getSessionStatusMock = vi.fn()
const getMeMock = vi.fn()
let hasKey = true

vi.mock('@/services/auth-api', () => ({
  authApi: {
    getProvider: (...a: unknown[]) => getProviderMock(...a),
    getSessionStatus: (...a: unknown[]) => getSessionStatusMock(...a),
    getMe: (...a: unknown[]) => getMeMock(...a),
    logout: vi.fn(),
    getLoginUrl: vi.fn(() => '/api/v1/auth/login'),
  },
}))

vi.mock('@/services/api', () => {
  const base: Record<string, unknown> = {
    hasAPIKey: () => hasKey,
    getAPIKeyPreview: () => 'key',
    createEventSource: () => ({ close: vi.fn(), addEventListener: vi.fn() }),
    addEventListener: (l: (e: unknown) => void) => {
      authErrorListener = l
      return () => { authErrorListener = null }
    },
  }
  // Every other client method counts its calls and resolves to an empty success.
  const api = new Proxy(base, {
    get(target, prop: string) {
      if (prop in target) return target[prop]
      return () => {
        calls[prop] = (calls[prop] ?? 0) + 1
        return Promise.resolve({ success: true, data: {} })
      }
    },
  })
  return { default: api }
})

const routes = [
  { path: '/', name: 'dashboard', component: { template: '<div />' } },
  { path: '/login', name: 'login', component: { template: '<div />' } },
]

async function mountApp() {
  getProviderMock.mockResolvedValue(null) // personal edition
  const router = createRouter({ history: createMemoryHistory(), routes })
  router.push('/')
  await router.isReady()
  const { default: App } = await import('@/App.vue')
  const wrapper = mount(App, {
    global: {
      plugins: [router],
      stubs: { AppFooter: true, ToastContainer: true, ConnectionStatus: true, AuthErrorModal: true, ProfileSwitcher: true, ModeSwitcher: true },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

async function recoverViaModal(wrapper: ReturnType<typeof mount>) {
  wrapper.findComponent({ name: 'AuthErrorModal' }).vm.$emit('authenticated')
}

describe('shell survives auth recovery (#1401)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    for (const k of Object.keys(calls)) delete calls[k]
    authErrorListener = null
    hasKey = true
    getProviderMock.mockReset()
    getSessionStatusMock.mockReset()
    getMeMock.mockReset()
  })

  it('keeps the same header/sidebar DOM while the fresh probe is pending', async () => {
    const { wrapper } = await mountApp()
    const header = wrapper.find('header, .navbar')
    expect(header.exists()).toBe(true)
    const headerEl = header.element
    const sidebarEl = wrapper.find('aside, .drawer-side').element

    let release!: (v: unknown) => void
    getProviderMock.mockReturnValue(new Promise((r) => { release = r }))
    await recoverViaModal(wrapper)
    await flushPromises()

    // Probe is pending: the shell must still be the very same nodes.
    expect(wrapper.find('header, .navbar').element).toBe(headerEl)
    expect(wrapper.find('aside, .drawer-side').element).toBe(sidebarEl)

    release(null)
    await flushPromises()
    expect(wrapper.find('header, .navbar').element).toBe(headerEl)
    expect(wrapper.find('aside, .drawer-side').element).toBe(sidebarEl)
  })

  it('loads badge and attention data exactly once per recovery', async () => {
    const { wrapper } = await mountApp()
    const before = { ...calls }
    // markAuthRecovered only bumps the epoch when auth had failed.
    const { useSystemStore } = await import('@/stores/system')
    useSystemStore().setAuthRequired(true)
    await recoverViaModal(wrapper)
    await flushPromises()

    // Header and sidebar each own one attention fetch; nothing else may add more.
    expect(calls.getAttention - (before.getAttention ?? 0), 'getAttention').toBe(2)
    for (const name of ['getReviewQueue', 'getGlobalTools', 'getConfigSecrets', 'getOnboardingState', 'getProfiles', 'getActiveProfile']) {
      expect(calls[name] - (before[name] ?? 0), name).toBe(1)
    }
  })

  it('still removes the shell when the fresh probe fails', async () => {
    const { wrapper, router } = await mountApp()
    expect(wrapper.find('header, .navbar').exists()).toBe(true)

    getProviderMock.mockResolvedValue(undefined) // unavailable server
    await recoverViaModal(wrapper)
    await flushPromises()

    expect(wrapper.find('header, .navbar').exists()).toBe(false)
    expect(wrapper.find('aside, .drawer-side').exists()).toBe(false)
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('does not show the shell during the very first probe', async () => {
    let release!: (v: unknown) => void
    getProviderMock.mockReturnValue(new Promise((r) => { release = r }))
    const router = createRouter({ history: createMemoryHistory(), routes })
    router.push('/')
    await router.isReady()
    const { default: App } = await import('@/App.vue')
    const wrapper = mount(App, {
      global: { plugins: [router], stubs: { AppFooter: true, ToastContainer: true, ConnectionStatus: true, AuthErrorModal: true } },
    })
    await flushPromises()
    expect(wrapper.find('header, .navbar').exists()).toBe(false)
    release(null)
    await flushPromises()
    expect(wrapper.find('header, .navbar').exists()).toBe(true)
  })

  it('never loads admin profiles for a tenant session on recovery', async () => {
    hasKey = false
    getProviderMock.mockResolvedValue({ display_name: 'Example Corp' })
    getSessionStatusMock.mockResolvedValue({ authenticated: true })
    getMeMock.mockResolvedValue({ id: 'u1', email: 'a@example.com', display_name: 'A', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' })
    const router = createRouter({ history: createMemoryHistory(), routes })
    router.push('/')
    await router.isReady()
    const { default: App } = await import('@/App.vue')
    const wrapper = mount(App, {
      global: { plugins: [router], stubs: { AppFooter: true, ToastContainer: true, ConnectionStatus: true, AuthErrorModal: true, ProfileSwitcher: true, ModeSwitcher: true } },
    })
    await flushPromises()
    expect(wrapper.find('header, .navbar').exists()).toBe(true)

    const { useSystemStore } = await import('@/stores/system')
    useSystemStore().setAuthRequired(true)
    await recoverViaModal(wrapper)
    await flushPromises()

    expect(calls.getProfiles ?? 0).toBe(0)
    expect(calls.getActiveProfile ?? 0).toBe(0)
    expect(wrapper.find('header, .navbar').exists()).toBe(true)
  })
})
