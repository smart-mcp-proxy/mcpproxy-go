import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import AuthorizeRequest from '@/views/AuthorizeRequest.vue'
import api, { ApiError } from '@/services/api'

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

function generic(over: Record<string, any> = {}) {
  return {
    id: 'r1', status: 'pending', expires_at: '2026-11-23T12:10:00Z', reported_name: 'Cursor',
    redirect_uri: 'http://127.0.0.1:54321/callback', resource: 'http://127.0.0.1:8080/mcp/oauth',
    address_kind: 'generic',
    existing_clients: [
      { id: 'codex', display_name: 'Codex', binding: { profile: 'ops', mode: 'switchable', revision: 'rc' } },
      { id: 'cursor', display_name: 'Cursor', binding: { profile: 'dev', mode: 'locked', revision: 'rv' } },
      { id: 'cursor-2', display_name: 'Cursor', binding: { profile: 'dev', mode: 'locked', revision: 'r2' } },
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

async function mountWith(detail: any) {
  A.getOAuthAuthorization.mockImplementation(async () => detail)
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

describe('untrusted names are isolated from the page copy', () => {
  it('puts the reported name in its own bdi with the disclaimer as the sibling after it', async () => {
    const name = 'cursor (official". verified by the mcpproxy tray. "x)'
    const w = await mountWith(generic({ reported_name: name, suggested_client: { id: 'cursor', matched: 'id' } }))
    const p = t(w, 'authorize-reported').element as HTMLElement
    const bdi = p.querySelector('bdi')!
    expect(bdi).not.toBeNull()
    expect(bdi.textContent).toBe(name)
    expect(bdi.nextSibling!.textContent).toBe('". MCPProxy cannot verify that.')
    expect(p.firstChild!.textContent).toBe('The app calls itself "')

    const note = t(w, 'authorize-suggestion-note').element as HTMLElement
    const nb = note.querySelector('bdi')!
    expect(nb.textContent).toBe(name)
    expect(nb.nextSibling!.textContent).toBe('". MCPProxy cannot verify that.')
  })

  it('also isolates the new-client name in the access sentence and the return address', async () => {
    const w = await mountWith(generic())
    await t(w, 'authorize-profile-select').setValue('ops')
    await flushPromises()
    const access = t(w, 'authorize-access').element as HTMLElement
    const bdis = access.querySelectorAll('bdi')
    expect(bdis[bdis.length - 1].textContent).toBe('Acme')
    expect((t(w, 'authorize-returns-to').element as HTMLElement).querySelector('bdi')!.textContent).toBe('http://127.0.0.1:54321/callback')
  })
})

describe('client options', () => {
  it('show the id next to the display name so two clients never look identical', async () => {
    const w = await mountWith(generic())
    const labels = t(w, 'authorize-client-select').findAll('option').map((o: any) => o.text())
    expect(labels).toContain('Cursor (cursor)')
    expect(labels).toContain('Cursor (cursor-2)')
    expect(labels).toContain('Codex (codex)')
  })
})

describe('previous logins per client', () => {
  it('offers replacement for the selected client, whichever login is newest overall', async () => {
    const prevCodex = { grant_id: 'gx', client_id: 'codex', approved_at: '2026-11-20T10:00:00Z' }
    const prevCursor = { grant_id: 'gc', client_id: 'cursor', approved_at: '2026-11-10T10:00:00Z' }
    const w = await mountWith(generic({ previous_login: prevCodex, previous_logins: [prevCodex, prevCursor] }))
    A.approveOAuthAuthorization.mockResolvedValue({ redirect_url: 'http://127.0.0.1:1/cb?code=c' })

    await t(w, 'authorize-client-select').setValue('cursor')
    await flushPromises()
    expect(t(w, 'authorize-replace').exists()).toBe(true)
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenLastCalledWith('r1', { client: { id: 'cursor' }, binding_revision: 'rv', replace_grant: 'gc' })

    const w2 = await mountWith(generic({ previous_login: prevCodex, previous_logins: [prevCodex, prevCursor] }))
    await t(w2, 'authorize-client-select').setValue('codex')
    await flushPromises()
    await t(w2, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(A.approveOAuthAuthorization).toHaveBeenLastCalledWith('r1', { client: { id: 'codex' }, binding_revision: 'rc', replace_grant: 'gx' })

    // A client with no login of its own gets no Replace option.
    const w3 = await mountWith(generic({ previous_login: prevCodex, previous_logins: [prevCodex, prevCursor] }))
    await t(w3, 'authorize-client-select').setValue('cursor-2')
    await flushPromises()
    expect(t(w3, 'authorize-replace').exists()).toBe(false)
  })
})

describe('client_not_found is an error on the choice, not an expired request', () => {
  it('from approve', async () => {
    const w = await mountWith(generic({ suggested_client: { id: 'cursor', matched: 'id' } }))
    A.approveOAuthAuthorization.mockRejectedValue(new ApiError(404, { code: 'client_not_found', error: 'unknown client' }, 'x'))
    await t(w, 'authorize-approve').trigger('click')
    await flushPromises()
    expect(t(w, 'authorize-gone').exists()).toBe(false)
    expect(t(w, 'authorize-error').text()).toContain('unknown client')
    expect(t(w, 'authorize-deny').attributes('disabled')).toBeUndefined()
    // Another choice can still be made.
    await t(w, 'authorize-client-select').setValue('codex')
    await flushPromises()
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeUndefined()
  })

  it('from preview', async () => {
    A.previewOAuthAuthorization.mockRejectedValue(new ApiError(404, { code: 'client_not_found', error: 'unknown client' }, 'x'))
    const w = await mountWith(generic({ suggested_client: { id: 'cursor', matched: 'id' } }))
    expect(t(w, 'authorize-gone').exists()).toBe(false)
    expect(t(w, 'authorize-error').text()).toContain('unknown client')
    expect(t(w, 'authorize-approve').attributes('disabled')).toBeDefined()
  })
})
