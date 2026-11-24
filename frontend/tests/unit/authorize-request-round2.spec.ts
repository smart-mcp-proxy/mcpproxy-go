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

function detail(id: string, over: Record<string, any> = {}) {
  return {
    id, status: 'pending', expires_at: '2026-11-23T12:10:00Z', reported_name: `App ${id}`,
    redirect_uri: 'http://127.0.0.1:54321/callback', resource: 'http://127.0.0.1:8080/mcp/oauth/cursor',
    address_kind: 'client',
    client: { id: 'cursor', display_name: 'Cursor', exists: true, has_api_key: true, logins: 1, binding: { profile: 'dev', mode: 'locked', revision: 'rev1' } },
    profiles: [{ name: 'dev' }], profile_required: false, guard: null, ...over,
  }
}

const complete = (body: any) => ({
  complete: true,
  access: { client: body.client?.id ?? body.client?.new?.id, profile: body.profile ?? 'dev', mode: body.mode ?? 'locked', same_access_as_existing_client: !!body.binding_revision },
  guard: null,
})

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

async function mountAt(details: Record<string, any>, query = '?request=r1') {
  A.getOAuthAuthorization.mockImplementation(async (id: string) => details[id])
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/authorize', component: AuthorizeRequest }] })
  await router.push('/authorize' + query)
  const w = mount(AuthorizeRequest, { global: { plugins: [router] } })
  await flushPromises()
  return { w, router }
}

beforeEach(() => {
  vi.clearAllMocks()
  A.hasAPIKey.mockReturnValue(true)
  A.previewOAuthAuthorization.mockImplementation(async (_id: string, body: any) => complete(body))
})

describe('obsolete decision responses are ignored', () => {
  async function pendingApproveThenNavigate() {
    const d = deferred<any>()
    const ctx = await mountAt({ r1: detail('r1'), r2: detail('r2') })
    A.approveOAuthAuthorization.mockImplementationOnce(() => d.promise)
    await t(ctx.w, 'authorize-approve').trigger('click')
    await ctx.router.push('/authorize?request=r2')
    await flushPromises()
    return { ...ctx, d }
  }

  it('a successful r1 answer neither navigates nor disables r2', async () => {
    const { w, d } = await pendingApproveThenNavigate()
    d.resolve({ redirect_url: 'http://127.0.0.1:1/cb?code=r1' })
    await flushPromises()
    expect(navigateTo).not.toHaveBeenCalled()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
    expect(t(w, 'authorize-deny').attributes('disabled')).toBeUndefined()
  })

  it('an already_decided / 404 answer for r1 does not hide r2', async () => {
    const { w, d } = await pendingApproveThenNavigate()
    d.reject(new ApiError(409, { code: 'already_decided', error: 'decided' }, 'x'))
    await flushPromises()
    expect(t(w, 'authorize-decided').exists()).toBe(false)
    expect(t(w, 'authorize-gone').exists()).toBe(false)
    expect(t(w, 'authorize-approve').exists()).toBe(true)
  })

  it("a failure of r1 does not release r2's own pending decision", async () => {
    const { w, d } = await pendingApproveThenNavigate()
    const d2 = deferred<any>()
    A.approveOAuthAuthorization.mockImplementationOnce(() => d2.promise)
    await t(w, 'authorize-approve').trigger('click')
    expect(A.approveOAuthAuthorization).toHaveBeenCalledTimes(2)
    d.reject(new Error('network down'))
    await flushPromises()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    await t(w, 'authorize-approve').trigger('click')
    expect(A.approveOAuthAuthorization).toHaveBeenCalledTimes(2)
    expect(t(w, 'authorize-error').exists()).toBe(false)
  })

  it('a deny answer for r1 is ignored after navigating to r2', async () => {
    const d = deferred<any>()
    const { w, router } = await mountAt({ r1: detail('r1'), r2: detail('r2') })
    A.denyOAuthAuthorization.mockImplementationOnce(() => d.promise)
    await t(w, 'authorize-deny').trigger('click')
    await router.push('/authorize?request=r2')
    await flushPromises()
    d.resolve({ redirect_url: 'http://127.0.0.1:1/cb?error=access_denied' })
    await flushPromises()
    expect(navigateTo).not.toHaveBeenCalled()
    expect(t(w, 'authorize-deny').attributes('disabled')).toBeUndefined()
  })

  it('an answer that arrives after the component is unmounted does nothing', async () => {
    const d = deferred<any>()
    const { w } = await mountAt({ r1: detail('r1') })
    A.approveOAuthAuthorization.mockImplementationOnce(() => d.promise)
    await t(w, 'authorize-approve').trigger('click')
    w.unmount()
    d.resolve({ redirect_url: 'http://127.0.0.1:1/cb?code=late' })
    await flushPromises()
    expect(navigateTo).not.toHaveBeenCalled()
  })
})

describe('binding_changed from the preview call', () => {
  it('on a generic page: a refused preview reloads with the new revision', async () => {
    const generic = (rev: string) => detail('r1', {
      address_kind: 'generic', client: undefined,
      existing_clients: [{ id: 'codex', display_name: 'Codex', binding: { profile: 'ops', mode: 'switchable', revision: rev } }],
      suggested_new_client: { id: 'acme', display_name: 'Acme' }, profile_required: true,
    })
    const { w } = await mountAt({ r1: generic('rc1') })
    A.getOAuthAuthorization.mockImplementation(async () => generic('rc2'))
    A.previewOAuthAuthorization.mockImplementation(async (_id: string, body: any) => {
      if (body.binding_revision === 'rc1') throw new ApiError(409, { code: 'binding_changed', error: 'changed' }, 'x')
      return complete(body)
    })
    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    expect(t(w, 'authorize-changed').exists()).toBe(true)
    expect(A.previewOAuthAuthorization.mock.calls.at(-1)![1].binding_revision).toBe('rc2')
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
  })
})

describe('unbound client with no profiles configured', () => {
  it('shows "All servers" selected and submits the empty profile', async () => {
    const d = detail('r1', {
      profiles: [], profile_required: false,
      client: { id: 'cursor', display_name: 'Cursor', exists: false, has_api_key: false, logins: 0 },
    })
    const { w } = await mountAt({ r1: d })
    const select = t(w, 'authorize-profile-select')
    expect((select.element as HTMLSelectElement).value).toBe('__all__')
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenCalledWith('r1', { client: { id: 'cursor' }, profile: '' })
  })
})
