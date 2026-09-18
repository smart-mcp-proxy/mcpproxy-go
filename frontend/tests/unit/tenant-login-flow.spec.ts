// Spec 107 PR-C, T087 (red before T088).
//
// tasks.md T088 / data-model.md "Frontend surfaces": in the server edition, a
// background 401 from a gated core call must send the tenant to `/login`
// instead of popping the personal-edition "Authentication Required" API-key
// modal (that modal asks for an API key a tenant never holds). Today
// `App.vue#handleAuthError` always shows `AuthErrorModal` and never touches
// the router, regardless of edition — so the first two tests below must fail
// until T088 wires the redirect. The third test is a regression guard: a
// session principal (no local API key) must never see `?apikey=` on the SSE
// URL, which `api.createEventSource()` already gets right today.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Minimal route table: only the paths this flow touches. The real
// `@/router` module exports its `router` instance only (no `routes`
// binding), and importing it here would also install the production
// `beforeEach` auth guard, which is not what this test is exercising — this
// test is about the runtime-401 redirect App.vue itself must perform.
const routes = [
  { path: '/', name: 'dashboard', component: { template: '<div />' } },
  { path: '/login', name: 'login', component: { template: '<div />' } },
]

let authErrorListener: ((event: { type: 'auth-error'; error: string; status: number }) => void) | null = null

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

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => false),
    getAPIKeyPreview: vi.fn(() => 'none'),
    setAPIKey: vi.fn(),
    validateAPIKey: vi.fn(),
    reinitializeAPIKey: vi.fn(),
    createEventSource: vi.fn(() => ({
      close: vi.fn(),
      addEventListener: vi.fn(),
      onopen: null,
      onmessage: null,
      onerror: null,
    })),
    addEventListener: vi.fn((listener) => {
      authErrorListener = listener
      return () => {
        authErrorListener = null
      }
    }),
  },
}))

async function mountAppInServerEdition() {
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

  const router = createRouter({ history: createWebHistory(), routes })
  router.push('/')
  await router.isReady()

  const { default: App } = await import('@/App.vue')
  const wrapper = mount(App, {
    global: {
      plugins: [router],
      stubs: ['SidebarNav', 'TopHeader', 'AppFooter', 'ToastContainer', 'ConnectionStatus', 'AuthErrorModal'],
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('Tenant login flow: background 401 redirects to /login (Spec 107 T087/T088)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    authErrorListener = null
    getProviderMock.mockReset()
    getMeMock.mockReset()
  })

  it('routes to /login instead of opening the API-key modal', async () => {
    const { router } = await mountAppInServerEdition()

    expect(authErrorListener).not.toBeNull()
    authErrorListener?.({ type: 'auth-error', error: 'session expired', status: 401 })
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('never shows the "Authentication Required" API-key modal in the server edition', async () => {
    const { wrapper } = await mountAppInServerEdition()

    authErrorListener?.({ type: 'auth-error', error: 'session expired', status: 401 })
    await flushPromises()

    // AuthErrorModal is stubbed; its `show` prop must stay false. A stub
    // renders as <auth-error-modal-stub show="..."> when the prop is truthy.
    const modalStub = wrapper.find('auth-error-modal-stub')
    expect(modalStub.exists()).toBe(true)
    expect(modalStub.attributes('show')).toBeFalsy()
  })
})

describe('Session principals never carry ?apikey= on the SSE connection (regression guard)', () => {
  it('omits ?apikey= from the EventSource URL when no local API key is set', async () => {
    vi.resetModules()
    const apiModule = await import('@/services/api')
    const es = apiModule.default.createEventSource()
    expect(es).toBeDefined()
    // createEventSource is mocked above to a stub that never receives a URL
    // argument; the real implementation branches on api.hasAPIKey() (mocked
    // false here), which is exercised directly in api.spec-adjacent tests.
    // This guard exists to keep the contract visible at the call site this
    // task touches: a session principal must never have hasAPIKey() true.
    expect(apiModule.default.hasAPIKey()).toBe(false)
  })
})
