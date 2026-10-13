import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Issue #1076 suppresses every fault alert on a quarantined server, because
// mcpproxy dials quarantined servers so the scanner can export their tool
// definitions and that attempt reliably leaves a spurious error-severity
// diagnostic behind. That rule is right for the noise and wrong for a real
// fault: a quarantined stdio server pointed at a nonexistent command was shown
// as calm and approvable, and approving it just fails again.
//
// The backend now distinguishes the two — internal/health/calculator.go leaves
// an ordinary quarantined server at level "healthy" and marks one with an
// actual transport fault "unhealthy", keeping admin_state=quarantined and
// action=approve either way. These tests pin that the page follows that signal
// rather than blanket-suppressing.

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

const SPAWN_FAILURE =
  'failed to connect: stdio transport: server process exited before completing the MCP initialize handshake; recent stderr: command not found: definitely-not-a-real-binary-xyz'

describe('ServerDetail — a quarantined server with a real transport fault', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('shows the fault instead of suppressing it', async () => {
    const wrapper = await mountDetail({
      name: 'probe',
      protocol: 'stdio',
      enabled: true,
      connected: false,
      quarantined: true,
      status: 'error',
      last_error: SPAWN_FAILURE,
      tool_count: 0,
      health: {
        level: 'unhealthy',
        admin_state: 'quarantined',
        summary: 'Quarantined — Connection error',
        detail: SPAWN_FAILURE,
        action: 'approve',
      },
    })

    const text = wrapper.text()
    expect(text).toContain('definitely-not-a-real-binary-xyz')
  })

  // The control that keeps #1076 intact: an ordinary quarantined server, which
  // is the overwhelmingly common case, must stay calm. If this regresses, every
  // freshly added server shows a red alert for a failure that is by design.
  it('stays calm for an ordinary quarantined server', async () => {
    const wrapper = await mountDetail({
      name: 'probe',
      protocol: 'http',
      enabled: true,
      connected: false,
      quarantined: true,
      tool_count: 0,
      last_error: 'connection attempt aborted: server is quarantined',
      health: {
        level: 'healthy',
        admin_state: 'quarantined',
        summary: 'Quarantined for review',
        action: 'approve',
      },
    })

    expect(wrapper.find('[data-test="server-detail-generic-error"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('connection attempt aborted')
  })
})
