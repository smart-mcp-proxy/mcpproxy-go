import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAvailableFeatures } from '@/composables/useScopeQuery'

// Spec 109 url-filter-contract rule 8 / T167 with a profile/client/token scope in
// the URL (review F1.1): resolving a server/tool conflict - by removing either
// conflicting chip or "Clear filters" - issues exactly ONE request, and that
// request already carries the scope. The scope param is independent of the
// conflict, so the conflicting URL must not hide it for the first request.

const getActivitiesMock = vi.hoisted(() => vi.fn())
const getStatusMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    getActivities: getActivitiesMock,
    getActivitySummary: vi.fn(() =>
      Promise.resolve({ success: true, data: { period: '24h', total_count: 0, success_count: 0, error_count: 0, blocked_count: 0, rejected_count: 0 } })
    ),
    getSessions: vi.fn(() => Promise.resolve({ success: true, data: { sessions: [] } })),
    getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    getStatus: getStatusMock,
    listAgentTokens: vi.fn(() => Promise.resolve({ success: true, data: { tokens: [] } })),
    getProfiles: vi.fn(() => Promise.resolve({ profiles: [] })),
    getClients: vi.fn(() => Promise.resolve({ success: true, data: { clients: [] } })),
  },
}))

const CONFLICT = '/activity?view=all&server=fixture&tool=other:x&profile=work'

async function mountActivityAt(path: string) {
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createMemoryHistory(),
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
  return { wrapper, router }
}

describe('Activity - clearing a conflict with a scope filter requests once (F1.1)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['profile', 'client', 'token'])
    getActivitiesMock.mockImplementation(() =>
      Promise.resolve({ success: true, data: { activities: [], total: 0, limit: 200, offset: 0 } })
    )
    getStatusMock.mockResolvedValue({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
  })
  afterEach(() => setAvailableFeatures([]))

  it('removing the tool chip: one request, server + profile', async () => {
    const { wrapper } = await mountActivityAt(CONFLICT)
    expect(getActivitiesMock).not.toHaveBeenCalled()
    await wrapper.find('[data-test="activity-filter-chip-tool"]').trigger('click')
    await flushPromises()
    await flushPromises()
    expect(getActivitiesMock).toHaveBeenCalledTimes(1)
    expect(getActivitiesMock.mock.calls[0][0]).toEqual(expect.objectContaining({ server: 'fixture', profile: 'work' }))
  })

  it('removing the server chip: one request, server:tool split + profile', async () => {
    const { wrapper } = await mountActivityAt(CONFLICT)
    await wrapper.find('[data-test="activity-filter-chip-server"]').trigger('click')
    await flushPromises()
    await flushPromises()
    expect(getActivitiesMock).toHaveBeenCalledTimes(1)
    expect(getActivitiesMock.mock.calls[0][0]).toEqual(expect.objectContaining({ server: 'other', tool: 'x', profile: 'work' }))
  })

  it('"Clear filters": one request that keeps the scope', async () => {
    const { wrapper } = await mountActivityAt(CONFLICT)
    await wrapper.find('[data-test="activity-scope-conflict-clear"]').trigger('click')
    await flushPromises()
    await flushPromises()
    expect(getActivitiesMock).toHaveBeenCalledTimes(1)
    const params = getActivitiesMock.mock.calls[0][0] as Record<string, unknown>
    expect(params.profile).toBe('work')
    expect(params.server).toBeUndefined()
    expect(params.tool).toBeUndefined()
  })
})
