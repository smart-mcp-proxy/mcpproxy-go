import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 134 PR 2 (FR-020, FR-023..FR-025): the Servers table page. Inventory
// rows P2-02..P2-25.

const scanAll = vi.fn()
const fetchSummary = vi.fn()
let scanners = true

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const base: Record<string, unknown> = {
    getServers: ok({ servers: [] }),
    getActivitySummary: (...a: unknown[]) => fetchSummary(...a),
    scanAll: (...a: unknown[]) => scanAll(...a),
    hasAPIKey: vi.fn(() => true),
    onAuthError: vi.fn(() => () => {}),
  }
  return { default: new Proxy(base, { get(t: Record<string, unknown>, p: string) { if (p in t) return t[p]; t[p] = ok(); return t[p] } }) }
})
vi.mock('@/composables/useSecurityScannerStatus', () => ({
  useSecurityScannerStatus: () => ({ hasEnabledScanners: () => scanners }),
}))

import Servers from '@/views/Servers.vue'
import { useServersStore } from '@/stores/servers'
import type { Server } from '@/types'

function server(name: string, status: string, over: Record<string, unknown> = {}): Server {
  return {
    name, protocol: 'stdio', enabled: status !== 'disabled', quarantined: status === 'needs_review',
    connected: status === 'ready', connecting: false, tool_count: 2,
    health: { level: status === 'ready' ? 'healthy' : 'unhealthy', admin_state: status === 'disabled' ? 'disabled' : status === 'needs_review' ? 'quarantined' : 'enabled', summary: `${name} summary`, status, usable: status === 'ready', actions: [] },
    ...over,
  } as Server
}

const FLEET = [server('ok-1', 'ready'), server('ok-2', 'ready', { tool_count: 5 }), server('bad-1', 'error'), server('off-1', 'disabled'), server('rev-1', 'needs_review')]

