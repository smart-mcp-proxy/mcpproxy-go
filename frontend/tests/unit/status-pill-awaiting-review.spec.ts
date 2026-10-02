import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-052 / navigation-map "Status pill" (fix-usertest-web T175, user
// test journey B): four servers that only wait for review read as "0 of 4
// online", which looks like a failed install. The pill says what is true:
// how many are online, how many await review, how many are really offline.
// Before the first server list it says it is loading, never "0 of 0 online".

vi.mock('@/services/api', () => ({
  default: { hasAPIKey: vi.fn(() => true) },
}))

import StatusPill from '@/components/StatusPill.vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'

const stub = { template: '<div />' }

function health(adminState: 'enabled' | 'disabled' | 'quarantined', usable: boolean) {
  return { level: usable ? 'healthy' : 'degraded', admin_state: adminState, summary: '', status: usable ? 'ready' : 'x', usable, actions: [] }
}

const quarantinedRow = (name: string) => ({
  name, enabled: true, quarantined: true, connected: false, tool_count: 0, status: 'needs_review', health: health('quarantined', false),
})

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

const full = (w: Awaited<ReturnType<typeof mountPill>>) => w.get('[data-test="header-status-full"]').text().replace(/\s+/g, ' ')

describe('StatusPill awaiting review and first load (fix-usertest-web T175)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    useServersStore().loaded = true
    useSystemStore().$patch({ status: { running: true, listen_addr: '127.0.0.1:8080', routing_mode: 'retrieve_tools' } as any })
  })

  it('names servers awaiting review instead of reading as a failed install', async () => {
    useServersStore().servers = ['memory', 'fixture', 'filesystem', 'everything'].map(quarantinedRow) as any
    const wrapper = await mountPill()
    expect(full(wrapper)).toBe('0 online · 4 awaiting review · 0 tools · Retrieve')
    expect(wrapper.get('[data-test="header-status-awaiting"]').text()).toBe('4 awaiting review')
    expect(wrapper.get('[data-test="header-status-online"]').text()).toBe('0 online')
    expect(wrapper.find('[data-test="header-status-offline"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="header-status-compact"]').text()).toBe('0/4')
    expect(wrapper.get('[data-test="header-status-dot"]').classes()).toContain('bg-info')
    const pill = wrapper.get('[data-test="header-status-pill"]')
    expect(pill.attributes('aria-label')).toBe('0 of 4 servers online, 4 awaiting review, 0 tools, routing mode Retrieve')
    expect(pill.attributes('title')).toBe('0 online · 4 awaiting review · 0 tools · Retrieve')
  })

  it('adds an offline count and goes amber when a server is really down', async () => {
    useServersStore().servers = [
      { name: 'ok', enabled: true, quarantined: false, connected: true, tool_count: 5, status: 'ready', health: health('enabled', true) },
      quarantinedRow('q1'),
      quarantinedRow('q2'),
      { name: 'bad', enabled: true, quarantined: false, connected: false, tool_count: 0, status: 'error', health: health('enabled', false) },
    ] as any
    const wrapper = await mountPill()
    expect(full(wrapper)).toBe('1 online · 2 awaiting review · 1 offline · 5 tools · Retrieve')
    expect(wrapper.get('[data-test="header-status-offline"]').text()).toBe('1 offline')
    expect(wrapper.get('[data-test="header-status-dot"]').classes()).toContain('bg-warning')
    expect(wrapper.get('[data-test="header-status-pill"]').attributes('aria-label')).toBe(
      '1 of 4 servers online, 2 awaiting review, 1 offline, 5 tools, routing mode Retrieve',
    )
  })

  it('counts a disabled row in no bucket', async () => {
    useServersStore().servers = [
      { name: 'off', enabled: false, quarantined: false, connected: false, tool_count: 0, status: 'disabled', health: health('disabled', false) },
      quarantinedRow('q'),
    ] as any
    const wrapper = await mountPill()
    expect(full(wrapper)).toBe('0 online · 1 awaiting review · 0 tools · Retrieve')
  })

  it('counts an old core row (no health) that is quarantined and enabled as awaiting', async () => {
    useServersStore().servers = [{ name: 'old', enabled: true, quarantined: true, connected: false, tool_count: 0 }] as any
    const wrapper = await mountPill()
    expect(full(wrapper)).toBe('0 online · 1 awaiting review · 0 tools · Retrieve')
  })

  it('keeps the legacy text when nothing awaits review', async () => {
    useServersStore().servers = [
      { name: 'ok', enabled: true, quarantined: false, connected: true, tool_count: 2, health: health('enabled', true) },
      { name: 'bad', enabled: true, quarantined: false, connected: false, tool_count: 0, health: health('enabled', false) },
    ] as any
    const wrapper = await mountPill()
    expect(full(wrapper)).toBe('1 of 2 online · 2 tools · Retrieve')
    expect(wrapper.find('[data-test="header-status-awaiting"]').exists()).toBe(false)
  })

  it('says it is loading before the first server list, never "0 of 0 online"', async () => {
    useServersStore().loaded = false
    const wrapper = await mountPill()
    expect(full(wrapper)).toBe('Loading servers…')
    expect(wrapper.get('[data-test="header-status-compact"]').text()).toBe('…')
    expect(wrapper.get('[data-test="header-status-pill"]').attributes('aria-label')).toBe('Loading server status')
    expect(wrapper.get('[data-test="header-status-dot"]').classes()).toContain('bg-base-content/30')
    expect(wrapper.text()).not.toContain('of 0')
  })

  it('says servers are unavailable when the first load failed', async () => {
    const store = useServersStore()
    store.loaded = false
    store.loading = { loading: false, error: 'boom' } as any
    const wrapper = await mountPill()
    expect(full(wrapper)).toBe('Servers unavailable')
    expect(wrapper.get('[data-test="header-status-compact"]').text()).toBe('!')
    expect(wrapper.get('[data-test="header-status-dot"]').classes()).toContain('bg-error')
    expect(wrapper.get('[data-test="header-status-pill"]').attributes('href')).toBe('/servers')
  })

  it('leaves the loading state when the server list arrives', async () => {
    const store = useServersStore()
    store.loaded = false
    const wrapper = await mountPill()
    expect(full(wrapper)).toBe('Loading servers…')
    store.servers = [quarantinedRow('a')] as any
    store.loaded = true
    await flushPromises()
    expect(full(wrapper)).toBe('0 online · 1 awaiting review · 0 tools · Retrieve')
  })
})
