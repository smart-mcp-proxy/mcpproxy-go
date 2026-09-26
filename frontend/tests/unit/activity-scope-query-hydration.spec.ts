import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Regression test for the live-QA finding on 109-k-activity-scope-filters:
// "Core PR feature is not wired into the Web UI: navigating to
// /activity?view=system (or ?view=calls, ?server=, ?tool=, ?type=) does not
// filter the Activity Log at all — Activity.vue never reads these
// route-query values on mount, so both the client-side rendering and the
// REST request stay fully unfiltered (GET /api/v1/activity?limit=200 with no
// params) regardless of the URL." This suite mounts Activity.vue with an
// incoming filtered route (unlike scope-query.spec.ts, which only exercises
// the useScopeQuery composable in isolation) and asserts BOTH that the
// rendered table narrows AND that the outbound request carries the filter
// (url-filter-contract.md "Parameters": server/tool/type/status are sent to
// REST on Activity, not just applied client-side).

const TOOL_CALL_GITHUB = {
  id: 'act-1',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-09-20T10:00:00Z',
  server_name: 'github',
  tool_name: 'create_issue',
  request_id: 'req-1',
  duration_ms: 100,
}

const INTERNAL_CALL = {
  id: 'act-2',
  type: 'internal_tool_call',
  status: 'success',
  timestamp: '2026-09-20T10:01:00Z',
  tool_name: 'retrieve_tools',
  request_id: 'req-2',
  duration_ms: 50,
  metadata: { internal_tool_name: 'retrieve_tools' },
}

const QUARANTINE_CHANGE = {
  id: 'act-3',
  type: 'quarantine_change',
  status: 'success',
  timestamp: '2026-09-20T10:02:00Z',
  server_name: 'filesystem',
  request_id: 'req-3',
}

const SECURITY_SCAN = {
  id: 'act-4',
  type: 'security_scan',
  status: 'success',
  timestamp: '2026-09-20T10:03:00Z',
  server_name: 'filesystem',
  request_id: 'req-4',
}

const FS_READ = {
  id: 'act-5',
  type: 'tool_call',
  status: 'success',
  timestamp: '2026-09-20T10:04:00Z',
  server_name: 'filesystem',
  tool_name: 'read',
  request_id: 'req-5',
  duration_ms: 10,
}

const FS_WRITE = {
  id: 'act-6',
  type: 'tool_call',
  status: 'error',
  timestamp: '2026-09-20T10:05:00Z',
  server_name: 'filesystem',
  tool_name: 'write',
  request_id: 'req-6',
  duration_ms: 10,
}

const ALL_ACTIVITIES = [TOOL_CALL_GITHUB, INTERNAL_CALL, QUARANTINE_CHANGE, SECURITY_SCAN, FS_READ, FS_WRITE]

function filterFixture(params?: {
  type?: string
  server?: string
  tool?: string
  status?: string
}) {
  let rows = ALL_ACTIVITIES
  if (params?.type) {
    const types = params.type.split(',')
    rows = rows.filter(a => types.includes(a.type))
  }
  if (params?.server) {
    rows = rows.filter(a => a.server_name === params.server)
  }
  if (params?.tool) {
    rows = rows.filter(a => a.tool_name === params.tool)
  }
  if (params?.status) {
    rows = rows.filter(a => a.status === params.status)
  }
  return rows
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivities: vi.fn((params?: { type?: string; server?: string; tool?: string; status?: string }) => {
        const activities = filterFixture(params)
        return ok({ activities, total: activities.length, limit: 200, offset: 0 })
      }),
      getActivitySummary: vi.fn(() =>
        ok({
          period: '24h',
          total_count: ALL_ACTIVITIES.length,
          success_count: 5,
          error_count: 1,
          blocked_count: 0,
          rejected_count: 0,
        })
      ),
      getSessions: vi.fn(() => ok({ sessions: [] })),
      getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export?format=json'),
    },
  }
})

