import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import SessionsPanel from '@/components/activity/SessionsPanel.vue'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { useAuthStore } from '@/stores/auth'
import { useClientsStore } from '@/stores/clients'
import { useSystemStore } from '@/stores/system'
import { useProfilesStore } from '@/stores/profiles'
import { makeClient, makeProfile } from './fixtures/profiles108i'
import { WS_FAILED, WS_OK, WS_SCOPED, timelinePage } from './fixtures/workSessions124'

// Spec 124 PR-3 (T110; FR-060, FR-061, FR-065): Activity -> Sessions lists work
// sessions from GET /api/v1/work-sessions, one row each.

const list = vi.hoisted(() => ({
  rows: [] as Record<string, unknown>[],
  total: undefined as number | undefined,
  fail: null as Error | null,
}))

vi.mock('@/services/api', () => ({
  default: {
    getProfiles: vi.fn(),
    getClients: vi.fn(),
    getStatus: vi.fn(() => Promise.resolve({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })),
    getWorkSessions: vi.fn((params: { limit?: number; offset?: number } = {}) =>
      list.fail
        ? Promise.reject(list.fail)
        : Promise.resolve({
            sessions: list.rows,
            total: list.total ?? list.rows.length,
            limit: params.limit ?? 25,
            offset: params.offset ?? 0,
          })),
    getWorkSessionTimeline: vi.fn(() => Promise.resolve(timelinePage())),
  },
}))

const stub = { template: '<div />' }

async function mountPanel(path = '/activity?view=sessions', connections: unknown[] = [], attach = false) {
  const api = (await import('@/services/api')).default as any
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: stub },
      { path: '/tools', name: 'tools', component: stub },
      { path: '/clients', name: 'clients', component: stub },
      { path: '/tokens', name: 'tokens', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
    ],
  })
  await router.push(path)
  await router.isReady()
  useProfilesStore().profiles = [makeProfile('work-readonly', { title: 'Work' })] as any
  useClientsStore().clients = [makeClient('cursor')] as any
  const wrapper = mount(SessionsPanel, {
    props: { sessions: connections as any },
    global: { plugins: [router] },
    ...(attach ? { attachTo: document.body } : {}),
  })
  await flushPromises()
  return { wrapper, router, api }
}

const lastListParams = (api: any) => api.getWorkSessions.mock.calls.at(-1)[0]

