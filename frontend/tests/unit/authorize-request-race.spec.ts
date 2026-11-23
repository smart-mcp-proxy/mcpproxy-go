import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import AuthorizeRequest from '@/views/AuthorizeRequest.vue'
import api from '@/services/api'
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
const bound = { profile: 'dev-readonly', mode: 'locked', revision: 'rev1' }
const t = (w: any, id: string) => w.find(`[data-test="${id}"]`)

function detail(id: string, over: Record<string, any> = {}) {
  return {
    id, status: 'pending', expires_at: '2026-11-23T12:10:00Z', reported_name: 'Cursor IDE',
    redirect_uri: 'http://127.0.0.1:54321/callback', resource: 'http://127.0.0.1:8080/mcp/oauth/cursor',
    address_kind: 'client',
    client: { id: 'cursor', display_name: 'Cursor', exists: true, has_api_key: true, logins: 1, binding: bound },
    profiles: [{ name: 'dev-readonly' }, { name: 'ops' }], profile_required: false, guard: null, ...over,
  }
}

function generic(id: string, over: Record<string, any> = {}) {
  return detail(id, {
    address_kind: 'generic', client: undefined, resource: 'http://127.0.0.1:8080/mcp/oauth',
    existing_clients: [
      { id: 'codex', display_name: 'Codex', binding: { profile: 'ops', mode: 'switchable', revision: 'rc' } },
      { id: 'cursor', display_name: 'Cursor', binding: bound },
    ],
    suggested_new_client: { id: 'acme-agent', display_name: 'Acme Agent' },
    profile_required: true, ...over,
  })
}

