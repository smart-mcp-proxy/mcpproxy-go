import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { WORK_RO, makeClient } from './fixtures/profiles108i'

// Spec 108-j T106a (FR-031, J12; url-filter-contract.md rules 1, 3, 7): Usage
// sends profile/client/token, shows them as removable chips, replaces the
// tokens-saved tile (the backend zeroes it for a scoped read) and carries the
// scope on to Activity.

const getUsageMock = vi.hoisted(() => vi.fn())
const getStatusMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    getActivityUsage: getUsageMock,
    getStatus: getStatusMock,
    getProfiles: vi.fn(() => Promise.resolve({ profiles: [] })),
    getClients: vi.fn(() => Promise.resolve({ success: true, data: { clients: [] } })),
    listAgentTokens: vi.fn(() => Promise.resolve({ success: true, data: { tokens: [] } })),
  },
}))

const USAGE = {
  window: '24h',
  generated_at: '2026-09-30T12:00:00Z',
  freshness_ms: 100,
  token_source: 'bytes',
  tokens_saved: 0,
  tokens_saved_percentage: 0,
  tools: [
    { server: 'github', tool: 'list_issues', calls: 4, errors: 0, error_rate: 0, blocked: 0, total_resp_bytes: 400, avg_resp_bytes: 100, total_req_bytes: 40, avg_req_bytes: 10, sized_calls: 4, p50_ms: 5, p95_ms: 9, last_used: '2026-09-30T11:00:00Z' },
  ],
  timeline: [{ start: '2026-09-30T11:00:00Z', calls: 4, errors: 0, total_resp_bytes: 400 }],
}
const stub = { template: '<div />' }

async function mountUsageAt(path: string) {
  const Usage = (await import('@/views/Usage.vue')).default
  const { useProfilesStore } = await import('@/stores/profiles')
  const { useClientsStore } = await import('@/stores/clients')
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/usage', name: 'usage', component: Usage },
      { path: '/activity', name: 'activity', component: stub },
    ],
  })
  await router.push(path)
  await router.isReady()
  useProfilesStore().profiles = [WORK_RO] as any
  useProfilesStore().loaded = true
  useClientsStore().clients = [makeClient('cursor')] as any
  const wrapper = mount(Usage, { global: { plugins: [router] } })
  await flushPromises()
  await flushPromises()
  return { wrapper, router }
}

