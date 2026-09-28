import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Live QA findings on 109-k-activity-scope-filters:
//
// 1. "Activity.vue has no 'Tool calls / Sessions / System events / All' view
//    UI ... no folding, no empty-column hiding, no Sessions/System-events/All
//    tabs anywhere in frontend/src/views/Activity.vue."
// 2. "removing the 'Tool: read_0' chip clears the filter in the table but
//    the URL is unchanged" (part of the broader "No scope-aware page writes
//    filter changes back to the URL" finding).
//
// This suite exercises the four-tab view UI, the batch fold for a server's
// baseline tool approvals (acceptance scenario 6), and chip/tab write-back.

const TOOL_CALL = {
  id: 'act-1',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-09-20T10:00:00Z',
  server_name: 'filesystem',
  tool_name: 'read_0',
  request_id: 'req-1',
  duration_ms: 5,
}

function quarantineApproval(n: number) {
  return {
    id: `qc-${n}`,
    type: 'tool_quarantine_change',
    status: 'approved',
    // A real baseline batch is emitted within milliseconds of the others —
    // seconds apart within the same minute, well inside the fold's 5-minute
    // window, and zero-padded (a bare `09:0${n}` breaks for n >= 10).
    timestamp: `2026-09-20T09:00:${String(n).padStart(2, '0')}Z`,
    server_name: 'filesystem',
    tool_name: `tool_${n}`,
    metadata: { action: 'approved', tool_name: `tool_${n}` },
  }
}

// 14 tools approved on connect + one call the user made — acceptance
// scenario 6's exact shape ("filesystem: 14 tools approved").
const BATCH_ACTIVITIES = [TOOL_CALL, ...Array.from({ length: 14 }, (_, i) => quarantineApproval(i + 1))]

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn((params?: { type?: string }) => {
        let rows = BATCH_ACTIVITIES
        if (params?.type) {
          const types = params.type.split(',')
          rows = rows.filter(a => types.includes(a.type))
        }
        return ok({ activities: rows, total: rows.length, limit: 200, offset: 0 })
      }),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: BATCH_ACTIVITIES.length, success_count: 15, error_count: 0, blocked_count: 0, rejected_count: 0 })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivityAt(path: string) {
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: Activity },
      { path: '/sessions', name: 'sessions', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return { wrapper, router }
}

const rows = (wrapper: Awaited<ReturnType<typeof mountActivityAt>>['wrapper']) =>
  wrapper.findAll('[data-test="activity-row"]')

