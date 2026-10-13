import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109-i FR-050 / T135: one sidebar structure — Home on its own, then
// CONNECT / PROTECT / MONITOR groups, a Settings/Docs/Feedback/Theme footer.
// The names and order come from navigation/navModel.ts.

const mocks = vi.hoisted(() => ({
  getAttention: vi.fn(),
  getReviewQueue: vi.fn(),
  getGlobalTools: vi.fn(),
  getConfigSecrets: vi.fn(),
  getClients: vi.fn(),
}))

vi.mock('@/services/api', () => {
  const base: Record<string, unknown> = {
    ...mocks,
    getOnboardingState: vi.fn().mockResolvedValue({ success: true, data: { incomplete_tab_count: 0, state: { engaged: true } } }),
    hasAPIKey: vi.fn(() => true),
    onAuthError: vi.fn(() => () => {}),
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = vi.fn().mockResolvedValue({ success: true, data: null })
        return target[prop]
      },
    }),
  }
})

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  useSecurityScannerStatus: () => ({ totalFindings: ref(0), totalScans: ref(0), loaded: ref(true) }),
}))

import SidebarNav from '@/components/SidebarNav.vue'
import { useSystemStore } from '@/stores/system'

const stub = { template: '<div />' }

async function mountSidebar(extraRoutes: Array<{ path: string }> = []) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: stub },
      ...extraRoutes.map((r) => ({ ...r, component: stub })),
      { path: '/:pathMatch(.*)*', component: stub },
    ],
  })
  router.push('/')
  await router.isReady()
  const wrapper = mount(SidebarNav, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

function testIds(wrapper: ReturnType<typeof mount>, prefix: string): string[] {
  return wrapper
    .findAll(`[data-test^="${prefix}"]`)
    .map((el) => el.attributes('data-test')!.slice(prefix.length))
}

describe('sidebar structure (Spec 109-i FR-050)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mocks.getAttention.mockReset().mockResolvedValue({ success: true, data: { count: 0, items: [] } })
    mocks.getReviewQueue.mockReset().mockResolvedValue({ success: true, data: { count: 0, servers: [] } })
    mocks.getGlobalTools.mockReset().mockResolvedValue({ success: true, data: { stats: { total: 0 } } })
    mocks.getConfigSecrets.mockReset().mockResolvedValue({ success: true, data: { total_secrets: 0 } })
    mocks.getClients.mockReset().mockResolvedValue({ success: true, data: { clients: [] } })
  })

  it('renders groups CONNECT, PROTECT, MONITOR in that order', async () => {
    const wrapper = await mountSidebar()
    const groups = wrapper.findAll('[data-test^="sidebar-group-"]')
    expect(groups.map((g) => g.attributes('data-test'))).toEqual([
      'sidebar-group-connect',
      'sidebar-group-protect',
      'sidebar-group-monitor',
    ])
    expect(groups.map((g) => g.text().toUpperCase())).toEqual(['CONNECT', 'PROTECT', 'MONITOR'])
  })

  it('orders items home, clients, servers, tools, review, secrets, activity, usage then the footer', async () => {
    const wrapper = await mountSidebar()
    expect(testIds(wrapper, 'sidebar-item-')).toEqual([
      'home', 'clients', 'servers', 'tools', 'review', 'secrets', 'activity', 'usage',
      'settings', 'docs', 'feedback', 'theme',
    ])
  })

  it('puts the version row below the footer links', async () => {
    const wrapper = await mountSidebar()
    useSystemStore().$patch({ info: { version: 'v1.2.3' } as any })
    await flushPromises()
    const footer = wrapper.get('[data-test="sidebar-footer"]').element
    const version = wrapper.get('[data-testid="sidebar-version-block"]').element
    expect(footer.contains(version)).toBe(true)
    const docs = wrapper.get('[data-test="sidebar-item-docs"]').element
    expect(docs.compareDocumentPosition(version) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('badges: Home = attention count, Clients = live clients, Review queue = review count', async () => {
    const attention = Array.from({ length: 5 }, (_, i) => ({
      id: `sign_in_required:server:s${i}`, kind: 'sign_in_required', rank: 10,
      subject: { type: 'server', id: `s${i}`, name: `s${i}` }, summary: `s${i}`,
      fix: { verb: 'login', label: 'Sign in', target: `/servers/s${i}` }, since: '2026-09-25T06:00:00Z',
    }))
    mocks.getAttention.mockResolvedValue({ success: true, data: { count: 5, items: attention } })
    mocks.getReviewQueue.mockResolvedValue({ success: true, data: { count: 7, servers: [] } })
    mocks.getClients.mockResolvedValue({
      success: true,
      data: { clients: [
        { id: 'a', display_name: 'A', active_sessions: 2 },
        { id: 'b', display_name: 'B', active_sessions: 0 },
        { id: 'c', display_name: 'C', active_sessions: 1 },
      ] },
    })
    const wrapper = await mountSidebar()
    expect(wrapper.get('[data-test="sidebar-home-badge"]').text()).toBe('5')
    expect(wrapper.get('[data-test="sidebar-clients-badge"]').text()).toBe('2')
    expect(wrapper.get('[data-test="sidebar-item-review"]').text()).toContain('7')
  })

  it('explains the Review badge: whole queue versus active blockers (UX-04)', async () => {
    mocks.getReviewQueue.mockResolvedValue({ success: true, data: { count: 3, servers: [
      { server: 'a', kind: 'server_review', quarantined: true, enabled: true },
      { server: 'b', kind: 'server_review', quarantined: true, enabled: false },
      { server: 'c', kind: 'server_review', quarantined: true, enabled: false },
    ] } })
    const wrapper = await mountSidebar()
    const badge = wrapper.get('[data-test="sidebar-review-badge"]')
    expect(badge.text()).toBe('3')
    expect(badge.attributes('title')).toBe('3 reviews: 1 active blocker, 2 on disabled servers.')
  })

  it('keeps the Clients badge live: tool-call activity and a 30 s tick refresh presence', async () => {
    vi.useFakeTimers()
    try {
      const wrapper = await mountSidebar()
      const initial = mocks.getClients.mock.calls.length
      expect(initial).toBeGreaterThan(0)

      window.dispatchEvent(new CustomEvent('mcpproxy:activity-completed'))
      window.dispatchEvent(new CustomEvent('mcpproxy:activity-completed'))
      await vi.advanceTimersByTimeAsync(1600)
      // Debounced: a burst of events is one refresh.
      expect(mocks.getClients.mock.calls.length).toBe(initial + 1)

      await vi.advanceTimersByTimeAsync(30_000)
      expect(mocks.getClients.mock.calls.length).toBeGreaterThanOrEqual(initial + 2)

      wrapper.unmount()
      const after = mocks.getClients.mock.calls.length
      await vi.advanceTimersByTimeAsync(60_000)
      expect(mocks.getClients.mock.calls.length).toBe(after)
    } finally {
      vi.useRealTimers()
    }
  })

  it('hides the Clients badge at zero live clients', async () => {
    const wrapper = await mountSidebar()
    expect(wrapper.find('[data-test="sidebar-clients-badge"]').exists()).toBe(false)
  })

  it('does not carry retired labels or links', async () => {
    const wrapper = await mountSidebar()
    const text = wrapper.get('aside').text()
    for (const gone of ['Dashboard', 'Sessions', 'Security', 'Repositories', 'Agent Tokens', 'Add Server', 'Activity Log', 'Workspace', 'Observability']) {
      expect(text).not.toContain(gone)
    }
    const hrefs = wrapper.findAll('a').map((a) => a.attributes('href') ?? '')
    for (const path of ['/tokens', '/sessions', '/repositories', '/security']) {
      expect(hrefs.filter((h) => h === path || h.startsWith(`${path}?`))).toEqual([])
    }
  })

  it('hides Profiles until a /profiles route exists', async () => {
    const without = await mountSidebar()
    expect(without.find('[data-test="sidebar-item-profiles"]').exists()).toBe(false)

    const withRoute = await mountSidebar([{ path: '/profiles' }])
    const ids = testIds(withRoute, 'sidebar-item-')
    expect(ids.slice(0, 4)).toEqual(['home', 'clients', 'profiles', 'servers'])
  })

  it('labels every link in collapsed mode', async () => {
    const wrapper = await mountSidebar()
    useSystemStore().$patch({ sidebarCollapsed: true })
    await flushPromises()
    const links = wrapper.findAll('[data-test^="sidebar-item-"]').filter((el) => el.element.tagName === 'A')
    expect(links.length).toBeGreaterThanOrEqual(11)
    for (const link of links) {
      expect(link.attributes('aria-label'), link.attributes('data-test')).toBeTruthy()
    }
  })

  it('opens Docs in a new tab with noopener', async () => {
    const wrapper = await mountSidebar()
    const docs = wrapper.get('[data-test="sidebar-item-docs"]')
    expect(docs.attributes('href')).toBe('https://docs.mcpproxy.app')
    expect(docs.attributes('target')).toBe('_blank')
    expect(docs.attributes('rel')).toContain('noopener')
  })
})
