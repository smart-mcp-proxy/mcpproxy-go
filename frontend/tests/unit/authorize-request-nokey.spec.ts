import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import AuthorizeRequest from '@/views/AuthorizeRequest.vue'
import api from '@/services/api'

vi.mock('@/services/api', async (orig) => {
  const actual = await orig<typeof import('@/services/api')>()
  return {
    ...actual,
    default: {
      hasAPIKey: vi.fn(() => false),
      setAPIKey: vi.fn(),
      getOAuthAuthorization: vi.fn(),
      previewOAuthAuthorization: vi.fn(),
      approveOAuthAuthorization: vi.fn(),
      denyOAuthAuthorization: vi.fn(),
    },
  }
})
vi.mock('@/utils/navigate', () => ({ navigateTo: vi.fn() }))

const A = api as any

describe('AuthorizeRequest without an API key in storage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    A.hasAPIKey.mockReturnValue(false)
  })

  it('shows only the open-from-tray message, makes no request, and continues after a pasted key', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/authorize', component: AuthorizeRequest }] })
    await router.push('/authorize?request=r1')
    const w = mount(AuthorizeRequest, { global: { plugins: [router] } })
    await flushPromises()

    expect(w.text()).toContain('Open this request from the MCPProxy tray, or paste the API key')
    expect(w.find('[data-test="authorize-approve"]').exists()).toBe(false)
    expect(w.find('[data-test="authorize-deny"]').exists()).toBe(false)
    expect(A.getOAuthAuthorization).not.toHaveBeenCalled()
    expect(A.previewOAuthAuthorization).not.toHaveBeenCalled()

    A.getOAuthAuthorization.mockResolvedValue({
      id: 'r1', status: 'pending', expires_at: '2026-11-23T12:10:00Z', reported_name: 'Cursor',
      redirect_uri: 'http://127.0.0.1:1/cb', resource: 'http://127.0.0.1:8080/mcp/oauth/cursor', address_kind: 'client',
      client: { id: 'cursor', display_name: 'Cursor', exists: true, has_api_key: false, logins: 0, binding: { profile: '', mode: 'switchable', revision: 'r' } },
      profiles: [], profile_required: false, guard: null,
    })
    A.previewOAuthAuthorization.mockResolvedValue({ complete: true, access: { client: 'cursor', profile: '', mode: 'switchable', same_access_as_existing_client: true }, guard: null })

    await w.find('[data-test="authorize-key-input"]').setValue('  the-admin-key ')
    await w.find('[data-test="authorize-key-submit"]').trigger('click')
    await flushPromises()

    expect(A.setAPIKey).toHaveBeenCalledWith('the-admin-key')
    expect(A.getOAuthAuthorization).toHaveBeenCalledWith('r1')
    expect(w.find('[data-test="authorize-approve"]').exists()).toBe(true)
  })

  it('does not call the API when the request id is missing', async () => {
    A.hasAPIKey.mockReturnValue(true)
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/authorize', component: AuthorizeRequest }] })
    await router.push('/authorize')
    const w = mount(AuthorizeRequest, { global: { plugins: [router] } })
    await flushPromises()
    expect(A.getOAuthAuthorization).not.toHaveBeenCalled()
    expect(w.find('[data-test="authorize-gone"]').exists()).toBe(true)
  })
})
