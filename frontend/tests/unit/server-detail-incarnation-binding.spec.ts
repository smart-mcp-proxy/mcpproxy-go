import { describe, it, expect, beforeEach, afterEach, vi, onTestFinished } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 116 PR2 review round 1 (FR-017): Server Detail binds a per-tool
// decision to the incarnation of the rows ON SCREEN (the tools and approvals
// responses carry it), not to the live store. A same-name replacement that
// reached the store over SSE before its rows did must not let a click on the
// old rows act on the replacement; and the replacement's identity alone is a
// tool-list change worth refetching.

const SERVER = 'acme'
let serverPayload: Record<string, unknown>
let holdLoads = false

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  const never = () => new Promise(() => {})
  return {
    default: {
      getServers: vi.fn(() => ok({ servers: [{ ...serverPayload }] })),
      getServerTools: vi.fn(() => (holdLoads ? never() : ok({ tools: [{ name: 'echo', description: 'Echo' }], incarnation: 3 }))),
      getToolApprovals: vi.fn(() => (holdLoads ? never() : ok({ tools: [{ server_name: SERVER, tool_name: 'echo', status: 'approved', enabled: true, disabled: false }], count: 1, incarnation: 3 }))),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: true })),
      listScanners: vi.fn(() => ok([])),
      getScanReport: vi.fn(() => ok({ job_id: 'scan-1', risk_score: 0, findings: [] })),
      getScanStatus: vi.fn(() => ok({ id: 'scan-1', status: 'completed', scan_pass: 1 })),
      getToolDiff: vi.fn(() => ok({})),
      setToolEnabled: vi.fn(() => ok({ server_name: SERVER, tool_name: 'echo', enabled: false })),
      setAllToolsEnabled: vi.fn(() => ok({ server_name: SERVER, enabled: false, changed: 1 })),
      blockTools: vi.fn(() => ok({ blocked: 1 })),
    },
  }
})

const mounted: Array<{ unmount: () => void }> = []
let pinia: ReturnType<typeof createPinia>

async function mountDetail() {
  const api = (await import('@/services/api')).default as any
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
  const wrapper = mount(ServerDetail, { props: { serverName: SERVER }, global: { plugins: [pinia, router] } })
  mounted.push(wrapper)
  await flushPromises()
  await flushPromises()
  return { wrapper, api }
}

beforeEach(() => {
  pinia = createPinia()
  setActivePinia(pinia)
  vi.clearAllMocks()
  holdLoads = false
  serverPayload = { name: SERVER, protocol: 'stdio', enabled: true, connected: true, quarantined: false, tool_count: 1, incarnation: 3 }
})

afterEach(async () => {
  while (mounted.length) mounted.pop()!.unmount()
  const { useServersStore } = await import('@/stores/servers')
  useServersStore(pinia).cleanupEventListeners()
})

describe('ServerDetail — decisions bound to the displayed incarnation', () => {
  it('sends the incarnation of the rows on screen', async () => {
    const { wrapper, api } = await mountDetail()
    await (wrapper.vm as any).toggleToolEnabled('echo', false)
    await flushPromises()
    expect(api.setToolEnabled).toHaveBeenCalledTimes(1)
    expect(api.setToolEnabled.mock.calls[0][3]).toBe(3)
  })

  it('refuses a decision on stale rows once the store shows a replacement', async () => {
    const { wrapper, api } = await mountDetail()
    const { useServersStore } = await import('@/stores/servers')
    const store = useServersStore(pinia)
    holdLoads = true // the replacement's rows have not arrived yet
    const entry = store.servers.find((s: any) => s.name === SERVER)!
    entry.incarnation = 4
    await flushPromises()

    await (wrapper.vm as any).toggleToolEnabled('echo', false)
    await (wrapper.vm as any).blockTool('echo')
    await (wrapper.vm as any).bulkToggleAllTools(false)
    await flushPromises()
    expect(api.setToolEnabled).not.toHaveBeenCalled()
    expect(api.blockTools).not.toHaveBeenCalled()
    expect(api.setAllToolsEnabled).not.toHaveBeenCalled()
  })

  it('treats a new incarnation alone as a tool-list change', async () => {
    const { wrapper } = await mountDetail()
    const key = (wrapper.vm as any).toolsStateKey
    const base = { quarantined: false, connected: true, enabled: true, tool_count: 1 }
    expect(key({ ...base, incarnation: 3 })).not.toBe(key({ ...base, incarnation: 4 }))
  })

  // Review round 5 (Sol): a toggle started on incarnation A whose failure
  // (409 server_replaced) arrives after the replacement B's rows loaded must
  // not roll A's saved row or quarantine counts back into B's view.
  for (const kind of ['single', 'bulk'] as const) {
    it(`a late ${kind} toggle failure for the replaced incarnation leaves the replacement's state alone`, async () => {
      const { wrapper, api } = await mountDetail()
      const { useServersStore } = await import('@/stores/servers')
      const store = useServersStore(pinia)
      const originals = ['getToolApprovals', 'getServerTools', 'getToolDiff'].map(k => [k, api[k].getMockImplementation()] as const)
      onTestFinished(() => { for (const [k, impl] of originals) api[k].mockImplementation(impl) })

      let resolveToggle!: (v: unknown) => void
      const deferred = new Promise(r => { resolveToggle = r })
      if (kind === 'single') api.setToolEnabled.mockImplementationOnce(() => deferred)
      else api.setAllToolsEnabled.mockImplementationOnce(() => deferred)
      const pending = kind === 'single'
        ? (wrapper.vm as any).toggleToolEnabled('echo', false)
        : (wrapper.vm as any).bulkToggleAllTools(false)
      await flushPromises()
      expect(kind === 'single' ? api.setToolEnabled : api.setAllToolsEnabled).toHaveBeenCalledTimes(1)

      // The server is removed and re-added as B (incarnation 5) with a
      // changed same-name tool; B's store entry and rows finish loading.
      const bQuarantine = { pending_count: 0, changed_count: 1, blocked_count: 0 }
      serverPayload = { ...serverPayload, incarnation: 5, quarantine: { ...bQuarantine } }
      api.getToolApprovals.mockImplementation(() => Promise.resolve({ success: true, data: {
        tools: [{ server_name: SERVER, tool_name: 'echo', status: 'changed', enabled: true, disabled: false }],
        count: 1, incarnation: 5 } }))
      api.getServerTools.mockImplementation(() => Promise.resolve({ success: true, data: {
        tools: [{ name: 'echo', description: 'Echo v2' }], incarnation: 5 } }))
      api.getToolDiff.mockImplementation(() => Promise.resolve({ success: true, data: {
        previous_description: 'Echo', current_description: 'Echo v2' } }))
      await store.fetchServers(true)
      await (wrapper.vm as any).loadToolApprovals()
      await flushPromises()
      const vm = wrapper.vm as any
      expect(vm.toolApprovals[0].status).toBe('changed')

      resolveToggle({ success: false, error: "server_replaced: server 'acme' was removed or replaced since it was reviewed" })
      await pending
      await flushPromises()

      const row = vm.toolApprovals[0]
      expect(row.status).toBe('changed')
      expect(row.previous_description).toBe('Echo')
      expect(row.current_description).toBe('Echo v2')
      expect(vm.approvalsIncarnation).toBe(5)
      const entry = store.servers.find((s: any) => s.name === SERVER)!
      expect(entry.incarnation).toBe(5)
      expect(entry.quarantine).toEqual(bQuarantine)
    })
  }
})
