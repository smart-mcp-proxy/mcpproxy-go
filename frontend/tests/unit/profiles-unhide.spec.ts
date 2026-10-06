import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ref } from 'vue'

// Spec 109-l T151 (vitest half): the profile / client / token filters, links and
// controls 109-k registered are un-hidden once Spec 108 ships. Table-driven over
//   features {absent, scope_filters lists profile+client+token} x
//   the /profiles route {absent, present}
// so every control is proven present only when ITS gate is open (FR-080, FR-080a,
// FR-082; url-filter-contract.md rule 7).

const calls: Array<{ fn: string; args: unknown[] }> = []
let statusFeatures: string[] | undefined

vi.mock('@/services/api', () => {
  const record = (fn: string, value: unknown) => vi.fn((...args: unknown[]) => {
    calls.push({ fn, args })
    return Promise.resolve(value)
  })
  const fakeEventSource = { onopen: null, onmessage: null, onerror: null, addEventListener() {}, removeEventListener() {}, close() {} }
  const base: Record<string, unknown> = {
    hasAPIKey: vi.fn(() => true),
    getAPIKeyPreview: vi.fn(() => 'key'),
    onAuthError: vi.fn(() => () => {}),
    createEventSource: vi.fn(() => fakeEventSource),
    getAttention: record('getAttention', { success: true, data: { count: 0, items: [] } }),
    getStatus: vi.fn(() => Promise.resolve({ success: true, data: { features: { scope_filters: statusFeatures } } })),
    getProfiles: record('getProfiles', { profiles: [{ name: 'work-ro', title: 'Work' }], anonymous_profile: '' }),
    getRouting: record('getRouting', { success: true, data: { endpoints: {}, routing_mode: 'retrieve_tools', available_modes: [] } }),
    getClients: record('getClients', {
      success: true,
      data: { clients: [{ id: 'cursor', display_name: 'Cursor', kind: 'supported', state: 'installed', active_sessions: 0, last_seen: null, credential_state: 'client' }] },
    }),
    getClient: record('getClient', { success: true, data: { id: 'cursor', display_name: 'Cursor', kind: 'supported', state: 'installed', active_sessions: 0, sessions: [] } }),
    listAgentTokens: record('listAgentTokens', {
      success: true,
      data: {
        tokens: [
          { name: 'ci-bot', kind: 'agent', token_prefix: 'mcp_agt_aaaa', allowed_servers: ['*'], permissions: ['read'], expires_at: '2099-01-01T00:00:00Z', created_at: '2026-01-01T00:00:00Z', revoked: false, profile_pin: 'work-ro' },
          { name: 'client-cursor', kind: 'client', client_id: 'cursor', token_prefix: 'mcp_cli_bbbb', allowed_servers: ['*'], permissions: ['read'], expires_at: '2099-01-01T00:00:00Z', created_at: '2026-01-01T00:00:00Z', revoked: false, profile_mode: 'locked' },
        ],
      },
    }),
    getServers: record('getServers', { success: true, data: { servers: [] } }),
    getActivitySummary: record('getActivitySummary', { success: true, data: { per_server: [] } }),
    getSecurityOverview: record('getSecurityOverview', { success: true, data: { scanners_enabled: 0, total_scans: 0 } }),
    searchTools: record('searchTools', { success: true, data: { tools: [] } }),
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = vi.fn().mockResolvedValue({ success: true, data: {} })
        return target[prop]
      },
    }),
  }
})

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  refreshSecurityScannerStatus: vi.fn().mockResolvedValue(undefined),
  useSecurityScannerStatus: () => ({ totalFindings: ref(0), totalScans: ref(0), loaded: ref(false), hasEnabledScanners: () => false }),
}))

import SidebarNav from '@/components/SidebarNav.vue'
import AddMenu from '@/components/AddMenu.vue'
import CommandPalette from '@/components/CommandPalette.vue'
import ViewingFilter from '@/components/ViewingFilter.vue'
import Clients from '@/views/Clients.vue'
import AgentTokens from '@/views/AgentTokens.vue'
import Servers from '@/views/Servers.vue'
import { useSystemStore } from '@/stores/system'
import { useProfilesStore } from '@/stores/profiles'
import { useServersStore } from '@/stores/servers'

const stub = { template: '<div />' }
const SCOPE_NAMES = ['profile', 'client', 'token']

const MATRIX = [
  { name: 'features absent, no /profiles route', features: false, route: false },
  { name: 'features absent, /profiles route', features: false, route: true },
  { name: 'features listed, no /profiles route', features: true, route: false },
  { name: 'features listed, /profiles route', features: true, route: true },
]

async function setup(cell: { features: boolean; route: boolean }, url: string, view?: unknown) {
  statusFeatures = cell.features ? ['profile', 'client', 'token'] : undefined
  const pinia = createPinia()
  setActivePinia(pinia)
  await useSystemStore().fetchScopeFilterFeatures()
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: stub },
      { path: '/clients', name: 'clients', component: view ?? stub },
      { path: '/activity', name: 'activity', component: stub },
      { path: '/tools', name: 'tools', component: stub },
      { path: '/usage', name: 'usage', component: stub },
      { path: '/servers', name: 'servers', component: view ?? stub },
      { path: '/add-server', component: stub },
      ...(cell.route ? [{ path: '/profiles', name: 'profiles', component: stub }] : []),
    ],
  })
  await router.push(url)
  await router.isReady()
  return { pinia, router }
}

