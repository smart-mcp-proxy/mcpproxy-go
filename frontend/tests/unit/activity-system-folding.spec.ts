import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109-k T112 / FR-071: "In System events, consecutive records of the
// same type and server within 60 s MUST fold into one summary row
// ('filesystem: 14 tools approved') that expands in place. Folding is
// presentation only; export and CLI output are unfolded." (Acceptance
// scenario 6.) The fold RULE itself (adjacency, 60s window, same
// type/server/status) is exhaustively unit-tested in
// activity-run-grouping.spec.ts against the pure `groupActivityRuns`
// helper; this suite is the mounted-component half: the System events view
// actually renders the one folded row, expanding it shows all 14, and
// exporting narrows by the same filters regardless of fold/expand state.

function approval(n: number) {
  return {
    id: `qc-${n}`,
    type: 'tool_quarantine_change',
    status: 'approved',
    timestamp: `2026-09-20T10:00:${String(n).padStart(2, '0')}Z`,
    server_name: 'filesystem',
    tool_name: `tool_${n}`,
    request_id: `req-qc-${n}`,
  }
}

const APPROVALS = Array.from({ length: 14 }, (_, i) => approval(i))

let getActivityExportUrlMock: ReturnType<typeof vi.fn>

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() => ok({ activities: APPROVALS, total: APPROVALS.length, limit: 200, offset: 0 })),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 14, success_count: 14, error_count: 0, blocked_count: 0, rejected_count: 0 })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivityAt(path: string) {
  const api = (await import('@/services/api')).default
  getActivityExportUrlMock = api.getActivityExportUrl as unknown as ReturnType<typeof vi.fn>
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [{ path: '/activity', component: Activity }],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('System events — batch fold (Spec 109-k T112, FR-071, acceptance scenario 6)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('folds 14 per-tool approvals into one summary row', async () => {
    const wrapper = await mountActivityAt('/activity?view=system')
    const rows = wrapper.findAll('[data-test="activity-row"]')
    expect(rows).toHaveLength(1)
    expect(wrapper.find('[data-test="activity-quarantine-batch-summary"]').text()).toContain('14 tools approved')
  })

  it('expands the folded row back into its 14 members', async () => {
    const wrapper = await mountActivityAt('/activity?view=system')
    await wrapper.find('[data-test="activity-run-count"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-test="activity-run-member"]')).toHaveLength(13)
  })

  it('exporting is unaffected by fold/expand state — same filter-derived request either way', async () => {
    const wrapper = await mountActivityAt('/activity?view=system')

    // exportActivities() is wired straight to filter state (server/tool/type/
    // status/parent_id/start_time/end_time) — it never reads the folded/
    // expanded row list at all, so calling it collapsed vs. expanded must
    // produce byte-identical export requests.
    const exportJsonLink = wrapper.findAll('a').find(a => a.text().includes('Export as JSON'))
    expect(exportJsonLink).toBeTruthy()
    await exportJsonLink!.trigger('click')
    const collapsedCall = [...getActivityExportUrlMock.mock.calls[0]]

    await wrapper.find('[data-test="activity-run-count"]').trigger('click')
    await flushPromises()
    await exportJsonLink!.trigger('click')
    const expandedCall = [...getActivityExportUrlMock.mock.calls[1]]

    expect(expandedCall).toEqual(collapsedCall)
    expect(collapsedCall[0]).toEqual(expect.objectContaining({ type: expect.stringContaining('quarantine_change') }))
  })
})
