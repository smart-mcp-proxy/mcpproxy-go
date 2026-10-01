import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { WORK_RO, makeClient } from './fixtures/profiles108i'

// Spec 108-j T107 (FR-031, FR-045; url-filter-contract.md rules 1, 5, 7): the
// Activity calls/system/all views send profile/client/token to the list, the
// summary and the export, show them as removable chips, and the first request
// already carries them.

const getActivitiesMock = vi.hoisted(() => vi.fn())
const getSummaryMock = vi.hoisted(() => vi.fn())
const getSessionsMock = vi.hoisted(() => vi.fn())
const getExportUrlMock = vi.hoisted(() => vi.fn())
const getStatusMock = vi.hoisted(() => vi.fn())
const listTokensMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    getActivities: getActivitiesMock,
    getActivitySummary: getSummaryMock,
    getSessions: getSessionsMock,
    getActivityExportUrl: getExportUrlMock,
    getStatus: getStatusMock,
    listAgentTokens: listTokensMock,
    getProfiles: vi.fn(() => Promise.resolve({ profiles: [] })),
    getClients: vi.fn(() => Promise.resolve({ success: true, data: { clients: [] } })),
  },
}))

const ok = (data: unknown) => Promise.resolve({ success: true, data })
const emptyList = () => ok({ activities: [], total: 0, limit: 200, offset: 0 })
const stub = { template: '<div />' }

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
  return { wrapper, router }
}

