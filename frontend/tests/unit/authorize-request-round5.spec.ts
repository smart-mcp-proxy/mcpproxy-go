import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import AuthorizeRequest from '@/views/AuthorizeRequest.vue'
import api, { ApiError } from '@/services/api'
import { navigateTo } from '@/utils/navigate'

vi.mock('@/services/api', async (orig) => {
  const actual = await orig<typeof import('@/services/api')>()
  return {
    ...actual,
    default: {
      hasAPIKey: vi.fn(() => true),
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
const t = (w: any, id: string) => w.find(`[data-test="${id}"]`)

const detail = () => ({
  id: 'r1', status: 'pending', expires_at: '2026-11-23T12:10:00Z', reported_name: 'App',
  redirect_uri: 'http://127.0.0.1:54321/callback', resource: 'http://127.0.0.1:8080/mcp/oauth/cursor',
  address_kind: 'client',
  client: { id: 'cursor', display_name: 'Cursor', exists: true, has_api_key: true, logins: 1, binding: { profile: 'dev', mode: 'locked', revision: 'rev1' } },
  profiles: [{ name: 'dev' }], profile_required: false, guard: null,
})

const complete = (body: any) => ({
  complete: true,
  access: { client: body.client?.id, profile: 'dev', mode: 'locked', same_access_as_existing_client: true },
  guard: null,
})

const changed = () => new ApiError(409, { code: 'binding_changed', error: 'changed' }, 'x')

async function mountPage() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/authorize', component: AuthorizeRequest }] })
  await router.push('/authorize?request=r1')
  const w = mount(AuthorizeRequest, { global: { plugins: [router] } })
  await flushPromises()
  return w
}

beforeEach(() => {
  vi.clearAllMocks()
  A.hasAPIKey.mockReturnValue(true)
  A.getOAuthAuthorization.mockImplementation(async () => detail())
  A.previewOAuthAuthorization.mockImplementation(async (_id: string, body: any) => complete(body))
})

describe('binding_changed from preview reloads at most once', () => {
  it('stops after one automatic reload, keeps Deny, shows the notice and offers Check again', async () => {
    A.previewOAuthAuthorization.mockRejectedValue(changed())
    const w = await mountPage()
    // initial GET + one automatic reload; initial preview + the reload's preview.
    expect(A.getOAuthAuthorization).toHaveBeenCalledTimes(2)
    expect(A.previewOAuthAuthorization).toHaveBeenCalledTimes(2)
    expect(t(w, 'authorize-changed').exists()).toBe(true)
    expect(t(w, 'authorize-retry').exists()).toBe(true)
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    expect(t(w, 'authorize-deny').attributes('disabled')).toBeUndefined()

    A.denyOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:54321/callback?error=access_denied' })
    await t(w, 'authorize-deny').trigger('click')
    await flushPromises()
    expect(navigateTo).toHaveBeenCalledWith('http://127.0.0.1:54321/callback?error=access_denied')
    expect(A.getOAuthAuthorization).toHaveBeenCalledTimes(2)
  })

  it('Check again is an explicit user action that can recover', async () => {
    A.previewOAuthAuthorization.mockRejectedValueOnce(changed()).mockRejectedValueOnce(changed())
    const w = await mountPage()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    await t(w, 'authorize-retry').trigger('click')
    await flushPromises()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
  })
})

describe('a failed review-again reload can be retried', () => {
  it('shows Try again for a transient failure (not for an expired request) and recovers', async () => {
    const w = await mountPage()
    A.approveOAuthAuthorization.mockRejectedValueOnce(changed())
    A.getOAuthAuthorization.mockRejectedValueOnce(new ApiError(503, { error: 'unavailable' }, 'x'))
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(t(w, 'authorize-load-error').exists()).toBe(true)
    expect(t(w, 'authorize-load-retry').exists()).toBe(true)
    await t(w, 'authorize-load-retry').trigger('click')
    await flushPromises()
    expect(t(w, 'authorize-load-error').exists()).toBe(false)
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
    expect(t(w, 'authorize-deny').exists()).toBe(true)
  })

  it('an expired request (404) has no retry', async () => {
    A.getOAuthAuthorization.mockRejectedValue(new ApiError(404, { error: 'not found' }, 'x'))
    const w = await mountPage()
    expect(t(w, 'authorize-gone').exists()).toBe(true)
    expect(t(w, 'authorize-load-retry').exists()).toBe(false)
  })
})
