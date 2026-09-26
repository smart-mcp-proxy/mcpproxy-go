import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// New-user walkthrough bug: approving a freshly added, quarantined stdio server
// on /ui/servers/<name> updated the header badge to "Connected (N tools)" but
// the Tools tile stayed 0 and the Tools tab kept "No tools available" until a
// reload.
//
// Root cause: the page only refetched its tool list from a watch on
// `connected`/`enabled`. A quarantined stdio server is already connected (its
// tools are merely withheld), so approval flips neither flag and nothing
// refetched. The page must refetch the tool list itself on approval success,
// and on a `servers.changed` event that touches the open server.

const SERVER = 'fixture'

let serverPayload: Record<string, unknown>
let inventoryTools: Array<Record<string, unknown>>

const TWO_TOOLS = [
  { name: 'echo', description: 'Echo the arguments back' },
  { name: 'ping', description: 'Reply with pong' },
]

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() => ok({ servers: [{ ...serverPayload }] })),
      getServerTools: vi.fn(() => ok({ tools: inventoryTools.map((t) => ({ ...t })) })),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: true })),
      listScanners: vi.fn(() => ok([])),
      getScanReport: vi.fn(() => ok({ job_id: 'scan-1', risk_score: 0, findings: [] })),
      getScanStatus: vi.fn(() => ok({ id: 'scan-1', status: 'completed', scan_pass: 1 })),
      startScan: vi.fn(() => ok({ id: 'scan-2' })),
      getToolDiff: vi.fn(() => ok({})),
      discoverServerTools: vi.fn(() => ok({})),
      patchServer: vi.fn(() => ok({ message: 'ok' })),
      // The core unquarantines the server and releases its tools.
      securityApprove: vi.fn(() => {
        serverPayload = { ...serverPayload, quarantined: false, tool_count: TWO_TOOLS.length }
        inventoryTools = TWO_TOOLS
        return ok({})
      }),
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
  await router.push(`/servers/${SERVER}?tab=tools`)
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: SERVER },
    global: { plugins: [pinia, router] },
  })
  mounted.push(wrapper)
  await flushPromises()
  await flushPromises()
  return { wrapper, api }
}

async function settle() {
  await flushPromises()
  await flushPromises()
}

async function emitServersChanged(detail: unknown) {
  window.dispatchEvent(new CustomEvent('mcpproxy:servers-changed', { detail }))
  await settle()
}

function toolsTabLabel(wrapper: Awaited<ReturnType<typeof mountDetail>>['wrapper']) {
  return wrapper
    .findAll('.tab')
    .map((t) => t.text().trim())
    .find((t) => t.startsWith('Tools'))
}

beforeEach(() => {
  pinia = createPinia()
  setActivePinia(pinia)
  vi.clearAllMocks()
  // A freshly added stdio server: connected (the proxy spawned it to read its
  // tools) but quarantined, so its tools are withheld.
  serverPayload = {
    name: SERVER,
    protocol: 'stdio',
    enabled: true,
    connected: true,
    quarantined: true,
    trust_mode: 'scan',
    tool_count: 0,
    security_scan: {
      status: 'clean',
      risk_score: 0,
      last_scan_at: '2026-09-25T06:00:00Z',
      finding_counts: { dangerous: 0, warning: 0, info: 0, total: 0 },
    },
  }
  inventoryTools = []
})

afterEach(async () => {
  while (mounted.length) mounted.pop()!.unmount()
  const { useServersStore } = await import('@/stores/servers')
  useServersStore(pinia).cleanupEventListeners()
})

