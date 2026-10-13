import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Activity Log density (user feedback): five stat cards plus a permanently open
// nine-control filter grid ate half the viewport before the first row. The page
// now opens on ONE compact strip — total + non-zero attention counts, a Filters
// toggle carrying a count badge, and the active-filter chips. The cards and the
// controls are still there, one click away, and the choice is remembered.
//
// The same pass de-coloured success (plain muted text, no pill) and removed the
// duplicated "code_execution" that appeared in Type AND Details on one row.

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

const OK_CALL = {
  id: 'act-ok',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-08-21T09:59:00Z',
  server_name: 'github',
  tool_name: 'list_repos',
  request_id: 'req-ok',
  duration_ms: 40,
  metadata: { intent: { operation_type: 'read', reason: 'enumerate repositories for the release notes' } },
}

const FAILED_CALL = {
  id: 'act-bad',
  type: 'tool_call',
  status: 'error',
  timestamp: '2026-08-21T09:58:00Z',
  server_name: 'slack',
  tool_name: 'post_message',
  request_id: 'req-bad',
  duration_ms: 80,
  metadata: { intent: { operation_type: 'write' } },
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() => {
        const activities = [PARENT, OK_CALL, FAILED_CALL]
        return ok({ activities, total: activities.length, limit: 200, offset: 0 })
      }),
      getActivitySummary: vi.fn(() =>
        ok({
          period: '24h',
          total_count: 54,
          call_count: 42,
          success_count: 47,
          error_count: 6,
          blocked_count: 1,
          rejected_count: 0,
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
      { path: '/activity', component: { template: '<div/>' } },
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
const rowFor = (wrapper: Wrapper, text: string) => rows(wrapper).find(r => r.text().includes(text))!

describe('Activity Log — compact header', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('opens collapsed: one counts strip, no stat cards, no filter grid', async () => {
    const wrapper = await mountActivity()

    const strip = wrapper.find('[data-test="activity-compact-summary"]')
    expect(strip.exists()).toBe(true)
    // Rows in the window are events; only 42 of them are calls (F1, #1046).
    expect(strip.text()).toContain('54 events')
    expect(strip.text()).toContain('42 calls')
    expect(strip.text()).toContain('6 errors')
    expect(strip.text()).toContain('1 blocked')
    // Zero rejected never earns a segment.
    expect(strip.text()).not.toContain('rejected')

    expect(wrapper.find('[data-test="kpi-card-total"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="activity-filter-panel"]').exists()).toBe(false)
  })

  it('colours only the attention counts', async () => {
    const wrapper = await mountActivity()

    expect(wrapper.find('[data-test="activity-compact-total"]').classes().join(' ')).not.toMatch(/text-(error|warning|success)/)
    expect(wrapper.find('[data-test="activity-compact-error"]').classes()).toContain('text-error')
    expect(wrapper.find('[data-test="activity-compact-blocked"]').classes()).toContain('text-warning')
  })

  it('a count segment drives the Status filter, and clicking it again clears it', async () => {
    const wrapper = await mountActivity()

    await wrapper.find('[data-test="activity-compact-error"]').trigger('click')
    await flushPromises()
    expect(rows(wrapper)).toHaveLength(1)
    expect(wrapper.find('[data-test="activity-filter-chip-status"]').text()).toContain('Status: error')

    await wrapper.find('[data-test="activity-compact-error"]').trigger('click')
    await flushPromises()
    expect(rows(wrapper)).toHaveLength(3)
  })

  it('reveals the stat cards and the filter grid on toggle, and persists the choice', async () => {
    const wrapper = await mountActivity()

    await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="kpi-card-total"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="activity-filter-panel"]').exists()).toBe(true)
    expect(window.localStorage.getItem('mcpproxy.activity.filtersExpanded')).toBe('true')

    // A fresh mount honours the stored preference.
    const reopened = await mountActivity()
    expect(reopened.find('[data-test="activity-filter-panel"]').exists()).toBe(true)
  })

  it('survives a localStorage that throws (private window / blocked site data)', async () => {
    // Scoped to OUR key: the shared system store touches localStorage too and is
    // not what this test is about.
    const KEY = 'mcpproxy.activity.filtersExpanded'
    const realGet = Storage.prototype.getItem
    const realSet = Storage.prototype.setItem
    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (this: Storage, key: string) {
      if (key === KEY) throw new Error('SecurityError')
      return realGet.call(this, key)
    })
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === KEY) throw new Error('SecurityError')
      return realSet.call(this, key, value)
    })

    try {
      const wrapper = await mountActivity()
      // Defaults to collapsed rather than blowing up the page.
      expect(wrapper.find('[data-test="activity-filter-panel"]').exists()).toBe(false)
      await wrapper.find('[data-test="activity-filters-toggle"]').trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-test="activity-filter-panel"]').exists()).toBe(true)
    } finally {
      getItem.mockRestore()
      setItem.mockRestore()
    }
  })

  it('shows active filters as dismissable chips with a count badge, even collapsed', async () => {
    const wrapper = await mountActivity()

    expect(wrapper.find('[data-test="activity-active-filters"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="activity-filters-count"]').exists()).toBe(false)

    await wrapper.find('[data-test="activity-compact-blocked"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="activity-filter-panel"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="activity-active-filters"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="activity-filters-count"]').text()).toBe('1')

    await wrapper.find('[data-test="activity-filter-chip-status"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="activity-active-filters"]').exists()).toBe(false)
  })
})

