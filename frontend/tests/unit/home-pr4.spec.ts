import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 134 PR 4 (FR-040, FR-041, FR-011 Home half): Home order, the attention
// list's See all, the one-line states, the fleet line that took the usable-tool
// count and routing mode from the header, the shortcut row, the scan summary and
// the collapsed topology. Inventory rows P4-xx.

const h = vi.hoisted(() => ({
  attention: vi.fn(),
  servers: vi.fn(),
  activities: vi.fn(),
  scan: { findings: 0, scans: 3 },
}))

function item(i: number) {
  return {
    id: `sign_in_required:server:s${i}`,
    kind: 'sign_in_required',
    rank: 10 + i,
    subject: { type: 'server', id: `s${i}`, name: `s${i}` },
    summary: `s${i}: needs sign in`,
    fix: { verb: 'login', label: `Fix ${i}`, target: `/servers/s${i}` },
    since: '2026-09-25T06:00:00Z',
  }
}
const items = (n: number) => Array.from({ length: n }, (_, i) => item(i + 1))
const usable = (name: string, tools: number) => ({
  name, enabled: true, connected: true, quarantined: false, tool_count: tools,
  health: { level: 'healthy', admin_state: 'enabled', summary: '', status: 'ready', usable: true, actions: [] },
})
const down = (name: string) => ({
  name, enabled: true, connected: false, quarantined: false, tool_count: 9,
  health: { level: 'unhealthy', admin_state: 'enabled', summary: '', status: 'error', usable: false, actions: [] },
})

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const fake = { onopen: null, onmessage: null, onerror: null, addEventListener() {}, removeEventListener() {}, close() {} }
  const base: Record<string, unknown> = {
    getAttention: h.attention,
    getServers: h.servers,
    getActivities: h.activities,
    getActivitySummary: ok({ call_count: 5, blocked_count: 1, call_error_count: 2 }),
    getSessions: ok({ sessions: [{ id: 's1', client_name: 'my-agent', status: 'active', last_activity: new Date().toISOString() }] }),
    getConnectStatus: ok([{ id: 'cursor', name: 'Cursor', supported: true, exists: true, connected: false }]),
    getTokenStats: ok({ saved_tokens: 900, saved_tokens_percentage: 90, total_server_tool_list_size: 1000, average_query_result_size: 100, per_server_tool_list_sizes: { a: 600, b: 400 }, estimated: true }),
    getOnboardingState: ok({ should_show_wizard: false }),
    createEventSource: vi.fn(() => fake),
    hasAPIKey: vi.fn(() => true),
    getAPIKeyPreview: vi.fn(() => 'test…'),
    onAuthError: vi.fn(() => () => {}),
  }
  return {
    default: new Proxy(base, {
      get(t: Record<string, unknown>, p: string) {
        if (p in t) return t[p]
        t[p] = ok()
        return t[p]
      },
    }),
  }
})

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  refreshSecurityScannerStatus: vi.fn().mockResolvedValue(undefined),
  useSecurityScannerStatus: () => ({
    totalFindings: ref(h.scan.findings),
    totalScans: ref(h.scan.scans),
    loaded: ref(true),
  }),
}))

import Home from '@/views/Home.vue'
import { useAuthStore } from '@/stores/auth'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'

class FakeEventSource {
  close() {}
  addEventListener() {}
  onmessage: ((e: unknown) => void) | null = null
  onerror: ((e: unknown) => void) | null = null
}

async function mountHome(opts: { tenant?: boolean; pinia?: ReturnType<typeof createPinia> } = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: Home },
      { path: '/activity', name: 'activity', component: { template: '<div />' } },
      { path: '/usage', name: 'usage', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
  router.push('/')
  await router.isReady()
  const pinia = opts.pinia ?? createPinia()
  setActivePinia(pinia)
  if (opts.tenant) {
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.user = { id: 'c', email: 'c@e.com', display_name: 'C', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' } as any
    ;(api.hasAPIKey as unknown as ReturnType<typeof vi.fn>).mockReturnValue(false)
  }
  const wrapper = shallowMount(Home, {
    global: { plugins: [pinia, router], stubs: { RouterLink: false, AttentionList: false, UsageSummaryStrip: false, RecentActivity: false } },
  })
  await flushPromises()
  return { wrapper, pinia, router }
}

const T = (id: string) => `[data-test="${id}"]`

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
  ;(api.hasAPIKey as unknown as ReturnType<typeof vi.fn>).mockReturnValue(true)
  ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  h.scan.findings = 0
  h.scan.scans = 3
  h.attention.mockReset()
  h.attention.mockResolvedValue({ success: true, data: { count: 7, items: items(7) } })
  h.servers.mockReset()
  h.servers.mockResolvedValue({ success: true, data: { servers: [usable('a', 5), usable('b', 8), down('c')] } })
  h.activities.mockReset()
  h.activities.mockResolvedValue({
    success: true,
    data: {
      activities: Array.from({ length: 8 }, (_, i) => ({
        id: `act${i}`, type: 'tool_call', status: 'success', server_name: 'srv', tool_name: `tool${i}`,
        timestamp: new Date(Date.now() - i * 60000).toISOString(),
      })),
      total: 8,
    },
  })
})

