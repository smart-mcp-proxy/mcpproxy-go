import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { authApi } from '@/services/auth-api'
import { useAuthStore } from '@/stores/auth'
import Login from '@/views/teams/Login.vue'

// The auth store must never need the operator's API key to learn the edition.
// Stub the keyed API service so any authenticated call through it is visible
// (and counted) rather than a real network request.
const getStatus = vi.fn()
vi.mock('@/services/api', () => ({
  default: {
    getStatus: (...args: unknown[]) => getStatus(...args),
    hasAPIKey: vi.fn(() => false),
  },
}))

type FetchLog = { url: string; init?: RequestInit }

function jsonResponse(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

// Route each fetch by path so a test can script the probe and /auth/me
// independently and then read back the call ORDER.
function installFetch(routes: Record<string, () => Response>): FetchLog[] {
  const log: FetchLog[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
      log.push({ url, init })
      const path = url.replace(/\?.*$/, '')
      const handler = routes[path]
      if (!handler) return jsonResponse(404, { error: `unrouted ${path}` })
      return handler()
    }),
  )
  return log
}

/**
 * Spec 107 FR-030 / FR-041 (PR-B, T054): `GET /api/v1/auth/provider` is the
 * public, side-effect-free edition probe. A tenant holds no API key, so the
 * old edition detection (`GET /api/v1/status` with the key, critic G1) 401'd
 * for exactly the people the server edition exists for. The store must learn
 * the edition from the probe BEFORE any authenticated call, and the login page
 * must label its button with the operator-chosen `display_name`.
 */
describe('auth-api provider probe (Spec 107 FR-030)', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('GETs /api/v1/auth/provider and returns only the display_name', async () => {
    const log = installFetch({
      '/api/v1/auth/provider': () => jsonResponse(200, { display_name: 'Acme Okta' }),
    })
    const res = await authApi.getProvider()
    expect(res).toEqual({ display_name: 'Acme Okta' })
    expect(log).toHaveLength(1)
    expect(log[0].url).toBe('/api/v1/auth/provider')
    expect(log[0].init?.method ?? 'GET').toBe('GET')
    // Public probe: it carries no API key header.
    const headers = new Headers(log[0].init?.headers)
    expect(headers.get('X-API-Key')).toBeNull()
  })

  it('answers null on 404 — the personal build, or server_edition.enabled=false', async () => {
    installFetch({ '/api/v1/auth/provider': () => jsonResponse(404, { error: 'not found' }) })
    expect(await authApi.getProvider()).toBeNull()
  })

  it('answers null when the probe cannot be reached at all', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('network down') }))
    expect(await authApi.getProvider()).toBeNull()
  })
})

describe('auth store edition detection (Spec 107 FR-041)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getStatus.mockReset()
    getStatus.mockResolvedValue({ success: true, data: { edition: 'server' } })
  })
  afterEach(() => vi.unstubAllGlobals())

  it('learns the server edition from the probe before /auth/me, never from a keyed call', async () => {
    const log = installFetch({
      '/api/v1/auth/provider': () => jsonResponse(200, { display_name: 'Acme Okta' }),
      '/api/v1/auth/me': () =>
        jsonResponse(200, { id: 'u1', email: 'a@acme.test', display_name: 'A', role: 'user', provider: 'oidc' }),
    })
    const store = useAuthStore()
    await store.checkAuth()

    expect(store.isTeamsEdition).toBe(true)
    expect(store.provider?.display_name).toBe('Acme Okta')
    expect(store.isAuthenticated).toBe(true)
    expect(store.loading).toBe(false)

    const paths = log.map((l) => l.url.replace(/\?.*$/, ''))
    expect(paths.indexOf('/api/v1/auth/provider')).toBe(0)
    expect(paths.indexOf('/api/v1/auth/me')).toBeGreaterThan(paths.indexOf('/api/v1/auth/provider'))
    // The keyed status call is not how the edition is learned any more.
    expect(getStatus).not.toHaveBeenCalled()
    expect(paths).not.toContain('/api/v1/status')
  })

  it('treats a 404 from the probe as the personal edition and makes no authenticated call', async () => {
    const log = installFetch({
      '/api/v1/auth/provider': () => jsonResponse(404, { error: 'not found' }),
    })
    const store = useAuthStore()
    await store.checkAuth()

    expect(store.isTeamsEdition).toBe(false)
    expect(store.provider).toBeNull()
    expect(store.user).toBeNull()
    expect(store.loading).toBe(false)
    const paths = log.map((l) => l.url.replace(/\?.*$/, ''))
    expect(paths).toEqual(['/api/v1/auth/provider'])
    expect(getStatus).not.toHaveBeenCalled()
  })

  it('keeps the edition and label when the session is missing (the /login case)', async () => {
    installFetch({
      '/api/v1/auth/provider': () => jsonResponse(200, { display_name: 'Acme Okta' }),
      '/api/v1/auth/me': () => jsonResponse(401, { error: 'unauthorized' }),
    })
    const store = useAuthStore()
    await store.checkAuth()
    expect(store.isTeamsEdition).toBe(true)
    expect(store.provider?.display_name).toBe('Acme Okta')
    expect(store.isAuthenticated).toBe(false)
  })
})

describe('Login.vue provider label (Spec 107 FR-030)', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => vi.unstubAllGlobals())

  it('labels the button with the probe display_name instead of a hardcoded organization', async () => {
    const store = useAuthStore()
    store.provider = { display_name: 'Acme Okta' }
    const wrapper = mount(Login)
    const btn = wrapper.find('button')
    expect(btn.text()).toBe('Sign in with Acme Okta')
    expect(wrapper.text()).not.toContain('your organization')
  })

  it('reacts when the probe result lands after mount', async () => {
    const store = useAuthStore()
    const wrapper = mount(Login)
    store.provider = { display_name: 'Contoso Entra' }
    await wrapper.vm.$nextTick()
    expect(wrapper.find('button').text()).toBe('Sign in with Contoso Entra')
  })

  it('never leaks anything but the label (no issuer / client id in the DOM)', async () => {
    const store = useAuthStore()
    store.provider = { display_name: 'Acme Okta' }
    const wrapper = mount(Login)
    expect(wrapper.html()).not.toMatch(/issuer|client_id|tenant/i)
  })
})
