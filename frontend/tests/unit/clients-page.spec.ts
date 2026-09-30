import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import Clients from '@/views/Clients.vue'
import api from '@/services/api'
import { useSystemStore } from '@/stores/system'
import { setAvailableFeatures } from '@/composables/useScopeQuery'

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getClients: vi.fn(),
    getRouting: vi.fn(),
    getClient: vi.fn(),
  },
}))

const initialClients = [
  { id: 'cursor', display_name: 'Cursor', kind: 'supported', state: 'installed', connection_unverified: true, active_sessions: 1, last_seen: null },
  { id: 'other:zed', display_name: 'Zed', kind: 'other', state: 'other', active_sessions: 1, last_seen: '2026-09-29T10:00:00Z' },
]

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/clients', name: 'clients', component: Clients },
      { path: '/activity', name: 'activity', component: { template: '<div />' } },
      { path: '/tools', name: 'tools', component: { template: '<div />' } },
      { path: '/usage', name: 'usage', component: { template: '<div />' } },
    ],
  })
}

describe('Clients page', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures([])
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: initialClients } })
    ;(api.getRouting as any).mockResolvedValue({
      success: true,
      data: {
        endpoints: { default: '/mcp', direct: '/mcp/all', code_execution: '/mcp/code', retrieve_tools: '/mcp/call' },
        routing_mode: 'retrieve_tools', description: 'Retrieve tools', available_modes: ['retrieve_tools', 'direct'],
        restart_required: true, pending_routing_mode: 'direct',
      },
    })
    ;(api.getClient as any).mockResolvedValue({
      success: true,
      data: { ...initialClients[0], reload_hint: 'Restart Cursor to load MCPProxy', sessions: [{ id: 'session-1', work_session_id: 'work-1' }] },
    })
    useSystemStore().$patch({ status: { listen_addr: '127.0.0.1:18081' } as any })
  })

  it('loads counts first, expands details on demand, and separates observed clients from the manual snippet', async () => {
    const router = makeRouter()
    await router.push('/clients')
    await router.isReady()
    const wrapper = mount(Clients, {
      global: {
        plugins: [router],
        stubs: { ClientConnectList: true, AgentTokens: true, ModeSwitcher: true },
      },
    })
    await flushPromises()

    expect(api.getClient).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Zed')
    expect(wrapper.find('[data-test="other-client-snippet"]').exists()).toBe(true)
    const snippet = wrapper.find('[data-test="other-client-snippet"] code').text()
    expect(snippet).toContain('127.0.0.1:18081/mcp')
    expect(snippet).not.toContain('api_key')

    await wrapper.findAll('tbody tr')[0].trigger('click')
    await flushPromises()
    expect(api.getClient).toHaveBeenCalledTimes(1)
    expect(api.getClient).toHaveBeenCalledWith('cursor')
    expect(wrapper.find('a[href="/activity?view=sessions&session=work-1"]').exists()).toBe(true)

    expect(wrapper.find('a[href*="client=cursor"]').exists()).toBe(false)
    setAvailableFeatures(['scope_filters'])
    await flushPromises()
    expect(wrapper.find('a[href*="/activity?"][href*="client=cursor"][href*="view=calls"]').exists()).toBe(true)
    expect(wrapper.find('a[href="/tools?client=cursor"]').exists()).toBe(true)
    expect(wrapper.find('a[href="/usage?client=cursor"]').exists()).toBe(true)
  })

  it('reads the tab from the URL and preserves unrelated query parameters when changing tabs', async () => {
    const router = makeRouter()
    await router.push('/clients?tab=endpoint&token=agent-1')
    await router.isReady()
    const wrapper = mount(Clients, {
      global: { plugins: [router], stubs: { ClientConnectList: true, AgentTokens: true, ModeSwitcher: true } },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('Endpoint & mode')
    expect(wrapper.text()).toContain('Restart MCPProxy to apply direct mode.')
    expect(wrapper.text()).toContain('/mcp/all')

    await wrapper.find('[data-test="clients-tabs"] button').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ token: 'agent-1' })
    expect(router.currentRoute.value.query.tab).toBeUndefined()
  })

  // Spec 109-i: the header MCP-endpoints dropdown is gone, so its per-URL copy
  // buttons and descriptions live on this tab (FR-031 intent, T140).
  it('offers a copy button and description per endpoint on the Endpoint & mode tab', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const router = makeRouter()
    await router.push('/clients?tab=endpoint')
    await router.isReady()
    const wrapper = mount(Clients, {
      global: { plugins: [router], stubs: { ClientConnectList: true, AgentTokens: true, ModeSwitcher: true } },
    })
    await flushPromises()

    for (const name of ['default', 'direct', 'code_execution', 'retrieve_tools']) {
      expect(wrapper.find(`[data-test="copy-endpoint-${name}"]`).exists(), name).toBe(true)
    }
    expect(wrapper.get('[data-test="endpoint-description-direct"]').text()).toContain('serverName__toolName')

    await wrapper.get('[data-test="copy-endpoint-direct"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('http://127.0.0.1:18081/mcp/all')
  })

  it('checks an installed hand-configured client only after the explicit action', async () => {
    const router = makeRouter()
    await router.push('/clients')
    await router.isReady()
    const wrapper = mount(Clients, { global: { plugins: [router], stubs: { ClientConnectList: true, AgentTokens: true, ModeSwitcher: true } } })
    await flushPromises()

    expect(api.getClient).not.toHaveBeenCalled()
    await wrapper.find('[data-test="check-client-connection"]').trigger('click')
    await flushPromises()
    expect(api.getClient).toHaveBeenCalledWith('cursor')
  })

  it('focuses the requested client and expands its reload hint', async () => {
    const router = makeRouter()
    await router.push('/clients?focus=cursor')
    await router.isReady()
    const wrapper = mount(Clients, { global: { plugins: [router], stubs: { ClientConnectList: true, AgentTokens: true, ModeSwitcher: true } } })
    await flushPromises()

    expect(wrapper.find('[data-test="focused-client-row"]').text()).toContain('Cursor')
    expect(api.getClient).toHaveBeenCalledWith('cursor')
    expect(wrapper.text()).toContain('Restart Cursor to load MCPProxy')
  })

  it('resets to the default tab and removes an invalid tab while preserving unrelated parameters', async () => {
    const router = makeRouter()
    await router.push('/clients?tab=endpoint&token=agent-1')
    await router.isReady()
    const wrapper = mount(Clients, { global: { plugins: [router], stubs: { ClientConnectList: true, AgentTokens: true, ModeSwitcher: true } } })
    await flushPromises()
    expect(wrapper.text()).toContain('Endpoint & mode')

    await router.push('/clients?token=agent-1')
    await flushPromises()
    expect(wrapper.text()).toContain('Cursor')

    await router.push('/clients?tab=unknown&token=agent-1')
    await flushPromises()
    expect(wrapper.text()).toContain('Cursor')
    expect(router.currentRoute.value.query).toEqual({ token: 'agent-1' })
  })

  it('reloads the native Clients list after the shared connect list reports a successful write', async () => {
    const router = makeRouter()
    await router.push('/clients')
    await router.isReady()
    const wrapper = mount(Clients, {
      global: {
        plugins: [router],
        stubs: {
          ClientConnectList: { template: '<button data-test="connect-write" @click="$emit(\'updated\')" />' },
          AgentTokens: true,
          ModeSwitcher: true,
        },
      },
    })
    await flushPromises()
    expect(api.getClients).toHaveBeenCalledTimes(1)

    await wrapper.find('[data-test="connect-write"]').trigger('click')
    await flushPromises()
    expect(api.getClients).toHaveBeenCalledTimes(2)
  })
})
