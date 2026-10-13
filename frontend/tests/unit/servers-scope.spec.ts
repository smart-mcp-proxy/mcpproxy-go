import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109-k (activity-scope-filters), T119/T113: Servers.vue wired to
// useScopeQuery's `status` and `q` parameters (url-filter-contract.md: both
// are client-side only on Servers — `GET /servers` takes no query string, so
// they never reach REST, only the rendered list).
//
// Regression this closes: `/review` (router T026a) redirects to
// `/servers?status=needs_review`, but until now Servers.vue never read
// `status` from the URL at all, so that redirect landed on the unfiltered
// list — the whole point of the redirect was silently lost.

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() => ok({ servers: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, total_scans: 0 })),
      scanAll: vi.fn(() => ok({})),
    },
  }
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

async function mountServersAt(path: string, servers: ReturnType<typeof makeServer>[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const { useServersStore } = await import('@/stores/servers')
  const store = useServersStore()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  store.servers = servers as any
  store.loaded = true

  const Servers = (await import('@/views/Servers.vue')).default
  const ServerCard = (await import('@/components/ServerCard.vue')).default
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
  return { wrapper, ServerCard }
}

describe('Servers list — scope filters (Spec 109-k, url-filter-contract.md)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('?status=needs_review shows server-quarantined and tool-pending-review servers only', async () => {
    const { wrapper, ServerCard } = await mountServersAt('/servers?status=needs_review', [
      makeServer('clean'),
      makeServer('server-quarantined', { quarantined: true }),
      makeServer('tool-pending', { quarantine: { pending_count: 2, changed_count: 0, blocked_count: 0 } }),
      makeServer('tool-changed', { quarantine: { pending_count: 0, changed_count: 1, blocked_count: 0 } }),
    ])

    const cards = wrapper.findAllComponents(ServerCard)
    const shown = cards.map(c => (c.props('server') as { name: string }).name).sort()
    expect(shown).toEqual(['server-quarantined', 'tool-changed', 'tool-pending'])
  })

  it('?status=needs_review never sends `status` to GET /servers', async () => {
    const api = (await import('@/services/api')).default
    await mountServersAt('/servers?status=needs_review', [makeServer('a', { quarantined: true })])
    for (const call of (api.getServers as ReturnType<typeof vi.fn>).mock.calls) {
      expect(call.length).toBe(0)
    }
  })

  it('?q=alpha filters the list client-side and prefills the search box', async () => {
    const { wrapper, ServerCard } = await mountServersAt('/servers?q=alpha', [
      makeServer('alpha-server'),
      makeServer('beta-server'),
    ])

    const cards = wrapper.findAllComponents(ServerCard)
    const shown = cards.map(c => (c.props('server') as { name: string }).name)
    expect(shown).toEqual(['alpha-server'])

    const search = wrapper.find('[data-test="servers-search"]')
    expect((search.element as HTMLInputElement).value).toBe('alpha')
  })

  it('an unrecognized `status` value is ignored (falls back to "all")', async () => {
    const { wrapper, ServerCard } = await mountServersAt('/servers?status=bogus', [
      makeServer('a'),
      makeServer('b', { quarantined: true }),
    ])
    const cards = wrapper.findAllComponents(ServerCard)
    expect(cards).toHaveLength(2)
  })
})