describe('Activity views (Spec 109-k, FR-070)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('renders all four view tabs, with "Tool calls" active by default', async () => {
    const { wrapper } = await mountActivityAt('/activity')
    const tabs = wrapper.find('[data-test="activity-view-tabs"]')
    expect(tabs.exists()).toBe(true)
    for (const id of ['calls', 'sessions', 'system', 'all']) {
      expect(wrapper.find(`[data-test="activity-view-tab-${id}"]`).exists()).toBe(true)
    }
    expect(wrapper.find('[data-test="activity-view-tab-calls"]').attributes('aria-selected')).toBe('true')
  })

  it('"System events" folds a server\'s tool-approval batch into one row (acceptance scenario 6)', async () => {
    const { wrapper } = await mountActivityAt('/activity?view=system')
    // The 14 quarantine-approval rows fold into exactly one line.
    expect(rows(wrapper)).toHaveLength(1)
    const summary = wrapper.find('[data-test="activity-quarantine-batch-summary"]')
    expect(summary.exists()).toBe(true)
    expect(summary.text()).toBe('filesystem: 14 tools approved')
  })

  it('clicking a tab writes `view` to the URL and drops any ?type= override, without a page reload', async () => {
    const { wrapper, router } = await mountActivityAt('/activity?view=calls&type=quarantine_change')
    // Precondition: the explicit `type=quarantine_change` override is really
    // in force before the click (0 rows: BATCH_ACTIVITIES has none of that
    // exact type, only tool_quarantine_change).
    expect(rows(wrapper)).toHaveLength(0)

    await wrapper.find('[data-test="activity-view-tab-system"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query.view).toBe('system')
    expect(router.currentRoute.value.query.type).toBeUndefined()
    // Verified zcode review finding: `effectiveTypes` only read `activeView`
    // inside the untaken branch of a ternary while an override was active,
    // so Vue never tracked it as a dependency — the tab visually activated
    // but the table (and the refetch) silently kept showing the OLD
    // filter's rows. This must now actually be "System events": the folded
    // quarantine batch, not the empty `type=quarantine_change` result.
    expect(rows(wrapper)).toHaveLength(1)
    expect(wrapper.find('[data-test="activity-quarantine-batch-summary"]').exists()).toBe(true)
  })

  it('switching back to "Tool calls" clears `view` from the URL (the canonical default)', async () => {
    const { wrapper, router } = await mountActivityAt('/activity?view=system')
    await wrapper.find('[data-test="activity-view-tab-calls"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.view).toBeUndefined()
  })

  it('"Sessions" swaps the activity table for the sessions panel, and issues no /activity request', async () => {
    const api = (await import('@/services/api')).default
    const { wrapper } = await mountActivityAt('/activity?view=sessions')
    expect(wrapper.find('[data-test="sessions-table"], [data-test="sessions-empty"]').exists()).toBe(true)
    expect(api.getActivities as ReturnType<typeof vi.fn>).not.toHaveBeenCalled()
  })

  // zcode review round 1, F4: from/to/server/tool/status/type/auth_type are
  // "not applicable here" in Sessions (rule 5) — they must render as disabled
  // chips, not vanish with zero chips shown.
  it('Sessions renders disabled "not applicable" chips for server/from/status left from a deep link', async () => {
    const { wrapper } = await mountActivityAt('/activity?view=sessions&server=filesystem&from=-24h&status=error')
    const strip = wrapper.find('[data-test="activity-sessions-disabled-filters"]')
    expect(strip.exists()).toBe(true)
    expect(wrapper.find('[data-test="activity-sessions-disabled-chip-server"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="activity-sessions-disabled-chip-start"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="activity-sessions-disabled-chip-status"]').exists()).toBe(true)
  })

  it('switching into Sessions keeps an explicit `type` in the URL (ignored, not cleared) and shows it as a disabled chip', async () => {
    const { wrapper, router } = await mountActivityAt('/activity?type=quarantine_change')
    await wrapper.find('[data-test="activity-view-tab-sessions"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query.type).toBe('quarantine_change')
    expect(wrapper.find('[data-test="activity-sessions-disabled-chip-type"]').exists()).toBe(true)
  })
})

describe('Activity filter chips write back to the URL (FR-080)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('removing the "Tool" chip clears `tool` from the URL, not only from the table', async () => {
    const { wrapper, router } = await mountActivityAt('/activity?view=calls&tool=read_0')
    expect(router.currentRoute.value.query.tool).toBe('read_0')
    expect(rows(wrapper)).toHaveLength(1)

    await wrapper.find('[data-test="activity-filter-chip-tool"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query.tool).toBeUndefined()
  })

  it('"Clear all" removes every filter param from the URL', async () => {
    const { wrapper, router } = await mountActivityAt('/activity?view=calls&tool=read_0&server=filesystem&status=success')
    await wrapper.find('.btn-xs.btn-ghost').trigger('click') // "Clear all"
    await flushPromises()
    expect(router.currentRoute.value.query.tool).toBeUndefined()
    expect(router.currentRoute.value.query.server).toBeUndefined()
    expect(router.currentRoute.value.query.status).toBeUndefined()
    // Clearing filters is not the same as leaving the tab.
    expect(router.currentRoute.value.query.view).toBe('calls')
  })
})

// Verified zcode review finding: Activity.vue read every URL parameter
// exactly once, in setup — a second deep link to the SAME route (Vue Router
// reuses the component; there is no remount) never re-applied. Concretely:
// SessionsPanel's own "View Activity" button, clicked from Activity's own
// Sessions tab, did nothing on the very first click; a second Tools-row
// "Calls" link clicked while Activity was already open kept showing the
// FIRST link's filter.
describe('Activity re-applies a second deep link without remounting (FR-080 / SC-009)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('a second session link updates which session is filtered, not just the URL', async () => {
    const { wrapper, router } = await mountActivityAt('/activity?view=calls&session=ws-aaaaa')
    await flushPromises()
    expect(wrapper.find('[data-test="activity-filter-chip-session"]').text()).toContain('aaaaa')

    // Same-route navigation, exactly what SessionsPanel's "View Activity" or
    // a second Tools-row "Calls" link does while Activity is already open.
    await router.push('/activity?view=calls&session=ws-bbbbb')
    await flushPromises()

    expect(wrapper.find('[data-test="activity-filter-chip-session"]').text()).toContain('bbbbb')
    expect(wrapper.find('[data-test="activity-filter-chip-session"]').text()).not.toContain('aaaaa')
  })

  it('a second tool link replaces the first tool filter (not additive, not stuck on the first one)', async () => {
    const { wrapper, router } = await mountActivityAt('/activity?view=calls&tool=read_0')
    await flushPromises()
    expect(rows(wrapper)).toHaveLength(1)

    await router.push('/activity?view=calls')
    await flushPromises()

    // The second link carries no `tool` at all — the stale filter from the
    // first one must not still be in force.
    expect(router.currentRoute.value.query.tool).toBeUndefined()
    expect(wrapper.find('[data-test="activity-filter-chip-tool"]').exists()).toBe(false)
  })
})

