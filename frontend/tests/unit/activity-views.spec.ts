import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109-k T112: url-filter-contract.md "view" -> REST table, applyRouteFilters()
// hydration from relative `from`/`to`, the write-back watch round trip, and the
// session scope param reaching REST (not just the client-side 200-row window).

const NOTES_CALL = {
  id: 'act-notes-1',
  type: 'tool_call',
  status: 'error',
  timestamp: new Date().toISOString(),
  server_name: 'notes',
  tool_name: 'search',
  request_id: 'req-notes-1',
  duration_ms: 10,
}

const OTHER_CALL = {
  id: 'act-other-1',
  type: 'tool_call',
  status: 'success',
  timestamp: new Date().toISOString(),
  server_name: 'filesystem',
  tool_name: 'read',
  request_id: 'req-other-1',
  duration_ms: 10,
}

const SESSION_CALL = {
  id: 'act-session-1',
  type: 'tool_call',
  status: 'success',
  timestamp: new Date().toISOString(),
  server_name: 'notes',
  tool_name: 'search',
  session_id: 'ws-abc123',
  work_session_id: 'ws-abc123',
  request_id: 'req-session-1',
  duration_ms: 10,
}

function filterFixture(all: typeof NOTES_CALL[], params?: Record<string, unknown>) {
  let rows = all
  if (params?.server) rows = rows.filter(a => a.server_name === params.server)
  if (params?.status) rows = rows.filter(a => a.status === params.status)
  if (params?.work_session_id) rows = rows.filter(a => (a as { work_session_id?: string }).work_session_id === params.work_session_id)
  if (params?.session_id) rows = rows.filter(a => (a as { session_id?: string }).session_id === params.session_id)
  return rows
}

let getActivitiesMock: ReturnType<typeof vi.fn>

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn((params?: Record<string, unknown>) => {
        const all = [NOTES_CALL, OTHER_CALL, SESSION_CALL]
        const activities = filterFixture(all, params)
        return ok({ activities, total: activities.length, limit: 200, offset: 0 })
      }),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 3, success_count: 2, error_count: 1, blocked_count: 0, rejected_count: 0 })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivityAt(path: string) {
  const api = (await import('@/services/api')).default
  getActivitiesMock = api.getActivities as unknown as ReturnType<typeof vi.fn>
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/activity', component: Activity },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return { wrapper, api, router }
}

const rows = (wrapper: Awaited<ReturnType<typeof mountActivityAt>>['wrapper']) =>
  wrapper.findAll('[data-test="activity-row"]')

describe('Activity views — URL filter contract (Spec 109-k T112)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('mounting /activity?server=notes&from=-24h&status=error sends server, status and a resolved start_time, and renders only the matching rows', async () => {
    const before = Date.now()
    const { wrapper } = await mountActivityAt('/activity?server=notes&from=-24h&status=error')

    expect(getActivitiesMock).toHaveBeenCalledWith(
      expect.objectContaining({ server: 'notes', status: 'error' })
    )
    const call = getActivitiesMock.mock.calls[0][0] as { start_time?: string }
    expect(call.start_time).toBeDefined()
    const startTime = new Date(call.start_time as string).getTime()
    // "-24h" resolved against "now": within a couple of minutes of exactly
    // 24h ago (isoToDateTimeLocal/dateTimeLocalToISO round-trip truncates to
    // whole minutes, so this is not exact to the second).
    expect(Math.abs(before - 24 * 3600 * 1000 - startTime)).toBeLessThan(2 * 60 * 1000)

    const visible = rows(wrapper)
    expect(visible).toHaveLength(1)
    expect(visible[0].text()).toContain('notes')
  })

  it('a session filter reaches REST as work_session_id/session_id, not just the client-side 200-row window', async () => {
    const { wrapper } = await mountActivityAt('/activity?session=ws-abc123')
    expect(getActivitiesMock).toHaveBeenCalledWith(
      expect.objectContaining({ work_session_id: 'ws-abc123' })
    )
    const visible = rows(wrapper)
    expect(visible).toHaveLength(1)
    expect(visible[0].text()).toContain('notes')
  })

  it('a legacy (non ws-) session value routes to session_id', async () => {
    await mountActivityAt('/activity?session=transport-xyz')
    expect(getActivitiesMock).toHaveBeenCalledWith(
      expect.objectContaining({ session_id: 'transport-xyz' })
    )
  })

  it('push then back re-applies the previous filters (route.query watch, no replace loop)', async () => {
    const { router } = await mountActivityAt('/activity?server=notes')
    expect(getActivitiesMock).toHaveBeenCalledWith(expect.objectContaining({ server: 'notes' }))
    getActivitiesMock.mockClear()

    await router.push('/activity?server=filesystem')
    await flushPromises()
    await flushPromises()
    expect(getActivitiesMock).toHaveBeenCalledWith(expect.objectContaining({ server: 'filesystem' }))
    getActivitiesMock.mockClear()

    await router.back()
    // vue-router's history mock needs a tick for the popstate to resolve.
    await flushPromises()
    await flushPromises()
    await flushPromises()

    expect(router.currentRoute.value.query.server).toBe('notes')
    expect(getActivitiesMock).toHaveBeenCalledWith(expect.objectContaining({ server: 'notes' }))
  })

  it('a relative from=-24h deep link is left untouched in the URL on mount (no replace loop, still resolved correctly for REST)', async () => {
    const { router } = await mountActivityAt('/activity?from=-24h')
    await flushPromises()
    // applyRouteFilters() hydrates the datetime-local control from the
    // relative shorthand BEFORE the write-back watch is registered, so its
    // initial value never counts as a "change" — the URL keeps the original
    // shareable "-24h", not some absolute instant nobody typed.
    expect(router.currentRoute.value.query.from).toBe('-24h')
  })

  it('editing the datetime-local control writes back a proper RFC 3339 value (not a naive local-clock string), and it round-trips', async () => {
    const { wrapper, router } = await mountActivityAt('/activity')
    await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()
    await wrapper.find('#activity-filter-from').setValue('2026-09-25T09:00')
    await flushPromises()
    await flushPromises()

    const from = router.currentRoute.value.query.from as string | undefined
    expect(from).toBeDefined()
    // Not the naive "YYYY-MM-DDTHH:mm" the control itself uses — an actual
    // instant, parseable the same way regardless of who opens the URL.
    expect(from).not.toBe('2026-09-25T09:00')
    expect(Number.isNaN(new Date(from as string).getTime())).toBe(false)

    const call = getActivitiesMock.mock.calls.at(-1)?.[0] as { start_time?: string } | undefined
    expect(call?.start_time).toBeDefined()
    expect(new Date(call!.start_time as string).getTime()).toBe(new Date(from as string).getTime())
  })
})
