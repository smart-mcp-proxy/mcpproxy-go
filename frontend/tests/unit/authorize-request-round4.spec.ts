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

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

async function mountPage() {
  A.getOAuthAuthorization.mockImplementation(async () => detail())
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/authorize', component: AuthorizeRequest }] })
  await router.push('/authorize?request=r1')
  const w = mount(AuthorizeRequest, { global: { plugins: [router] } })
  await flushPromises()
  return w
}

beforeEach(() => {
  vi.clearAllMocks()
  A.hasAPIKey.mockReturnValue(true)
  A.previewOAuthAuthorization.mockImplementation(async (_id: string, body: any) => complete(body))
})

describe('Deny does not depend on the preview', () => {
  it('a pending preview that later answers binding_changed does not drop the Deny redirect', async () => {
    const pv = deferred<any>()
    A.previewOAuthAuthorization.mockImplementationOnce(() => pv.promise)
    const w = await mountPage()
    const deny = deferred<any>()
    A.denyOAuthAuthorization.mockImplementationOnce(() => deny.promise)
    await t(w, 'authorize-deny').trigger('click')
    const loads = A.getOAuthAuthorization.mock.calls.length
    pv.reject(new ApiError(409, { code: 'binding_changed', error: 'changed' }, 'x'))
    await flushPromises()
    expect(A.getOAuthAuthorization.mock.calls.length).toBe(loads) // no reload while deciding
    expect(t(w, 'authorize-deny').attributes('disabled')).toBeDefined()
    deny.resolve({ redirect_url: 'http://127.0.0.1:54321/callback?error=access_denied' })
    await flushPromises()
    expect(navigateTo).toHaveBeenCalledWith('http://127.0.0.1:54321/callback?error=access_denied')
  })

  it.each([
    ['failed', async () => { throw new Error('boom') }],
    ['incomplete', async () => ({ complete: false, guard: null })],
  ])('works with a %s preview', async (_n, impl) => {
    A.previewOAuthAuthorization.mockImplementation(impl)
    const w = await mountPage()
    A.denyOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?error=access_denied' })
    await t(w, 'authorize-deny').trigger('click')
    await flushPromises()
    expect(navigateTo).toHaveBeenCalledTimes(1)
  })

  it('restarts the preview after a recoverable decision failure', async () => {
    const pv = deferred<any>()
    A.previewOAuthAuthorization.mockImplementationOnce(() => pv.promise)
    const w = await mountPage()
    const deny = deferred<any>()
    A.denyOAuthAuthorization.mockImplementationOnce(() => deny.promise)
    await t(w, 'authorize-deny').trigger('click')
    pv.resolve(complete({ client: { id: 'cursor' } })) // obsolete: a decision is pending
    await flushPromises()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    const calls = A.previewOAuthAuthorization.mock.calls.length
    deny.reject(new Error('network down'))
    await flushPromises()
    expect(A.previewOAuthAuthorization.mock.calls.length).toBe(calls + 1)
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
  })
})

describe('a failed preview can be retried', () => {
  it('shows Check again, and Approve enables after a successful retry without remount', async () => {
    A.previewOAuthAuthorization.mockRejectedValueOnce(new Error('HTTP 503'))
    const w = await mountPage()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    expect(t(w, 'authorize-retry').exists()).toBe(true)
    await t(w, 'authorize-retry').trigger('click')
    await flushPromises()
    expect(t(w, 'authorize-retry').exists()).toBe(false)
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
    expect(t(w, 'authorize-access').exists()).toBe(true)
  })
})
