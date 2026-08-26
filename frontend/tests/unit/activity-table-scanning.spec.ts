import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// The Activity table is SCANNED, not read. Three audit findings (#1046) are all
// about what that costs:
//
//   F5  — twelve consecutive identical calls produced twelve rows, and every one
//         of them was stamped "Success".
//   F26 — Intent and Sensitive were unlabelled emoji: 📖 / ✏️ / ⚠️ and a red ☢ 1,
//         with no legend and colour+glyph as the only encoding.
//   F2  — the status tiles above the table did not sum to the total beside them.

const ECHO = (n: number, over: Record<string, unknown> = {}) => ({
  id: `echo-${n}`,
  type: 'tool_call',
  status: 'success',
  timestamp: `2026-08-21T10:0${n}:00Z`,
  server_name: 'everything',
  tool_name: 'echo',
  duration_ms: 4,
  metadata: { intent: { operation_type: 'read', reason: 'poll the echo endpoint' } },
  ...over,
})

// Newest first, as the default sort delivers them: eleven successes with one
// failure in the middle — the exact shape F5 describes.
const ACTIVITIES = [
  ECHO(9),
  ECHO(8),
  ECHO(7),
  ECHO(6, { status: 'error', id: 'echo-bad' }),
  ECHO(5),
  ECHO(4),
  ECHO(3),
  {
    id: 'scan-1',
    type: 'security_scan',
    status: 'success',
    timestamp: '2026-08-21T09:50:00Z',
    server_name: 'everything',
    tool_name: 'security_scan',
    metadata: { findings_summary: { high: 2, low: 1 } },
  },
]

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() =>
        ok({ activities: ACTIVITIES, total: ACTIVITIES.length, limit: 200, offset: 0 })
      ),
      getActivitySummary: vi.fn(() =>
        ok({
          period: '24h',
          total_count: 42,
          call_count: 19,
          success_count: 15,
          error_count: 4,
          blocked_count: 0,
          rejected_count: 0,
          // The 23 rows that used to be in the total and in no tile.
          other_count: 23,
        })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivity() {
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: { template: '<div/>' } },
      { path: '/sessions', name: 'sessions', component: { template: '<div/>' } },
      { path: '/security', name: 'security', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push('/activity')
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  return wrapper
}

type Wrapper = Awaited<ReturnType<typeof mountActivity>>
const rows = (wrapper: Wrapper) => wrapper.findAll('[data-test="activity-row"]')

describe('Activity table — repeats fold (F5)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('collapses consecutive identical calls, splitting on the failure', async () => {
    const wrapper = await mountActivity()

    // 3 successes, the error, 3 successes, the scan -> four lines, not eight.
    expect(rows(wrapper)).toHaveLength(4)
    const counts = wrapper.findAll('[data-test="activity-run-count"]').map(b => b.text())
    expect(counts.some(t => t.includes('×3'))).toBe(true)

    // The failure is on its own line and is not inside any run.
    const errorRow = rows(wrapper).find(r => r.text().includes('Error'))!
    expect(errorRow.find('[data-test="activity-run-count"]').exists()).toBe(false)
  })

  it('expands a run back into its rows on demand', async () => {
    const wrapper = await mountActivity()

    await wrapper.find('[data-test="activity-run-count"]').trigger('click')
    await flushPromises()

    // The two folded members appear beneath the lead.
    expect(wrapper.findAll('[data-test="activity-run-member"]')).toHaveLength(2)
  })

  it('reports how many rows the folding removed', async () => {
    const wrapper = await mountActivity()
    // Eight records rendered as four lines.
    expect(wrapper.find('[data-test="activity-folded-count"]').text()).toBe('−4')
  })

  it('turning folding off restores every row, and the choice is remembered', async () => {
    const wrapper = await mountActivity()

    await wrapper.find('[data-test="activity-group-toggle"]').trigger('click')
    await flushPromises()

    expect(rows(wrapper)).toHaveLength(ACTIVITIES.length)
    expect(window.localStorage.getItem('mcpproxy.activity.groupRepeats')).toBe('false')

    const reopened = await mountActivity()
    expect(rows(reopened)).toHaveLength(ACTIVITIES.length)
  })
})

describe('Activity table — only failures are marked (F5)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('leaves successful rows visually blank while keeping the word for screen readers', async () => {
    const wrapper = await mountActivity()

    const successCell = rows(wrapper)[0].find('[data-test="activity-status"]')
    expect(successCell.text()).toBe('Success')
    // sr-only: present in the accessibility tree, absent from the scan.
    expect(successCell.classes()).toContain('sr-only')

    const errorCell = rows(wrapper)
      .find(r => r.text().includes('Error'))!
      .find('[data-test="activity-status"]')
    expect(errorCell.classes()).toContain('badge-error')
    expect(errorCell.classes()).not.toContain('sr-only')
  })
})

describe('Activity table — glyphs carry their words (F26)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('paints the operation type beside the intent glyph', async () => {
    const wrapper = await mountActivity()
    expect(wrapper.find('[data-test="activity-intent-label"]').text()).toBe('read')
  })

  it('offers a visible legend for both glyph columns in the filter panel', async () => {
    const wrapper = await mountActivity()
    expect(wrapper.find('[data-test="activity-legend"]').exists()).toBe(false)

    await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()

    const legend = wrapper.find('[data-test="activity-legend"]')
    expect(legend.exists()).toBe(true)
    expect(legend.text()).toContain('read')
    expect(legend.text()).toContain('destructive')
    expect(legend.text()).toContain('critical')
  })
})

describe('Activity status tiles — the row adds up (F2)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('renders the Other bucket so the tiles sum to the Events total', async () => {
    const wrapper = await mountActivity()
    await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="kpi-card-total"]').text()).toContain('42')
    expect(wrapper.find('[data-test="kpi-card-other"]').text()).toContain('23')

    const sum = ['success', 'error', 'blocked', 'rejected', 'other'].reduce((acc, status) => {
      const tile = wrapper.find(`[data-test="kpi-card-${status}"]`)
      return acc + Number(tile.text().replace(/\D+/g, '') || 0)
    }, 0)
    expect(sum).toBe(42)
  })

  it('the Other tile filters the list like every other tile', async () => {
    const wrapper = await mountActivity()
    await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()

    await wrapper.find('[data-test="kpi-card-other"]').trigger('click')
    await flushPromises()

    // None of the loaded rows carries a non-vocabulary status, so the list is
    // empty rather than unchanged — the filter really applied.
    expect(rows(wrapper)).toHaveLength(0)
    expect(wrapper.find('[data-test="activity-filter-chip-status"]').text())
      .toContain('other / internal')
  })
})

describe('Activity drawer — internal events lead somewhere (F25)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('shows a scan verdict with its findings and a way out', async () => {
    const wrapper = await mountActivity()

    const scanRow = rows(wrapper).find(r => r.text().includes('security_scan'))!
    await scanRow.trigger('click')
    await flushPromises()

    const verdict = wrapper.find('[data-test="activity-scan-verdict"]')
    expect(verdict.exists()).toBe(true)
    expect(verdict.text()).toContain('3 findings')
    expect(verdict.text()).toContain('high ×2')
    expect(wrapper.find('[data-test="activity-scan-open-security"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="activity-scan-open-server"]').exists()).toBe(true)
  })
})
