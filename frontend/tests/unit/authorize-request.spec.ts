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

const bound = { profile: 'dev-readonly', mode: 'locked', revision: 'rev1' }

function base(over: Record<string, any> = {}) {
  return {
    id: 'r1', status: 'pending', expires_at: '2026-11-23T12:10:00Z', reported_name: 'Cursor IDE',
    redirect_uri: 'http://127.0.0.1:54321/callback', resource: 'http://127.0.0.1:8080/mcp/oauth/cursor',
    address_kind: 'client',
    client: { id: 'cursor', display_name: 'Cursor', exists: true, has_api_key: true, logins: 1, binding: bound },
    profiles: [{ name: 'dev-readonly' }, { name: 'ops' }], profile_required: false, guard: null,
    ...over,
  }
}

function generic(over: Record<string, any> = {}) {
  return base({
    address_kind: 'generic', client: undefined, resource: 'http://127.0.0.1:8080/mcp/oauth',
    existing_clients: [
      { id: 'codex', display_name: 'Codex', binding: { profile: 'ops', mode: 'switchable', revision: 'rc' } },
      { id: 'cursor', display_name: 'Cursor', binding: bound },
    ],
    suggested_new_client: { id: 'acme-agent', display_name: 'Acme Agent' },
    profile_required: true,
    ...over,
  })
}

async function mountPage(detail: any, query = '?request=r1') {
  A.getOAuthAuthorization.mockResolvedValue(detail)
  A.previewOAuthAuthorization.mockImplementation(async (_id: string, body: any) => {
    const client = body.client?.id ?? body.client?.new?.id
    if (!client) return { complete: false, guard: null }
    const existing = !!body.binding_revision
    return {
      complete: true,
      access: { client, profile: body.profile ?? 'dev-readonly', mode: body.mode ?? 'locked', same_access_as_existing_client: existing },
      guard: null,
    }
  })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/authorize', name: 'authorize', component: AuthorizeRequest },
      { path: '/settings', name: 'settings', component: { template: '<div />' } },
    ],
  })
  await router.push('/authorize' + query)
  await router.isReady()
  const w = mount(AuthorizeRequest, { global: { plugins: [router] } })
  await flushPromises()
  return w
}

const t = (w: any, id: string) => w.find(`[data-test="${id}"]`)