describe('Work-session list (Spec 124 FR-060)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setAvailableFeatures(['profile', 'client', 'token'])
    vi.clearAllMocks()
    list.rows = [WS_FAILED, WS_OK]
    list.total = undefined
    list.fail = null
  })

  afterEach(() => setAvailableFeatures([]))

  it('shows one row per work session with token, client, project, started, duration, calls, failures, last failure and reason', async () => {
    const { wrapper } = await mountPanel()
    const rows = wrapper.findAll('[data-test="work-sessions-row"]')
    expect(rows).toHaveLength(2)

    const failed = rows[0]
    expect(failed.get('[data-test="ws-token"]').text()).toContain('nightly-bot')
    expect(failed.get('[data-test="ws-client"]').text()).toContain('Cursor')
    expect(failed.get('[data-test="ws-project"]').text()).toBe('payments')
    expect(failed.get('[data-test="ws-started"]').text()).toMatch(/2026-11-2\d \d{2}:\d{2}:\d{2}/)
    expect(failed.get('[data-test="ws-duration"]').text()).toBe('41m 05s')
    expect(failed.get('[data-test="ws-calls"]').text()).toBe('120')
    expect(failed.get('[data-test="ws-failures"]').text()).toContain('7')
    expect(failed.get('[data-test="ws-failures"]').text()).toContain('2 blocked')
    expect(failed.get('[data-test="ws-last-failure"]').text()).toMatch(/2026-11-2\d \d{2}:\d{2}:\d{2}/)
    expect(failed.get('[data-test="ws-reason"]').text()).toContain('The upstream server needs a login.')
    expect(failed.get('[data-test="ws-reason"]').text()).toContain('auth · github:list_issues')

    const clean = rows[1]
    expect(clean.get('[data-test="ws-client"]').text()).toContain('rest')
    expect(clean.get('[data-test="ws-client"]').text()).toContain('nightly-2026-11-26')
    expect(clean.get('[data-test="ws-failures"]').text()).toBe('0')
    expect(clean.get('[data-test="ws-last-failure"]').text()).toBe('—')
    expect(clean.find('[data-test="ws-reason"]').exists()).toBe(false)
    // A client that disclosed no project has no project line.
    expect(clean.find('[data-test="ws-project"]').exists()).toBe(false)
    expect(clean.get('[data-test="ws-duration"]').text()).toBe('12s')
  })

  it('reads the list door with the default order and the first page', async () => {
    const { api } = await mountPanel()
    expect(api.getWorkSessions).toHaveBeenCalledTimes(1)
    expect(lastListParams(api)).toEqual({ limit: 25, offset: 0, sort: 'last_activity' })
  })

  it('"Failed only" asks the door for failed sessions and writes the address', async () => {
    const { wrapper, router, api } = await mountPanel()
    expect(wrapper.find('[data-test="work-sessions-include-blocked"]').exists()).toBe(false)
    await wrapper.get('[data-test="work-sessions-failed-toggle"]').setValue(true)
    await flushPromises()
    // Failed sessions read newest failure first (User Story 1), without the
    // order being written to the address.
    expect(lastListParams(api)).toMatchObject({ failed: true, offset: 0, sort: 'last_failure' })
    expect(router.currentRoute.value.query.sort).toBeUndefined()
    expect((wrapper.get('[data-test="work-sessions-sort"]').element as HTMLSelectElement).value).toBe('last_failure')
    expect(lastListParams(api).include_blocked).toBeUndefined()
    expect(router.currentRoute.value.query.failed).toBe('true')

    // "Include blocked" is on by default; switching it off counts real failures only.
    const blocked = wrapper.get('[data-test="work-sessions-include-blocked"]')
    expect((blocked.element as HTMLInputElement).checked).toBe(true)
    await blocked.setValue(false)
    await flushPromises()
    expect(lastListParams(api)).toMatchObject({ failed: true, include_blocked: false })
    expect(router.currentRoute.value.query.include_blocked).toBe('false')
  })

  it('sorts by last failure on the server', async () => {
    const { wrapper, router, api } = await mountPanel()
    await wrapper.get('[data-test="work-sessions-sort"]').setValue('last_failure')
    await flushPromises()
    expect(lastListParams(api)).toMatchObject({ sort: 'last_failure', offset: 0 })
    expect(router.currentRoute.value.query.sort).toBe('last_failure')
  })

  it('keeps an order chosen against the default of the filter', async () => {
    const { wrapper, router, api } = await mountPanel('/activity?view=sessions&failed=true')
    expect(api.getWorkSessions.mock.calls[0][0]).toMatchObject({ failed: true, sort: 'last_failure' })
    await wrapper.get('[data-test="work-sessions-sort"]').setValue('last_activity')
    await flushPromises()
    expect(lastListParams(api)).toMatchObject({ failed: true, sort: 'last_activity' })
    expect(router.currentRoute.value.query.sort).toBe('last_activity')
    // Choosing the default again takes the order out of the address.
    await wrapper.get('[data-test="work-sessions-sort"]').setValue('last_failure')
    await flushPromises()
    expect(router.currentRoute.value.query.sort).toBeUndefined()
  })

  it('restores the filter and the order from the address', async () => {
    const { wrapper, api } = await mountPanel('/activity?view=sessions&failed=true&include_blocked=false&sort=last_failure')
    expect(api.getWorkSessions.mock.calls[0][0]).toEqual({
      limit: 25, offset: 0, sort: 'last_failure', failed: true, include_blocked: false,
    })
    expect((wrapper.get('[data-test="work-sessions-failed-toggle"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-test="work-sessions-sort"]').element as HTMLSelectElement).value).toBe('last_failure')
  })

  it('pages on the server', async () => {
    list.total = 60
    const { wrapper, api } = await mountPanel()
    expect(wrapper.get('[data-test="work-sessions-range"]').text()).toBe('1–2 of 60')
    expect(wrapper.get('[data-test="work-sessions-prev"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="work-sessions-next"]').trigger('click')
    await flushPromises()
    expect(lastListParams(api)).toMatchObject({ limit: 25, offset: 25 })
    await wrapper.get('[data-test="work-sessions-prev"]').trigger('click')
    await flushPromises()
    expect(lastListParams(api)).toMatchObject({ offset: 0 })
  })

  it('shows the last page that exists when the open page is past the end', async () => {
    list.total = 60
    const { wrapper, api } = await mountPanel()
    await wrapper.get('[data-test="work-sessions-next"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="work-sessions-next"]').trigger('click')
    await flushPromises()
    expect(lastListParams(api)).toMatchObject({ offset: 50 })
    // Retention removed sessions: 30 are left, so offset 50 is empty.
    api.getWorkSessions.mockImplementationOnce(() => Promise.resolve({ sessions: [], total: 30, limit: 25, offset: 30 }))
    list.total = 30
    await wrapper.get('[data-test="work-sessions-refresh"]').trigger('click')
    await flushPromises()
    expect(lastListParams(api)).toMatchObject({ offset: 25 })
    expect(wrapper.find('[data-test="work-sessions-empty"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="work-sessions-row"]')).toHaveLength(2)
  })

  it('returns from a timeline to the same page of the list', async () => {
    list.total = 60
    const { wrapper, router, api } = await mountPanel()
    await wrapper.get('[data-test="work-sessions-next"]').trigger('click')
    await flushPromises()
    await router.push({ query: { ...router.currentRoute.value.query, ws: WS_FAILED.id } })
    await flushPromises()
    expect(wrapper.find('[data-test="session-timeline"]').exists()).toBe(true)
    const calls = api.getWorkSessions.mock.calls.length
    await router.back()
    await flushPromises()
    await flushPromises()
    expect(wrapper.find('[data-test="work-sessions-table"]').exists()).toBe(true)
    expect(api.getWorkSessions.mock.calls.length).toBe(calls + 1)
    expect(lastListParams(api)).toMatchObject({ offset: 25 })
  })

  it('moves focus into the timeline when one opens, and back to its row on return', async () => {
    const { wrapper, router } = await mountPanel('/activity?view=sessions', [], true)
    await router.push({ query: { ...router.currentRoute.value.query, ws: WS_FAILED.id } })
    await flushPromises()
    await flushPromises()
    expect(document.activeElement?.getAttribute('data-test')).toBe('timeline-heading')
    await router.back()
    await flushPromises()
    await flushPromises()
    expect(document.activeElement?.getAttribute('data-test')).toBe(`ws-open-${WS_FAILED.id}`)
    wrapper.unmount()
  })

  it('says so on the row when the start of a session was trimmed (FR-061)', async () => {
    const { wrapper } = await mountPanel()
    const rows = wrapper.findAll('[data-test="work-sessions-row"]')
    expect(rows[0].get('[data-test="ws-truncated"]').text()).toContain('Start trimmed')
    expect(rows[1].find('[data-test="ws-truncated"]').exists()).toBe(false)
  })

  it('keeps the link to Tool calls filtered by the session (FR-065)', async () => {
    const { wrapper, router } = await mountPanel()
    const link = wrapper.get(`[data-test="ws-calls-link-${WS_FAILED.id}"]`)
    // The calls count is the link, so it is there at every width.
    expect(link.text()).toBe('120')
    expect(link.attributes('aria-label')).toContain('120 calls')
    expect(link.attributes('href')).toBe(
      router.resolve({ name: 'activity', query: { view: 'calls', session: WS_FAILED.id } }).fullPath,
    )
  })

  it('a session that never failed opens at its most recent row (US1 scenario 6)', async () => {
    const { wrapper } = await mountPanel()
    const query = new URL(wrapper.get(`[data-test="ws-open-${WS_OK.id}"]`).attributes('href')!, 'http://x').searchParams
    expect(query.get('ws')).toBe(WS_OK.id)
    expect(query.get('at')).toBe('end')
  })

  it('prints the last failure to the millisecond, and keeps it readable below sm', async () => {
    const { wrapper } = await mountPanel()
    const row = wrapper.findAll('[data-test="work-sessions-row"]')[0]
    expect(row.get('[data-test="ws-last-failure"]').text()).toMatch(/\d{2}:\d{2}:00\.250/)
    expect(row.get('[data-test="ws-last-failure"]').attributes('title')).toMatch(/^First failure: 2026-11-2\d \d{2}:\d{2}:00\.000$/)
    // Started and duration fold into the same cell below md.
    const started = row.get('[data-test="ws-started-narrow"]')
    expect(started.classes()).toContain('md:hidden')
    expect(started.text()).toMatch(/2026-11-2\d \d{2}:\d{2}:00 · 41m 05s · ended/)
    const narrow = row.get('[data-test="ws-reason-narrow"]')
    expect(narrow.classes()).toContain('sm:hidden')
    expect(narrow.text()).toContain('The upstream server needs a login.')
    expect(narrow.text()).toMatch(/\d{2}:\d{2}:00\.250/)
  })

  it('rows of the previous page cannot be opened while the next one is on its way', async () => {
    list.total = 60
    const { wrapper, api } = await mountPanel()
    let answer!: (value: unknown) => void
    api.getWorkSessions.mockImplementationOnce(() => new Promise(resolve => { answer = resolve }))
    await wrapper.get('[data-test="work-sessions-next"]').trigger('click')
    await flushPromises()
    const wrap = wrapper.get('[data-test="work-sessions-table-wrap"]')
    expect(wrap.attributes('inert')).toBeDefined()
    expect(wrap.attributes('aria-busy')).toBe('true')
    // The range still names the rows on screen.
    expect(wrapper.get('[data-test="work-sessions-range"]').text()).toBe('1–2 of 60')
    answer({ sessions: [WS_OK], total: 60, limit: 25, offset: 25 })
    await flushPromises()
    expect(wrapper.get('[data-test="work-sessions-table-wrap"]').attributes('inert')).toBeUndefined()
    expect(wrapper.get('[data-test="work-sessions-range"]').text()).toBe('26–26 of 60')
  })

  it('a row opens the timeline of its session at the default position', async () => {
    const { wrapper } = await mountPanel('/activity?view=sessions&failed=true')
    const href = wrapper.get(`[data-test="ws-open-${WS_FAILED.id}"]`).attributes('href')!
    const query = new URL(href, 'http://x').searchParams
    expect(query.get('view')).toBe('sessions')
    expect(query.get('ws')).toBe(WS_FAILED.id)
    expect(query.has('at')).toBe(false)
    // The list filter stays in the address, so "back" returns to the same list.
    expect(query.get('failed')).toBe('true')
  })

  it('renders a scoped caller\'s row without the administrator-only fields', async () => {
    list.rows = [WS_SCOPED]
    const { wrapper } = await mountPanel()
    const row = wrapper.get('[data-test="work-sessions-row"]')
    expect(row.text()).not.toMatch(/undefined|NaN|null/)
    expect(row.find('[data-test="ws-truncated"]').exists()).toBe(false)
    expect(row.get('[data-test="ws-token"]').text()).toContain('ro-bot')
    expect(row.find('[data-test="ws-project"]').exists()).toBe(false)
    expect(row.get('[data-test="ws-reason"]').text()).toContain('Blocked by MCPProxy')
  })

  it("does not show a client's own credential as an agent token", async () => {
    list.rows = [{ ...WS_FAILED, token_name: 'client-cursor' }]
    const { wrapper } = await mountPanel()
    const row = wrapper.get('[data-test="work-sessions-row"]')
    expect(row.get('[data-test="ws-token"]').text()).toBe('—')
    expect(row.find('[data-test="ws-token-narrow"]').exists()).toBe(false)
    expect(row.get('[data-test="ws-client"]').text()).toContain('Cursor')
  })

  it('sends the profile, client and token filters of the page to the door', async () => {
    const { wrapper, api } = await mountPanel('/activity?view=sessions&profile=work-readonly&client=cursor&token=nightly-bot')
    expect(api.getWorkSessions.mock.calls[0][0]).toMatchObject({ profile: 'work-readonly', client: 'cursor', token: 'nightly-bot' })
    expect(wrapper.find('[data-test="work-sessions-unattributed-note"]').exists()).toBe(false)
  })

  it('never sends a filter the build does not advertise', async () => {
    setAvailableFeatures([])
    useSystemStore().scopeFeaturesKnown = true
    const { api } = await mountPanel('/activity?view=sessions&client=cursor')
    expect(api.getWorkSessions.mock.calls[0][0].client).toBeUndefined()
  })

  it('holds the first request until the build has said which filters it accepts (rule 1)', async () => {
    setAvailableFeatures([])
    const { api } = await mountPanel('/activity?view=sessions&client=cursor')
    // Not known yet: nothing is asked, so nothing unfiltered is listed.
    expect(api.getWorkSessions).not.toHaveBeenCalled()
    await useSystemStore().fetchScopeFilterFeatures()
    await flushPromises()
    expect(api.getWorkSessions).toHaveBeenCalledTimes(1)
    expect(api.getWorkSessions.mock.calls[0][0]).toMatchObject({ client: 'cursor' })
  })

  it('does not send a profile filter for a signed-in user, and says so', async () => {
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.user = { id: 'u1', email: 'u@example.com', role: 'user' } as any
    expect(auth.principalKind).toBe('tenant')
    const { wrapper, api } = await mountPanel('/activity?view=sessions&profile=work-readonly&client=cursor')
    expect(api.getWorkSessions.mock.calls[0][0].profile).toBeUndefined()
    expect(api.getWorkSessions.mock.calls[0][0]).toMatchObject({ client: 'cursor' })
    expect(wrapper.get('[data-test="work-sessions-unattributed-note"]').text()).toContain('Not applied to work sessions: Profile: Work')
  })

  it('does not send the unattributed filter, and says so', async () => {
    const { wrapper, api } = await mountPanel('/activity?view=sessions&client=-')
    expect(api.getWorkSessions.mock.calls[0][0].client).toBeUndefined()
    expect(wrapper.get('[data-test="work-sessions-unattributed-note"]').text()).toContain('Client: unattributed')
  })

  it('names the reason when the list is empty', async () => {
    list.rows = []
    const plain = await mountPanel()
    expect(plain.wrapper.get('[data-test="work-sessions-empty"]').text()).toContain('No work sessions yet')
    const failed = await mountPanel('/activity?view=sessions&failed=true')
    expect(failed.wrapper.get('[data-test="work-sessions-empty"]').text()).toContain('No failed sessions')
    const scoped = await mountPanel('/activity?view=sessions&client=cursor')
    expect(scoped.wrapper.get('[data-test="work-sessions-empty"]').text()).toContain('No sessions for Client: Cursor')
  })

  it('shows the error and retries', async () => {
    list.fail = new Error('core said no')
    const { wrapper, api } = await mountPanel()
    expect(wrapper.get('[data-test="work-sessions-error"]').text()).toContain('core said no')
    list.fail = null
    await wrapper.get('[data-test="work-sessions-retry"]').trigger('click')
    await flushPromises()
    expect(api.getWorkSessions).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('[data-test="work-sessions-row"]')).toHaveLength(2)
  })

  it('with a session in the address it shows that timeline, not the list', async () => {
    const { wrapper, api } = await mountPanel(`/activity?view=sessions&ws=${WS_FAILED.id}&at=failure:2`)
    expect(api.getWorkSessions).not.toHaveBeenCalled()
    expect(api.getWorkSessionTimeline).toHaveBeenCalledWith(WS_FAILED.id, expect.objectContaining({ at: 'failure:2' }))
    expect(wrapper.find('[data-test="work-sessions-table"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="session-timeline"]').exists()).toBe(true)
  })

  it('keeps the client connections table one level down', async () => {
    const connection = {
      id: 'conn-1', client_id: 'cursor', client_name: 'Cursor', status: 'active',
      start_time: '2026-11-27T08:00:00Z', last_activity: '2026-11-27T08:01:00Z',
      tool_call_count: 3, total_tokens: 1200, has_roots: true, work_session_id: 'ws-aaa111',
    }
    const { wrapper } = await mountPanel('/activity?view=sessions', [connection])
    const section = wrapper.get('[data-test="sessions-connections"]')
    expect(section.element.tagName).toBe('DETAILS')
    expect(section.get('summary').text()).toContain('Client connections (1)')
    expect(section.findAll('[data-test="sessions-row"]')).toHaveLength(1)
    expect(section.find('[data-test="sessions-tools-it-sees-conn-1"]').exists()).toBe(true)
    expect(section.find('[data-test="session-view-activity"]').exists()).toBe(true)
  })
})
