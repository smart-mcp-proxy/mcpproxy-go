import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Issue #1173. There are TWO response truncations and they point in OPPOSITE
// directions, so one "Truncated" badge cannot serve both:
//
//   response_truncated          the agent received LESS than this record holds
//                               (the log kept the full pre-forward text)
//   response_storage_truncated  the agent received MORE than this record holds
//                               (activity_max_response_size cut it on the way
//                               into BBolt)
//
// Showing the same badge for both — or showing none for the second — leaves the
// reader looking at a body ending in `...[truncated]` with the wrong story, or
// no story. The drawer badges them separately; these are the assertions that
// keep it that way.

const BASE = {
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-09-02T10:00:00Z',
  server_name: 'github',
  tool_name: 'list_issues',
  duration_ms: 12,
  arguments: { repo: 'smart-mcp-proxy/mcpproxy-go' },
  response: 'only the first 64KB of a much longer payload...[truncated]',
}

const STORAGE_TRUNCATED = {
  ...BASE,
  id: 'act-storage-cut',
  request_id: 'req-storage-cut',
  response_storage_truncated: true,
  response_bytes: 200039,
}

const FORWARD_TRUNCATED = {
  ...BASE,
  id: 'act-forward-cut',
  request_id: 'req-forward-cut',
  response_truncated: true,
}

const WHOLE = {
  ...BASE,
  id: 'act-whole',
  request_id: 'req-whole',
  response: 'a short, complete response',
}

const activities = vi.fn()

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() => ok({ activities: activities(), total: activities().length, limit: 200, offset: 0 })),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 1, success_count: 1, error_count: 0, blocked_count: 0, rejected_count: 0 })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function openDrawer(record: Record<string, unknown>) {
  activities.mockReturnValue([record])

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

  const row = wrapper.findAll('[data-test="activity-row"]')[0]
  await row.trigger('click')
  await flushPromises()
  return wrapper
}

describe('Activity drawer — storage truncation badge (#1173)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('badges a body the activity log itself shortened', async () => {
    const wrapper = await openDrawer(STORAGE_TRUNCATED)

    const badge = wrapper.get('[data-test="response-storage-truncated-badge"]')
    expect(badge.text()).toBe('Shortened for storage')
    // The tooltip has to say WHICH direction, and name the setting an operator
    // can actually change — "Truncated" alone is what made the two cuts
    // indistinguishable in the first place.
    expect(badge.attributes('title')).toContain('activity_max_response_size')
    expect(badge.attributes('title')).toContain('more than this')
  })

  it('does not also raise the forward-truncation badge, which means the opposite', async () => {
    const wrapper = await openDrawer(STORAGE_TRUNCATED)

    expect(wrapper.find('[data-test="response-truncated-badge"]').exists()).toBe(false)
  })

  it('keeps the forward-truncation badge for the other direction', async () => {
    const wrapper = await openDrawer(FORWARD_TRUNCATED)

    const badge = wrapper.get('[data-test="response-truncated-badge"]')
    expect(badge.text()).toBe('Truncated')
    expect(badge.attributes('title')).toContain('less than this')
    expect(wrapper.find('[data-test="response-storage-truncated-badge"]').exists()).toBe(false)
  })

  it('badges an untouched response with neither', async () => {
    const wrapper = await openDrawer(WHOLE)

    expect(wrapper.find('[data-test="response-storage-truncated-badge"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="response-truncated-badge"]').exists()).toBe(false)
  })
})
