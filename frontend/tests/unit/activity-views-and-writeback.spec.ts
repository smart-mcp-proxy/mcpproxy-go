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
    timestamp: `2026-09-20T09:0${n}:00Z`,
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
    await wrapper.find('[data-test="activity-view-tab-system"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.view).toBe('system')
    expect(router.currentRoute.value.query.type).toBeUndefined()
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
