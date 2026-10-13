import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { WORK_RO, makeClient } from './fixtures/profiles108i'

// Spec 108-l (SC-006, L8, T122): the Web UI half of the cross-surface record-set
// test. internal/httpapi/testdata/sc006_activity_recordsets.json holds, for one
// seeded dataset, the query and the ids of every (profile, client, token,
// status) combination (generated, and checked against an independent reference
// filter, by TestSC006ActivityRecordSets). For each combination the Activity
// page is opened at the combination's URL: its FIRST list request must carry
// exactly the combination's parameters, and the page must render exactly the
// ids the server returned for them.

interface RecordSet { url_query: string; rest_query: string; ids: string[] }
const golden: { recordsets: RecordSet[] } = JSON.parse(
  readFileSync(resolve(__dirname, '../../../internal/httpapi/testdata/sc006_activity_recordsets.json'), 'utf8'),
)

const getActivitiesMock = vi.hoisted(() => vi.fn())
const getSummaryMock = vi.hoisted(() => vi.fn())
const getSessionsMock = vi.hoisted(() => vi.fn())
const getStatusMock = vi.hoisted(() => vi.fn())
const listTokensMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    getActivities: getActivitiesMock,
    getActivitySummary: getSummaryMock,
    getSessions: getSessionsMock,
    getActivityExportUrl: vi.fn(() => 'http://localhost/api/v1/activity/export'),
    getStatus: getStatusMock,
    listAgentTokens: listTokensMock,
    getProfiles: vi.fn(() => Promise.resolve({ profiles: [] })),
    getClients: vi.fn(() => Promise.resolve({ success: true, data: { clients: [] } })),
  },
}))

const ok = (data: unknown) => Promise.resolve({ success: true, data })
const stub = { template: '<div />' }
const SCOPE_KEYS = ['profile', 'client', 'token', 'status'] as const

// The server's answer for a set: one record per id, each with its own tool name so
// the page can never fold two of them into one row, and the status the filter asked for.
function recordsFor(ids: string[], status: string) {
  return ids.map((id, index) => ({
    id,
    type: 'tool_call',
    source: 'mcp',
    server_name: 'srv',
    tool_name: `tool_${id}`,
    status,
    timestamp: new Date(Date.UTC(2026, 9, 1, 9, 0, 0) - index * 60_000).toISOString(),
  }))
}

async function mountActivityAt(path: string) {
  const Activity = (await import('@/views/Activity.vue')).default
  const { useProfilesStore } = await import('@/stores/profiles')
  const { useClientsStore } = await import('@/stores/clients')
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: Activity },
      { path: '/clients', name: 'clients', component: stub },
      { path: '/tokens', name: 'tokens', component: stub },
      { path: '/tools', name: 'tools', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
      { path: '/servers/:serverName', component: stub },
    ],
  })
  await router.push(path)
  await router.isReady()
  useProfilesStore().profiles = [WORK_RO] as any
  useProfilesStore().loaded = true
  useClientsStore().clients = [makeClient('cursor')] as any
  const wrapper = mount(Activity, { global: { plugins: [router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('SC-006: the Activity page sends and renders the golden record sets (Web UI)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['profile', 'client', 'token'])
    getSummaryMock.mockImplementation(() => ok({ period: '24h', total_count: 0, success_count: 0, error_count: 0, blocked_count: 0, rejected_count: 0 }))
    getSessionsMock.mockImplementation(() => ok({ sessions: [] }))
    getStatusMock.mockResolvedValue({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
    listTokensMock.mockResolvedValue({ success: true, data: { tokens: [{ name: 'ro-bot', kind: 'agent' }] } })
  })
  afterEach(() => {
    setAvailableFeatures([])
  })

  it('reads the full 4 x 4 x 2 x 2 matrix', () => {
    expect(golden.recordsets).toHaveLength(64)
    expect(new Set(golden.recordsets.map(set => set.ids.join(','))).size).toBeGreaterThanOrEqual(12)
  })

  for (const set of golden.recordsets) {
    it(`/activity?${set.url_query || '(no filter)'}: first request = ${set.rest_query || '(no scope parameter)'}, renders ${set.ids.length} record(s)`, async () => {
      const byQuery = new Map(golden.recordsets.map(s => [s.rest_query, s.ids]))
      getActivitiesMock.mockImplementation((params: Record<string, string | undefined>) => {
        const query = new URLSearchParams()
        for (const key of [...SCOPE_KEYS].sort()) if (params[key]) query.set(key, params[key] as string)
        const ids = byQuery.get(query.toString()) ?? []
        // every record the server returned for a status filter has that status
        return ok({ activities: recordsFor(ids, params.status || 'success'), total: ids.length, limit: 200, offset: 0 })
      })

      const wrapper = await mountActivityAt(set.url_query ? `/activity?${set.url_query}` : '/activity')

      // The very first list request already carries exactly the combination.
      expect(getActivitiesMock).toHaveBeenCalled()
      const first = getActivitiesMock.mock.calls[0][0] as Record<string, string | undefined>
      const sent = new URLSearchParams()
      for (const key of [...SCOPE_KEYS].sort()) if (first[key]) sent.set(key, first[key] as string)
      expect(sent.toString()).toBe(set.rest_query)

      // The page renders exactly the ids the server returned, in order.
      const rows = wrapper.findAll('[data-test="activity-row"]')
      expect(rows).toHaveLength(set.ids.length)
      set.ids.forEach((id, index) => expect(rows[index].text()).toContain(`tool_${id}`))
    })
  }

  it('mutation: a page that dropped the client filter would send a different query', async () => {
    getActivitiesMock.mockImplementation(() => ok({ activities: [], total: 0, limit: 200, offset: 0 }))
    await mountActivityAt('/activity?client=cursor&status=blocked')
    const first = getActivitiesMock.mock.calls[0][0] as Record<string, string | undefined>
    expect(first.client).toBe('cursor')
    expect(first.status).toBe('blocked')
    expect(first.profile).toBeUndefined()
  })
})