describe('Home order (FR-040, P4-01..P4-46)', () => {
  it('renders the sections in the specified order', async () => {
    const { wrapper } = await mountHome()
    const html = wrapper.html()
    const order = ['home-attention-list', 'home-usage-strip', 'home-recent-activity', 'home-fleet-line', 'home-shortcuts', 'home-security-scan', 'dashboard-token-savings-section', 'home-topology']
    const at = order.map((id) => html.indexOf(`data-test="${id}"`))
    expect(at.every((n) => n >= 0)).toBe(true)
    expect([...at].sort((a, b) => a - b)).toEqual(at)
  })
})

describe('attention list See all (P4-04..P4-09, SC-002)', () => {
  it('shows five items and reveals the sixth and seventh in place', async () => {
    const { wrapper } = await mountHome()
    expect(wrapper.findAll('[data-test^="attention-item-"]')).toHaveLength(5)
    expect(wrapper.get(T('attention-list-card')).text()).toContain('Needs attention (7)')
    const btn = wrapper.get(T('attention-see-all'))
    expect(btn.attributes('aria-expanded')).toBe('false')
    await btn.trigger('click')
    expect(wrapper.findAll('[data-test^="attention-item-"]')).toHaveLength(7)
    expect(wrapper.get(T('attention-see-all')).attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('a[data-test="attention-fix-sign_in_required:server:s6"]').attributes('href')).toBe('/servers/s6')
    await wrapper.get(T('attention-see-all')).trigger('click')
    expect(wrapper.findAll('[data-test^="attention-item-"]')).toHaveLength(5)
  })

  it('has no See all with five items or fewer', async () => {
    h.attention.mockResolvedValue({ success: true, data: { count: 5, items: items(5) } })
    const { wrapper } = await mountHome()
    expect(wrapper.findAll('[data-test^="attention-item-"]')).toHaveLength(5)
    expect(wrapper.find(T('attention-see-all')).exists()).toBe(false)
  })

  it('says nothing needs attention in one line when loaded and empty', async () => {
    h.attention.mockResolvedValue({ success: true, data: { count: 0, items: [] } })
    const { wrapper } = await mountHome()
    const line = wrapper.get(T('attention-all-clear'))
    expect(line.text()).toBe('Nothing needs attention.')
  })

  it('says it is loading, and says when it failed with a retry', async () => {
    h.attention.mockReturnValue(new Promise(() => {}))
    let m = await mountHome()
    expect(m.wrapper.get(T('attention-loading')).text()).toContain('Checking')
    expect(m.wrapper.find(T('attention-all-clear')).exists()).toBe(false)

    h.attention.mockReset()
    h.attention.mockRejectedValue(new Error('502'))
    m = await mountHome()
    expect(m.wrapper.get(T('attention-failed')).text()).toContain('Could not load')
    h.attention.mockResolvedValue({ success: true, data: { count: 1, items: items(1) } })
    await m.wrapper.get(T('attention-retry')).trigger('click')
    await flushPromises()
    expect(m.wrapper.findAll('[data-test^="attention-item-"]')).toHaveLength(1)
  })
})

describe('usage strip window label (P4-19)', () => {
  it('says the window is the rolling last 24 hours, not today', async () => {
    const { wrapper } = await mountHome()
    const strip = wrapper.get(T('home-usage-strip'))
    expect(strip.text()).toContain('last 24 hours')
    expect(strip.text()).not.toContain('today')
    expect(strip.find(T('usage-strip-view-usage')).exists()).toBe(true)
  })
})

describe('recent activity (P4-21)', () => {
  it('shows five rows and links to every Activity event', async () => {
    const { wrapper } = await mountHome()
    expect(wrapper.findAll(T('recent-activity-row'))).toHaveLength(5)
    // The list above mixes calls, approvals and system events, so the link opens the view that shows all of them.
    expect(wrapper.get('a[data-test="recent-activity-view-all"]').attributes('href')).toBe('/activity?view=all')
  })

  it('is empty, failed and absent for a tenant', async () => {
    h.activities.mockResolvedValue({ success: true, data: { activities: [], total: 0 } })
    let m = await mountHome()
    expect(m.wrapper.get(T('recent-activity-empty')).text()).toContain('No activity yet')
    h.activities.mockRejectedValue(new Error('boom'))
    m = await mountHome()
    expect(m.wrapper.get(T('recent-activity-error')).text()).toContain('boom')
    h.activities.mockClear()
    m = await mountHome({ tenant: true })
    expect(m.wrapper.find(T('home-recent-activity')).exists()).toBe(false)
    expect(h.activities).not.toHaveBeenCalled()
  })
})

