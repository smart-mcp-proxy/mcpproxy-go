import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Live QA finding on 109-k-activity-scope-filters: "No scope-aware page
// writes filter changes back to the URL. `router.replace` does not appear
// anywhere in frontend/src/views/Activity.vue, Tools.vue, or Servers.vue
// ... on /tools?server=filesystem&tier=read, clicking 'Clear Filters'
// visibly clears the Server/Tier controls but window.location.href stays
// '...?server=filesystem&tier=read'." FR-080 requires `router.replace` on
// every filter change; this suite is the missing other half of
// tools-scope-query-hydration.spec.ts / servers-scope.spec.ts (which only
// ever exercised URL -> state), asserting state -> URL now round-trips too
// (SC-009).
//
// One `vi.mock('@/services/api', ...)` for the whole file — vitest hoists
// mocks per module specifier, so a second one for the same path in the
// Servers describe block below would silently replace this one for the
// entire file rather than adding to it.
const TOOLS = [
  { name: 'read_file', server_name: 'filesystem', description: '', enabled: true, tier: 'read', approval_status: 'approved' },
  { name: 'create_issue', server_name: 'github', description: '', enabled: true, tier: 'write', approval_status: 'approved' },
]

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getGlobalTools: vi.fn(() =>
        ok({ tools: TOOLS, stats: { total: TOOLS.length, enabled: 2, disabled: 0, pending_approval: 0 } })
      ),
      getToolApprovals: vi.fn(() => ok({ approvals: [] })),
      getQuarantinedTools: vi.fn(() => ok({ tools: [] })),
      getServers: vi.fn(() => ok({ servers: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, total_scans: 0 })),
      scanAll: vi.fn(() => ok({})),
    },
  }
})

describe('Tools page — filter changes write back to the URL (FR-080)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  async function mountToolsAt(path: string) {
    const Tools = (await import('@/views/Tools.vue')).default
    const router = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/tools', component: Tools },
        { path: '/servers/:serverName', component: { template: '<div/>' } },
        { path: '/activity', name: 'activity', component: { template: '<div/>' } },
      ],
    })
    await router.push(path)
    await router.isReady()
    const wrapper = mount(Tools, { global: { plugins: [createPinia(), router] } })
    await flushPromises()
    await flushPromises()
    return { wrapper, router }
  }

  it('picking a server filter replaces the URL, not just the table', async () => {
    const { wrapper, router } = await mountToolsAt('/tools')
    await wrapper.find('[data-test="filter-server"]').setValue('filesystem')
    await flushPromises()
    expect(router.currentRoute.value.query.server).toBe('filesystem')
  })

  it('"Clear Filters" removes every filter param from the URL, not only from the controls', async () => {
    const { wrapper, router } = await mountToolsAt('/tools?server=filesystem&tier=read')
    expect(router.currentRoute.value.query.server).toBe('filesystem')

    const clearBtn = wrapper.findAll('button').find(b => b.text() === 'Clear Filters')
    expect(clearBtn).toBeTruthy()
    await clearBtn!.trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query.server).toBeUndefined()
    expect(router.currentRoute.value.query.tier).toBeUndefined()
  })

  it('resolves the `risk` alias to the canonical `tier` on write-back, dropping the stale `risk`', async () => {
    // applyQueryParam() (read side) resolves `risk` into `filterTier` on
    // mount, and the write-back watch normalizes the URL to match as soon as
    // it observes that value — an old `?risk=` link is rewritten to the
    // canonical `?tier=` the first time the page's own filters change at
    // all, not only when the Tier control itself is touched.
    const { router } = await mountToolsAt('/tools?risk=read')
    expect(router.currentRoute.value.query.tier).toBe('read')
    expect(router.currentRoute.value.query.risk).toBeUndefined()
  })
})

describe('Servers page — filter changes write back to the URL (FR-080)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  function makeServer(name: string, overrides: Record<string, unknown> = {}) {
    return {
      name,
      protocol: 'stdio' as const,
      enabled: true,
      connected: true,
      quarantined: false,
      status: 'ready',
      reconnect_count: 0,
      tool_count: 3,
      created: '2026-07-28T00:00:00Z',
      updated: '2026-07-28T00:00:00Z',
      ...overrides,
    }
  }

  async function mountServersAt(path: string) {
    const pinia = createPinia()
    setActivePinia(pinia)
    const { useServersStore } = await import('@/stores/servers')
    const store = useServersStore()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    store.servers = [makeServer('alpha'), makeServer('beta', { quarantined: true })] as any
    store.loaded = true

    const Servers = (await import('@/views/Servers.vue')).default
    const router = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/servers', name: 'servers', component: Servers },
        { path: '/servers/:serverName', component: { template: '<div/>' } },
      ],
    })
    await router.push(path)
    await router.isReady()
    const wrapper = mount(Servers, { global: { plugins: [pinia, router] } })
    await flushPromises()
    return { wrapper, router }
  }

  it('clicking a filter pill writes `status` to the URL', async () => {
    const { wrapper, router } = await mountServersAt('/servers')
    const quarantinedBtn = wrapper.findAll('button').find(b => b.text().startsWith('Quarantined'))
    expect(quarantinedBtn).toBeTruthy()
    await quarantinedBtn!.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.status).toBe('quarantined')
  })

  it('clicking "All" after a redirect clears `status` from the URL, matching the cleared pill', async () => {
    const { wrapper, router } = await mountServersAt('/servers?status=quarantined')
    expect(router.currentRoute.value.query.status).toBe('quarantined')
    const allBtn = wrapper.findAll('button').find(b => b.text().startsWith('All ('))
    expect(allBtn).toBeTruthy()
    await allBtn!.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.status).toBeUndefined()
  })

  it('typing in the search box writes `q` to the URL', async () => {
    const { wrapper, router } = await mountServersAt('/servers')
    await wrapper.find('[data-test="servers-search"]').setValue('alpha')
    await flushPromises()
    expect(router.currentRoute.value.query.q).toBe('alpha')
  })
})