// Verified zcode review finding: the Usage/Home chart-bar links (T119) carry
// `from`/`to`, but Activity.vue never read them at all — a Timeline-bucket
// click landed on the fully unfiltered Tool-calls list, silently dropping
// its whole time bound.
describe('Activity applies an incoming from/to deep link (FR-082 link map)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    // The filter panel's open/closed state persists to localStorage (and
    // jsdom's localStorage is shared across tests in this file) — reset it
    // so each test's own "open the panel" click starts from the same state.
    window.localStorage.clear()
  })

  it('reads ?from=&to= into the date pickers and sends them to the REST request', async () => {
    const { wrapper } = await mountActivityAt(
      '/activity?view=calls&from=2026-09-20T09%3A00%3A00.000Z&to=2026-09-20T09%3A05%3A00.000Z'
    )
    await flushPromises()
    await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()

    const api = (await import('@/services/api')).default
    const lastCall = (api.getActivities as ReturnType<typeof vi.fn>).mock.calls.at(-1)![0]
    expect(lastCall.start_time).toBe('2026-09-20T09:00:00.000Z')
    expect(lastCall.end_time).toBe('2026-09-20T09:05:00.000Z')

    // The native datetime-local inputs actually show something (not blank —
    // an absolute "...Z" ISO string is not a valid value for that input type
    // on its own).
    const fromInput = wrapper.find('#activity-filter-from')
    const toInput = wrapper.find('#activity-filter-to')
    expect((fromInput.element as HTMLInputElement).value).not.toBe('')
    expect((toInput.element as HTMLInputElement).value).not.toBe('')
  })

  it('resolves a relative ?from= (e.g. -24h, the CallHistogram link\'s shape) the same way the composable does', async () => {
    const { wrapper } = await mountActivityAt('/activity?view=calls&tool=read_0&from=-24h')
    await flushPromises()
    await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()
    const fromInput = wrapper.find('#activity-filter-from')
    expect((fromInput.element as HTMLInputElement).value).not.toBe('')

    const api = (await import('@/services/api')).default
    const lastCall = (api.getActivities as ReturnType<typeof vi.fn>).mock.calls.at(-1)![0]
    expect(lastCall.start_time).toBeTruthy()
    expect(lastCall.tool).toBe('read_0')
  })

  // zcode review round 1, F5: the write-back watch used to fire for ANY
  // tracked filter change and always wrote the RESOLVED absolute instant,
  // freezing a sticky relative `from=-24h` the moment an unrelated filter
  // (e.g. status) changed — a URL bookmarked afterward stopped rolling.
  it('a sticky relative ?from=-24h survives an unrelated filter change untouched', async () => {
    const { wrapper, router } = await mountActivityAt('/activity?view=calls&from=-24h')
    await flushPromises()
    expect(router.currentRoute.value.query.from).toBe('-24h')

    await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()

    // Changing an unrelated filter (status) must not rewrite `from` into a
    // frozen absolute instant.
    const statusSelect = wrapper.find('select[aria-label="Filter by status"]')
    await statusSelect.setValue('error')
    await flushPromises()

    expect(router.currentRoute.value.query.from).toBe('-24h')
  })

  it('actually editing the date picker DOES write the resolved absolute instant', async () => {
    const { wrapper, router } = await mountActivityAt('/activity?view=calls&from=-24h')
    await flushPromises()
    await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()

    const fromInput = wrapper.find('#activity-filter-from')
    await fromInput.setValue('2026-01-01T00:00')
    await flushPromises()

    const fromQuery = router.currentRoute.value.query.from as string
    expect(fromQuery).not.toBe('-24h')
    // An absolute RFC3339 instant, not the relative shorthand still in force.
    expect(new Date(fromQuery).getTime()).toBe(new Date('2026-01-01T00:00').getTime())
  })
})

// Live QA finding: `?session=` is read into the filter picker and applied
// client-side over the loaded 200-row window, but was never sent to REST at
// all — contracts/url-filter-contract.md's `session` row (`work_session_id`
// when the value starts with `ws-`, otherwise `session_id`, the CLI's
// existing `sessionQueryParam` rule) and quickstart.md's 109-k pass condition
// ("no unfiltered fetch in the network log") both require it on the request,
// not just on the table.
describe('Activity sends the session filter to REST (url-filter-contract.md "session" row)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('a work session id (ws- prefix) is sent as work_session_id', async () => {
    await mountActivityAt('/activity?view=calls&session=ws-aaaaa')
    await flushPromises()

    const api = (await import('@/services/api')).default
    const lastCall = (api.getActivities as ReturnType<typeof vi.fn>).mock.calls.at(-1)![0]
    expect(lastCall.work_session_id).toBe('ws-aaaaa')
    expect(lastCall.session_id).toBeUndefined()
  })

  it('a raw transport session id (no ws- prefix) is sent as session_id', async () => {
    await mountActivityAt('/activity?view=calls&session=raw-transport-123')
    await flushPromises()

    const api = (await import('@/services/api')).default
    const lastCall = (api.getActivities as ReturnType<typeof vi.fn>).mock.calls.at(-1)![0]
    expect(lastCall.session_id).toBe('raw-transport-123')
    expect(lastCall.work_session_id).toBeUndefined()
  })
})