describe('fleet line (P4-22, P4-23): usable tools and routing mode moved from the header', () => {
  it('counts tools on usable servers only and names the mode', async () => {
    const { wrapper } = await mountHome()
    expect(wrapper.get(T('home-fleet-tools')).text()).toBe('13')
    expect(wrapper.get(T('home-fleet-line')).text()).toContain('usable tools')
    expect(wrapper.get(T('home-fleet-mode')).text()).toBe('Retrieve')
    expect(wrapper.find(T('home-fleet-pending')).exists()).toBe(false)
  })

  it('names the pending mode in text', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    useSystemStore().routing = { routing_mode: 'retrieve_tools', pending_routing_mode: 'direct', restart_required: true, endpoints: {}, available_modes: [], description: '' } as any
    const { wrapper } = await mountHome({ pinia })
    expect(wrapper.get(T('home-fleet-pending')).text()).toContain('Restart pending: /mcp will serve Direct after the next start')
  })

  it('says it is loading before servers load, and shows nothing for a tenant', async () => {
    h.servers.mockReturnValue(new Promise(() => {}))
    let m = await mountHome()
    expect(m.wrapper.get(T('home-fleet-state')).text()).toBe('Loading servers…')
    expect(m.wrapper.find(T('home-fleet-mode')).exists()).toBe(false)
    m = await mountHome({ tenant: true })
    expect(m.wrapper.find(T('home-fleet-line')).exists()).toBe(false)
    expect(m.wrapper.find(T('home-fleet-state')).exists()).toBe(false)
  })

  it('is also shown on a fresh instance (first run), where the header used to show it', async () => {
    h.attention.mockResolvedValue({ success: true, data: { count: 0, items: [] } })
    h.servers.mockResolvedValue({ success: true, data: { servers: [] } })
    const { wrapper } = await mountHome()
    expect(wrapper.find(T('home-getting-started')).exists()).toBe(true)
    expect(wrapper.get(T('home-fleet-tools')).text()).toBe('0')
  })
})

describe('shortcuts and scan summary (P4-25..P4-30)', () => {
  it('lists the five shortcuts with their destinations', async () => {
    const { wrapper } = await mountHome()
    const href = (id: string) => wrapper.get(T(id)).attributes('href')
    expect(href('home-shortcut-connect')).toBe('/clients')
    expect(href('home-shortcut-import')).toBe('/add-server?tab=import')
    expect(href('home-shortcut-sessions')).toBe('/sessions')
    expect(href('home-shortcut-add-server')).toBe('/add-server')
    expect(href('home-shortcut-catalog')).toBe('/repositories')
    expect(wrapper.get(T('home-shortcuts')).findAll('.btn-primary')).toHaveLength(0)
  })

  it('keeps only Browse catalog for a tenant', async () => {
    const { wrapper } = await mountHome({ tenant: true })
    expect(wrapper.find(T('home-shortcut-catalog')).exists()).toBe(true)
    for (const id of ['connect', 'import', 'sessions', 'add-server']) expect(wrapper.find(T(`home-shortcut-${id}`)).exists()).toBe(false)
  })

  it('summarises the security scan: run first scan, issues, or clean', async () => {
    h.scan.scans = 0
    let m = await mountHome()
    expect(m.wrapper.get(T('home-security-scan')).text()).toContain('Run first scan')
    expect(m.wrapper.get('a[data-test="home-security-scan-link"]').attributes('href')).toBe('/security')
    h.scan.scans = 3
    h.scan.findings = 2
    m = await mountHome()
    expect(m.wrapper.get(T('home-security-scan')).text()).toContain('2 issues')
    h.scan.findings = 0
    m = await mountHome()
    expect(m.wrapper.get(T('home-security-scan')).text()).not.toContain('No issues')
  })
})

describe('token savings section (P4-31..P4-34)', () => {
  it('keeps the chip with its estimate marker and opens the breakdown', async () => {
    const { wrapper } = await mountHome()
    const section = wrapper.get(T('dashboard-token-savings-section'))
    expect(section.find(T('dashboard-token-savings-chip')).text()).toContain('90.0%')
    expect(section.find(T('dashboard-token-savings-estimate')).exists()).toBe(true)
    expect(section.find(T('dashboard-token-savings-details')).exists()).toBe(true)
    await section.get(T('dashboard-token-savings-chip')).trigger('click')
    expect((section.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(true)
  })
})

describe('topology section (P4-36..P4-43)', () => {
  it('is collapsed by default, opens in place with its contents and remembers the state', async () => {
    const m = await mountHome()
    const toggle = m.wrapper.get(T('home-topology-toggle'))
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(m.wrapper.find(T('dashboard-agents-box')).exists()).toBe(false)
    await toggle.trigger('click')
    expect(m.wrapper.get(T('home-topology-toggle')).attributes('aria-expanded')).toBe('true')
    expect(m.wrapper.find(T('dashboard-live-client-my-agent')).exists()).toBe(true)
    expect(m.wrapper.get(T('dashboard-available-clients')).text()).toContain('Cursor')
    expect(m.wrapper.find(T('overview-connected-count')).exists()).toBe(true)
    expect(localStorage.getItem('mcpproxy.home.topology')).toBe('open')

    const again = await mountHome()
    expect(again.wrapper.get(T('home-topology-toggle')).attributes('aria-expanded')).toBe('true')
    await again.wrapper.get(T('home-topology-toggle')).trigger('click')
    expect(localStorage.getItem('mcpproxy.home.topology')).toBe('closed')
  })
})
