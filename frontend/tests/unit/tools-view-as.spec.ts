import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { WORK_RO, makeClient } from './fixtures/profiles108i'

// Spec 108-j T104/T106 (FR-032, FR-046; url-filter-contract.md rules 1, 5, 7, 8):
// the Tools page sends `client`/`profile` to GET /tools, renders the subject's
// verdict per row for an administrator and a count for a tenant, and never
// flashes an unfiltered list first.

const getGlobalToolsMock = vi.hoisted(() => vi.fn())
const getStatusMock = vi.hoisted(() => vi.fn())
const explainAccessMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    getGlobalTools: getGlobalToolsMock,
    getStatus: getStatusMock,
    explainAccess: explainAccessMock,
    getProfiles: vi.fn(() => Promise.resolve({ profiles: [] })),
    getClients: vi.fn(() => Promise.resolve({ success: true, data: { clients: [] } })),
    getToolApprovals: vi.fn(() => Promise.resolve({ success: true, data: { approvals: [] } })),
  },
}))

const GO_SRC = readFileSync(resolve(__dirname, '../../../internal/profile/access.go'), 'utf8')
const ALL_REASONS = [...GO_SRC.matchAll(/AccessReason[A-Za-z]+\s+AccessReason = "([a-z_]+)"/g)].map(m => m[1])

const ok = (data: unknown) => Promise.resolve({ success: true, data })
const stats = (tools: unknown[]) => ({ total: tools.length, enabled: tools.length, disabled: 0, pending_approval: 0 })

function row(name: string, server: string, access?: { visible: boolean; callable: boolean; reason?: string }, extra: Record<string, unknown> = {}) {
  return { name, server_name: server, description: '', tier: 'read', approval_status: 'approved', disabled: false, config_denied: false, usage: 0, ...(access ? { access } : {}), ...extra }
}

const stub = { template: '<div />' }

async function mountToolsAt(path: string, options: { tenant?: boolean } = {}) {
  const Tools = (await import('@/views/Tools.vue')).default
  const { useAuthStore } = await import('@/stores/auth')
  const { useProfilesStore } = await import('@/stores/profiles')
  const { useClientsStore } = await import('@/stores/clients')
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: stub },
      { path: '/tools', name: 'tools', component: Tools },
      { path: '/servers', name: 'servers', component: stub },
      { path: '/servers/:serverName', component: stub },
      { path: '/activity', name: 'activity', component: stub },
      { path: '/clients', name: 'clients', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
      { path: '/review', name: 'review', component: stub },
      { path: '/settings', name: 'settings', component: stub },
    ],
  })
  await router.push(path)
  await router.isReady()
  if (options.tenant) {
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.user = { id: 'c', email: 'c@x', display_name: 'C', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' } as any
  }
  useProfilesStore().profiles = [WORK_RO] as any
  useProfilesStore().loaded = true
  useClientsStore().clients = [makeClient('cursor', { profile: 'work-ro' } as any)] as any
  const wrapper = mount(Tools, { global: { plugins: [router] } })
  await flushPromises()
  await flushPromises()
  return { wrapper, router }
}