async function mountAt(path: string, servers: Server[] = FLEET, width = 1440) {
  window.matchMedia = ((q: string) => ({ matches: q.includes('min-width: 768px') ? width >= 768 : false, media: q, addEventListener() {}, removeEventListener() {} })) as never
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useServersStore()
  store.servers = servers as never
  store.loaded = true
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/servers', name: 'servers', component: Servers },
      { path: '/:p(.*)*', component: { template: '<div />' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Servers, { global: { plugins: [pinia, router], stubs: { CollapsibleHintsPanel: true } } })
  await flushPromises()
  return { wrapper, router }
}
const names = (w: ReturnType<typeof mount>) => w.findAll('[data-test="server-card-title"]').map(n => n.text())

beforeEach(() => {
  scanners = true
  scanAll.mockReset().mockResolvedValue({ success: true })
  fetchSummary.mockReset().mockResolvedValue({ success: true, data: { per_server: [{ name: 'bad-1', calls: 4, errors: 3, last_call_at: new Date().toISOString() }] } })
})

describe('chips and order (P2-10, P2-25)', () => {
  it('default All, problems first, counts on chips', async () => {
    const { wrapper } = await mountAt('/servers')
    expect(names(wrapper)).toEqual(['bad-1', 'rev-1', 'ok-1', 'ok-2', 'off-1'])
    const text = (id: string) => wrapper.get(`[data-test="${id}"]`).text()
    expect(text('servers-chip-not-online')).toBe('Not online (2)')
    expect(text('kpi-card-total')).toBe('All (5)')
    expect(text('servers-chip-online')).toBe('Online (2)')
    expect(text('servers-chip-disabled')).toBe('Disabled (1)')
    expect(wrapper.get('[data-test="kpi-card-total"]').attributes('aria-pressed')).toBe('true')
  })

  it('a chip writes ?filter=, clears status, keeps q and profile', async () => {
    const { wrapper, router } = await mountAt('/servers?status=connected&q=o&profile=p1')
    await wrapper.get('[data-test="servers-chip-online"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ filter: 'online', q: 'o', profile: 'p1' })
    expect(router.currentRoute.value.query.status).toBeUndefined()
  })

  it('All is written explicitly', async () => {
    const { wrapper, router } = await mountAt('/servers')
    await wrapper.get('[data-test="kpi-card-total"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.filter).toBe('all')
  })

  it('bare visit at 390 px defaults to Not online; explicit all and q-only links do not', async () => {
    expect(names((await mountAt('/servers', FLEET, 390)).wrapper)).toEqual(['bad-1', 'rev-1'])
    expect(names((await mountAt('/servers?filter=all', FLEET, 390)).wrapper)).toHaveLength(5)
    expect(names((await mountAt('/servers?q=o', FLEET, 390)).wrapper)).toEqual(['bad-1', 'rev-1', 'ok-1', 'ok-2', 'off-1'].filter(n => n.includes('o')))
  })
})

describe('More filters and legacy status (P2-07, P2-08, P2-12)', () => {
  it('lists Connected, Enabled, Quarantined, Needs review with counts', async () => {
    const { wrapper } = await mountAt('/servers')
    expect(wrapper.get('[data-test="kpi-card-connected"]').text()).toBe('Connected (2)')
    expect(wrapper.get('[data-test="kpi-card-enabled"]').text()).toBe('Enabled (4)')
    expect(wrapper.get('[data-test="kpi-card-quarantined"]').text()).toBe('Quarantined (1)')
    expect(wrapper.get('[data-test="kpi-card-needs_review"]').text()).toBe('Needs review (1)')
  })

  it.each([
    ['connected', ['ok-1', 'ok-2']], ['enabled', ['bad-1', 'rev-1', 'ok-1', 'ok-2']],
    ['quarantined', ['rev-1']], ['needs_review', ['rev-1']],
  ])('?status=%s keeps its meaning', async (status, expected) => {
    const { wrapper } = await mountAt(`/servers?status=${status}`)
    expect(names(wrapper)).toEqual(expected)
    expect(wrapper.get('[data-test="servers-more-filters-toggle"]').text()).toContain('More filters (')
  })

  it('picking an option writes status and drops filter; picking it again returns to All', async () => {
    const { wrapper, router } = await mountAt('/servers?filter=online')
    await wrapper.get('[data-test="kpi-card-quarantined"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ status: 'quarantined' })
    expect(router.currentRoute.value.query.filter).toBeUndefined()
    await wrapper.get('[data-test="kpi-card-quarantined"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.filter).toBe('all')
  })
})

describe('totals, search, actions (P2-02, P2-03, P2-09, P2-11)', () => {
  it('totals line: enabled denominator and available tools of enabled, non-quarantined servers', async () => {
    const { wrapper } = await mountAt('/servers')
    expect(wrapper.get('[data-test="servers-total-count"]').text()).toBe('5 servers, 4 enabled')
    expect(wrapper.get('[data-test="servers-connected-denominator"]').text()).toBe('2 of 4 enabled online')
    expect(wrapper.get('[data-test="kpi-card-total-tools"]').text()).toBe('9 tools available')
  })

  it('search filters by name and writes ?q=', async () => {
    const { wrapper, router } = await mountAt('/servers')
    await wrapper.get('[data-test="servers-search"]').setValue('ok-2')
    await flushPromises()
    expect(names(wrapper)).toEqual(['ok-2'])
    expect(router.currentRoute.value.query.q).toBe('ok-2')
  })

  it('Refresh refetches the list and the activity summary', async () => {
    const { wrapper } = await mountAt('/servers')
    const store = useServersStore()
    const spy = vi.spyOn(store, 'fetchServers').mockResolvedValue(undefined as never)
    fetchSummary.mockClear()
    await wrapper.get('[data-test="servers-refresh"]').trigger('click')
    await flushPromises()
    expect(spy).toHaveBeenCalled()
    expect(fetchSummary).toHaveBeenCalled()
  })

  it('Scan All is gated by enabled scanners and starts the batch scan', async () => {
    const { wrapper } = await mountAt('/servers')
    await wrapper.get('[data-test="servers-scan-all"]').trigger('click')
    await flushPromises()
    expect(scanAll).toHaveBeenCalledTimes(1)
    scanners = false
    expect((await mountAt('/servers')).wrapper.find('[data-test="servers-scan-all"]').exists()).toBe(false)
  })
})

describe('rows (P2-21, P2-22, P2-24)', () => {
  it('shows the last call with its error count from the one activity summary, linking to filtered Activity', async () => {
    const { wrapper } = await mountAt('/servers')
    const row = wrapper.findAll('[data-test="server-row"]')[0]
    expect(row.attributes('data-server')).toBe('bad-1')
    expect(row.get('[data-test="server-card-stats-line"]').text()).toContain('3 errors (24h)')
    expect(fetchSummary).toHaveBeenCalledTimes(1)
  })

  it('renders a table with column headers and the CLI hints panel', async () => {
    const { wrapper } = await mountAt('/servers')
    expect(wrapper.get('[role="table"]').findAll('[role="columnheader"]').map(h => h.text())).toEqual(['Server', 'Status', 'Reason', 'Tools', 'Last call', 'Action', 'More'])
    expect(wrapper.findComponent({ name: 'CollapsibleHintsPanel' }).exists() || wrapper.html().includes('collapsible-hints-panel')).toBe(true)
  })
})

describe('states (P2-14, P2-17, P2-20)', () => {
  it('no match: Clear Search and Show all servers recover', async () => {
    const { wrapper, router } = await mountAt('/servers?q=zzz')
    expect(wrapper.get('[data-test="servers-filter-empty"]').text()).toContain('No servers match your search criteria')
    await wrapper.get('[data-test="servers-filter-empty"] button').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.q).toBeUndefined()
    const second = await mountAt('/servers?filter=disabled', [server('a', 'ready')])
    expect(second.wrapper.get('[data-test="servers-filter-empty"]').text()).toContain('No servers are disabled')
    await second.wrapper.get('[data-test="servers-show-all"]').trigger('click')
    await flushPromises()
    expect(second.router.currentRoute.value.query.filter).toBe('all')
    expect(names(second.wrapper)).toEqual(['a'])
  })

  it('stale list: warning with Retry keeps the rows', async () => {
    const { wrapper } = await mountAt('/servers')
    useServersStore().loading.error = 'boom'
    await flushPromises()
    expect(wrapper.get('[data-test="servers-refresh-error"]').text()).toContain('boom')
    expect(names(wrapper)).toHaveLength(5)
    expect(wrapper.get('[data-test="servers-refresh-error"] button').text()).toBe('Retry')
  })
})
