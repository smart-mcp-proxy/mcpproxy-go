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

function generic(id: string, over: Record<string, any> = {}) {
  return {
    id, status: 'pending', expires_at: '2026-11-23T12:10:00Z', reported_name: `App ${id}`,
    redirect_uri: 'http://127.0.0.1:54321/callback', resource: 'http://127.0.0.1:8080/mcp/oauth',
    address_kind: 'generic',
    existing_clients: [
      { id: 'codex', display_name: 'Codex', binding: { profile: 'ops', mode: 'switchable', revision: 'rc' } },
      { id: 'cursor', display_name: 'Cursor', binding: { profile: 'dev', mode: 'locked', revision: 'rv' } },
    ],
    suggested_new_client: { id: 'acme', display_name: 'Acme' },
    profiles: [{ name: 'ops' }, { name: 'dev' }], profile_required: true, guard: null, ...over,
  }
}

const complete = (body: any) => ({
  complete: true,
  access: { client: body.client?.id ?? body.client?.new?.id, profile: body.profile ?? '', mode: body.mode ?? 'locked', same_access_as_existing_client: !!body.binding_revision },
  guard: null,
})

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

async function mountAt(details: Record<string, any>) {
  A.getOAuthAuthorization.mockImplementation(async (id: string) => details[id])
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/authorize', component: AuthorizeRequest }] })
  await router.push('/authorize?request=r1')
  const w = mount(AuthorizeRequest, { global: { plugins: [router] } })
  await flushPromises()
  return { w, router }
}

const changed = () => new ApiError(409, { code: 'binding_changed', error: 'changed' }, 'x')

beforeEach(() => {
  vi.clearAllMocks()
  A.hasAPIKey.mockReturnValue(true)
  A.previewOAuthAuthorization.mockImplementation(async (_id: string, body: any) => complete(body))
})

describe('review-again reload validates what it keeps', () => {
  it('a kept named profile that no profiles remain for becomes All servers (select and body agree)', async () => {
    const { w } = await mountAt({ r1: generic('r1') })
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    // The next preview is refused; the reload finds that every profile was deleted.
    A.previewOAuthAuthorization.mockImplementationOnce(async () => { throw changed() })
    A.getOAuthAuthorization.mockImplementation(async () => generic('r1', { profiles: [], profile_required: false }))
    await t(w, 'authorize-new-id').setValue('acme2')
    await t(w, 'authorize-new-id').trigger('change')
    await flushPromises()
    expect((t(w, 'authorize-profile-select').element as HTMLSelectElement).value).toBe('__all__')
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization.mock.calls.at(-1)![1].profile).toBe('')
  })

  it('a kept unset choice with no profiles left becomes All servers', async () => {
    const { w } = await mountAt({ r1: generic('r1') })
    expect((t(w, 'authorize-profile-select').element as HTMLSelectElement).value).toBe('__unset__')
    A.getOAuthAuthorization.mockImplementation(async () => generic('r1', { profiles: [], profile_required: false }))
    A.previewOAuthAuthorization.mockImplementationOnce(async () => { throw changed() })
    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    await t(w, 'authorize-client-select').setValue('__new__')
    await flushPromises()
    expect((t(w, 'authorize-profile-select').element as HTMLSelectElement).value).toBe('__all__')
  })

  it('a kept named profile that was deleted (others remain) goes back to unset and Approve stays disabled', async () => {
    const { w } = await mountAt({ r1: generic('r1') })
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    A.previewOAuthAuthorization.mockImplementationOnce(async () => { throw changed() })
    A.getOAuthAuthorization.mockImplementation(async () => generic('r1', { profiles: [{ name: 'dev' }] }))
    await t(w, 'authorize-new-id').setValue('acme2')
    await t(w, 'authorize-new-id').trigger('change')
    await flushPromises()
    expect((t(w, 'authorize-profile-select').element as HTMLSelectElement).value).toBe('__unset__')
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
  })

  it('a kept replace flag does not survive a previous login that is gone', async () => {
    const prev = { grant_id: 'g1', client_id: 'cursor', approved_at: '2026-11-03T10:00:00Z' }
    const { w } = await mountAt({ r1: generic('r1', { previous_login: prev, suggested_client: { id: 'cursor', matched: 'id' } }) })
    await t(w, 'authorize-replace').setValue(false)
    A.previewOAuthAuthorization.mockImplementationOnce(async () => { throw changed() })
    A.getOAuthAuthorization.mockImplementation(async () => generic('r1', { previous_login: { ...prev, grant_id: 'g2' } }))
    await t(w, 'authorize-client-select').setValue('cursor')
    await t(w, 'authorize-client-select').trigger('change')
    // Force a preview on the same client by switching away and back.
    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    await t(w, 'authorize-client-select').setValue('cursor')
    await flushPromises()
    const box = t(w, 'authorize-replace')
    if (box.exists()) {
      A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
      await t(w, 'authorize-approve').trigger('click')
      await flushPromises()
      const sent = A.approveOAuthAuthorization.mock.calls.at(-1)![1]
      expect(sent.replace_grant === undefined).toBe(!(box.element as HTMLInputElement).checked)
    }
  })
})

