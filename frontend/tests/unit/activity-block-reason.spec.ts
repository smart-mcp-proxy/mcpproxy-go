import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109-k T112 / FR-072: "show the block reason on blocked rows (from
// record metadata today, from Spec 108's `block_reason` once present) ...".
// The detail drawer's Policy Decision panel reads `metadata.reason` first,
// falls back to `metadata.policy_rule`, and finally to a generic sentence —
// never a blank "Reason:" line with nothing after it.

const BLOCKED_WITH_REASON = {
  id: 'act-blocked-reason',
  type: 'tool_call',
  status: 'blocked',
  timestamp: '2026-09-20T10:00:00Z',
  server_name: 'filesystem',
  tool_name: 'write',
  request_id: 'req-blocked-reason',
  metadata: { decision: 'deny', reason: 'destructive tools require approval in this profile' },
}

const BLOCKED_WITH_POLICY_RULE_ONLY = {
  id: 'act-blocked-rule',
  type: 'tool_call',
  status: 'blocked',
  timestamp: '2026-09-20T10:01:00Z',
  server_name: 'filesystem',
  tool_name: 'delete',
  request_id: 'req-blocked-rule',
  metadata: { decision: 'deny', policy_rule: 'deny-destructive' },
}

const BLOCKED_WITH_NO_METADATA = {
  id: 'act-blocked-bare',
  type: 'tool_call',
  status: 'blocked',
  timestamp: '2026-09-20T10:02:00Z',
  server_name: 'filesystem',
  tool_name: 'move',
  request_id: 'req-blocked-bare',
}

const ALL = [BLOCKED_WITH_REASON, BLOCKED_WITH_POLICY_RULE_ONLY, BLOCKED_WITH_NO_METADATA]

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() => ok({ activities: ALL, total: ALL.length, limit: 200, offset: 0 })),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 3, success_count: 0, error_count: 0, blocked_count: 3, rejected_count: 0 })
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
      { path: '/activity', component: Activity },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push('/activity?view=all')
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('Activity detail drawer — block reason (Spec 109-k T112, FR-072)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('shows the declared reason from metadata.reason', async () => {
    const wrapper = await mountActivity()
    const row = wrapper.findAll('[data-test="activity-row"]').find(r => r.text().includes('write'))!
    await row.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('destructive tools require approval in this profile')
  })

  it('falls back to metadata.policy_rule when there is no reason', async () => {
    const wrapper = await mountActivity()
    const row = wrapper.findAll('[data-test="activity-row"]').find(r => r.text().includes('delete'))!
    await row.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('deny-destructive')
  })

  it('falls back to a generic sentence when metadata carries neither (never a blank line)', async () => {
    const wrapper = await mountActivity()
    const row = wrapper.findAll('[data-test="activity-row"]').find(r => r.text().includes('move'))!
    await row.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Tool call was blocked by security policy')
  })
})
