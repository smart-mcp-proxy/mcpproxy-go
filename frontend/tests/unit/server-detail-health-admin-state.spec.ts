import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// UX audit F10 — "Status labels do not answer 'can my client use it?'".
//
// internal/health/calculator.go short-circuits a quarantined server to
// `level: healthy` on purpose ("quarantined is intentional, not broken"), so
// the Health tile — which prints the LEVEL — read a green "Healthy" on exactly
// the servers no client can reach. Worse, the badge overrides the summary with
// "Sign-in required" while the tile still says Healthy, a combination only
// reachable on a quarantined/disabled server (a plain login-needed server is
// LevelDegraded).
//
// The tile now leads with the answer to "can my client use it?": a non-enabled
// admin state wins over the level, and never renders in the success tone. The
// admin-state caption stops claiming an automatic quarantine-on-add was "set by
// you".

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
    history: createWebHistory(),
    routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }],
  })
  await router.push('/servers/probe')
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

describe('ServerDetail — Health tile answers "can my client use it?" (F10)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('does not read "Healthy" for a quarantined server', async () => {
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
    const tile = wrapper.find('[data-test="server-health-level"]')
    expect(tile.exists()).toBe(true)
    expect(tile.text()).not.toBe('Healthy')
    expect(tile.text()).toBe('Blocked')
    // …and never in the success tone.
    expect(tile.classes()).not.toContain('text-success')
    // The observed fact stays on the sub-line.
    expect(wrapper.find('[data-test="server-health-summary"]').text()).toContain(
      'Quarantined for review'
    )
  })

  it('does not read "Healthy" for a disabled server', async () => {
    const wrapper = await mountDetail({
      ...base,
      enabled: false,
      connected: false,
      health: {
        level: 'healthy',
        admin_state: 'disabled',
        summary: 'Disabled',
        action: 'enable',
      },
    })
    const tile = wrapper.find('[data-test="server-health-level"]')
    expect(tile.text()).toBe('Off')
    expect(tile.classes()).not.toContain('text-success')
  })

  it('still reads "Healthy" for an enabled, healthy server', async () => {
    const wrapper = await mountDetail({
      ...base,
      health: {
        level: 'healthy',
        admin_state: 'enabled',
        summary: 'Connected (2 tools)',
        action: 'none',
      },
    })
    const tile = wrapper.find('[data-test="server-health-level"]')
    expect(tile.text()).toBe('Healthy')
    expect(tile.classes()).toContain('text-success')
  })

  it('still reads "Degraded"/"Unhealthy" for an enabled server that is in trouble', async () => {
    const wrapper = await mountDetail({
      ...base,
      connected: false,
      health: {
        level: 'unhealthy',
        admin_state: 'enabled',
        summary: 'Connection failed',
        action: 'view_logs',
      },
    })
    expect(wrapper.find('[data-test="server-health-level"]').text()).toBe('Unhealthy')

    const degraded = await mountDetail({
      ...base,
      health: {
        level: 'degraded',
        admin_state: 'enabled',
        summary: 'Sign-in required soon',
        action: 'login',
      },
    })
    expect(degraded.find('[data-test="server-health-level"]').text()).toBe('Degraded')
  })

  it('does not claim an automatic quarantine-on-add was "set by you"', async () => {
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
    expect(wrapper.find('[data-test="server-admin-state"]').text()).toBe('Quarantined')
    const caption = wrapper.find('[data-test="server-admin-state-desc"]')
    expect(caption.exists()).toBe(true)
    expect(caption.text()).not.toContain('set by you')
    expect(caption.text()).toContain('awaiting your review')
  })

  it('keeps "set by you" for an admin state the operator really did set', async () => {
    const wrapper = await mountDetail({
      ...base,
      enabled: false,
      connected: false,
      health: {
        level: 'healthy',
        admin_state: 'disabled',
        summary: 'Disabled',
        action: 'enable',
      },
    })
    expect(wrapper.find('[data-test="server-admin-state-desc"]').text()).toBe('set by you')
  })
})