describe('AuthorizeRequest: per-client address', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    A.hasAPIKey.mockReturnValue(true)
  })

  it('fixes the client, labels the reported name as unverified and states the access', async () => {
    const w = await mountPage(base())
    expect(t(w, 'authorize-reported').text()).toContain('The app calls itself "Cursor IDE". MCPProxy cannot verify that.')
    expect(t(w, 'authorize-client-fixed').text()).toContain('Cursor')
    expect(t(w, 'authorize-client-select').exists()).toBe(false)
    expect(t(w, 'authorize-binding').text()).toContain('dev-readonly')
    expect(t(w, 'authorize-binding').text()).toContain('locked')
    expect(t(w, 'authorize-profile-select').exists()).toBe(false)
    expect(t(w, 'authorize-access').text()).toContain('the same access as Cursor')
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
  })

  it('never renders reported text as HTML', async () => {
    const w = await mountPage(base({ reported_name: '<img src=x onerror=alert(1)>' }))
    expect(w.find('img').exists()).toBe(false)
    expect(t(w, 'authorize-reported').text()).toContain('<img src=x onerror=alert(1)>')
  })

  it('disables Approve until a profile is chosen when profile_required', async () => {
    const detail = base({
      profile_required: true,
      client: { id: 'cursor', display_name: 'Cursor', exists: false, has_api_key: false, logins: 0 },
    })
    const w = await mountPage(detail)
    expect(t(w, 'authorize-binding').exists()).toBe(false)
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    expect(A.previewOAuthAuthorization).toHaveBeenLastCalledWith('r1', expect.objectContaining({ client: { id: 'cursor' }, profile: 'ops', mode: 'locked' }))
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
  })

  it('offers the replace-login checkbox only with previous_login, pre-checked, and sends it', async () => {
    const without = await mountPage(base())
    expect(t(without, 'authorize-replace').exists()).toBe(false)

    const w = await mountPage(base({ previous_login: { grant_id: 'g1', approved_at: '2026-11-03T10:00:00Z' } }))
    const box = t(w, 'authorize-replace')
    expect(box.exists()).toBe(true)
    expect((box.element as HTMLInputElement).checked).toBe(true)
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:54321/callback?code=c&state=s' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenCalledWith('r1', expect.objectContaining({
      client: { id: 'cursor' }, binding_revision: 'rev1', replace_grant: 'g1',
    }))
    expect(navigateTo).toHaveBeenCalledWith('http://127.0.0.1:54321/callback?code=c&state=s')

    await box.setValue(false)
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization.mock.calls.at(-1)![1].replace_grant).toBeUndefined()
  })

  it('fetches the guard verdict through preview, shows the refusal with its fixes and keeps Approve disabled', async () => {
    const detail = base()
    A.getOAuthAuthorization.mockResolvedValue(detail)
    A.previewOAuthAuthorization.mockResolvedValue({
      complete: true,
      access: { client: 'cursor', profile: 'dev-readonly', mode: 'locked', same_access_as_existing_client: true },
      guard: { code: 'binding_bypassable_without_auth', bindings: [{ client_id: 'cursor', token_name: 'client-cursor', profile: 'dev-readonly', mode: 'locked' }], fixes: [{ kind: 'require_mcp_auth' }] },
    })
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/authorize', component: AuthorizeRequest }, { path: '/settings', component: { template: '<div />' } }] })
    await router.push('/authorize?request=r1')
    const w = mount(AuthorizeRequest, { global: { plugins: [router] } })
    await flushPromises()
    expect(A.previewOAuthAuthorization).toHaveBeenCalled()
    expect(w.find('[data-test="guard-refusal"]').exists()).toBe(true)
    expect(w.find('[data-test="guard-fix-require_mcp_auth"]').exists()).toBe(true)
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
  })

  it('shows the refusal when approve itself answers binding_bypassable_without_auth', async () => {
    const w = await mountPage(base())
    A.approveOAuthAuthorization.mockRejectedValue(new ApiError(409, {
      code: 'binding_bypassable_without_auth', error: 'would leave a client reachable without authentication',
      bindings: [], fixes: [{ kind: 'require_mcp_auth' }],
    }, 'x'))
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(w.find('[data-test="guard-refusal"]').exists()).toBe(true)
    expect(navigateTo).not.toHaveBeenCalled()
  })

  it('handles already_decided, binding_changed and 404', async () => {
    const w = await mountPage(base())
    A.approveOAuthAuthorization.mockRejectedValueOnce(new ApiError(409, { code: 'already_decided', error: 'decided' }, 'x'))
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(t(w, 'authorize-decided').exists()).toBe(true)
    expect(t(w, 'authorize-approve').exists()).toBe(false)

    const w2 = await mountPage(base())
    const calls = A.getOAuthAuthorization.mock.calls.length
    A.approveOAuthAuthorization.mockRejectedValueOnce(new ApiError(409, { code: 'binding_changed', error: 'changed' }, 'x'))
    await t(w2, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.getOAuthAuthorization.mock.calls.length).toBe(calls + 1)
    expect(t(w2, 'authorize-changed').exists()).toBe(true)
    expect(navigateTo).not.toHaveBeenCalled()

    A.getOAuthAuthorization.mockRejectedValueOnce(new ApiError(404, { error: 'not found' }, 'x'))
    const w3 = await mountPage(base())
    expect(t(w3, 'authorize-gone').exists()).toBe(true)
    expect(t(w3, 'authorize-approve').exists()).toBe(false)
  })

  it('deny navigates to the returned redirect', async () => {
    const w = await mountPage(base())
    A.denyOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:54321/callback?error=access_denied' })
    await t(w, 'authorize-deny').trigger('click')
    await flushPromises()
    expect(A.denyOAuthAuthorization).toHaveBeenCalledWith('r1')
    expect(navigateTo).toHaveBeenCalledWith('http://127.0.0.1:54321/callback?error=access_denied')
  })
})

