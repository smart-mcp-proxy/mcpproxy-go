import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { WS_FAILED, WS_OK, timelinePage } from './fixtures/workSessions124'

// Spec 124 PR-3: the Sessions view inside the Activity page. The list and the
// timeline have their own specs (sessions-panel, session-timeline); this one
// covers what only the page does: which door each view reads, leaving a
// timeline through the tabs, and the way into a timeline from a call's detail.

const CALL = {
  id: 'act-1', type: 'tool_call', status: 'error', timestamp: '2026-11-26T22:40:00.250Z',
  server_name: 'github', tool_name: 'list_issues', request_id: 'r1', duration_ms: 40,
  session_id: 'conn-1', work_session_id: WS_FAILED.id, error_message: 'The upstream server needs a login.',
}
const NO_SESSION = {
  id: 'act-2', type: 'tool_call', status: 'success', timestamp: '2026-11-26T22:39:00Z',
  server_name: 'github', tool_name: 'get_repo', request_id: 'r2', duration_ms: 12,
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() => ok({ activities: [CALL, NO_SESSION], total: 2, limit: 200, offset: 0 })),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 2, call_count: 2, success_count: 1, error_count: 1, blocked_count: 0, rejected_count: 0 })),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/export'),
      // The door filters: a failed-only request does not hold the clean session.
      getWorkSessions: vi.fn((params: { failed?: boolean } = {}) => {
        const sessions = params.failed ? [WS_FAILED] : [WS_FAILED, WS_OK]
        return Promise.resolve({ sessions, total: sessions.length, limit: 25, offset: 0 })
      }),
      getWorkSessionTimeline: vi.fn(() => Promise.resolve(timelinePage())),
    },
  }
})

async function mountAt(path: string) {
  const api = (await import('@/services/api')).default as any
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/clients', name: 'clients', component: { template: '<div/>' } },
      { path: '/tokens', name: 'tokens', component: { template: '<div/>' } },
      { path: '/sessions', name: 'sessions', component: { template: '<div/>' } },
      { path: '/profiles/:name', name: 'profile-editor', component: { template: '<div/>' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] }, attachTo: document.body })
  await flushPromises()
  await flushPromises()
  return { wrapper, router, api }
}

/** Opens the filter popover (its open state is remembered per browser) and returns "Clear Filters". */
async function clearFiltersButton(wrapper: Awaited<ReturnType<typeof mountAt>>['wrapper']) {
  if (!wrapper.find('[data-test="activity-filter-popover"]').exists()) {
    await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
  }
  const button = wrapper.findAll('[data-test="activity-filter-popover"] button').find(b => b.text().includes('Clear Filters'))
  expect(button, 'the popover offers Clear Filters').toBeTruthy()
  return button!
}

