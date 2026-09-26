import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// zcode review finding (Spec 109 self-import PR): the tools-refresh dedup key
// `[quarantined, connected, enabled, tool_count].join('|')` can get stuck.
//
// `tool_count` on the wire is a STICKY value (#1064 / MCP-2083): the
// supervisor re-sticks the pre-disconnect count on reconcile, and it is only
// zeroed while `quarantined`. Approving a quarantined server flips
// `quarantined` false but triggers a disconnect/reconnect that clears the
// real StateView tool list; the sticky count survives unchanged across that
// cycle. So:
//
//   1. Approval fires a servers.changed event with key `false|true|true|2`
//      (sticky count from before quarantine). The tool refetch races the
//      reconnect and returns an EMPTY list. The page commits the empty list
//      AND records key `false|true|true|2` as "current".
//   2. Background discovery finishes and re-populates the real tools. The
//      follow-up servers.changed (tools_indexed) event carries the SAME key
//      `false|true|true|2` (count never changed — it was sticky the whole
//      time), so the naive key comparison sees "no change" and skips the
//      refetch — permanently. Badge says "Connected (2 tools)", Tools tab
//      stays "No tools available" until a manual reload.
//
// The fix must not trust the key while the visible list is empty for an
// active server: it has to keep retrying until a real list lands.

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
  // Already unquarantined and connected with a STICKY non-zero tool_count
  // from before a disconnect/reconnect cycle, but the real list is
  // momentarily empty (cleared by the reconnect, not yet rediscovered).
  serverPayload = {
    name: SERVER,
    protocol: 'stdio',
    enabled: true,
    connected: true,
    quarantined: false,
    trust_mode: 'scan',
    tool_count: 2,
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

describe('ServerDetail — sticky tool_count must not stall the refetch', () => {
  it('keeps retrying while empty for an active server, even when the key is unchanged', async () => {
    const { wrapper, api } = await mountDetail()
    expect(toolsTabLabel(wrapper)).toBe('Tools (0)')

    // First event under this (already stuck) key: still empty upstream.
    await emitServersChanged({
      reason: 'server_connected',
      payload: { servers: [{ ...serverPayload }] },
    })
    expect(toolsTabLabel(wrapper)).toBe('Tools (0)')

    // Background discovery finishes and repopulates the real list. The
    // follow-up event carries the IDENTICAL key (quarantined/connected/
    // enabled/tool_count all unchanged — tool_count was sticky throughout).
    inventoryTools = TWO_TOOLS
    const before = api.getServerTools.mock.calls.length
    await emitServersChanged({
      reason: 'tools_indexed',
      payload: { servers: [{ ...serverPayload }] },
    })

    expect(api.getServerTools.mock.calls.length).toBeGreaterThan(before)
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')
    expect(wrapper.find('[data-test="server-tools-empty"]').exists()).toBe(false)
  })

  it('still dedupes once the list is non-empty and the key is unchanged', async () => {
    inventoryTools = TWO_TOOLS
    const { wrapper, api } = await mountDetail()
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')

    const before = api.getServerTools.mock.calls.length
    // Unrelated servers.changed broadcast with the exact same entry: no
    // reason to refetch, the dedup guard should still hold here.
    await emitServersChanged({
      reason: 'server_state_changed',
      payload: { servers: [{ ...serverPayload }] },
    })

    expect(api.getServerTools.mock.calls.length).toBe(before)
    expect(toolsTabLabel(wrapper)).toBe('Tools (2)')
  })
})
