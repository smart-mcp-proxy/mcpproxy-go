import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Issue #1173. There are TWO response truncations, they are independent, and
// only ONE of them has a direction that is fixed by the flag alone:
//
//   response_storage_truncated  activity_max_response_size cut the text on the
//                               way into BBolt. Always means the stored body is
//                               a prefix, on every record type.
//   response_truncated          the response was cut to tool_response_limit on
//                               the way to the agent. WHICH SIDE this record
//                               holds depends on `type`:
//                                 internal_tool_call → the log kept the FULL
//                                   text, so the agent received LESS
//                                 tool_call → handleToolCallCompleted stores
//                                   the POST-forward text, so the log kept the
//                                   agent's OWN copy, not more than it
//
// The earlier version of this spec asserted 'less than this' against a
// tool_call fixture, which PINNED the backwards wording for the dominant
// population carrying the flag. These tests vary the TYPE, not just the flags.

const BASE = {
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
  type: 'tool_call',
  id: 'act-storage-cut',
  request_id: 'req-storage-cut',
  response_storage_truncated: true,
  response_bytes: 200039,
}

// An upstream call. The record holds the forwarded copy — exactly what the
// agent got — so nothing here may say the agent received less than this.
const TOOL_CALL_FORWARD_TRUNCATED = {
  ...BASE,
  type: 'tool_call',
  id: 'act-forward-cut',
  request_id: 'req-forward-cut',
  response_truncated: true,
  response_bytes: 200039,
}

// A built-in. This is the one case where the log really does hold more than
// the agent consumed, and it is the only case usage_aggregate.go acts on.
const INTERNAL_FORWARD_TRUNCATED = {
  ...BASE,
  type: 'internal_tool_call',
  server_name: '',
  tool_name: 'retrieve_tools',
  id: 'act-internal-cut',
  request_id: 'req-internal-cut',
  response_truncated: true,
  response_bytes: 200039,
}

const BOTH_CUTS = {
  ...BASE,
  type: 'tool_call',
  id: 'act-both-cuts',
  request_id: 'req-both-cuts',
  response_truncated: true,
  response_storage_truncated: true,
  response_bytes: 200039,
}

const WHOLE = {
  ...BASE,
  type: 'tool_call',
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

  it('does not also raise the forward-truncation badge, which means something else', async () => {
    const wrapper = await openDrawer(STORAGE_TRUNCATED)

    expect(wrapper.find('[data-test="response-truncated-badge"]').exists()).toBe(false)
  })

  it('badges an untouched response with neither', async () => {
    const wrapper = await openDrawer(WHOLE)

    expect(wrapper.find('[data-test="response-storage-truncated-badge"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="response-truncated-badge"]').exists()).toBe(false)
  })
})

describe('Activity drawer — forward truncation reads by record type (#1174)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it("does not tell the operator a tool_call's agent received less than the log holds", async () => {
    const wrapper = await openDrawer(TOOL_CALL_FORWARD_TRUNCATED)

    const badge = wrapper.get('[data-test="response-truncated-badge"]')
    expect(badge.text()).toBe('Truncated')
    const title = badge.attributes('title') ?? ''
    // The record holds the POST-forward text, so the agent received EXACTLY
    // this. "less than this" is the exact inverse of what happened.
    expect(title).not.toContain('less than this')
    expect(title).toContain("agent's own copy")
    expect(title).toContain('tool_response_limit')
    expect(wrapper.find('[data-test="response-storage-truncated-badge"]').exists()).toBe(false)
  })

  it('keeps the "agent received less" wording for the built-in, where it is true', async () => {
    const wrapper = await openDrawer(INTERNAL_FORWARD_TRUNCATED)

    const badge = wrapper.get('[data-test="response-truncated-badge"]')
    expect(badge.text()).toBe('Truncated')
    const title = badge.attributes('title') ?? ''
    expect(title).toContain('less than this')
    expect(title).toContain('tool_response_limit')
    expect(wrapper.find('[data-test="response-storage-truncated-badge"]').exists()).toBe(false)
  })

  it('gives the two record types different tooltips', async () => {
    const toolCall = await openDrawer(TOOL_CALL_FORWARD_TRUNCATED)
    const toolTitle = toolCall.get('[data-test="response-truncated-badge"]').attributes('title')

    const internal = await openDrawer(INTERNAL_FORWARD_TRUNCATED)
    const internalTitle = internal.get('[data-test="response-truncated-badge"]').attributes('title')

    // One sentence serving both types IS the defect.
    expect(toolTitle).not.toBe(internalTitle)
  })

  it('stops claiming the delivered size once both cuts are in play', async () => {
    const wrapper = await openDrawer(BOTH_CUTS)

    const storage = wrapper.get('[data-test="response-storage-truncated-badge"]')
    const title = storage.attributes('title') ?? ''
    // With a forward cut too, response_bytes describes the pre-forward upstream
    // payload — neither the stored body nor the delivered one — so the "agent
    // received more than this" claim no longer follows.
    expect(title).not.toContain('more than this')
    expect(title).toContain('activity_max_response_size')
    expect(wrapper.find('[data-test="response-truncated-badge"]').exists()).toBe(true)
  })
})
