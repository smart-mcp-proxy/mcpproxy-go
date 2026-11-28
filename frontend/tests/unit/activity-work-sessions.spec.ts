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
    expect(wrapper.find('[data-test="activity-open-timeline"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
