import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109-l P10c: the Servers page renders the `profile` row 109-k registered
// (url-filter-contract.md: Servers `profile`, requires scope_filters). A profile
// in the URL asks GET /servers?profile= once, before the first list is shown
// (rule 1: no flash of the unfiltered list), shows a removable chip and a
// Profile select, and never sends the parameter before the build lists it.

const getServers = vi.fn()
const getProfiles = vi.fn()
// What GET /status lists under features.scope_filters for the build under test.
let statusFeatures: string[] | undefined

vi.mock('@/services/api', () => ({
  default: {
    getServers: (...args: unknown[]) => getServers(...args),
    getProfiles: (...args: unknown[]) => getProfiles(...args),
    getSecurityOverview: vi.fn(() => Promise.resolve({ success: true, data: { scanners_enabled: 0, total_scans: 0 } })),
    getActivitySummary: vi.fn(() => Promise.resolve({ success: true, data: { per_server: [] } })),
    getStatus: vi.fn(() => Promise.resolve({ success: true, data: { features: { scope_filters: statusFeatures } } })),
    scanAll: vi.fn(() => Promise.resolve({ success: true, data: {} })),
  },
}))

function makeServer(name: string, overrides: Record<string, unknown> = {}) {
  return {
    name,
    protocol: 'stdio' as const,
    enabled: true,
    connected: true,
    quarantined: false,
    status: 'ready',
    reconnect_count: 0,
    tool_count: 3,
    created: '2026-07-28T00:00:00Z',
    updated: '2026-07-28T00:00:00Z',
    ...overrides,
  }
}

const ALL = [makeServer('github', { tool_count: 5 }), makeServer('notion'), makeServer('filesystem')]

async function mountServersAt(path: string) {
  const pinia = createPinia()
  setActivePinia(pinia)
  // The page reads which filters the build offers from GET /status (rule 7).
  const { useSystemStore } = await import('@/stores/system')
  await useSystemStore().fetchScopeFilterFeatures()
  const { useServersStore } = await import('@/stores/servers')
  const store = useServersStore()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  store.servers = ALL as any
  store.loaded = true
  const { useProfilesStore } = await import('@/stores/profiles')
  const profiles = useProfilesStore()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  profiles.profiles = [{ name: 'work-readonly', title: 'Work (read-only)' }, { name: 'work-full', title: 'Work' }] as any
  profiles.loaded = true

  const Servers = (await import('@/views/Servers.vue')).default
  const ServerCard = (await import('@/components/ServerCard.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers', name: 'servers', component: Servers },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Servers, { global: { plugins: [pinia, router] } })
  await flushPromises()
  return { wrapper, ServerCard, router }
}

function shownNames(wrapper: ReturnType<typeof mount>, ServerCard: unknown): string[] {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return wrapper.findAllComponents(ServerCard as any).map(c => (c.props('server') as { name: string }).name).sort()
}

