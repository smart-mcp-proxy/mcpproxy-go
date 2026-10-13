import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Audit F13: the drawer printed the credential the detector had just flagged,
// in cleartext, next to a Copy button. The masking itself happens on the SERVER
// (internal/httpapi/activity.go → maskActivityPayloads), so what this view owes
// the user is the explanation: that the payloads below are masked, and where a
// full value can still be obtained. The fixture below is what the API now
// returns for the audit's own repro.

const FLAGGED_CALL = {
  id: 'act-flagged',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-08-25T10:00:00Z',
  server_name: 'everything',
  tool_name: 'echo',
  request_id: 'req-flagged',
  duration_ms: 12,
  // Server-masked payloads — no `_auth_*` keys, no raw secret.
  arguments: { message: 'AKIA…**** aws key here' },
  response: 'Echo: AKIA…**** aws key here',
  has_sensitive_data: true,
  max_severity: 'critical',
  detection_types: ['aws_access_key'],
  metadata: {
    sensitive_data_detection: {
      detected: true,
      detection_count: 1,
      detections: [
        { type: 'aws_access_key', category: 'cloud_credentials', severity: 'critical', location: 'arguments' },
      ],
    },
  },
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn(() => ok({ activities: [FLAGGED_CALL], total: 1, limit: 200, offset: 0 })),
      getActivitySummary: vi.fn(() =>
        ok({ period: '24h', total_count: 1, success_count: 1, error_count: 0, blocked_count: 0, rejected_count: 0 })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function openDrawer() {
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

describe('Activity drawer — sensitive payloads (audit F13)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    window.localStorage.clear()
  })

  it('says the payloads are masked and where a full value still comes from', async () => {
    const wrapper = await openDrawer()

    const note = wrapper.get('[data-test="sensitive-mask-note"]')
    expect(note.text()).toContain('masked')
    expect(note.text()).toContain('mcpproxy activity export --include-bodies')
  })

  it('badges both payload panels so the mask does not read as a rendering glitch', async () => {
    const wrapper = await openDrawer()

    expect(wrapper.get('[data-test="arguments-masked-badge"]').text()).toBe('Masked')
    expect(wrapper.get('[data-test="response-masked-badge"]').text()).toBe('Masked')
  })

  it('renders the masked preview the server sent, never a raw key', async () => {
    const wrapper = await openDrawer()

    const body = wrapper.text()
    expect(body).toContain('AKIA…****')
    expect(body).not.toContain('AKIAIOSFODNN7EXAMPLE')
  })
})
