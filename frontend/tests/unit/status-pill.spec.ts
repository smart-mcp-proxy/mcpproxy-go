import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109-i FR-053 / T136: the header status pill counts servers by the ONE
// usable predicate (health.usable), not by "connected", so a connected but
// quarantined server never reads as online. It never reads /status
// upstream_stats.connected_servers.

vi.mock('@/services/api', () => ({
  default: { hasAPIKey: vi.fn(() => true) },
}))

import StatusPill from '@/components/StatusPill.vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'

const stub = { template: '<div />' }

function health(usable: boolean) {
  return { level: usable ? 'healthy' : 'unhealthy', admin_state: 'enabled', summary: '', status: usable ? 'ready' : 'disabled', usable, actions: [] }
}

const fixture = [
  { name: 'A', enabled: true, quarantined: false, connected: true, tool_count: 10, health: health(true) },
  { name: 'B', enabled: true, quarantined: true, connected: true, tool_count: 4, health: health(false) },
  { name: 'C', enabled: false, quarantined: false, connected: false, tool_count: 0, health: health(false) },
  // Old core: no health object, so fall back to connected && enabled && !quarantined.
  { name: 'D', enabled: true, quarantined: false, connected: true, tool_count: 3 },
]

async function mountPill() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: stub }, { path: '/servers', component: stub }],
  })
  router.push('/')
  await router.isReady()
  const wrapper = mount(StatusPill, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('StatusPill (Spec 109-i FR-053)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    useServersStore().servers = fixture as any
    useSystemStore().$patch({ status: { running: true, listen_addr: '127.0.0.1:8080', routing_mode: 'retrieve_tools' } as any })
  })

  it('counts usable servers and their tools, with the routing mode label', async () => {
    const wrapper = await mountPill()
    expect(wrapper.get('[data-test="header-status-full"]').text().replace(/\s+/g, ' ')).toBe('2 of 4 online · 13 tools · Retrieve')
    expect(wrapper.get('[data-test="header-status-compact"]').text()).toBe('2/4')
    expect(wrapper.get('[data-test="header-status-online"]').text()).toBe('2 of 4 online')
    expect(wrapper.get('[data-test="header-status-tools"]').text()).toBe('13 tools')
    expect(wrapper.get('[data-test="header-status-mode"]').text()).toBe('Retrieve')
  })

  it('ignores upstream_stats.connected_servers from /status', async () => {
    useSystemStore().$patch({ status: { running: true, upstream_stats: { connected_servers: 3, total_servers: 4 } } as any })
    const wrapper = await mountPill()
    expect(wrapper.get('[data-test="header-status-online"]').text()).toBe('2 of 4 online')
  })

  it('names the direct routing mode', async () => {
    useSystemStore().$patch({ status: { running: true, routing_mode: 'direct' } as any })
    const wrapper = await mountPill()
    expect(wrapper.get('[data-test="header-status-mode"]').text()).toBe('Direct')
  })

  it('links to /servers with an exact aria-label', async () => {
    const wrapper = await mountPill()
    const pill = wrapper.get('[data-test="header-status-pill"]')
    expect(pill.attributes('href')).toBe('/servers')
    expect(pill.attributes('aria-label')).toBe('2 of 4 servers online, 13 tools, routing mode Retrieve')
  })

  it('names the pending mode when a routing restart is required', async () => {
    useSystemStore().routing = {
      routing_mode: 'retrieve_tools', pending_routing_mode: 'direct', restart_required: true,
      endpoints: {}, available_modes: [], description: '',
    } as any
    const wrapper = await mountPill()
    expect(wrapper.get('[data-test="header-status-mode"]').attributes('title')).toContain('Direct')
  })

  it('reads neutral with no servers and green when every server is online', async () => {
    useServersStore().servers = [] as any
    let wrapper = await mountPill()
    expect(wrapper.get('[data-test="header-status-dot"]').classes()).toContain('bg-base-content/30')

    useServersStore().servers = [fixture[0]] as any
    wrapper = await mountPill()
    expect(wrapper.get('[data-test="header-status-dot"]').classes()).toContain('bg-success')
  })

  it('reads amber when some servers are down and red when the core is not running', async () => {
    let wrapper = await mountPill()
    expect(wrapper.get('[data-test="header-status-dot"]').classes()).toContain('bg-warning')

    useSystemStore().$patch({ status: { running: false } as any })
    wrapper = await mountPill()
    expect(wrapper.get('[data-test="header-status-dot"]').classes()).toContain('bg-error')
  })
})