describe('Servers page — profile scope (Spec 109-l)', () => {
  beforeEach(() => {
    getServers.mockReset()
    getProfiles.mockReset()
    getProfiles.mockResolvedValue({ profiles: [], anonymous_profile: '' })
    getServers.mockImplementation(async (scope?: { profile?: string }) => ({
      success: true,
      data: { servers: scope?.profile ? [makeServer('github', { tool_count: 2 })] : ALL },
    }))
    statusFeatures = ['profile', 'client', 'token']
  })

  it('?profile= requests GET /servers once with the profile and shows only that profile\'s servers', async () => {
    const { wrapper, ServerCard } = await mountServersAt('/servers?profile=work-readonly')
    const scopedCalls = getServers.mock.calls.filter(call => call[0]?.profile)
    expect(scopedCalls).toEqual([[{ profile: 'work-readonly' }]])
    expect(getServers.mock.calls.filter(call => !call[0]?.profile)).toHaveLength(0)
    expect(shownNames(wrapper, ServerCard)).toEqual(['github'])
    expect(wrapper.find('[data-test="scope-chip-profile"]').text()).toContain('Work (read-only)')
    const select = wrapper.find('[data-test="servers-profile-select"]')
    expect(select.exists()).toBe(true)
    expect((select.element as HTMLSelectElement).value).toBe('work-readonly')
    expect(wrapper.find('label[for="servers-profile-select"]').text()).toBe('Profile')
  })

  it('removing the chip clears the URL parameter and shows the full list without another profile request', async () => {
    const { wrapper, ServerCard, router } = await mountServersAt('/servers?profile=work-readonly')
    await wrapper.find('[data-test="scope-chip-remove-profile"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.profile).toBeUndefined()
    expect(shownNames(wrapper, ServerCard)).toEqual(['filesystem', 'github', 'notion'])
    expect(getServers.mock.calls.filter(call => call[0]?.profile)).toHaveLength(1)
  })

  it('choosing a profile in the select writes ?profile= and fetches the scoped list; All clears it', async () => {
    const { wrapper, ServerCard, router } = await mountServersAt('/servers')
    expect(getServers).not.toHaveBeenCalled()
    await wrapper.find('[data-test="servers-profile-select"]').setValue('work-full')
    await flushPromises()
    expect(router.currentRoute.value.query.profile).toBe('work-full')
    expect(getServers).toHaveBeenCalledWith({ profile: 'work-full' })
    expect(shownNames(wrapper, ServerCard)).toEqual(['github'])

    await wrapper.find('[data-test="servers-profile-select"]').setValue('')
    await flushPromises()
    expect(router.currentRoute.value.query.profile).toBeUndefined()
    expect(shownNames(wrapper, ServerCard)).toEqual(['filesystem', 'github', 'notion'])
  })

  it('refetches the scoped list when a profile changes elsewhere (#1460)', async () => {
    const { PROFILES_CHANGED_EVENT } = await import('@/stores/profiles')
    const { wrapper } = await mountServersAt('/servers?profile=work-readonly')
    const before = getServers.mock.calls.length
    window.dispatchEvent(new Event(PROFILES_CHANGED_EVENT))
    await flushPromises()
    expect(getServers.mock.calls.length).toBe(before + 1)
    expect(getServers).toHaveBeenLastCalledWith({ profile: 'work-readonly' })
    wrapper.unmount()
    window.dispatchEvent(new Event(PROFILES_CHANGED_EVENT))
    await flushPromises()
    expect(getServers.mock.calls.length).toBe(before + 1)
  })

  it('refetches the scoped list when only a security scan verdict changes (#1460)', async () => {
    vi.useFakeTimers()
    try {
      const { wrapper } = await mountServersAt('/servers?profile=work-readonly')
      const before = getServers.mock.calls.length
      const { useServersStore } = await import('@/stores/servers')
      const store = useServersStore()
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      store.servers = store.servers.map(s => (s.name === 'github' ? { ...s, security_scan: { status: 'warnings', finding_counts: { warning: 2 } } } : s)) as any
      await vi.advanceTimersByTimeAsync(400)
      expect(getServers.mock.calls.length).toBe(before + 1)
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  it('profile=- renders a disabled chip and is never sent', async () => {
    const { wrapper, ServerCard } = await mountServersAt('/servers?profile=-')
    expect(getServers).not.toHaveBeenCalled()
    const chip = wrapper.find('[data-test="scope-chip-profile"]')
    expect(chip.text()).toContain('Unattributed applies to Activity and Usage only')
    expect(chip.attributes('aria-disabled')).toBe('true')
    expect(shownNames(wrapper, ServerCard)).toEqual(['filesystem', 'github', 'notion'])
  })

  it('a 404 shows "Profile not found" inline with a Clear that removes the parameter', async () => {
    getServers.mockRejectedValueOnce(Object.assign(new Error('HTTP 404'), { status: 404 }))
    const { wrapper, router } = await mountServersAt('/servers?profile=gone')
    expect(wrapper.find('[data-test="servers-profile-not-found"]').text()).toContain('Profile not found')
    await wrapper.find('[data-test="servers-profile-clear"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.profile).toBeUndefined()
    expect(wrapper.find('[data-test="servers-profile-not-found"]').exists()).toBe(false)
  })

  it('a profile with no servers says so and offers Clear', async () => {
    getServers.mockResolvedValueOnce({ success: true, data: { servers: [] } })
    const { wrapper } = await mountServersAt('/servers?profile=work-readonly')
    expect(wrapper.text()).toContain('No servers in this profile')
    expect(wrapper.find('[data-test="servers-profile-clear"]').exists()).toBe(true)
  })

  it('without the feature: no select, no chip, no profile request, and the URL keeps the parameter (rule 7)', async () => {
    statusFeatures = undefined
    const { wrapper, ServerCard, router } = await mountServersAt('/servers?profile=work-readonly')
    expect(wrapper.find('[data-test="servers-profile-select"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="scope-chip-profile"]').exists()).toBe(false)
    expect(getServers.mock.calls.filter(call => call[0]?.profile)).toHaveLength(0)
    expect(router.currentRoute.value.query.profile).toBe('work-readonly')
    expect(shownNames(wrapper, ServerCard)).toEqual(['filesystem', 'github', 'notion'])
  })
})
