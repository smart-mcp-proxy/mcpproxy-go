import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Activity transparency: one `code_execution` run must be navigable in BOTH
// directions from the Activity Log —
//   parent -> children  GET /api/v1/activity?parent_id=<parent request_id>
//   child  -> parent    GET /api/v1/activity?request_id=<child parent_id>
// The sub-call view is a real filter (server-side too, so children beyond the
// loaded page are included) and clearing it restores the normal list.

const PARENT = {
  id: 'act-parent',
  type: 'internal_tool_call',
  status: 'success',
  timestamp: '2026-08-21T10:00:00Z',
  tool_name: 'code_execution',
  request_id: 'req-parent-abcdefgh',
  duration_ms: 900,
  metadata: { internal_tool_name: 'code_execution' },
}

const CHILD_A = {
  id: 'act-child-a',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-08-21T10:00:01Z',
  server_name: 'github',
  tool_name: 'create_issue',
  request_id: 'req-child-a',
  parent_id: 'req-parent-abcdefgh',
  duration_ms: 120,
}

const CHILD_B = {
  id: 'act-child-b',
  type: 'tool_call',
  status: 'error',
  timestamp: '2026-08-21T10:00:02Z',
  server_name: 'slack',
  tool_name: 'post_message',
  request_id: 'req-child-b',
  parent_id: 'req-parent-abcdefgh',
  duration_ms: 80,
}

const UNRELATED = {
  id: 'act-plain',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-08-21T09:59:00Z',
  server_name: 'github',
  tool_name: 'list_repos',
  request_id: 'req-plain',
  duration_ms: 40,
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      // The mock behaves like the contract: parent_id narrows to sub-calls,
      // request_id resolves one record by correlation id.
      getActivities: vi.fn((params?: { parent_id?: string; request_id?: string }) => {
        const all = [PARENT, CHILD_A, CHILD_B, UNRELATED]
        let activities = all
        if (params?.parent_id) {
          activities = all.filter(a => (a as { parent_id?: string }).parent_id === params.parent_id)
        } else if (params?.request_id) {
          activities = all.filter(a => a.request_id === params.request_id)
        }
        return ok({ activities, total: activities.length, limit: 200, offset: 0 })
      }),
      getActivitySummary: vi.fn(() =>
        ok({
          period: '24h',
          total_count: 4,
          success_count: 3,
          error_count: 1,
          blocked_count: 0,
          rejected_count: 0,
        })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivity() {
  const api = (await import('@/services/api')).default
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/activity', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push('/activity')
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  return { wrapper, api: api as unknown as { getActivities: ReturnType<typeof vi.fn> } }
}

type Wrapper = Awaited<ReturnType<typeof mountActivity>>['wrapper']

const rows = (wrapper: Wrapper) => wrapper.findAll('[data-test="activity-row"]')

/** The one row carrying the code_execution PARENT badge. */
const parentRowOf = (wrapper: Wrapper) =>
  rows(wrapper).find(r => r.find('[data-test="activity-parent-badge"]').exists())!

describe('Activity Log — code_execution parent/sub-call navigation', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('badges sub-call rows and the code_execution parent row', async () => {
    const { wrapper } = await mountActivity()

    expect(rows(wrapper)).toHaveLength(4)
    expect(wrapper.findAll('[data-test="activity-child-badge"]')).toHaveLength(2)
    expect(wrapper.find('[data-test="activity-child-badge"]').text()).toContain('via code_execution')
    expect(wrapper.findAll('[data-test="activity-parent-badge"]')).toHaveLength(1)
  })

  it('filters to the sub-calls of a run and refetches with parent_id', async () => {
    const { wrapper, api } = await mountActivity()

    // Open the parent row's drawer. Sub-call rows also mention code_execution
    // (in their "↳ via" badge), so key on the parent badge instead.
    const parentRow = parentRowOf(wrapper)
    await parentRow.trigger('click')
    await flushPromises()

    const viewSubCalls = wrapper.find('[data-test="activity-view-subcalls"]')
    expect(viewSubCalls.exists()).toBe(true)
    expect(viewSubCalls.text()).toContain('(2)')

    await viewSubCalls.trigger('click')
    await flushPromises()

    expect(api.getActivities).toHaveBeenCalledWith(
      expect.objectContaining({ parent_id: 'req-parent-abcdefgh' })
    )
    const visible = rows(wrapper)
    expect(visible).toHaveLength(2)
    expect(visible.map(r => r.text()).join(' ')).not.toContain('list_repos')
    expect(wrapper.find('[data-test="activity-parent-filter-chip"]').exists()).toBe(true)
  })

  it('restores the full list when the sub-call filter chip is cleared', async () => {
    const { wrapper } = await mountActivity()

    const parentRow = parentRowOf(wrapper)
    await parentRow.trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="activity-view-subcalls"]').trigger('click')
    await flushPromises()
    expect(rows(wrapper)).toHaveLength(2)

    await wrapper.find('[data-test="activity-parent-filter-chip"]').trigger('click')
    await flushPromises()

    expect(rows(wrapper)).toHaveLength(4)
    expect(wrapper.find('[data-test="activity-parent-filter-chip"]').exists()).toBe(false)
  })

  it('jumps from a sub-call back to its parent call', async () => {
    const { wrapper } = await mountActivity()

    const childRow = rows(wrapper).find(r => r.text().includes('create_issue'))!
    await childRow.trigger('click')
    await flushPromises()

    const viewParent = wrapper.find('[data-test="activity-view-parent"]')
    expect(viewParent.exists()).toBe(true)

    await viewParent.trigger('click')
    await flushPromises()

    // The drawer now describes the parent run.
    const drawer = wrapper.find('.drawer-side')
    expect(drawer.text()).toContain('req-parent-abcdefgh')
    expect(wrapper.find('[data-test="activity-view-subcalls"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="activity-view-parent"]').exists()).toBe(false)
  })
})