describe('choice controls are frozen while a decision is pending or done', () => {
  const controls = ['authorize-client-select', 'authorize-profile-select']

  it('disables every choice control, and a late answer lands on the choice that was approved', async () => {
    const prev = { grant_id: 'g1', client_id: 'cursor', approved_at: '2026-11-03T10:00:00Z' }
    const { w } = await mountAt({ r1: generic('r1', { previous_login: prev, suggested_client: { id: 'cursor', matched: 'id' } }) })
    const d = deferred<any>()
    A.approveOAuthAuthorization.mockImplementationOnce(() => d.promise)
    await t(w, 'authorize-approve').trigger('click')
    for (const id of ['authorize-client-select', 'authorize-replace']) {
      expect(t(w, id).attributes('disabled'), id).toBeDefined()
    }
    const calls = A.previewOAuthAuthorization.mock.calls.length
    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    expect(A.previewOAuthAuthorization.mock.calls.length).toBe(calls)
    expect(t(w, 'authorize-access').text()).toContain('Cursor')
    d.resolve({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })
    await flushPromises()
    expect(navigateTo).toHaveBeenCalledTimes(1)
    // Terminal: still frozen.
    expect(t(w, 'authorize-client-select').attributes('disabled')).toBeDefined()
  })

  it('freezes the new-client fields, the profile and the lock', async () => {
    const { w } = await mountAt({ r1: generic('r1') })
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    const d = deferred<any>()
    A.approveOAuthAuthorization.mockImplementationOnce(() => d.promise)
    await t(w, 'authorize-approve').trigger('click')
    for (const id of [...controls, 'authorize-new-id', 'authorize-new-name', 'authorize-lock']) {
      expect(t(w, id).attributes('disabled'), id).toBeDefined()
    }
    d.reject(new Error('network down'))
    await flushPromises()
    for (const id of controls) expect(t(w, id).attributes('disabled'), id).toBeUndefined()
  })
})

describe('the review-again notice belongs to the request that was reloaded', () => {
  it('is not shown on r2 when the page navigated during the reload', async () => {
    const { w, router } = await mountAt({ r1: generic('r1'), r2: generic('r2') })
    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    const reload = deferred<any>()
    A.approveOAuthAuthorization.mockRejectedValueOnce(changed())
    A.getOAuthAuthorization.mockImplementation((id: string) => (id === 'r1' ? reload.promise : Promise.resolve(generic(id))))
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    await router.push('/authorize?request=r2')
    await flushPromises()
    reload.resolve(generic('r1'))
    await flushPromises()
    expect(t(w, 'authorize-changed').exists()).toBe(false)
    expect(t(w, 'authorize-reported').text()).toContain('App r2')
  })
})