describe.each(MATRIX)('profiles un-hide: $name (Spec 109-l T151)', cell => {
  beforeEach(() => {
    calls.length = 0
    document.body.innerHTML = ''
  })
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('sidebar Profiles entry, + Add -> Profile and palette "Create profile" follow the route only', async () => {
    const { pinia, router } = await setup(cell, '/')

    const sidebar = shallowMount(SidebarNav, { global: { plugins: [pinia, router], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    expect(sidebar.find('[data-test="sidebar-item-profiles"]').exists()).toBe(cell.route)
    sidebar.unmount()

    const add = mount(AddMenu, { attachTo: document.body, global: { plugins: [pinia, router], stubs: { ClientConnectList: true } } })
    await add.get('[data-test="header-add-menu"]').trigger('click')
    expect(add.find('[data-test="add-menu-profile"]').exists()).toBe(cell.route)
    add.unmount()

    const palette = mount(CommandPalette, { attachTo: document.body, global: { plugins: [pinia, router] } })
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true, bubbles: true, cancelable: true }))
    await flushPromises()
    const dialog = document.querySelector('dialog[data-test="command-palette"]') as HTMLDialogElement
    expect(dialog.textContent?.includes('Create profile')).toBe(cell.route)
    palette.unmount()
  })

  it('the header viewing chip needs the build to list the filters', async () => {
    const { pinia, router } = await setup(cell, '/activity')
    const profiles = useProfilesStore()
    profiles.$patch({ profiles: [{ name: 'work-ro', title: 'Work' }] as never, loaded: true })
    const wrapper = mount(ViewingFilter, { global: { plugins: [pinia, router] } })
    await flushPromises()
    expect(wrapper.find('[data-test="viewing-filter"]').exists()).toBe(cell.features)
  })

  it('Clients row Activity / Sessions / Tools it sees / Usage carry client=<id> only with the filters', async () => {
    const { pinia, router } = await setup(cell, '/clients?client=cursor', Clients)
    const wrapper = mount(Clients, { global: { plugins: [pinia, router], stubs: { ClientConnectList: true, AgentTokens: true, ModeSwitcher: true } } })
    await flushPromises()
    await wrapper.findAll('tbody tr')[0].trigger('click')
    await flushPromises()

    const labels = ['activity', 'sessions', 'tools', 'usage']
    for (const label of labels) {
      expect(wrapper.find(`[data-test="clients-row-link-${label}-cursor"]`).exists()).toBe(cell.features)
    }
    if (cell.features) {
      const hrefOf = (label: string) => router.resolve(wrapper.get(`[data-test="clients-row-link-${label}-cursor"]`).attributes('href')!)
      expect(hrefOf('sessions').path).toBe('/activity')
      expect(hrefOf('sessions').query).toMatchObject({ view: 'sessions', client: 'cursor' })
      expect(hrefOf('tools').query).toMatchObject({ client: 'cursor' })
      expect(wrapper.get('[data-test="clients-row-link-tools-cursor"]').text()).toBe('Tools it sees')
      expect(wrapper.get('[data-test="clients-row-link-sessions-cursor"]').attributes('aria-label')).toBe('Sessions for Cursor')
    } else {
      // Rule 7: the parameter stays untouched in the URL and is never sent.
      expect(router.currentRoute.value.query.client).toBe('cursor')
      for (const call of calls) {
        for (const arg of call.args) {
          if (arg && typeof arg === 'object') for (const name of SCOPE_NAMES) expect((arg as Record<string, unknown>)[name]).toBeUndefined()
        }
      }
    }
  })

  it('token rows link Activity / Usage by token for agent tokens only', async () => {
    const { pinia, router } = await setup(cell, '/clients?tab=tokens', AgentTokens)
    const wrapper = mount(AgentTokens, { global: { plugins: [pinia, router] } })
    // AgentTokens waits 100 ms before its first load.
    await new Promise(resolve => setTimeout(resolve, 150))
    await flushPromises()
    expect(wrapper.find('[data-test="token-row-link-activity-ci-bot"]').exists()).toBe(cell.features)
    expect(wrapper.find('[data-test="token-row-link-usage-ci-bot"]').exists()).toBe(cell.features)
    // A client credential is filtered as a client, from the Clients page.
    expect(wrapper.find('[data-test="token-row-link-activity-client-cursor"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="token-row-link-usage-client-cursor"]').exists()).toBe(false)
    if (cell.features) {
      const activity = router.resolve(wrapper.get('[data-test="token-row-link-activity-ci-bot"]').attributes('href')!)
      expect(activity.path).toBe('/activity')
      expect(activity.query).toMatchObject({ view: 'calls', token: 'ci-bot' })
      const usage = router.resolve(wrapper.get('[data-test="token-row-link-usage-ci-bot"]').attributes('href')!)
      expect(usage.path).toBe('/usage')
      expect(usage.query).toMatchObject({ token: 'ci-bot' })
    }
  })

  it('Servers shows the Profile select and chip only with the filters, and sends the parameter only then', async () => {
    const { pinia, router } = await setup(cell, '/servers?profile=work-ro', Servers)
    useServersStore().$patch({ servers: [{ name: 'github', enabled: true, connected: true, quarantined: false, tool_count: 1 }] as never, loaded: true })
    const wrapper = mount(Servers, { global: { plugins: [pinia, router] } })
    await flushPromises()
    expect(wrapper.find('[data-test="servers-profile-select"]').exists()).toBe(cell.features)
    expect(wrapper.find('[data-test="scope-chip-profile"]').exists()).toBe(cell.features)
    const scoped = calls.filter(call => call.fn === 'getServers' && (call.args[0] as { profile?: string } | undefined)?.profile)
    expect(scoped).toHaveLength(cell.features ? 1 : 0)
    expect(router.currentRoute.value.query.profile).toBe('work-ro')
  })
})