describe('Activity scope params (Spec 108-j)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['profile', 'client', 'token'])
    getActivitiesMock.mockImplementation(emptyList)
    getSummaryMock.mockImplementation(() => ok({ period: '24h', total_count: 0, success_count: 0, error_count: 0, blocked_count: 0, rejected_count: 0 }))
    getSessionsMock.mockImplementation(() => ok({ sessions: [] }))
    getExportUrlMock.mockReturnValue('http://localhost/api/v1/activity/export?format=json')
    getStatusMock.mockResolvedValue({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
    listTokensMock.mockResolvedValue({ success: true, data: { tokens: [{ name: 'ro-bot', kind: 'agent' }, { name: 'client-cursor', kind: 'client' }] } })
  })
  afterEach(() => {
    setAvailableFeatures([])
    vi.unstubAllGlobals()
  })

  it('?client=cursor&status=blocked: the list and the summary carry the client, and the first request already does', async () => {
    await mountActivityAt('/activity?client=cursor&status=blocked')
    expect(getActivitiesMock).toHaveBeenCalled()
    for (const call of getActivitiesMock.mock.calls) {
      expect(call[0]).toEqual(expect.objectContaining({ client: 'cursor', status: 'blocked' }))
    }
    expect(getSummaryMock).toHaveBeenCalledWith('24h', { client: 'cursor' })
  })

  it('the export URL carries the same scope', async () => {
    const open = vi.fn()
    vi.stubGlobal('open', open)
    const { wrapper } = await mountActivityAt('/activity?client=cursor')
    const exportJson = wrapper.findAll('a').find(a => a.text() === 'Export as JSON')!
    await exportJson.trigger('click')
    expect(getExportUrlMock).toHaveBeenCalledWith(expect.objectContaining({ format: 'json', client: 'cursor' }))
    expect(open).toHaveBeenCalled()
  })

  it('?profile=- sends profile "-" and shows the chip "Profile: unattributed"', async () => {
    const { wrapper } = await mountActivityAt('/activity?profile=-')
    expect(getActivitiesMock.mock.calls.at(-1)![0]).toEqual(expect.objectContaining({ profile: '-' }))
    expect(wrapper.get('[data-test="scope-chip-profile"]').text()).toContain('Profile: unattributed')
  })

  it('a request with no scope in the URL sends no scope parameter at all', async () => {
    await mountActivityAt('/activity')
    const params = getActivitiesMock.mock.calls.at(-1)![0]
    expect(params.client).toBeUndefined()
    expect(params.profile).toBeUndefined()
    expect(params.token).toBeUndefined()
    expect(getSummaryMock).toHaveBeenCalledWith('24h', {})
  })

  it('no unfiltered flash: with /status pending the first fetch waits, then carries the param', async () => {
    setAvailableFeatures([])
    let resolveStatus!: (v: unknown) => void
    getStatusMock.mockReturnValue(new Promise(r => { resolveStatus = r }))
    const { useSystemStore } = await import('@/stores/system')
    void useSystemStore().fetchScopeFilterFeatures()
    await mountActivityAt('/activity?client=cursor')
    expect(getActivitiesMock).not.toHaveBeenCalled()
    resolveStatus({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
    await flushPromises()
    await flushPromises()
    expect(getActivitiesMock).toHaveBeenCalledTimes(1)
    expect(getActivitiesMock.mock.calls[0][0]).toEqual(expect.objectContaining({ client: 'cursor' }))
  })

  it('rule 7: with the filters not advertised, no scope parameter is sent and no chip shows (the URL keeps it)', async () => {
    setAvailableFeatures([])
    getStatusMock.mockResolvedValue({ success: true, data: {} })
    const { useSystemStore } = await import('@/stores/system')
    await useSystemStore().fetchScopeFilterFeatures()
    const { wrapper, router } = await mountActivityAt('/activity?client=cursor')
    expect(getActivitiesMock.mock.calls.at(-1)![0].client).toBeUndefined()
    expect(wrapper.find('[data-test="scope-chips"]').exists()).toBe(false)
    expect(router.currentRoute.value.query.client).toBe('cursor')
  })

  it('removing the chip drops the parameter and refetches unscoped; a late scoped answer does not overwrite it', async () => {
    let releaseScoped!: () => void
    const scopedRow = { id: 'scoped-1', type: 'tool_call', server_name: 'github', tool_name: 'only_scoped', status: 'success', timestamp: '2026-09-30T10:00:00Z' }
    const otherRow = { id: 'plain-1', type: 'tool_call', server_name: 'github', tool_name: 'plain_call', status: 'success', timestamp: '2026-09-30T10:00:00Z' }
    getActivitiesMock.mockImplementation((params: { client?: string }) => params.client
      ? new Promise(resolveScoped => { releaseScoped = () => resolveScoped({ success: true, data: { activities: [scopedRow], total: 1, limit: 200, offset: 0 } }) })
      : ok({ activities: [otherRow], total: 1, limit: 200, offset: 0 }))
    const { wrapper, router } = await mountActivityAt('/activity?client=cursor')
    await wrapper.get('[data-test="scope-chip-remove-client"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.client).toBeUndefined()
    expect(getActivitiesMock.mock.calls.at(-1)![0].client).toBeUndefined()
    releaseScoped()
    await flushPromises()
    expect(wrapper.text()).toContain('plain_call')
    expect(wrapper.text()).not.toContain('only_scoped')
  })

  it('the Token select (in the filter panel) writes ?token= and the list refetches with it', async () => {
    const { wrapper, router } = await mountActivityAt('/activity')
    await wrapper.get('[data-test="activity-filters-toggle"]').trigger('click')
    await flushPromises()
    const select = wrapper.get('[data-test="scope-select-token"]')
    // Per-client credentials are filtered as clients, never listed as tokens.
    expect(select.findAll('option').map(o => o.text())).toEqual(['All tokens', 'Unattributed', 'ro-bot'])
    await select.setValue('ro-bot')
    await flushPromises()
    expect(router.currentRoute.value.query.token).toBe('ro-bot')
    expect(getActivitiesMock.mock.calls.at(-1)![0]).toEqual(expect.objectContaining({ token: 'ro-bot' }))
  })

  it('the Sessions view issues no /activity request but still narrows /sessions by the same scope', async () => {
    await mountActivityAt('/activity?view=sessions&client=cursor')
    expect(getActivitiesMock).not.toHaveBeenCalled()
    expect(getSessionsMock.mock.calls.some(call => call[2]?.client === 'cursor')).toBe(true)
  })

  it('the chips render on the Sessions view too', async () => {
    const { wrapper } = await mountActivityAt('/activity?view=sessions&client=cursor')
    expect(wrapper.get('[data-test="scope-chip-client"]').text()).toContain('Client: Cursor')
  })
})
