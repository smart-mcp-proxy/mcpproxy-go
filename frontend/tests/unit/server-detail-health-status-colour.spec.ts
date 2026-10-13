import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 health-vocabulary contract, "Colors" (T169): colour keys on
// `health.status`, never on `level`, on every Server Detail element that
// renders a status label. A quarantined server has `level: healthy` (being off
// is intentional), so the Configuration -> Health badge used to read a green
// "Needs review" and the tile said "Blocked" in grey for the same server.
// `level` colours only a payload that carries no `status` (an older core).

type ServerOverrides = Record<string, unknown>

const state = { server: {} as ServerOverrides }

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() => ok({ servers: [state.server] })),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getToolDiff: vi.fn(() => ok({})),
      getSecurityOverview: vi.fn(() => ok({})),
      listScanners: vi.fn(() => ok({ scanners: [] })),
      getScanReport: vi.fn(() => ok({})),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
    },
  }
})

async function mountDetail(server: ServerOverrides) {
  state.server = server
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }],
  })
  await router.push('/servers/probe?tab=config')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'probe' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return wrapper
}

const base = {
  name: 'probe',
  protocol: 'http',
  enabled: true,
  connected: true,
  quarantined: false,
  tool_count: 2,
}

const configBadge = (w: Awaited<ReturnType<typeof mountDetail>>) =>
  w.find('[data-test="server-config-health-status"] span')

describe('ServerDetail — health colour keys on status (T169)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  const cases: Array<[string, string, string]> = [
    ['ready', 'healthy', 'badge-success'],
    ['connecting', 'healthy', 'badge-neutral'],
    ['disabled', 'healthy', 'badge-neutral'],
    ['sign_in_required', 'degraded', 'badge-warning'],
    ['needs_review', 'healthy', 'badge-warning'],
    ['needs_secret', 'degraded', 'badge-warning'],
    ['needs_config', 'degraded', 'badge-warning'],
    ['error', 'unhealthy', 'badge-error'],
  ]

  it.each(cases)('Configuration health badge for status %s (level %s) is %s', async (status, level, cls) => {
    const wrapper = await mountDetail({
      ...base,
      health: {
        level,
        admin_state: 'enabled',
        summary: status,
        action: '',
        status,
        usable: status === 'ready',
        actions: [],
      },
    })
    const badge = configBadge(wrapper)
    expect(badge.exists()).toBe(true)
    expect(badge.classes()).toContain(cls)
    if (cls !== 'badge-success') expect(badge.classes()).not.toContain('badge-success')
  })

  it('colours a quarantined, level=healthy server warning in badge, tile and header, and never says Blocked', async () => {
    const wrapper = await mountDetail({
      ...base,
      connected: false,
      quarantined: true,
      health: {
        level: 'healthy',
        admin_state: 'quarantined',
        summary: 'Quarantined for review',
        action: 'approve',
        status: 'needs_review',
        usable: false,
        actions: ['approve'],
      },
    })
    const badge = configBadge(wrapper)
    expect(badge.classes()).toContain('badge-warning')
    expect(badge.classes()).not.toContain('badge-success')

    const tile = wrapper.find('[data-test="server-health-level"]')
    expect(tile.text()).toBe('Needs review')
    expect(tile.classes()).toContain('text-warning')
    expect(tile.text()).not.toContain('Blocked')

    const header = wrapper.find('[data-test="server-status-badge"]')
    expect(header.classes()).toContain('badge-warning')
    // The header badge text stays the summary.
    expect(header.text()).toContain('Quarantined for review')
  })

  it('reads "Sign-in required" in warning for a quarantined OAuth server', async () => {
    const wrapper = await mountDetail({
      ...base,
      connected: false,
      quarantined: true,
      health: {
        level: 'healthy',
        admin_state: 'quarantined',
        summary: 'Quarantined for review',
        action: 'login',
        status: 'sign_in_required',
        usable: false,
        actions: ['login'],
      },
    })
    const tile = wrapper.find('[data-test="server-health-level"]')
    expect(tile.text()).toBe('Sign-in required')
    expect(tile.classes()).toContain('text-warning')
  })

  it('reads "Disabled" (not "Off") in neutral for a disabled server, never success', async () => {
    const wrapper = await mountDetail({
      ...base,
      enabled: false,
      connected: false,
      health: {
        level: 'healthy',
        admin_state: 'disabled',
        summary: 'Disabled',
        action: 'enable',
        status: 'disabled',
        usable: false,
        actions: ['enable'],
      },
    })
    const tile = wrapper.find('[data-test="server-health-level"]')
    expect(tile.text()).toBe('Disabled')
    expect(tile.text()).not.toBe('Off')
    expect(tile.classes()).toContain('text-base-content/50')
    expect(tile.classes()).not.toContain('text-success')
  })

  it('keeps the level-based reading for an old core that sends no status', async () => {
    const wrapper = await mountDetail({
      ...base,
      connected: false,
      quarantined: true,
      health: {
        level: 'healthy',
        admin_state: 'quarantined',
        summary: 'Quarantined for review',
        action: 'approve',
      },
    })
    expect(configBadge(wrapper).classes()).toContain('badge-success')
    expect(wrapper.find('[data-test="server-health-level"]').text()).toBe('Blocked')
  })
})