describe('Usage scope params (Spec 108-j)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['profile', 'client', 'token'])
    getUsageMock.mockImplementation(() => Promise.resolve({ success: true, data: USAGE }))
    getStatusMock.mockResolvedValue({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
  })
  afterEach(() => setAvailableFeatures([]))

  it('?profile=work-ro sends profile, shows the chip, and replaces the tokens-saved tile with the note', async () => {
    const { wrapper } = await mountUsageAt('/usage?profile=work-ro')
    expect(getUsageMock).toHaveBeenCalledTimes(1)
    expect(getUsageMock.mock.calls[0][0]).toEqual(expect.objectContaining({ profile: 'work-ro' }))
    expect(wrapper.get('[data-test="usage-scope-chips"] [data-test="scope-chip-profile"]').text()).toContain('Profile: Work')
    expect(wrapper.get('[data-test="usage-tokens-saved-scoped"]').text()).toContain('not computed for a filtered view')
    expect(wrapper.find('[data-test="usage-tokens-saved-tile"]').exists()).toBe(false)
  })

  it('with no scope the tokens-saved tile is unchanged and no scope parameter is sent', async () => {
    const { wrapper } = await mountUsageAt('/usage')
    expect(wrapper.find('[data-test="usage-tokens-saved-tile"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="usage-tokens-saved-scoped"]').exists()).toBe(false)
    const params = getUsageMock.mock.calls[0][0]
    expect(params.profile).toBeUndefined()
    expect(params.client).toBeUndefined()
    expect(params.token).toBeUndefined()
  })

  it('no unfiltered flash: with /status pending the first fetch waits, then carries the param', async () => {
    setAvailableFeatures([])
    let resolveStatus!: (v: unknown) => void
    getStatusMock.mockReturnValue(new Promise(r => { resolveStatus = r }))
    const { useSystemStore } = await import('@/stores/system')
    void useSystemStore().fetchScopeFilterFeatures()
    await mountUsageAt('/usage?client=cursor')
    expect(getUsageMock).not.toHaveBeenCalled()
    resolveStatus({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
    await flushPromises()
    await flushPromises()
    expect(getUsageMock).toHaveBeenCalledTimes(1)
    expect(getUsageMock.mock.calls[0][0]).toEqual(expect.objectContaining({ client: 'cursor' }))
  })

  it('rule 7: with the filters not advertised nothing is sent, only the disabled chip shows and the tile is unchanged', async () => {
    setAvailableFeatures([])
    getStatusMock.mockResolvedValue({ success: true, data: {} })
    const { useSystemStore } = await import('@/stores/system')
    await useSystemStore().fetchScopeFilterFeatures()
    const { wrapper, router } = await mountUsageAt('/usage?client=cursor')
    expect(getUsageMock.mock.calls[0][0].client).toBeUndefined()
    // No active filter chip, but the URL's parameter is not silently dropped
    // either: the disabled "Filter unavailable on this server" chip names it (F5.2).
    expect(wrapper.find('[data-test="scope-chip-client"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="scope-chip-na-client"] [data-test="scope-chip-note"]').text()).toContain('Filter unavailable on this server')
    expect(wrapper.find('[data-test="usage-tokens-saved-tile"]').exists()).toBe(true)
    expect(router.currentRoute.value.query.client).toBe('cursor')
  })

  it('F5.2: /status failing still ends in the disabled "Filter unavailable" chip', async () => {
    setAvailableFeatures([])
    getStatusMock.mockRejectedValue(new Error('down'))
    const { useSystemStore } = await import('@/stores/system')
    await useSystemStore().fetchScopeFilterFeatures()
    const { wrapper } = await mountUsageAt('/usage?profile=work-ro')
    expect(wrapper.get('[data-test="scope-chip-na-profile"]').text()).toContain('Filter unavailable on this server')
  })

  it('F4.1: a sort/status control used while /status is pending sends nothing unfiltered', async () => {
    setAvailableFeatures([])
    let resolveStatus!: (v: unknown) => void
    getStatusMock.mockReturnValue(new Promise(r => { resolveStatus = r }))
    const { useSystemStore } = await import('@/stores/system')
    void useSystemStore().fetchScopeFilterFeatures()
    const { wrapper } = await mountUsageAt('/usage?profile=work-ro')
    await wrapper.get('[data-test="usage-sort"]').setValue('calls')
    await flushPromises()
    expect(getUsageMock).not.toHaveBeenCalled()
    resolveStatus({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })
    await flushPromises()
    await flushPromises()
    expect(getUsageMock.mock.calls.length).toBeGreaterThan(0)
    for (const call of getUsageMock.mock.calls) {
      expect(call[0]).toEqual(expect.objectContaining({ profile: 'work-ro' }))
    }
  })

  it('removing the chip refetches unscoped and brings the tile back', async () => {
    const { wrapper, router } = await mountUsageAt('/usage?client=cursor')
    await wrapper.get('[data-test="scope-chip-remove-client"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.client).toBeUndefined()
    expect(getUsageMock.mock.calls.at(-1)![0].client).toBeUndefined()
    expect(wrapper.find('[data-test="usage-tokens-saved-tile"]').exists()).toBe(true)
  })

  it('the Profile select writes ?profile= and refetches', async () => {
    const { wrapper, router } = await mountUsageAt('/usage')
    await wrapper.get('[data-test="scope-select-profile"]').setValue('work-ro')
    await flushPromises()
    expect(router.currentRoute.value.query.profile).toBe('work-ro')
    expect(getUsageMock.mock.calls.at(-1)![0]).toEqual(expect.objectContaining({ profile: 'work-ro' }))
  })

  it('an empty scoped view names the filter and the window', async () => {
    getUsageMock.mockImplementation(() => Promise.resolve({ success: true, data: { ...USAGE, tools: [], timeline: [] } }))
    const { wrapper } = await mountUsageAt('/usage?profile=work-ro')
    expect(wrapper.get('[data-test="usage-empty-state"]').text()).toContain('No calls for Profile: Work in the last 24h')
  })

  it('a chart bar carries the profile on to Activity (sticky, rule 3)', async () => {
    const { wrapper, router } = await mountUsageAt('/usage?profile=work-ro')
    const push = vi.spyOn(router, 'push')
    const histogram = wrapper.findComponent({ name: 'CallHistogram' })
    histogram.vm.$emit('select-tool', USAGE.tools[0])
    await flushPromises()
    expect(push).toHaveBeenCalled()
    const target = router.resolve(push.mock.calls[0][0] as any)
    expect(target.name).toBe('activity')
    expect(target.query.profile).toBe('work-ro')
    expect(target.query.tool).toBe('github:list_issues')
  })
})