describe('Activity Sessions view (Spec 124)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('lists work sessions from their own door and issues no /activity request', async () => {
    const { wrapper, api } = await mountAt('/activity?view=sessions')
    expect(api.getWorkSessions).toHaveBeenCalledTimes(1)
    expect(api.getWorkSessionTimeline).not.toHaveBeenCalled()
    expect(api.getActivities).not.toHaveBeenCalled()
    expect(wrapper.findAll('[data-test="work-sessions-row"]')).toHaveLength(2)
    wrapper.unmount()
  })

  it('the other views never read the work-session doors', async () => {
    const { wrapper, api } = await mountAt('/activity')
    expect(api.getWorkSessions).not.toHaveBeenCalled()
    expect(api.getWorkSessionTimeline).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('opens the timeline named by the address, and the Sessions tab leads back to the list', async () => {
    const { wrapper, router, api } = await mountAt(`/activity?view=sessions&ws=${WS_FAILED.id}&at=failure:2&failed=true`)
    expect(wrapper.find('[data-test="session-timeline"]').exists()).toBe(true)
    expect(api.getWorkSessions).not.toHaveBeenCalled()

    await wrapper.get('[data-test="activity-view-tab-sessions"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.ws).toBeUndefined()
    expect(router.currentRoute.value.query.at).toBeUndefined()
    expect(router.currentRoute.value.query).toMatchObject({ view: 'sessions', failed: 'true' })
    expect(wrapper.find('[data-test="session-timeline"]').exists()).toBe(false)
    expect(api.getWorkSessions.mock.calls.at(-1)[0]).toMatchObject({ failed: true })
    // The rows on screen are the answer to that request: the failed session only.
    expect(wrapper.findAll('[data-test="work-sessions-row"]').map(r => r.attributes('data-session'))).toEqual([WS_FAILED.id])
    wrapper.unmount()
  })

  it('the filter strip is put away while a timeline is open and is back with the list', async () => {
    // Nothing on the strip narrows a timeline; the list one step back has it.
    const { wrapper } = await mountAt(`/activity?view=sessions&ws=${WS_FAILED.id}&at=failure:2`)
    const strip = () => wrapper.get('[data-test="activity-filter-strip"]')
    expect(strip().attributes('style') ?? '').toContain('display: none')
    await wrapper.get('[data-test="activity-view-tab-sessions"]').trigger('click')
    await flushPromises()
    expect(strip().attributes('style') ?? '').not.toContain('display: none')
    expect(wrapper.find('[data-test="activity-filters-toggle"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('switching to Tool calls from a timeline drops the session from the address', async () => {
    const { wrapper, router } = await mountAt(`/activity?view=sessions&ws=${WS_FAILED.id}&at=end`)
    await wrapper.get('[data-test="activity-view-tab-calls"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.ws).toBeUndefined()
    expect(router.currentRoute.value.query.at).toBeUndefined()
    expect(router.currentRoute.value.query.view).toBeUndefined()
    wrapper.unmount()
  })

  it('"Failed only" counts as a filter of the page: it is in the count and Clear Filters removes it', async () => {
    const { wrapper, router } = await mountAt('/activity?view=sessions&failed=true&include_blocked=false&sort=last_activity')
    expect(wrapper.get('[data-test="activity-filters-count"]').text()).toBe('1')
    await (await clearFiltersButton(wrapper)).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.failed).toBeUndefined()
    expect(router.currentRoute.value.query.include_blocked).toBeUndefined()
    // The order is not a filter.
    expect(router.currentRoute.value.query).toMatchObject({ view: 'sessions', sort: 'last_activity' })
    wrapper.unmount()
  })

  it('Clear Filters removes "Failed only" together with another filter of the page', async () => {
    // Clearing `status` makes the page write the address a second time; that
    // write must not put the session filters back.
    const { wrapper, router } = await mountAt('/activity?view=sessions&failed=true&include_blocked=false&status=error')
    await (await clearFiltersButton(wrapper)).trigger('click')
    await flushPromises()
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ view: 'sessions' })
    wrapper.unmount()
  })

  it('a session link out of the Sessions view does not inherit filters that only the calls view showed', async () => {
    // "Sensitive data" and "View sub-calls" are not in the address and have no
    // chip in Sessions: a session's calls would come up narrowed, or empty.
    const { wrapper, router, api } = await mountAt('/activity')
    await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()
    await wrapper.get('select[aria-label="Filter by sensitive data"]').setValue('true')
    await flushPromises()
    expect(wrapper.findAll('[data-test="activity-row"]').length).toBe(0)
    await wrapper.get('[data-test="activity-view-tab-sessions"]').trigger('click')
    await flushPromises()
    await flushPromises()
    await wrapper.get(`[data-test="ws-calls-link-${WS_FAILED.id}"]`).trigger('click')
    await flushPromises()
    await flushPromises()
    expect(router.currentRoute.value.query.session).toBe(WS_FAILED.id)
    // The session's call is listed (the other row belongs to no session).
    expect(wrapper.findAll('[data-test="activity-row"]').length).toBe(1)
    expect(api.getActivities.mock.calls.at(-1)[0].parent_id).toBeUndefined()
    wrapper.unmount()
  })

  it('the severity of the sensitive-data filter goes with it', async () => {
    const { wrapper } = await mountAt('/activity')
    await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()
    await wrapper.get('select[aria-label="Filter by sensitive data"]').setValue('true')
    await flushPromises()
    await wrapper.get('select[aria-label="Filter by severity"]').setValue('critical')
    await flushPromises()
    await wrapper.get('[data-test="activity-view-tab-sessions"]').trigger('click')
    await flushPromises()
    await flushPromises()
    await wrapper.get(`[data-test="ws-calls-link-${WS_FAILED.id}"]`).trigger('click')
    await flushPromises()
    await flushPromises()
    if (!wrapper.find('select[aria-label="Filter by sensitive data"]').exists()) {
      await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
      await flushPromises()
    }
    await wrapper.get('select[aria-label="Filter by sensitive data"]').setValue('true')
    await flushPromises()
    expect((wrapper.get('select[aria-label="Filter by severity"]').element as HTMLSelectElement).value).toBe('')
    wrapper.unmount()
  })

  it('the Tool calls link of the session the calls view already shows keeps its filters', async () => {
    const { wrapper } = await mountAt(`/activity?session=${WS_FAILED.id}`)
    await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()
    await wrapper.get('select[aria-label="Filter by sensitive data"]').setValue('true')
    await flushPromises()
    await wrapper.get('[data-test="activity-view-tab-sessions"]').trigger('click')
    await flushPromises()
    await flushPromises()
    await wrapper.get(`[data-test="ws-calls-link-${WS_FAILED.id}"]`).trigger('click')
    await flushPromises()
    await flushPromises()
    expect(wrapper.findAll('[data-test="activity-row"]').length).toBe(0)
    // The other session's link does reset them.
    await wrapper.get('[data-test="activity-view-tab-sessions"]').trigger('click')
    await flushPromises()
    await flushPromises()
    await wrapper.get(`[data-test="ws-calls-link-${WS_OK.id}"]`).trigger('click')
    await flushPromises()
    await flushPromises()
    if (!wrapper.find('select[aria-label="Filter by sensitive data"]').exists()) {
      await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
      await flushPromises()
    }
    expect((wrapper.get('select[aria-label="Filter by sensitive data"]').element as HTMLSelectElement).value).toBe('')
    wrapper.unmount()
  })

  it('the Tool calls link of a second session reads that session\'s calls', async () => {
    const { wrapper, api } = await mountAt('/activity?view=sessions')
    const sessionOf = (call: any[]) => call[0].work_session_id ?? call[0].session_id
    await wrapper.get(`[data-test="ws-calls-link-${WS_FAILED.id}"]`).trigger('click')
    await flushPromises()
    await flushPromises()
    expect(sessionOf(api.getActivities.mock.calls.at(-1))).toBe(WS_FAILED.id)
    await wrapper.get('[data-test="activity-view-tab-sessions"]').trigger('click')
    await flushPromises()
    await flushPromises()
    await wrapper.get(`[data-test="ws-calls-link-${WS_OK.id}"]`).trigger('click')
    await flushPromises()
    await flushPromises()
    expect(sessionOf(api.getActivities.mock.calls.at(-1))).toBe(WS_OK.id)
    wrapper.unmount()
  })

  it('a Tool calls link followed from a reloaded Sessions address reads the calls, though only the view changes', async () => {
    // The address a look back at Sessions leaves behind: the session and the
    // type list of its Tool calls link are still in it.
    const types = 'tool_call,internal_tool_call,policy_decision'
    const { wrapper, api } = await mountAt(`/activity?view=sessions&session=${WS_FAILED.id}&type=${types}`)
    expect(api.getActivities).not.toHaveBeenCalled()
    await wrapper.get(`[data-test="ws-calls-link-${WS_FAILED.id}"]`).trigger('click')
    await flushPromises()
    await flushPromises()
    expect(api.getActivities).toHaveBeenCalledTimes(1)
    expect(wrapper.findAll('[data-test="activity-row"]').length).toBe(1)
    wrapper.unmount()
  })

  it('a Tool calls link reads the calls once, not twice', async () => {
    const { wrapper, api } = await mountAt('/activity?view=sessions')
    await wrapper.get(`[data-test="ws-calls-link-${WS_FAILED.id}"]`).trigger('click')
    await flushPromises()
    await flushPromises()
    expect(api.getActivities).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('choosing a session in the calls view narrows the rows already read, as before', async () => {
    const { wrapper, router, api } = await mountAt('/activity')
    const before = api.getActivities.mock.calls.length
    await router.push({ query: { session: WS_FAILED.id } })
    await flushPromises()
    await flushPromises()
    expect(wrapper.findAll('[data-test="activity-row"]').length).toBe(1)
    expect(api.getActivities.mock.calls.length).toBe(before)
    wrapper.unmount()
  })

  it('a timeline opened from a call and its Tool calls link back keep the filters of that session\'s calls', async () => {
    const { wrapper } = await mountAt(`/activity?session=${WS_FAILED.id}`)
    await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()
    await wrapper.get('select[aria-label="Filter by sensitive data"]').setValue('false')
    await flushPromises()
    await wrapper.get('[data-test="activity-open-act-1"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="activity-open-timeline"]').trigger('click')
    await flushPromises()
    await flushPromises()
    await wrapper.get('[data-test="timeline-calls-link"]').trigger('click')
    await flushPromises()
    await flushPromises()
    if (!wrapper.find('select[aria-label="Filter by sensitive data"]').exists()) {
      await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
      await flushPromises()
    }
    expect((wrapper.get('select[aria-label="Filter by sensitive data"]').element as HTMLSelectElement).value).toBe('false')
    wrapper.unmount()
  })

  it('a click that opens the timeline in another tab leaves the call\'s details open here', async () => {
    const { wrapper } = await mountAt('/activity')
    await wrapper.get('[data-test="activity-open-act-1"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="activity-open-timeline"]').trigger('click', { ctrlKey: true })
    await flushPromises()
    expect(wrapper.find('[data-test="activity-detail-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="activity-open-timeline"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('Back from a timeline to the calls of its session keeps the filters set there', async () => {
    const { wrapper, router } = await mountAt(`/activity?session=${WS_FAILED.id}`)
    await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()
    await wrapper.get('select[aria-label="Filter by sensitive data"]').setValue('false')
    await flushPromises()
    expect(wrapper.findAll('[data-test="activity-row"]').length).toBe(1)
    await router.push({ query: { view: 'sessions', ws: WS_FAILED.id, at: 'row:act-1' } })
    await flushPromises()
    await flushPromises()
    expect(wrapper.find('[data-test="session-timeline"]').exists()).toBe(true)
    await router.push({ query: { session: WS_FAILED.id } })
    await flushPromises()
    await flushPromises()
    if (!wrapper.find('select[aria-label="Filter by sensitive data"]').exists()) {
      await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
      await flushPromises()
    }
    expect((wrapper.get('select[aria-label="Filter by sensitive data"]').element as HTMLSelectElement).value).toBe('false')
    wrapper.unmount()
  })

  it('a session link out of the Sessions view does not inherit the parent of "View sub-calls"', async () => {
    const { wrapper, router, api } = await mountAt('/activity')
    const run = { id: 'act-3', type: 'internal_tool_call', status: 'success', timestamp: '2026-11-26T22:38:00Z', tool_name: 'code_execution', request_id: 'r3', duration_ms: 5 }
    api.getActivities.mockImplementation(() => Promise.resolve({ success: true, data: { activities: [CALL, NO_SESSION, run], total: 3, limit: 200, offset: 0 } }))
    await router.push({ query: { view: 'all' } })
    await flushPromises()
    await flushPromises()
    await wrapper.get('[data-test="activity-open-act-3"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="activity-view-subcalls"]').trigger('click')
    await flushPromises()
    expect(api.getActivities.mock.calls.at(-1)[0].parent_id).toBe('r3')
    await router.push({ query: { view: 'sessions', ws: WS_FAILED.id } })
    await flushPromises()
    await flushPromises()
    // From the timeline this time: its link does the same.
    await wrapper.get('[data-test="timeline-calls-link"]').trigger('click')
    await flushPromises()
    await flushPromises()
    expect(router.currentRoute.value.query.session).toBe(WS_FAILED.id)
    expect(api.getActivities.mock.calls.at(-1)[0].parent_id).toBeUndefined()
    wrapper.unmount()
  })

  it('a look at Sessions and back by the tabs keeps the filters of the calls view', async () => {
    // Only a link that names another session resets them, not a tab round trip.
    const { wrapper } = await mountAt(`/activity?session=${WS_FAILED.id}`)
    await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()
    await wrapper.get('select[aria-label="Filter by sensitive data"]').setValue('true')
    await flushPromises()
    await wrapper.get('[data-test="activity-view-tab-sessions"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="activity-view-tab-calls"]').trigger('click')
    await flushPromises()
    await flushPromises()
    expect(wrapper.findAll('[data-test="activity-row"]').length).toBe(0)
    if (!wrapper.find('select[aria-label="Filter by sensitive data"]').exists()) {
      await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
      await flushPromises()
    }
    expect((wrapper.get('select[aria-label="Filter by sensitive data"]').element as HTMLSelectElement).value).toBe('true')
    wrapper.unmount()
  })

  it('reads a repeated Sessions parameter as its first value, like the panel does', async () => {
    const { wrapper } = await mountAt(`/activity?view=sessions&failed=true&failed=false`)
    expect(wrapper.get('[data-test="activity-filters-count"]').text()).toBe('1')
    wrapper.unmount()
    const timeline = await mountAt(`/activity?view=sessions&ws=${WS_FAILED.id}&ws=other`)
    expect(timeline.wrapper.find('[data-test="session-timeline"]').exists()).toBe(true)
    // The first value names the session: that one is asked for, not the other.
    expect(timeline.api.getWorkSessionTimeline.mock.calls.every((call: any[]) => call[0] === WS_FAILED.id)).toBe(true)
    expect(timeline.api.getWorkSessionTimeline).toHaveBeenCalled()
    expect(timeline.wrapper.get('[data-test="activity-filter-strip"]').attributes('style') ?? '').toContain('display: none')
    timeline.wrapper.unmount()
  })

  it('the calls view does not count a "failed" left in the address', async () => {
    const { wrapper } = await mountAt('/activity?failed=true')
    expect(wrapper.find('[data-test="activity-filters-count"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('a call\'s detail links to its session timeline at that row', async () => {
    const { wrapper } = await mountAt('/activity')
    await wrapper.get('[data-test="activity-open-act-1"]').trigger('click')
    await flushPromises()
    const href = wrapper.get('[data-test="activity-open-timeline"]').attributes('href')!
    const query = new URL(href, 'http://x').searchParams
    expect(query.get('view')).toBe('sessions')
    expect(query.get('ws')).toBe(WS_FAILED.id)
    expect(query.get('at')).toBe('row:act-1')

    // Following it moves the focus into the timeline that replaces the calls view.
    await wrapper.get('[data-test="activity-open-timeline"]').trigger('click')
    await flushPromises()
    await flushPromises()
    expect(wrapper.find('[data-test="session-timeline"]').exists()).toBe(true)
    expect(document.activeElement?.getAttribute('data-test')).toBe('timeline-heading')
    wrapper.unmount()
  })

  it('a row with no work session offers no timeline', async () => {
    const { wrapper } = await mountAt('/activity')
    await wrapper.get('[data-test="activity-open-act-2"]').trigger('click')
    await flushPromises()
    // The details did open: the row keeps them, only the timeline link is absent.
    expect(wrapper.get('[data-test="activity-detail-panel"]').text()).toContain(NO_SESSION.tool_name)
    expect(wrapper.find('[data-test="activity-open-timeline"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
