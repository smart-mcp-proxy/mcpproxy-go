import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Issues #1173 / #1174. There are TWO response truncations and they interact:
//
//   response_storage_truncated  activity_max_response_size cut the text on the
//                               way into BBolt. Always means the stored body is
//                               a prefix of what the emitter handed over.
//   response_truncated          the response was cut to tool_response_limit on
//                               the way to the agent.
//
// Which side of the forward cut a record holds depends on its TYPE *and* on
// whether the storage flag is also set — a tool_call with only the forward flag
// holds exactly the agent's copy, but the same record with the storage flag too
// holds strictly LESS than it, because activity_service.go cuts the
// already-forwarded text again.
//
// This drawer used to encode that table itself, split across two per-badge
// tooltips. A per-badge tooltip can only see one flag, so a reader hovering
// "Truncated" on a both-flags record was told the stored body is the agent's own
// copy — definitionally false, with no correction anywhere in that tooltip.
//
// The backend now resolves the cell once (contracts.ResolveResponseTruncation,
// pinned across all 12 cells in internal/contracts/activity_truncation_test.go)
// and ships the answer as `response_truncation_notice`. These fixtures carry the
// sentences that resolver actually produces, and the drawer must put the SAME
// one on BOTH badges.

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
  response_truncation_notice:
    'The agent received MORE than this: the recorded text was shortened to fit ' +
    'activity_max_response_size. response_bytes (200039) is what the agent received.',
}

// An upstream call with ONLY the forward cut. The record holds the forwarded
// copy — exactly what the agent got — so nothing here may say the agent
// received less than this.
const TOOL_CALL_FORWARD_TRUNCATED = {
  ...BASE,
  type: 'tool_call',
  id: 'act-forward-cut',
  request_id: 'req-forward-cut',
  response_truncated: true,
  response_bytes: 200039,
  response_truncation_notice:
    "This is the agent's own copy: the upstream response was cut to tool_response_limit before " +
    'being both forwarded and recorded. response_bytes (200039) is the pre-forward upstream size, ' +
    "so it describes neither this record nor the agent's copy.",
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
  response_truncation_notice:
    'The agent received LESS than this: the built-in recorded its full response, and the agent ' +
    'got it cut to tool_response_limit. response_bytes (200039) is the size of this recorded text.',
}

// The cell four review rounds got wrong. Reachable at stock defaults: the
// forward cut applies per text BLOCK, so the joined recorded text routinely
// exceeds tool_response_limit and then trips activity_max_response_size too.
const BOTH_CUTS = {
  ...BASE,
  type: 'tool_call',
  id: 'act-both-cuts',
  request_id: 'req-both-cuts',
  response_truncated: true,
  response_storage_truncated: true,
  response_bytes: 200039,
  response_truncation_notice:
    'The agent received MORE than this: the upstream response was cut to tool_response_limit ' +
    'before being forwarded and recorded, and the recorded copy was then shortened AGAIN to fit ' +
    'activity_max_response_size. response_bytes (200039) is the pre-forward upstream size, so it ' +
    "describes neither this record nor the agent's copy.",
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
    expect(badge.attributes('title')).toContain('MORE than this')
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
    // this. "LESS than this" is the exact inverse of what happened.
    expect(title).not.toContain('LESS than this')
    expect(title).toContain("agent's own copy")
    expect(title).toContain('tool_response_limit')
    expect(wrapper.find('[data-test="response-storage-truncated-badge"]').exists()).toBe(false)
  })

  it('keeps the "agent received less" wording for the built-in, where it is true', async () => {
    const wrapper = await openDrawer(INTERNAL_FORWARD_TRUNCATED)

    const badge = wrapper.get('[data-test="response-truncated-badge"]')
    expect(badge.text()).toBe('Truncated')
    const title = badge.attributes('title') ?? ''
    expect(title).toContain('LESS than this')
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

  // The blocking finding. Hovering EITHER badge on a both-flags record must give
  // the reader the whole truth, because a reader hovers one badge, not both.
  it('puts one true sentence on BOTH badges when both cuts landed', async () => {
    const wrapper = await openDrawer(BOTH_CUTS)

    const forwardTitle = wrapper.get('[data-test="response-truncated-badge"]').attributes('title') ?? ''
    const storageTitle =
      wrapper.get('[data-test="response-storage-truncated-badge"]').attributes('title') ?? ''

    expect(forwardTitle).toBe(storageTitle)

    for (const title of [forwardTitle, storageTitle]) {
      // The stored body is STRICTLY shorter than the delivered one here: the
      // storage cut shortened the already-forwarded text again.
      expect(title).toContain('MORE than this')
      expect(title).not.toContain("agent's own copy")
      // Both settings an operator can change are named, so neither cut is
      // invisible from whichever badge was hovered.
      expect(title).toContain('tool_response_limit')
      expect(title).toContain('activity_max_response_size')
      // response_bytes is the PRE-forward upstream size, larger than both
      // bodies, so it must not be quoted as what was delivered.
      expect(title).toContain('describes neither')
    }
  })
})
