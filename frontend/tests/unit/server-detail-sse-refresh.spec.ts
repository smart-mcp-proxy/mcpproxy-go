import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 088 US5 (T024) — the server detail page keeps itself current from the
// live event stream, scoped to the displayed server (FR-019/FR-020, research D5).
//
// Two window events (re-dispatched by stores/system.ts from SSE):
//
//   `mcpproxy:scan-settled`   — one debounced event per server per scan. Its
//                               payload carries NO verdict/risk (event_bus.go),
//                               so the page must REFETCH the server projection
//                               (scan summary + banner) and the tool approvals
//                               rather than trust anything in `detail`.
//   `mcpproxy:servers-changed`— accompanies approvals made via CLI/MCP, which do
//                               NOT emit scan-settled. Refresh the approvals and
//                               the server projection.
//
// Scoping: scan-settled carries `server_name`, so events for OTHER servers are
// ignored outright. Listeners live for the lifetime of the mounted view only,
// and the absence of events must change nothing — the existing manual-refresh
// and scan-polling paths are untouched (FR-020).

let serverPayload: Record<string, unknown>
let approvalTools: Array<Record<string, unknown>>
let inventoryTools: Array<Record<string, unknown>>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() => ok({ servers: [{ ...serverPayload }] })),
      getToolApprovals: vi.fn(() =>
        ok({ tools: approvalTools.map((t) => ({ ...t })), count: approvalTools.length })
      ),
      getServerTools: vi.fn(() => ok({ tools: inventoryTools.map((t) => ({ ...t })) })),
      getToolDiff: vi.fn(() => ok({})),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: true })),
      listScanners: vi.fn(() => ok([])),
      getScanReport: vi.fn(() => ok({ job_id: 'scan-github-1', risk_score: 0, findings: [] })),
      getScanStatus: vi.fn(() => ok({ id: 'scan-github-1', status: 'completed', scan_pass: 1 })),
      startScan: vi.fn(() => ok({ id: 'scan-github-2' })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      approveTools: vi.fn(() => ok({ approved: 1 })),
      blockTools: vi.fn(() => ok({ blocked: 1 })),
      patchServer: vi.fn(() => ok({ message: 'ok' })),
    },
  }
})

const mounted: Array<{ unmount: () => void }> = []
let pinia: ReturnType<typeof createPinia>

async function mountDetail() {
  const api = (await import('@/services/api')).default
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', component: { template: '<div/>' } },
    ],
  })
  await router.push('/servers/github?tab=tools')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'github' },
    global: { plugins: [pinia, router] },
  })
  mounted.push(wrapper)
  await flushPromises()
  await flushPromises()
  return { wrapper, api }
}

/** Dispatch a window event the way stores/system.ts does, then settle the view. */
async function emit(name: string, detail: unknown) {
  window.dispatchEvent(new CustomEvent(name, { detail }))
  await flushPromises()
  await flushPromises()
}

beforeEach(() => {
  pinia = createPinia()
  setActivePinia(pinia)
  vi.clearAllMocks()
  serverPayload = {
    name: 'github',
    protocol: 'stdio',
    enabled: true,
    connected: true,
    quarantined: true,
    trust_mode: 'scan',
    tool_count: 1,
    security_scan: { status: 'scanning', risk_score: 0 },
  }
  approvalTools = [{ tool_name: 'create_issue', status: 'pending', description: 'Create an issue' }]
  inventoryTools = [{ name: 'create_issue', description: 'Create an issue' }]
})

afterEach(async () => {
  // Each mounted view owns window listeners; leaving instances alive would let
  // an earlier test's component answer a later test's event and skew counts.
  while (mounted.length) mounted.pop()!.unmount()
  // The servers store registers its OWN long-lived `mcpproxy:servers-changed`
  // listener at creation (stores/servers.ts) — drop it too, otherwise every
  // previous test's store keeps refetching on this test's events.
  const { useServersStore } = await import('@/stores/servers')
  useServersStore(pinia).cleanupEventListeners()
})

