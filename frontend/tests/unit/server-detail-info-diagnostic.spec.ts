import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// MCPX_HTTP_CANCELED is the first severity=info code in the spec-044 catalog.
// ServerDetail's diagnostic gate accepted only warn/error, on the stated
// grounds that info diagnostics belong in "verbose/admin views, per spec" —
// spec 044 says no such thing, and the exclusion hid nothing: it fell through
// to the generic red "Server Error" box, which prints the raw last_error with
// no explanation, fix steps or docs link. The calmest code in the catalog was
// therefore rendered the loudest, and stripped of its content on the way.
//
// ErrorPanel already renders info calmly (alert-info / badge-info / the
// neutral "Diagnostic" header), so the fix is in the gate, not the component.

let serverExtra: Record<string, unknown>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() =>
        ok({
          servers: [
            {
              name: 'remote-api',
              protocol: 'http',
              enabled: true,
              connected: false,
              quarantined: false,
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
      invokeDiagnosticFix: vi.fn(() => ok({})),
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
  await router.push('/servers/remote-api?tab=tools')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'remote-api' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

const canceledDiagnostic = {
  code: 'MCPX_HTTP_CANCELED',
  severity: 'info',
  cause: 'connect aborted: context canceled',
  user_message:
    'The connection attempt was canceled — usually a shutdown, a config reload, or a manual disconnect. No action needed unless it repeats.',
  docs_url: 'https://docs.mcpproxy.app/errors/MCPX_HTTP_CANCELED',
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  serverExtra = {}
})

describe('ServerDetail — info-severity diagnostics reach the panel', () => {
  it('renders the structured panel for MCPX_HTTP_CANCELED instead of the red generic error', async () => {
    serverExtra = {
      last_error: 'connect aborted: context canceled',
      diagnostic: canceledDiagnostic,
      health: { level: 'degraded', admin_state: 'enabled', summary: 'Disconnected', action: 'retry' },
    }
    const wrapper = await mountDetail()

    // The explanation the code exists to give must be on screen...
    expect(wrapper.find('[data-testid="error-panel-code"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="error-panel-severity"]').text()).toBe('info')
    expect(wrapper.text()).toContain('No action needed unless it repeats')

    // ...and the raw-last_error red box must not fire in its place.
    expect(wrapper.find('[data-test="server-detail-generic-error"]').exists()).toBe(false)
  })

  it('still suppresses everything for a quarantined server (issue #1076 guard intact)', async () => {
    serverExtra = {
      quarantined: true,
      trust_mode: 'manual',
      last_error: 'connect aborted: context canceled',
      diagnostic: canceledDiagnostic,
      health: {
        level: 'healthy',
        admin_state: 'quarantined',
        summary: 'Quarantined for review',
        action: 'approve',
      },
    }
    const wrapper = await mountDetail()

    expect(wrapper.find('[data-testid="error-panel-code"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="server-detail-generic-error"]').exists()).toBe(false)
  })
})