async function mountActivityAt(path: string) {
  const api = (await import('@/services/api')).default
  const Activity = (await import('@/views/Activity.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/activity', component: Activity },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Activity, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return { wrapper, api: api as unknown as { getActivities: ReturnType<typeof vi.fn> } }
}

const rows = (wrapper: Awaited<ReturnType<typeof mountActivityAt>>['wrapper']) =>
  wrapper.findAll('[data-test="activity-row"]')

describe('Activity Log — URL query hydration (Spec 109-k live-QA regression)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('with no query, fetches and shows everything (baseline)', async () => {
    const { wrapper, api } = await mountActivityAt('/activity')
    expect(rows(wrapper)).toHaveLength(ALL_ACTIVITIES.length)
    expect(api.getActivities).toHaveBeenCalledWith(
      expect.objectContaining({ type: undefined, server: undefined, tool: undefined, status: undefined })
    )
  })

  it('?view=calls narrows to tool_call/internal_tool_call, both client-side and on the request', async () => {
    const { wrapper, api } = await mountActivityAt('/activity?view=calls')
    // TOOL_CALL_GITHUB, INTERNAL_CALL, FS_READ, FS_WRITE are all tool_call/internal_tool_call;
    // QUARANTINE_CHANGE and SECURITY_SCAN are excluded.
    expect(rows(wrapper)).toHaveLength(4)
    const text = rows(wrapper).map(r => r.text()).join(' ')
    expect(text).toContain('create_issue')
    expect(text).not.toContain('Quarantine Change')
    expect(text).not.toContain('Security Scan')
    expect(api.getActivities).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'tool_call,internal_tool_call' })
    )
  })

  it('?view=system narrows to every non-call type, incl. types the CLI once missed', async () => {
    const { wrapper, api } = await mountActivityAt('/activity?view=system')
    const visible = rows(wrapper)
    // quarantine_change + security_scan only (the two system-type fixture rows).
    expect(visible).toHaveLength(2)
    expect(visible.map(r => r.text()).join(' ')).not.toContain('create_issue')
    const call = api.getActivities.mock.calls[0][0] as { type?: string }
    expect(call.type).toContain('quarantine_change')
    expect(call.type).toContain('security_scan')
    expect(call.type).not.toContain('tool_call,')
  })

  it('?server= narrows the log to that server alone', async () => {
    const { wrapper, api } = await mountActivityAt('/activity?server=filesystem')
    expect(rows(wrapper)).toHaveLength(4) // quarantine_change, security_scan, read, write
    expect(rows(wrapper).map(r => r.text()).join(' ')).not.toContain('create_issue')
    expect(api.getActivities).toHaveBeenCalledWith(expect.objectContaining({ server: 'filesystem' }))
  })

  it('?type= (explicit) overrides any view mapping', async () => {
    const { wrapper, api } = await mountActivityAt('/activity?view=calls&type=quarantine_change')
    expect(rows(wrapper)).toHaveLength(1)
    expect(rows(wrapper)[0].text()).toContain('Quarantine Change')
    expect(api.getActivities).toHaveBeenCalledWith(expect.objectContaining({ type: 'quarantine_change' }))
  })

  // This is the Tools row "Calls" link's exact target
  // (url-filter-contract.md link map): /activity?view=calls&tool=<server:tool>.
  it('?view=calls&tool=<server:tool> (the Tools row "Calls" link) shows only that tool\'s calls', async () => {
    const { wrapper, api } = await mountActivityAt('/activity?view=calls&tool=filesystem:read')
    const visible = rows(wrapper)
    expect(visible).toHaveLength(1)
    expect(visible[0].text()).toContain('read')
    expect(visible[0].text()).not.toContain('write')
    expect(api.getActivities).toHaveBeenCalledWith(
      expect.objectContaining({ server: 'filesystem', tool: 'read' })
    )
  })

  it('a bare (server-less) ?tool= applies only the tool part', async () => {
    const { wrapper, api } = await mountActivityAt('/activity?tool=read')
    expect(rows(wrapper)).toHaveLength(1)
    expect(api.getActivities).toHaveBeenCalledWith(
      expect.objectContaining({ tool: 'read', server: undefined })
    )
  })
})
