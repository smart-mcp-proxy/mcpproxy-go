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

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() => ok({ activities: [], total: 0, limit: 200, offset: 0 })),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 0, success_count: 0, error_count: 0, blocked_count: 0, rejected_count: 0 })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivityAt(path: string) {
  const api = (await import('@/services/api')).default
  getSessionsMock = api.getSessions as unknown as ReturnType<typeof vi.fn>
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

  it('does not send scope params outside the Sessions view', async () => {
    setAvailableFeatures(['scope_filters'])
    const { wrapper } = await mountActivityAt('/activity?view=calls&client=cursor')
    for (const call of getSessionsMock.mock.calls) {
      expect(call[2]).toBeUndefined()
    }
    wrapper.unmount()
  })
})