describe('ServerDetail — scan-settled live refresh (Spec 088 T024, FR-019)', () => {
  it('refetches the server projection and the tool approvals for the displayed server', async () => {
    const { api } = await mountDetail()
    const serversBefore = api.getServers.mock.calls.length
    const approvalsBefore = api.getToolApprovals.mock.calls.length

    await emit('mcpproxy:scan-settled', { server_name: 'github', status: 'dangerous' })

    expect(api.getServers.mock.calls.length).toBeGreaterThan(serversBefore)
    expect(api.getToolApprovals.mock.calls.length).toBe(approvalsBefore + 1)
  })

  it('renders the REFETCHED verdict, never the fields carried on the event payload', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="quarantine-banner-state"]').attributes('data-state')).toBe(
      'scan-running'
    )

    // The refetched projection is the only source of truth: the payload here
    // claims "clean" but the server actually came back dangerous.
    serverPayload = {
      ...serverPayload,
      security_scan: {
        status: 'dangerous',
        risk_score: 82,
        finding_counts: { dangerous: 2, warning: 1, info: 0, total: 3 },
      },
    }
    await emit('mcpproxy:scan-settled', { server_name: 'github', status: 'clean' })

    expect(wrapper.find('[data-test="quarantine-banner-state"]').attributes('data-state')).toBe(
      'scan-blocked'
    )
    expect(wrapper.find('[data-test="quarantine-scan-summary"]').text()).toContain('dangerous')
  })

  it('refreshes the latest scan report so evidence links point at the settled run', async () => {
    const { api } = await mountDetail()
    const reportsBefore = api.getScanReport.mock.calls.length

    await emit('mcpproxy:scan-settled', { server_name: 'github', status: 'clean' })

    expect(api.getScanReport.mock.calls.length).toBeGreaterThan(reportsBefore)
  })

  it('ignores scan-settled events for other servers', async () => {
    const { api } = await mountDetail()
    const serversBefore = api.getServers.mock.calls.length
    const approvalsBefore = api.getToolApprovals.mock.calls.length

    await emit('mcpproxy:scan-settled', { server_name: 'other-server', status: 'dangerous' })

    expect(api.getServers.mock.calls.length).toBe(serversBefore)
    expect(api.getToolApprovals.mock.calls.length).toBe(approvalsBefore)
  })

  it('refreshes exactly once per settled event — no refetch loop', async () => {
    const { api } = await mountDetail()
    const approvalsBefore = api.getToolApprovals.mock.calls.length

    await emit('mcpproxy:scan-settled', { server_name: 'github', status: 'clean' })
    await flushPromises()
    await flushPromises()

    expect(api.getToolApprovals.mock.calls.length).toBe(approvalsBefore + 1)
  })
})

describe('ServerDetail — servers-changed live refresh (Spec 088 T024, FR-019)', () => {
  it('refetches tool approvals so a CLI/MCP approval becomes visible', async () => {
    // The tool-quarantine panel is suppressed while the SERVER itself is
    // quarantined (the server-level banner takes over), so this case uses an
    // admitted server with a pending tool.
    serverPayload = { ...serverPayload, quarantined: false }
    const { wrapper, api } = await mountDetail()
    expect(wrapper.find('[data-test="tool-quarantine-banner"]').exists()).toBe(true)
    const approvalsBefore = api.getToolApprovals.mock.calls.length

    // Somebody approved the tool from the CLI: only servers.changed is emitted.
    approvalTools = [{ tool_name: 'create_issue', status: 'approved', description: 'Create an issue' }]
    await emit('mcpproxy:servers-changed', {})

    expect(api.getToolApprovals.mock.calls.length).toBe(approvalsBefore + 1)
    expect(wrapper.find('[data-test="tool-quarantine-banner"]').exists()).toBe(false)
  })

  it('refreshes the server projection', async () => {
    const { wrapper } = await mountDetail()

    serverPayload = { ...serverPayload, quarantined: false }
    await emit('mcpproxy:servers-changed', {})

    expect(wrapper.find('[data-test="security-quarantine-banner"]').exists()).toBe(false)
  })
})

describe('ServerDetail — listener lifecycle and event-free baseline (Spec 088 T024, FR-020)', () => {
  it('stops listening once the view is unmounted', async () => {
    const { wrapper, api } = await mountDetail()
    wrapper.unmount()
    const serversBefore = api.getServers.mock.calls.length
    const approvalsBefore = api.getToolApprovals.mock.calls.length

    await emit('mcpproxy:scan-settled', { server_name: 'github', status: 'dangerous' })
    await emit('mcpproxy:servers-changed', {})

    expect(api.getToolApprovals.mock.calls.length).toBe(approvalsBefore)
    // The unmounted view triggers nothing of its own; only the servers store's
    // own long-lived listener may still refetch the list.
    expect(api.getServers.mock.calls.length).toBeLessThanOrEqual(serversBefore + 1)
  })

  it('changes nothing while no events arrive (manual refresh/polling untouched)', async () => {
    const { api } = await mountDetail()
    const snapshot = {
      servers: api.getServers.mock.calls.length,
      approvals: api.getToolApprovals.mock.calls.length,
      report: api.getScanReport.mock.calls.length,
    }

    await flushPromises()
    await flushPromises()

    expect(api.getServers.mock.calls.length).toBe(snapshot.servers)
    expect(api.getToolApprovals.mock.calls.length).toBe(snapshot.approvals)
    expect(api.getScanReport.mock.calls.length).toBe(snapshot.report)
  })
})