describe('AuthorizeRequest: generic address', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    A.hasAPIKey.mockReturnValue(true)
  })

  it('selects "New client" with the id and name filled from the reported name when nothing is suggested', async () => {
    const w = await mountPage(generic())
    expect((t(w, 'authorize-client-select').element as HTMLSelectElement).value).toBe('__new__')
    expect((t(w, 'authorize-new-id').element as HTMLInputElement).value).toBe('acme-agent')
    expect((t(w, 'authorize-new-name').element as HTMLInputElement).value).toBe('Acme Agent')
    expect(t(w, 'authorize-suggestion-note').exists()).toBe(false)
    // Profiles exist and the client is new: nothing pre-selected, Approve disabled.
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
  })

  it('opens with the suggested client selected and says why', async () => {
    const w = await mountPage(generic({ reported_name: 'Cursor', suggested_client: { id: 'cursor', matched: 'display_name' } }))
    expect((t(w, 'authorize-client-select').element as HTMLSelectElement).value).toBe('cursor')
    expect(t(w, 'authorize-suggestion-note').text()).toBe('Suggested because the app calls itself "Cursor". MCPProxy cannot verify that.')
    expect(t(w, 'authorize-new-id').exists()).toBe(false)
    expect(t(w, 'authorize-access').text()).toContain('the same access as Cursor')
    expect(A.previewOAuthAuthorization).toHaveBeenLastCalledWith('r1', expect.objectContaining({ client: { id: 'cursor' }, binding_revision: 'rev1' }))
  })

  it('sends the client the user changed to, not the suggestion', async () => {
    const w = await mountPage(generic({ reported_name: 'Cursor', suggested_client: { id: 'cursor', matched: 'id' } }))
    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    expect(t(w, 'authorize-suggestion-note').exists()).toBe(false)
    expect(A.previewOAuthAuthorization).toHaveBeenLastCalledWith('r1', expect.objectContaining({ client: { id: 'codex' }, binding_revision: 'rc' }))
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:54321/callback?code=c' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenCalledWith('r1', expect.objectContaining({ client: { id: 'codex' }, binding_revision: 'rc' }))
  })

  it('creates a new client with the edited id and name', async () => {
    const w = await mountPage(generic())
    const id = t(w, 'authorize-new-id')
    await id.setValue('my-agent')
    await id.trigger('change')
    const name = t(w, 'authorize-new-name')
    await name.setValue('My Agent')
    await name.trigger('change')
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:54321/callback?code=c' })
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenCalledWith('r1', expect.objectContaining({
      client: { new: { id: 'my-agent', display_name: 'My Agent' } }, profile: 'ops', mode: 'locked',
    }))
  })

  it.each([
    ['identity_exists', 'A client with this id already exists'],
    ['reserved_identity', 'This id belongs to a supported client'],
  ])('shows %s next to the id and does not navigate', async (code, text) => {
    const w = await mountPage(generic())
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    A.approveOAuthAuthorization.mockRejectedValue(new ApiError(422, { code, error: text }, 'x'))
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(t(w, 'authorize-error').text()).toContain(text)
    expect(navigateTo).not.toHaveBeenCalled()
  })

  it('shows a profile-bound existing client without asking for a profile', async () => {
    const w = await mountPage(generic())
    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    expect(t(w, 'authorize-binding').text()).toContain('ops')
    expect(t(w, 'authorize-profile-select').exists()).toBe(false)
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
  })
})