const complete = (body: any) => ({
  complete: true,
  access: { client: body.client?.id ?? body.client?.new?.id, profile: body.profile ?? 'dev-readonly', mode: body.mode ?? 'locked', same_access_as_existing_client: !!body.binding_revision },
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
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/authorize', component: AuthorizeRequest }, { path: '/settings', component: { template: '<div />' } }] })
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

describe('AuthorizeRequest: request id changes in the same component', () => {
  it('drops r1 at once, shows r2 and binds both decisions to the displayed request', async () => {
    const slow = deferred<any>()
    const { w, router } = await mountAt({ r1: detail('r1', { reported_name: 'First App' }), r2: detail('r2', { reported_name: 'Second App' }) })
    expect(t(w, 'authorize-reported').text()).toContain('First App')

    A.getOAuthAuthorization.mockImplementation((id: string) => (id === 'r2' ? slow.promise : Promise.resolve(detail(id))))
    await router.push('/authorize?request=r2')
    await flushPromises()
    // r2 is still loading: r1 must not be on screen or decidable.
    expect(w.text()).not.toContain('First App')
    expect(t(w, 'authorize-approve').exists()).toBe(false)
    expect(t(w, 'authorize-deny').exists()).toBe(false)

    slow.resolve(detail('r2', { reported_name: 'Second App' }))
    await flushPromises()
    expect(t(w, 'authorize-reported').text()).toContain('Second App')
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenCalledWith('r2', expect.anything())
    expect(A.approveOAuthAuthorization).not.toHaveBeenCalledWith('r1', expect.anything())
  })

  it('ignores a late answer for the request that was left', async () => {
    const late = deferred<any>()
    A.getOAuthAuthorization.mockImplementation((id: string) => (id === 'r1' ? late.promise : Promise.resolve(detail(id, { reported_name: 'Second App' }))))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/authorize', component: AuthorizeRequest }] })
    await router.push('/authorize?request=r1')
    const w = mount(AuthorizeRequest, { global: { plugins: [router] } })
    await flushPromises()
    await router.push('/authorize?request=r2')
    await flushPromises()
    late.resolve(detail('r1', { reported_name: 'First App' }))
    await flushPromises()
    expect(t(w, 'authorize-reported').text()).toContain('Second App')
    A.denyOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?error=access_denied' })
    await t(w, 'authorize-deny').trigger('click')
    await flushPromises()
    expect(A.denyOAuthAuthorization).toHaveBeenCalledWith('r2')
  })
})

describe('AuthorizeRequest: Approve needs a complete, current preview', () => {
  it('stays disabled on {complete:false} and while the preview is pending', async () => {
    const first = deferred<any>()
    A.previewOAuthAuthorization.mockImplementation(() => first.promise)
    const { w } = await mountAt({ r1: detail('r1') })
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    first.resolve({ complete: false, guard: null })
    await flushPromises()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    expect(t(w, 'authorize-access').exists()).toBe(false)
  })

  it('ignores an out-of-order preview and sends the body the accepted preview was for', async () => {
    const p1 = deferred<any>()
    const p2 = deferred<any>()
    const pending: Array<[any, ReturnType<typeof deferred<any>>]> = []
    A.previewOAuthAuthorization.mockImplementation((_id: string, body: any) => {
      const d = pending.length === 0 ? p1 : p2
      pending.push([body, d])
      return d.promise
    })
    const { w } = await mountAt({ r1: generic('r1') })
    // First preview (new client acme-agent, no profile yet) is not even asked: incomplete. Choose ops.
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    expect(pending).toHaveLength(1)
    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    expect(pending).toHaveLength(2)
    // The newer preview (codex) answers first; the stale one (acme-agent) arrives late.
    p2.resolve(complete(pending[1][0]))
    await flushPromises()
    p1.resolve(complete(pending[0][0]))
    await flushPromises()
    expect(t(w, 'authorize-access').text()).toContain('Codex')
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenCalledWith('r1', pending[1][0])
  })

  it('disables Approve again as soon as the choice changes, until its preview answers', async () => {
    const { w } = await mountAt({ r1: generic('r1') })
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
    const next = deferred<any>()
    A.previewOAuthAuthorization.mockImplementation(() => next.promise)
    await t(w, 'authorize-client-select').setValue('codex')
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    expect(t(w, 'authorize-access').exists()).toBe(false)
    next.resolve(complete({ client: { id: 'codex' }, binding_revision: 'rc' }))
    await flushPromises()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
  })
})

describe('AuthorizeRequest: replacing the previous login', () => {
  const prev = { grant_id: 'gc', client_id: 'cursor', approved_at: '2026-11-03T10:00:00Z' }

  it('offers it only for the client that owns it, and sends the exact body', async () => {
    const { w } = await mountAt({ r1: generic('r1', { previous_login: prev, suggested_client: { id: 'cursor', matched: 'id' } }) })
    expect(t(w, 'authorize-replace').exists()).toBe(true)

    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    expect(t(w, 'authorize-replace').exists()).toBe(false)
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenLastCalledWith('r1', { client: { id: 'codex' }, binding_revision: 'rc' })
  })

  it('does not show or send it for a new client, and brings it back for the owner', async () => {
    const { w } = await mountAt({ r1: generic('r1', { previous_login: prev, suggested_client: { id: 'cursor', matched: 'id' } }) })
    await t(w, 'authorize-client-select').setValue('__new__')
    await flushPromises()
    expect(t(w, 'authorize-replace').exists()).toBe(false)
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    expect(A.previewOAuthAuthorization.mock.calls.at(-1)![1].replace_grant).toBeUndefined()
    await t(w, 'authorize-client-select').setValue('cursor')
    await flushPromises()
    expect(t(w, 'authorize-replace').exists()).toBe(true)
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenLastCalledWith('r1', { client: { id: 'cursor' }, binding_revision: 'rev1', replace_grant: 'gc' })
  })
})

describe('AuthorizeRequest: new-client draft', () => {
  it('is populated from suggested_new_client when the page opened on a suggested client', async () => {
    const { w } = await mountAt({ r1: generic('r1', { suggested_client: { id: 'cursor', matched: 'id' } }) })
    expect(t(w, 'authorize-new-id').exists()).toBe(false)
    await t(w, 'authorize-client-select').setValue('__new__')
    await flushPromises()
    expect((t(w, 'authorize-new-id').element as HTMLInputElement).value).toBe('acme-agent')
    expect((t(w, 'authorize-new-name').element as HTMLInputElement).value).toBe('Acme Agent')
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenLastCalledWith('r1', {
      client: { new: { id: 'acme-agent', display_name: 'Acme Agent' } }, profile: 'ops', mode: 'locked',
    })
  })
})

describe('AuthorizeRequest: one decision per request', () => {
  it('ignores repeated and mixed clicks while a decision is in flight, and stays decided after success', async () => {
    const d = deferred<any>()
    const { w } = await mountAt({ r1: detail('r1') })
    A.approveOAuthAuthorization.mockImplementation(() => d.promise)
    await t(w, 'authorize-approve').trigger('click')
    await t(w, 'authorize-approve').trigger('click')
    await t(w, 'authorize-deny').trigger('click')
    expect(A.approveOAuthAuthorization).toHaveBeenCalledTimes(1)
    expect(A.denyOAuthAuthorization).not.toHaveBeenCalled()
    d.resolve({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
    await flushPromises()
    expect(navigateTo).toHaveBeenCalledTimes(1)
    // Terminal: the buttons stay disabled and no second decision is possible.
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    expect(t(w, 'authorize-deny').attributes('disabled')).toBeDefined()
    await t(w, 'authorize-approve').trigger('click')
    await t(w, 'authorize-deny').trigger('click')
    expect(A.approveOAuthAuthorization).toHaveBeenCalledTimes(1)
    expect(A.denyOAuthAuthorization).not.toHaveBeenCalled()
  })

  it('guards Deny too, and releases the buttons after a recoverable failure', async () => {
    const d = deferred<any>()
    const { w } = await mountAt({ r1: detail('r1') })
    A.denyOAuthAuthorization.mockImplementation(() => d.promise)
    await t(w, 'authorize-deny').trigger('click')
    await t(w, 'authorize-deny').trigger('click')
    await t(w, 'authorize-approve').trigger('click')
    expect(A.denyOAuthAuthorization).toHaveBeenCalledTimes(1)
    expect(A.approveOAuthAuthorization).not.toHaveBeenCalled()
    d.reject(new Error('network down'))
    await flushPromises()
    expect(t(w, 'authorize-error').text()).toContain('network down')
    expect(t(w, 'authorize-deny').attributes('disabled')).toBeUndefined()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
    expect(navigateTo).not.toHaveBeenCalled()
  })
})