describe('Activity Log — row treatment', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('renders success as plain muted text and error as a red pill', async () => {
    const wrapper = await mountActivity()

    const okStatus = rowFor(wrapper, 'list_repos').find('[data-test="activity-status"]')
    expect(okStatus.text()).toBe('Success')
    expect(okStatus.classes().join(' ')).not.toContain('badge')
    expect(okStatus.classes().join(' ')).not.toContain('success')

    const badStatus = rowFor(wrapper, 'post_message').find('[data-test="activity-status"]')
    expect(badStatus.text()).toBe('Error')
    expect(badStatus.classes()).toContain('badge-error')
  })

  it('says code_execution once on the parent row, not twice', async () => {
    const wrapper = await mountActivity()

    const parentRow = rowFor(wrapper, 'code_execution')
    const occurrences = parentRow.text().split('code_execution').length - 1
    expect(occurrences).toBe(1)

    // The marker survives as a quiet glyph beside the Details text.
    const marker = parentRow.find('[data-test="activity-parent-badge"]')
    expect(marker.exists()).toBe(true)
    expect(marker.text()).toBe('🧩')
    expect(marker.classes().join(' ')).not.toContain('badge-accent')
  })

  it('shows the intent reason inline, glyph first, no coloured pill', async () => {
    const wrapper = await mountActivity()

    const intent = rowFor(wrapper, 'list_repos').find('[data-test="activity-intent"]')
    expect(intent.exists()).toBe(true)
    expect(intent.attributes('title')).toBe('read — enumerate repositories for the release notes')
    expect(intent.text()).toContain('enumerate repositories for the release notes')
    expect(intent.classes().join(' ')).not.toContain('badge')
    expect(intent.find('[data-test="activity-intent-reason"]').classes()).toContain('truncate')

    // Declared operation with no reason: glyph only, no dangling text node.
    const noReason = rowFor(wrapper, 'post_message').find('[data-test="activity-intent"]')
    expect(noReason.attributes('title')).toBe('write')
    expect(noReason.find('[data-test="activity-intent-reason"]').exists()).toBe(false)

    // No intent at all: a muted dash, not a "❓" chip.
    expect(rowFor(wrapper, 'code_execution').find('[data-test="activity-intent"]').exists()).toBe(false)
  })
})