describe('ServerDetail — tool list after approval', () => {
  it('refetches and renders the tools once Approve succeeds', async () => {
    const { wrapper, api } = await mountDetail()
    expect(toolsTabLabel(wrapper)).toBe('Tools (0)')
    expect(wrapper.find('[data-test="server-tools-empty"]').exists()).toBe(true)
    const toolsBefore = api.getServerTools.mock.calls.length

    await wrapper.get('[data-test="quarantine-action-approve"]').trigger('click')
    await settle()

    expect(api.securityApprove).toHaveBeenCalledWith(SERVER, false)
    expect(api.getServerTools.mock.calls.length).toBeGreaterThan(toolsBefore)
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')
    expect(wrapper.find('[data-test="server-tools-empty"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('echo')
  })
})

describe('ServerDetail — tool list on servers.changed', () => {
  it('refetches when the event shows the open server changed (e.g. approved elsewhere, tools released late)', async () => {
    const { wrapper, api } = await mountDetail()
    expect(toolsTabLabel(wrapper)).toBe('Tools (0)')
    const toolsBefore = api.getServerTools.mock.calls.length

    serverPayload = { ...serverPayload, quarantined: false, tool_count: 2 }
    inventoryTools = TWO_TOOLS
    await emitServersChanged({ reason: 'quarantine_changed', payload: { servers: [{ ...serverPayload }] } })

    expect(api.getServerTools.mock.calls.length).toBe(toolsBefore + 1)
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')
    expect(wrapper.find('[data-test="server-tools-empty"]').exists()).toBe(false)
  })

  it('refetches on a notify-only event, since it cannot tell which server changed', async () => {
    const { wrapper, api } = await mountDetail()
    const toolsBefore = api.getServerTools.mock.calls.length

    serverPayload = { ...serverPayload, quarantined: false, tool_count: 2 }
    inventoryTools = TWO_TOOLS
    await emitServersChanged({})

    expect(api.getServerTools.mock.calls.length).toBe(toolsBefore + 1)
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')
  })

  it('does not refetch when the event leaves the open server unchanged', async () => {
    const { api } = await mountDetail()
    const toolsBefore = api.getServerTools.mock.calls.length

    // Another server changed; ours is byte-identical in the embedded list.
    await emitServersChanged({
      reason: 'server_state_changed',
      payload: {
        servers: [{ ...serverPayload }, { name: 'other', enabled: true, connected: true, tool_count: 5 }],
      },
    })

    expect(api.getServerTools.mock.calls.length).toBe(toolsBefore)
  })

  it('does not flash the loading spinner during a background refetch', async () => {
    const { wrapper, api } = await mountDetail()
    serverPayload = { ...serverPayload, quarantined: false, tool_count: 2 }

    // Hold the refetch open so the in-flight render can be inspected.
    let release!: () => void
    api.getServerTools.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          release = () => resolve({ success: true, data: { tools: TWO_TOOLS.map((t) => ({ ...t })) } })
        })
    )
    await emitServersChanged({ payload: { servers: [{ ...serverPayload }] } })

    expect(release).toBeTypeOf('function')
    expect(wrapper.text()).not.toContain('Loading tools...')
    release()
    await settle()
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')
  })
})

describe('ServerDetail — background tool refetch bookkeeping', () => {
  it('retries on the next event after a failed background refetch', async () => {
    const { wrapper, api } = await mountDetail()
    serverPayload = { ...serverPayload, quarantined: false, tool_count: 2 }
    const changed = { payload: { servers: [{ ...serverPayload }] } }

    // First refetch fails transiently: the list must not be marked current.
    api.getServerTools.mockImplementationOnce(() => Promise.resolve({ success: false, error: '502' }))
    await emitServersChanged(changed)
    expect(toolsTabLabel(wrapper)).toBe('Tools (0)')

    // The same server state arrives again (e.g. another server changed).
    inventoryTools = TWO_TOOLS
    const before = api.getServerTools.mock.calls.length
    await emitServersChanged(changed)

    expect(api.getServerTools.mock.calls.length).toBe(before + 1)
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')
  })

  it('never lets an older overlapping response overwrite a newer one', async () => {
    const { wrapper, api } = await mountDetail()

    // Request A (older state) is held; request B (newer state) resolves first.
    let releaseA!: () => void
    api.getServerTools.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          releaseA = () => resolve({ success: true, data: { tools: [] } })
        })
    )
    serverPayload = { ...serverPayload, quarantined: false, tool_count: 0 }
    await emitServersChanged({ payload: { servers: [{ ...serverPayload }] } })

    serverPayload = { ...serverPayload, tool_count: 2 }
    inventoryTools = TWO_TOOLS
    await emitServersChanged({ payload: { servers: [{ ...serverPayload }] } })
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')

    releaseA()
    await settle()
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')
  })
})
