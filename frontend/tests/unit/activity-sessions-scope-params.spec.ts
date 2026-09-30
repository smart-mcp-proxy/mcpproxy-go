import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { setAvailableFeatures } from '@/composables/useScopeQuery'

// zcode review round 1, F8: acceptance scenario #9's second clause ("once
// features.scope_filters lists profile/client/token, the Sessions view sends
// them to GET /sessions on both Web and macOS") was only implemented on
// macOS (ScopeFilterTests.testSessionsViewCarriesScopeFiltersOnceAvailable).
// Web's Sessions view called api.getSessions(limit, status) with no scope
// plumbing at all, so it could never send client/profile/token to GET
// /sessions even once the feature was listed.

let getSessionsMock: ReturnType<typeof vi.fn>
let getActivitiesMock: ReturnType<typeof vi.fn>
let sessionsFixture: Array<Record<string, unknown>> = []

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() => ok({ activities: [], total: 0, limit: 200, offset: 0 })),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 0, success_count: 0, error_count: 0, blocked_count: 0, rejected_count: 0 })
      ),
      getSessions: vi.fn(() => ok({ sessions: sessionsFixture })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivityAt(path: string) {
  const api = (await import('@/services/api')).default
  getSessionsMock = api.getSessions as unknown as ReturnType<typeof vi.fn>
  getActivitiesMock = api.getActivities as unknown as ReturnType<typeof vi.fn>
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: Activity },
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

describe('Activity Sessions view — scope params to GET /sessions (Spec 108 FR-031, zcode F8)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    sessionsFixture = []
  })

  afterEach(() => {
    setAvailableFeatures([])
  })

  it('hidden until features.scope_filters lists them: no scope params sent even with ?client= in the URL', async () => {
    setAvailableFeatures([])
    const { wrapper } = await mountActivityAt('/activity?view=sessions&client=cursor')
    // Only ever called with (limit) or (limit, status) — never a scoped third arg.
    for (const call of getSessionsMock.mock.calls) {
      expect(call[2]).toBeUndefined()
    }
    wrapper.unmount()
  })

  it('once features.scope_filters lists profile/client/token, the Sessions view sends them to GET /sessions', async () => {
    setAvailableFeatures(['scope_filters'])
    const { wrapper } = await mountActivityAt('/activity?view=sessions&client=cursor')
    const scopedCall = getSessionsMock.mock.calls.find(c => c[2] && c[2].client)
    expect(scopedCall).toBeTruthy()
    expect(scopedCall![2]).toEqual(expect.objectContaining({ client: 'cursor' }))
    wrapper.unmount()
  })

  it('a late unscoped on-mount /sessions response never overwrites the scoped rows (live QA failure 1)', async () => {
    setAvailableFeatures(['scope_filters'])
    const row = (id: string, client: string) => ({
      id,
      work_session_id: `ws-${id}`,
      client_name: client,
      status: 'active',
      tool_call_count: 1,
      total_tokens: 10,
      start_time: '2026-09-28T10:00:00Z',
      last_activity: '2026-09-28T10:01:00Z',
    })
    const api = (await import('@/services/api')).default
    ;(api.getSessions as unknown as ReturnType<typeof vi.fn>).mockImplementation(
      (_limit: number, _status?: string, scope?: { client?: string }) => {
        if (scope && scope.client) {
          return Promise.resolve({ success: true, data: { sessions: [row('c1', 'cursor')] } })
        }
        // The unscoped fetch resolves LAST, as it did in the live repro.
        return new Promise(resolve =>
          setTimeout(
            () => resolve({ success: true, data: { sessions: [row('c1', 'cursor'), row('x1', 'other'), row('x2', 'other')] } }),
            30
          )
        )
      }
    )
    const { wrapper } = await mountActivityAt('/activity?view=sessions&client=cursor')
    await new Promise(r => setTimeout(r, 80))
    await flushPromises()
    const rows = wrapper.findAll('[data-test="session-view-activity"]').length
    wrapper.unmount()
    // vi.clearAllMocks keeps implementations: restore the file-wide default.
    ;(api.getSessions as unknown as ReturnType<typeof vi.fn>).mockImplementation(() =>
      Promise.resolve({ success: true, data: { sessions: sessionsFixture } })
    )
    expect(rows).toBe(1)
  })

  it('does not send scope params outside the Sessions view', async () => {
    setAvailableFeatures(['scope_filters'])
    const { wrapper } = await mountActivityAt('/activity?view=calls&client=cursor')
    for (const call of getSessionsMock.mock.calls) {
      expect(call[2]).toBeUndefined()
    }
    wrapper.unmount()
  })

  it('a Sessions row links and filters by its work-session id', async () => {
    sessionsFixture = [{
      id: 'transport-S1',
      work_session_id: 'ws-W1',
      client_name: 'Test client',
      status: 'active',
      tool_call_count: 1,
      total_tokens: 10,
      start_time: '2026-09-28T10:00:00Z',
      last_activity: '2026-09-28T10:01:00Z',
    }]
    const { wrapper, router } = await mountActivityAt('/activity?view=sessions')

    const link = wrapper.find('[data-test="session-view-activity"]')
    expect(link.attributes('href')).toBe('/activity?view=calls&session=ws-W1')
    await link.trigger('click')
    await flushPromises()
    await flushPromises()

    expect(router.currentRoute.value.query).toMatchObject({ view: 'calls', session: 'ws-W1' })
    expect(getActivitiesMock).toHaveBeenCalledWith(expect.objectContaining({ work_session_id: 'ws-W1' }))
    expect(getActivitiesMock.mock.calls.at(-1)?.[0].session_id).toBeUndefined()
    wrapper.unmount()
  })

  it('a legacy Sessions row falls back to its raw transport id', async () => {
    sessionsFixture = [{
      id: 'legacy-S1',
      client_name: 'Legacy client',
      status: 'closed',
      tool_call_count: 1,
      total_tokens: 10,
      start_time: '2026-09-28T10:00:00Z',
      last_activity: '2026-09-28T10:01:00Z',
    }]
    const { wrapper, router } = await mountActivityAt('/activity?view=sessions')

    const link = wrapper.find('[data-test="session-view-activity"]')
    expect(link.attributes('href')).toBe('/activity?view=calls&session=legacy-S1')
    await link.trigger('click')
    await flushPromises()
    await flushPromises()

    expect(router.currentRoute.value.query).toMatchObject({ view: 'calls', session: 'legacy-S1' })
    expect(getActivitiesMock).toHaveBeenCalledWith(expect.objectContaining({ session_id: 'legacy-S1' }))
    expect(getActivitiesMock.mock.calls.at(-1)?.[0].work_session_id).toBeUndefined()
    wrapper.unmount()
  })
})