describe('Tools view-as (Spec 108-j)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['profile', 'client', 'token'])
    getStatusMock.mockResolvedValue({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
    const tools = [row('list_issues', 'github'), row('create_issue', 'github')]
    getGlobalToolsMock.mockImplementation(() => ok({ tools, stats: stats(tools) }))
  })

  it('?client=cursor calls getGlobalTools once with {client}, never without it', async () => {
    await mountToolsAt('/tools?client=cursor')
    expect(getGlobalToolsMock).toHaveBeenCalledTimes(1)
    expect(getGlobalToolsMock).toHaveBeenCalledWith({ client: 'cursor' })
  })

  it('no unfiltered flash: with /status still pending, the first request waits and then carries the param', async () => {
    setAvailableFeatures([])
    let resolveStatus!: (v: unknown) => void
    getStatusMock.mockReturnValue(new Promise(r => { resolveStatus = r }))
    const { useSystemStore } = await import('@/stores/system')
    const system = useSystemStore()
    void system.fetchScopeFilterFeatures()

    await mountToolsAt('/tools?client=cursor')
    expect(getGlobalToolsMock).not.toHaveBeenCalled()

    resolveStatus({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
    await flushPromises()
    await flushPromises()
    expect(getGlobalToolsMock).toHaveBeenCalledTimes(1)
    expect(getGlobalToolsMock).toHaveBeenCalledWith({ client: 'cursor' })
  })

  it('without a scope parameter in the URL the page does not wait and sends no argument', async () => {
    setAvailableFeatures([])
    getStatusMock.mockReturnValue(new Promise(() => {}))
    await mountToolsAt('/tools')
    expect(getGlobalToolsMock).toHaveBeenCalledTimes(1)
    expect(getGlobalToolsMock.mock.calls[0]).toHaveLength(0)
  })

  it('an administrator gets a verdict per row: greyed secondary cells, a text badge and the reason word for every reason', async () => {
    const tools = [
      row('open_tool', 'github', { visible: true, callable: true, reason: '' }),
      ...ALL_REASONS.map(reason => row(`t_${reason}`, 'github', { visible: reason === 'tool_approval', callable: false, reason })),
      row('off_tool', 'github', { visible: true, callable: false, reason: 'tool_approval' }, { disabled: true }),
    ]
    getGlobalToolsMock.mockImplementation(() => ok({ tools, stats: stats(tools) }))
    const { wrapper } = await mountToolsAt('/tools?client=cursor')

    expect(wrapper.get('[data-test="tools-row-access-github__open_tool"]').text()).toContain('Callable')
    const labels: Record<string, string> = {
      server_not_in_profile: 'Server not in profile',
      denied_by_rule: 'Denied by rule',
      above_tier_cap: 'Above tier cap',
      credential: 'Credential revoked or expired',
      server_state: 'Server disabled or not connected',
    }
    for (const reason of ALL_REASONS) {
      const cell = wrapper.get(`[data-test="tools-row-access-github__t_${reason}"]`)
      expect(cell.text(), reason).toMatch(/Hidden|Not callable/)
      expect(cell.get('[data-test="tools-access-reason"]').text(), reason).toBeTruthy()
      if (labels[reason]) expect(cell.text()).toContain(labels[reason])
    }
    expect(wrapper.get('[data-test="tools-row-access-github__off_tool"]').text()).toContain('Disabled')
    expect(wrapper.get('[data-test="tools-row-access-github__t_tool_approval"]').text()).toContain('Awaiting approval')

    const blockedRow = wrapper.findAll('[data-test="tool-row"]').find(r => r.text().includes('t_above_tier_cap'))!
    expect(blockedRow.attributes('data-not-callable')).toBe('true')
    // Greyed by a tinted row, never by opacity (which would drop the text under AA contrast).
    expect(blockedRow.classes()).toContain('bg-base-200/70')
    expect(blockedRow.findAll('.opacity-60')).toHaveLength(0)
    const openRow = wrapper.findAll('[data-test="tool-row"]').find(r => r.text().includes('open_tool'))!
    expect(openRow.attributes('data-not-callable')).toBeUndefined()
    expect(openRow.classes()).not.toContain('bg-base-200/70')
  })

  it('"Why?" opens the explainer for the subject and the tool', async () => {
    explainAccessMock.mockResolvedValue({ subject: { kind: 'client', name: 'cursor' }, tool: 'github:create_issue', profile: { name: 'work-ro', source: 'pin' }, steps: [], verdict: 'hidden', first_failure: 'tier_cap', fixes: [] })
    const tools = [row('create_issue', 'github', { visible: false, callable: false, reason: 'above_tier_cap' })]
    getGlobalToolsMock.mockImplementation(() => ok({ tools, stats: stats(tools) }))
    const { wrapper } = await mountToolsAt('/tools?client=cursor')
    await wrapper.get('[data-test="tools-why-github__create_issue"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="tools-why-github__create_issue"]').attributes('aria-label')).toBe('Why is create_issue not callable?')
    expect(explainAccessMock).toHaveBeenCalledWith({ tool: 'github:create_issue', client: 'cursor' })
    expect(wrapper.find('[data-test="access-explainer"]').exists()).toBe(true)
  })

  it('"Why?" for a profile view asks about the profile', async () => {
    explainAccessMock.mockResolvedValue({ subject: { kind: 'profile', name: 'work-ro' }, tool: 'github:create_issue', profile: { name: 'work-ro', source: 'url' }, steps: [], verdict: 'hidden', first_failure: 'tier_cap', fixes: [] })
    const tools = [row('create_issue', 'github', { visible: false, callable: false, reason: 'above_tier_cap' })]
    getGlobalToolsMock.mockImplementation(() => ok({ tools, stats: stats(tools) }))
    const { wrapper } = await mountToolsAt('/tools?profile=work-ro')
    await wrapper.get('[data-test="tools-why-github__create_issue"]').trigger('click')
    await flushPromises()
    expect(explainAccessMock).toHaveBeenCalledWith({ tool: 'github:create_issue', profile: 'work-ro' })
  })

  it('the banner reads the visible, callable and hidden numbers computed from the rows', async () => {
    const tools = [
      row('a', 'github', { visible: true, callable: true }),
      row('b', 'github', { visible: true, callable: false, reason: 'tool_approval' }),
      row('c', 'github', { visible: false, callable: false, reason: 'above_tier_cap' }),
      row('d', 'notion', { visible: false, callable: false, reason: 'server_not_in_profile' }),
    ]
    getGlobalToolsMock.mockImplementation(() => ok({ tools, stats: stats(tools) }))
    const { wrapper } = await mountToolsAt('/tools?client=cursor')
    const banner = wrapper.get('[data-test="tools-view-as-banner"]')
    expect(banner.attributes('role')).toBe('status')
    expect(banner.text()).toContain('Viewing as Cursor (Work · Read-only): 2 visible · 1 callable · 2 hidden')
    expect(banner.text()).toContain('Nothing here changes it')
  })

  it('J14: the banner says how many disabled servers there are and that their tools may not be listed, with a link to them', async () => {
    const { useServersStore } = await import('@/stores/servers')
    const { wrapper } = await (async () => {
      const mounted = mountToolsAt('/tools?profile=work-ro')
      return mounted
    })()
    useServersStore().servers = [{ name: 'notion', enabled: false, quarantined: false }] as any
    await flushPromises()
    const note = wrapper.get('[data-test="tools-view-as-disabled-note"]')
    expect(note.text()).toContain('1 disabled server: its tools may not be listed')
    expect(note.get('a').attributes('href')).toContain('/servers?')
    expect(note.get('a').attributes('href')).toContain('status=disabled')
  })

  it('selection, batch actions and approvals are disabled in view-as mode', async () => {
    const { wrapper } = await mountToolsAt('/tools?client=cursor')
    expect(wrapper.get('[data-test="tools-select-all"]').attributes('disabled')).toBeDefined()
    for (const box of wrapper.findAll('tbody input[type="checkbox"]')) expect(box.attributes('disabled')).toBeDefined()
  })

  it('outside view-as the selection checkboxes stay enabled', async () => {
    const { wrapper } = await mountToolsAt('/tools')
    expect(wrapper.get('[data-test="tools-select-all"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-test="tools-view-as-banner"]').exists()).toBe(false)
  })

  it('a tenant viewing a profile gets visible rows only, "N tools hidden" from counts and no reason or Why?', async () => {
    const tools = [row('list_issues', 'github'), row('get_issue', 'github')]
    getGlobalToolsMock.mockImplementation(() => ok({ tools, stats: stats(tools), counts: { visible: 2, hidden: 7 } }))
    const { wrapper } = await mountToolsAt('/tools?profile=work-ro', { tenant: true })
    expect(getGlobalToolsMock).toHaveBeenCalledWith({ profile: 'work-ro' })
    expect(wrapper.get('[data-test="tools-view-as-summary"]').text()).toBe('7 tools hidden by this profile')
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(2)
    expect(wrapper.find('[data-test^="tools-why-"]').exists()).toBe(false)
    expect(wrapper.find('[data-test^="tools-row-access-"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="tools-show-filter"]').exists()).toBe(false)
  })

  it('a tenant with ?client= sends no client and shows the disabled chip', async () => {
    const { wrapper } = await mountToolsAt('/tools?client=cursor', { tenant: true })
    expect(getGlobalToolsMock).toHaveBeenCalledTimes(1)
    expect(getGlobalToolsMock.mock.calls[0]).toHaveLength(0)
    const chip = wrapper.get('[data-test="scope-chip-client"]')
    expect(chip.attributes('aria-disabled')).toBe('true')
    expect(chip.text()).toContain('Viewing as a client requires an administrator')
  })

  it('both client and profile: no request, the conflict state, and each button clears the other parameter', async () => {
    const { wrapper, router } = await mountToolsAt('/tools?client=cursor&profile=work-ro')
    expect(getGlobalToolsMock).not.toHaveBeenCalled()
    const conflict = wrapper.get('[data-test="tools-view-as-conflict"]')
    expect(conflict.text()).toContain('Tools can view as a client or a profile, not both')
    expect(wrapper.get('[data-test="scope-chip-client"]').attributes('data-conflicting')).toBe('true')
    expect(wrapper.get('[data-test="scope-chip-profile"]').attributes('data-conflicting')).toBe('true')

    await wrapper.get('[data-test="tools-view-as-keep-client"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ client: 'cursor' })
    expect(getGlobalToolsMock).toHaveBeenCalledWith({ client: 'cursor' })
  })

  it('keeping the profile clears the client', async () => {
    const { wrapper, router } = await mountToolsAt('/tools?client=cursor&profile=work-ro')
    await wrapper.get('[data-test="tools-view-as-keep-profile"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ profile: 'work-ro' })
    expect(getGlobalToolsMock).toHaveBeenCalledWith({ profile: 'work-ro' })
  })

  it('?client=- shows the disabled chip and the request carries no client', async () => {
    const { wrapper } = await mountToolsAt('/tools?client=-')
    expect(getGlobalToolsMock.mock.calls[0]).toHaveLength(0)
    const chip = wrapper.get('[data-test="scope-chip-client"]')
    expect(chip.attributes('aria-disabled')).toBe('true')
    expect(chip.text()).toContain('Unattributed applies to Activity and Usage only')
    expect(wrapper.find('[data-test="tools-view-as-banner"]').exists()).toBe(false)
  })

  it('a 404 is an inline "Client not found" with a Clear filter that drops the parameter', async () => {
    getGlobalToolsMock.mockImplementation((scope?: { client?: string }) => (scope?.client
      ? Promise.reject(Object.assign(new Error('client not found'), { status: 404 }))
      : ok({ tools: [row('x', 'github')], stats: stats([1]) })))
    const { wrapper, router } = await mountToolsAt('/tools?client=ghost')
    expect(wrapper.get('[data-test="tools-scope-error"]').text()).toContain('Client not found')
    expect(wrapper.find('[data-test="tools-view-as-banner"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="tools-page"]').exists()).toBe(true)
    await wrapper.get('[data-test="tools-scope-error-clear"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({})
    expect(wrapper.find('[data-test="tools-scope-error"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(1)
  })

  it('a 404 for a profile reads "Profile not found"', async () => {
    getGlobalToolsMock.mockImplementation(() => Promise.reject(Object.assign(new Error('profile not found'), { status: 404 })))
    const { wrapper } = await mountToolsAt('/tools?profile=ghost')
    expect(wrapper.get('[data-test="tools-scope-error"]').text()).toContain('Profile not found')
    // The "0 visible / 0 callable / 0 hidden" banner would describe a subject that does not exist.
    expect(wrapper.find('[data-test="tools-view-as-banner"]').exists()).toBe(false)
  })

  it('rule 7: with scope_filters absent the page sends one unscoped request, shows only the disabled chip and keeps client in the URL', async () => {
    setAvailableFeatures([])
    getStatusMock.mockResolvedValue({ success: true, data: {} })
    const { useSystemStore } = await import('@/stores/system')
    await useSystemStore().fetchScopeFilterFeatures()
    const { wrapper, router } = await mountToolsAt('/tools?client=cursor')
    expect(getGlobalToolsMock).toHaveBeenCalledTimes(1)
    expect(getGlobalToolsMock.mock.calls[0]).toHaveLength(0)
    // No active chip, but the disabled "Filter unavailable" chip names the param (F5.2).
    expect(wrapper.find('[data-test="scope-chip-client"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="scope-chip-na-client"]').text()).toContain('Filter unavailable on this server')
    expect(router.currentRoute.value.query.client).toBe('cursor')
  })

  it('F2.1: Refresh during the startup /status wait sends nothing unfiltered', async () => {
    setAvailableFeatures([])
    let resolveStatus!: (v: unknown) => void
    getStatusMock.mockReturnValue(new Promise(r => { resolveStatus = r }))
    const { useSystemStore } = await import('@/stores/system')
    void useSystemStore().fetchScopeFilterFeatures()
    const { wrapper } = await mountToolsAt('/tools?client=cursor')
    await wrapper.get('[data-test="tools-refresh"]').trigger('click')
    await flushPromises()
    expect(getGlobalToolsMock).not.toHaveBeenCalled()
    resolveStatus({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
    await flushPromises()
    await flushPromises()
    expect(getGlobalToolsMock.mock.calls.length).toBeGreaterThan(0)
    for (const call of getGlobalToolsMock.mock.calls) expect(call[0]).toEqual({ client: 'cursor' })
  })

  it('F2.2: a smaller result after the view-as subject or Show changes returns to page 1, never an empty page without a pager', async () => {
    const many = Array.from({ length: 30 }, (_, i) => row(`t${i}`, 'github', { visible: true, callable: i < 10 }))
    const few = many.slice(0, 10)
    getGlobalToolsMock.mockImplementation((scope?: { client?: string; profile?: string }) =>
      scope?.profile ? ok({ tools: few, stats: stats(few) }) : ok({ tools: many, stats: stats(many) }))
    const { wrapper } = await mountToolsAt('/tools?client=cursor')
    // Page 2 (25 rows per page).
    const nextPage = () => wrapper.findAll('button').find(b => b.text() === '›')!
    await nextPage().trigger('click')
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(5)
    await wrapper.get('[data-test="tools-view-as-select"]').setValue('profile:work-ro')
    await flushPromises()
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(10)

    // Show: Callable on a 30-row list where 10 match, from page 2.
    getGlobalToolsMock.mockImplementation(() => ok({ tools: many, stats: stats(many) }))
    await wrapper.get('[data-test="tools-view-as-select"]').setValue('client:cursor')
    await flushPromises()
    await nextPage().trigger('click')
    await wrapper.get('[data-test="tools-show-callable"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(10)
  })

  it('"Show: Callable" filters the loaded rows without touching the URL or refetching', async () => {
    const tools = [
      row('a', 'github', { visible: true, callable: true }),
      row('b', 'github', { visible: false, callable: false, reason: 'above_tier_cap' }),
    ]
    getGlobalToolsMock.mockImplementation(() => ok({ tools, stats: stats(tools) }))
    const { wrapper, router } = await mountToolsAt('/tools?client=cursor')
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(2)
    await wrapper.get('[data-test="tools-show-callable"]').trigger('click')
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(1)
    await wrapper.get('[data-test="tools-show-not_callable"]').trigger('click')
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(1)
    expect(wrapper.findAll('[data-test="tool-row"]')[0].text()).toContain('b')
    expect(router.currentRoute.value.query).toEqual({ client: 'cursor' })
    expect(getGlobalToolsMock).toHaveBeenCalledTimes(1)
  })

  it('the View as select writes one parameter and clears the other', async () => {
    const { wrapper, router } = await mountToolsAt('/tools?profile=work-ro')
    await wrapper.get('[data-test="tools-view-as-select"]').setValue('client:cursor')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ client: 'cursor' })
    expect(getGlobalToolsMock).toHaveBeenLastCalledWith({ client: 'cursor' })
    await wrapper.get('[data-test="tools-view-as-select"]').setValue('')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({})
    expect(getGlobalToolsMock.mock.calls.at(-1)).toHaveLength(0)
  })

  it('removing the chip drops the parameter and refetches unscoped; a late scoped answer does not overwrite it', async () => {
    let releaseScoped!: () => void
    const scoped = [row('only_cursor', 'github', { visible: true, callable: true })]
    const unscoped = [row('a', 'github'), row('b', 'github'), row('c', 'github')]
    getGlobalToolsMock.mockImplementation((scope?: { client?: string }) => scope?.client
      ? new Promise(resolveScoped => { releaseScoped = () => resolveScoped({ success: true, data: { tools: scoped, stats: stats(scoped) } }) })
      : ok({ tools: unscoped, stats: stats(unscoped) }))
    const { wrapper, router } = await mountToolsAt('/tools?client=cursor')
    await wrapper.get('[data-test="scope-chip-remove-client"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({})
    releaseScoped()
    await flushPromises()
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(3)
  })
})
