import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 url-filter-contract rule 8 / US6-8 (T167, #1394 item 2): clearing a
// server/tool conflict - "Clear filters", or removing either conflicting chip -
// issues exactly one request for what remains and leaves the page on rows, not
// on "No activity records found".

const FS_READ = {
  id: 'a1',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-09-20T10:04:00Z',
  server_name: 'fixture',
  tool_name: 'read',
  request_id: 'r1',
  duration_ms: 10,
}
const OTHER = {
  id: 'a2',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-09-20T10:05:00Z',
  server_name: 'other',
  tool_name: 'x',
  request_id: 'r2',
  duration_ms: 10,
}
const ALL = [FS_READ, OTHER]

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn((params?: { server?: string; tool?: string }) => {
        let rows = ALL
        if (params?.server) rows = rows.filter(a => a.server_name === params.server)
        if (params?.tool) rows = rows.filter(a => a.tool_name === params.tool)
        return ok({ activities: rows, total: rows.length, limit: 200, offset: 0 })
      }),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 2, success_count: 2, error_count: 0, blocked_count: 0, rejected_count: 0 })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivityAt(path: string) {
  const api = (await import('@/services/api')).default as unknown as { getActivities: ReturnType<typeof vi.fn> }
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
  return { wrapper, router, api }
}

const CONFLICT = '/activity?view=all&server=fixture&tool=other:x'

describe('Activity - clearing a server/tool conflict refetches once (T167)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('"Clear filters" on the banner issues one unfiltered request and shows rows', async () => {
    const { wrapper, router, api } = await mountActivityAt(CONFLICT)
    expect(api.getActivities).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="activity-scope-conflict"]').exists()).toBe(true)

    await wrapper.find('[data-test="activity-scope-conflict-clear"]').trigger('click')
    await flushPromises()
    await flushPromises()

    expect(api.getActivities).toHaveBeenCalledTimes(1)
    const params = api.getActivities.mock.calls[0][0] as Record<string, unknown>
    expect(params.server).toBeUndefined()
    expect(params.tool).toBeUndefined()
    expect(params.type).toBeUndefined()
    expect(wrapper.find('[data-test="activity-scope-conflict"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="activity-row"]')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('No activity records found')
    expect(router.currentRoute.value.query).toEqual({ view: 'all' })
  })

  it('removing only the tool chip requests the remaining server once', async () => {
    const { wrapper, router, api } = await mountActivityAt(CONFLICT)
    await wrapper.find('[data-test="activity-filter-chip-tool"]').trigger('click')
    await flushPromises()
    await flushPromises()

    expect(api.getActivities).toHaveBeenCalledTimes(1)
    const params = api.getActivities.mock.calls[0][0] as Record<string, unknown>
    expect(params.server).toBe('fixture')
    expect(params.tool).toBeUndefined()
    expect(wrapper.find('[data-test="activity-scope-conflict"]').exists()).toBe(false)
    expect(router.currentRoute.value.query.server).toBe('fixture')
    expect(router.currentRoute.value.query.tool).toBeUndefined()
    expect(wrapper.findAll('[data-test="activity-row"]')).toHaveLength(1)
  })

  it('removing only the server chip requests the remaining "server:tool" once', async () => {
    const { wrapper, api } = await mountActivityAt(CONFLICT)
    await wrapper.find('[data-test="activity-filter-chip-server"]').trigger('click')
    await flushPromises()
    await flushPromises()

    expect(api.getActivities).toHaveBeenCalledTimes(1)
    const params = api.getActivities.mock.calls[0][0] as Record<string, unknown>
    // The tool's own server prefix carries the server once the explicit one is gone.
    expect(params.server).toBe('other')
    expect(params.tool).toBe('x')
    expect(wrapper.find('[data-test="activity-scope-conflict"]').exists()).toBe(false)
  })

  it('editing the URL to resolve the conflict requests once', async () => {
    const { wrapper, router, api } = await mountActivityAt(CONFLICT)
    await router.replace('/activity?view=all&server=fixture')
    await flushPromises()
    await flushPromises()

    expect(api.getActivities).toHaveBeenCalledTimes(1)
    expect((api.getActivities.mock.calls[0][0] as { server?: string }).server).toBe('fixture')
    expect(wrapper.find('[data-test="activity-scope-conflict"]').exists()).toBe(false)
  })

  it('agreeing servers are not a conflict and request once on mount', async () => {
    const { wrapper, api } = await mountActivityAt('/activity?view=all&server=fixture&tool=fixture:read')
    expect(wrapper.find('[data-test="activity-scope-conflict"]').exists()).toBe(false)
    expect(api.getActivities).toHaveBeenCalledTimes(1)
  })
})
