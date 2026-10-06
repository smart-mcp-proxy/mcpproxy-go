import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-070 / US6-6 (T168): a refused attempt is stored as a
// `policy_decision` row (status `blocked`). The Tool calls view lists it only
// under `status=blocked`, so the "N blocked" chip must open that filter, and an
// empty Tool calls table with blocked attempts in the window must offer it.

const CALL = {
  id: 'c1',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-09-20T10:04:00Z',
  server_name: 'fixture',
  tool_name: 'read',
  request_id: 'r1',
  duration_ms: 10,
}
const REFUSAL = {
  id: 'p1',
  type: 'policy_decision',
  status: 'blocked',
  timestamp: '2026-09-20T10:06:00Z',
  server_name: 'fixture',
  tool_name: 'write',
  request_id: 'r2',
  metadata: { block_reason: 'profile refused' },
}

const mockState = vi.hoisted(() => ({
  rows: [] as unknown[],
  summary: {} as Record<string, unknown>,
}))

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn((params?: { type?: string; status?: string }) => {
        const types = params?.type ? params.type.split(',') : null
        let rows = mockState.rows as Array<{ type: string; status: string }>
        if (types) rows = rows.filter(a => types.includes(a.type))
        if (params?.status) rows = rows.filter(a => a.status === params.status)
        return ok({ activities: rows, total: rows.length, limit: 200, offset: 0 })
      }),
      getActivitySummary: vi.fn(() => ok(mockState.summary)),
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

describe('Activity - blocked attempts in the Tool calls view (T168)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
    mockState.rows = [CALL, REFUSAL]
    mockState.summary = { period: '24h', total_count: 3, call_count: 1, success_count: 1, blocked_count: 1 }
  })

  it('the "1 blocked" chip names refused calls and opens the blocked rows', async () => {
    const { wrapper, router, api } = await mountActivityAt('/activity')
    expect(wrapper.findAll('[data-test="activity-row"]')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('profile refused')

    const chip = wrapper.find('[data-test="activity-compact-blocked"]')
    expect(chip.attributes('title')).toContain('refused')

    api.getActivities.mockClear()
    await chip.trigger('click')
    await flushPromises()
    await flushPromises()

    expect(api.getActivities).toHaveBeenCalledTimes(1)
    expect(api.getActivities).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'tool_call,internal_tool_call,policy_decision', status: 'blocked' })
    )
    expect(router.currentRoute.value.query.status).toBe('blocked')
    expect(wrapper.findAll('[data-test="activity-row"]')).toHaveLength(1)
    expect(wrapper.findAll('[data-test="activity-row"]')[0].text()).toContain('write')
  })

  it('an empty Tool calls table with blocked attempts offers "Show 1 blocked attempt"', async () => {
    mockState.rows = [REFUSAL]
    mockState.summary = { period: '24h', total_count: 1, call_count: 0, blocked_count: 1 }
    const { wrapper, router } = await mountActivityAt('/activity?view=calls')

    const button = wrapper.find('[data-test="activity-empty-show-blocked"]')
    expect(button.exists()).toBe(true)
    expect(button.text()).toBe('Show 1 blocked attempt')

    await button.trigger('click')
    await flushPromises()
    await flushPromises()
    expect(router.currentRoute.value.query.status).toBe('blocked')
    expect(wrapper.findAll('[data-test="activity-row"]')).toHaveLength(1)
  })

  it('the offer still lists refusals when an explicit type filter excludes policy_decision (#1466)', async () => {
    mockState.rows = [REFUSAL]
    mockState.summary = { period: '24h', total_count: 1, call_count: 0, blocked_count: 1 }
    const { wrapper } = await mountActivityAt('/activity?view=calls&type=tool_call')

    const button = wrapper.find('[data-test="activity-empty-show-blocked"]')
    expect(button.exists()).toBe(true)
    await button.trigger('click')
    await flushPromises()
    await flushPromises()
    expect(wrapper.findAll('[data-test="activity-row"]')).toHaveLength(1)
    expect(wrapper.findAll('[data-test="activity-row"]')[0].text()).toContain('write')
  })

  it('pluralises the offer for several blocked attempts', async () => {
    mockState.rows = []
    mockState.summary = { period: '24h', total_count: 2, call_count: 0, blocked_count: 2 }
    const { wrapper } = await mountActivityAt('/activity?view=calls')
    expect(wrapper.find('[data-test="activity-empty-show-blocked"]').text()).toBe('Show 2 blocked attempts')
  })

  it('offers nothing on the All view, which already lists refusals', async () => {
    mockState.rows = [REFUSAL]
    const { wrapper } = await mountActivityAt('/activity?view=all')
    expect(wrapper.findAll('[data-test="activity-row"]')).toHaveLength(1)
    expect(wrapper.find('[data-test="activity-empty-show-blocked"]').exists()).toBe(false)
  })

  it('says "1 call", not "1 calls", on the Events tile', async () => {
    window.localStorage.setItem('mcpproxy.activity.filtersExpanded', 'true')
    mockState.summary = { period: '24h', total_count: 3, call_count: 1, success_count: 1, blocked_count: 1 }
    const one = await mountActivityAt('/activity')
    expect(one.wrapper.find('[data-test="kpi-card-total"] .stat-desc').text()).toBe('1 call')

    mockState.summary = { period: '24h', total_count: 3, call_count: 2, success_count: 2, blocked_count: 1 }
    const two = await mountActivityAt('/activity')
    expect(two.wrapper.find('[data-test="kpi-card-total"] .stat-desc').text()).toBe('2 calls')
  })
})
