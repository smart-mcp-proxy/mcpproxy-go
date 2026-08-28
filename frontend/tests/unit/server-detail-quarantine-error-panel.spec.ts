import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Issue #1076 — a quarantined server rendered the red ErrorPanel AND the calm
// Security-Quarantine banner off ONE payload that says healthy/quarantined.
//
// mcpproxy still attempts a connection to a quarantined server so the scanner
// can export its tool definitions. That attempt fails and leaves an
// error-severity diagnostic behind, while internal/health/calculator.go
// short-circuits quarantined servers to level:healthy, admin_state:quarantined.
// Both facts are true in one response; the UI must read the admin state first.
//
// The precedent is SignInPanel (MCP-1821): a server that needs sign-in is a
// calm state, not a fault. Quarantine is the second such state. The tray
// equivalent is ServerStatus.isBadgeExempt (isOAuthLoginRequired ||
// isQuarantineReview) — `admin_state` is the cross-surface contract.

let serverExtra: Record<string, unknown>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() =>
        ok({
          servers: [
            {
              name: 'everything',
              protocol: 'stdio',
              enabled: true,
              connected: false,
              quarantined: true,
              tool_count: 0,
              ...serverExtra,
            },
          ],
        })
      ),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getToolDiff: vi.fn(() => ok({})),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: true })),
      listScanners: vi.fn(() => ok([])),
      getScanReport: vi.fn(() => Promise.resolve({ success: false, error: 'no report' })),
      getScanStatus: vi.fn(() => ok({ id: 'scan-1', status: 'completed', scan_pass: 1 })),
      startScan: vi.fn(() => ok({ id: 'scan-2' })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      approveTools: vi.fn(() => ok({ approved: 1 })),
      blockTools: vi.fn(() => ok({ blocked: 1 })),
      patchServer: vi.fn(() => ok({ message: 'ok' })),
    },
  }
})

async function mountDetail() {
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', component: { template: '<div/>' } },
    ],
  })
  await router.push('/servers/everything?tab=tools')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'everything' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

// The real payload from the issue: an unclassified error diagnostic riding
// along with a healthy/quarantined health block.
const unclassifiedDiagnostic = {
  code: 'MCPX_UNKNOWN_UNCLASSIFIED',
  severity: 'error',
  user_message:
    'mcpproxy could not classify this failure. Please file a bug report so we can add a specific code.',
}

const quarantinedHealth = {
  level: 'healthy',
  admin_state: 'quarantined',
  summary: 'Quarantined for review',
  action: 'approve',
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  serverExtra = {}
})

describe('ServerDetail — quarantine suppresses the fault alerts (issue #1076)', () => {
  it('renders the quarantine banner alone, not the red ErrorPanel', async () => {
    serverExtra = {
      trust_mode: 'manual',
      diagnostic: unclassifiedDiagnostic,
      health: quarantinedHealth,
    }
    const wrapper = await mountDetail()

    expect(wrapper.find('[data-test="security-quarantine-banner"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="error-panel-code"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('file a bug report')
  })

  it('does not let the generic last_error alert through in the ErrorPanel’s place', async () => {
    // Suppressing the diagnostic panel must not simply fall through to the
    // `v-else-if="server.last_error"` branch — that prints the same fault in a
    // different red box.
    serverExtra = {
      trust_mode: 'manual',
      last_error: 'failed to connect: stdio transport: transport error: transport closed',
      diagnostic: unclassifiedDiagnostic,
      health: quarantinedHealth,
    }
    const wrapper = await mountDetail()

    expect(wrapper.find('[data-test="security-quarantine-banner"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-detail-generic-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="error-panel-code"]').exists()).toBe(false)
  })

  it('still shows the ErrorPanel for the same diagnostic when NOT quarantined', async () => {
    // The guard is narrow: quarantine is the exemption, not the diagnostic.
    serverExtra = {
      quarantined: false,
      diagnostic: unclassifiedDiagnostic,
      health: { level: 'unhealthy', admin_state: 'enabled', summary: 'Connection failed', action: 'retry' },
    }
    const wrapper = await mountDetail()

    expect(wrapper.find('[data-test="security-quarantine-banner"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="error-panel-code"]').text()).toBe('MCPX_UNKNOWN_UNCLASSIFIED')
  })

  it('still shows the generic last_error alert when NOT quarantined and no diagnostic', async () => {
    serverExtra = {
      quarantined: false,
      last_error: 'dial tcp: lookup example.invalid: no such host',
      health: { level: 'unhealthy', admin_state: 'enabled', summary: 'Host not found', action: 'edit_url' },
    }
    const wrapper = await mountDetail()

    expect(wrapper.find('[data-test="server-detail-generic-error"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-detail-generic-error"]').text()).toContain('no such host')
  })

  it('keeps SignInPanel precedence for a quarantined server that needs sign-in', async () => {
    // MCP-1821's calm panel already handles quarantine (it takes a
    // `quarantined` prop); the new guard must not swallow it.
    serverExtra = {
      trust_mode: 'manual',
      diagnostic: {
        code: 'MCPX_OAUTH_LOGIN_REQUIRED',
        severity: 'error',
        user_message: 'Sign in to continue.',
      },
      health: { ...quarantinedHealth, action: 'login' },
    }
    const wrapper = await mountDetail()

    expect(wrapper.find('[data-test="oauth-signin-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="error-panel-code"]').exists()).toBe(false)
  })
})
