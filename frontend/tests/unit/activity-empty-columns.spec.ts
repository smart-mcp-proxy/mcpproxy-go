import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109-k T112 / FR-072: "Activity MUST hide columns that are empty for
// every visible row" (acceptance scenario 6: with no sensitive-data hits and
// no declared intent among the visible rows, the Sensitive and Intent
// columns disappear entirely rather than rendering as an empty column of
// dashes). Implemented as `hasSensitiveColumn`/`hasIntentColumn` guarding
// both the `<th>` and its matching `<td>` at desktop widths (`lg:table-cell`).

const PLAIN_CALL = {
  id: 'act-plain',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-09-20T10:00:00Z',
  server_name: 'filesystem',
  tool_name: 'read',
  request_id: 'req-plain',
  duration_ms: 10,
}

const SENSITIVE_CALL = {
  ...PLAIN_CALL,
  id: 'act-sensitive',
  request_id: 'req-sensitive',
  has_sensitive_data: true,
}

const INTENT_CALL = {
  ...PLAIN_CALL,
  id: 'act-intent',
  request_id: 'req-intent',
  metadata: { intent: { operation_type: 'write', reason: 'update the config file' } },
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      // Overwritten per test via the mock's own `mockResolvedValueOnce`-free
      // pattern: getActivities reads a module-level fixture array so each
      // test can point it at a different row set before mounting.
      getActivities: vi.fn(() => ok({ activities: [], total: 0, limit: 200, offset: 0 })),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 0, success_count: 0, error_count: 0, blocked_count: 0, rejected_count: 0 })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivityWith(activities: Record<string, unknown>[]) {
  const api = (await import('@/services/api')).default
  ;(api.getActivities as ReturnType<typeof vi.fn>).mockImplementation(() =>
    Promise.resolve({ success: true, data: { activities, total: activities.length, limit: 200, offset: 0 } })
  )
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [{ path: '/activity', component: Activity }],
  })
  await router.push('/activity?view=all')
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

function headerTexts(wrapper: Awaited<ReturnType<typeof mountActivityWith>>): string[] {
  return wrapper.findAll('thead th').map(th => th.text())
}

describe('Activity table — empty columns hide (Spec 109-k T112, FR-072)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('hides both Sensitive and Intent when no visible row has either', async () => {
    const wrapper = await mountActivityWith([PLAIN_CALL])
    const headers = headerTexts(wrapper)
    expect(headers).not.toContain('Sensitive')
    expect(headers).not.toContain('Intent')
  })

  it('shows Sensitive once any visible row has sensitive data, but still hides Intent', async () => {
    const wrapper = await mountActivityWith([PLAIN_CALL, SENSITIVE_CALL])
    const headers = headerTexts(wrapper)
    expect(headers).toContain('Sensitive')
    expect(headers).not.toContain('Intent')
  })

  it('shows Intent once any visible row declares one, but still hides Sensitive', async () => {
    const wrapper = await mountActivityWith([PLAIN_CALL, INTENT_CALL])
    const headers = headerTexts(wrapper)
    expect(headers).toContain('Intent')
    expect(headers).not.toContain('Sensitive')
  })

  it('shows both when both are present among visible rows', async () => {
    const wrapper = await mountActivityWith([SENSITIVE_CALL, INTENT_CALL])
    const headers = headerTexts(wrapper)
    expect(headers).toContain('Sensitive')
    expect(headers).toContain('Intent')
  })
})
