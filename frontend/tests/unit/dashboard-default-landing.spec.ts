import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Roadmap analytics-dashboard / analytics-default-landing: the analytics
// (Usage) panel of the Dashboard is what the Web UI lands on. `/` and `/usage`
// open the analytics panel, `/overview` stays deep-linkable for the hub
// overview, and a brand-new install (zero upstream servers) gets an
// "add your first server" CTA instead of an empty chart grid.

const serversSpy = vi.hoisted(() =>
  vi.fn().mockResolvedValue({ success: true, data: { servers: [] } })
)

const usageSpy = vi.hoisted(() =>
  vi.fn().mockResolvedValue({
    success: true,
    data: { window: '24h', tokens_saved: 0, tokens_saved_percentage: 0, tools: [], timeline: [] },
  })
)

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const fakeEventSource = {
    onopen: null,
    onmessage: null,
    onerror: null,
    addEventListener() {},
    removeEventListener() {},
    close() {},
  }
  const base: Record<string, unknown> = {
    getServers: serversSpy,
    getActivityUsage: usageSpy,
    createEventSource: vi.fn(() => fakeEventSource),
    hasAPIKey: vi.fn(() => true),
    getAPIKeyPreview: vi.fn(() => 'test…'),
    onAuthError: vi.fn(() => () => {}),
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = ok()
        return target[prop]
      },
    }),
  }
})

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  refreshSecurityScannerStatus: vi.fn().mockResolvedValue(undefined),
  useSecurityScannerStatus: () => ({
    totalFindings: ref(0),
    totalScans: ref(0),
    loaded: ref(true),
  }),
}))

import Dashboard from '@/views/Dashboard.vue'
import appRouter from '@/router'
import { useServersStore } from '@/stores/servers'

class FakeEventSource {
  close() {}
  addEventListener() {}
  onmessage: ((e: unknown) => void) | null = null
  onerror: ((e: unknown) => void) | null = null
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: Dashboard, meta: { dashboardView: 'usage' } },
      { path: '/usage', name: 'usage', component: Dashboard, meta: { dashboardView: 'usage' } },
      { path: '/overview', name: 'dashboard-overview', component: Dashboard, meta: { dashboardView: 'overview' } },
      { path: '/repositories', name: 'repositories', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountDashboard(path = '/') {
  const router = makeRouter()
  router.push(path)
  await router.isReady()
  const wrapper = shallowMount(Dashboard, {
    global: {
      plugins: [createPinia(), router],
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
        // Keep the lazy Usage panel as an inert stub — this suite is about the
        // landing panel and the first-run CTA, not the charts.
        Suspense: false,
        UsageView: { template: '<div data-test="usage-view-stub" />' },
      },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('analytics dashboard as the default landing page', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    usageSpy.mockClear()
    serversSpy.mockClear()
    serversSpy.mockResolvedValue({ success: true, data: { servers: [] } })
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('routes "/" to the Dashboard on its usage panel, with /usage and /overview deep-linkable', () => {
    const root = appRouter.resolve('/')
    expect(root.name).toBe('dashboard')
    expect(root.meta.dashboardView).toBe('usage')

    const usage = appRouter.resolve('/usage')
    expect(usage.meta.dashboardView).toBe('usage')
    expect(usage.matched[0].components?.default).toBe(root.matched[0].components?.default)

    const overview = appRouter.resolve('/overview')
    expect(overview.name).toBe('dashboard-overview')
    expect(overview.meta.dashboardView).toBe('overview')
    expect(overview.matched[0].components?.default).toBe(root.matched[0].components?.default)
  })

  it('shows the usage panel (not the overview) when landing on "/"', async () => {
    serversSpy.mockResolvedValue({
      success: true,
      data: { servers: [{ name: 'srv-a', enabled: true, connected: true }] },
    })
    const { wrapper } = await mountDashboard('/')

    expect(wrapper.find('[data-test="dashboard-usage-panel"]').isVisible()).toBe(true)
    expect(wrapper.find('[data-test="dashboard-overview-panel"]').isVisible()).toBe(false)
    expect(wrapper.find('[data-test="usage-view-stub"]').exists()).toBe(true)
  })

  it('honours a /overview deep link', async () => {
    serversSpy.mockResolvedValue({
      success: true,
      data: { servers: [{ name: 'srv-a', enabled: true, connected: true }] },
    })
    const { wrapper } = await mountDashboard('/overview')

    expect(wrapper.find('[data-test="dashboard-overview-panel"]').isVisible()).toBe(true)
    expect(wrapper.find('[data-test="dashboard-usage-panel"]').isVisible()).toBe(false)
  })

  it('rewrites the URL when the tabs are used, so the panel survives a reload', async () => {
    const { wrapper, router } = await mountDashboard('/')

    await wrapper.find('[data-test="dashboard-tab-overview"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/overview')

    await wrapper.find('[data-test="dashboard-tab-usage"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/usage')
  })

  it('follows the route when it changes underneath the component', async () => {
    const { wrapper, router } = await mountDashboard('/')

    await router.push('/overview')
    await flushPromises()
    expect(wrapper.find('[data-test="dashboard-overview-panel"]').isVisible()).toBe(true)

    await router.push('/')
    await flushPromises()
    expect(wrapper.find('[data-test="dashboard-usage-panel"]').isVisible()).toBe(true)
  })

  it('shows an "add your first server" CTA instead of empty charts on a fresh install', async () => {
    const { wrapper } = await mountDashboard('/')

    const cta = wrapper.find('[data-test="dashboard-usage-first-run"]')
    expect(cta.exists()).toBe(true)
    expect(cta.text()).toContain('Add your first server')
    // The chart panel is not rendered (and no usage aggregate is fetched)
    // while there is nothing to chart.
    expect(wrapper.find('[data-test="usage-view-stub"]').exists()).toBe(false)
  })

  it('offers a one-click escape to the Overview panel from the first-run CTA', async () => {
    const { wrapper, router } = await mountDashboard('/')

    await wrapper.find('[data-test="dashboard-first-run-overview"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="dashboard-overview-panel"]').isVisible()).toBe(true)
    expect(router.currentRoute.value.path).toBe('/overview')
  })

  it('renders the usage panel once at least one server is configured', async () => {
    serversSpy.mockResolvedValue({
      success: true,
      data: { servers: [{ name: 'srv-a', enabled: true, connected: true }] },
    })
    const { wrapper } = await mountDashboard('/')

    expect(wrapper.find('[data-test="dashboard-usage-first-run"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="usage-view-stub"]').exists()).toBe(true)
  })

  it('does not claim "no servers" when the server list request failed', async () => {
    // `fetchServers` swallows failures: it records the error and leaves the
    // (empty) server list alone, so a transport error must not be mistaken for
    // a fresh install and tell a user with servers that they have none.
    serversSpy.mockResolvedValue({ success: false, error: 'boom' })
    const { wrapper } = await mountDashboard('/')

    expect(wrapper.find('[data-test="dashboard-usage-first-run"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="usage-view-stub"]').exists()).toBe(true)
  })

  it('holds the charts back until the server list has arrived', async () => {
    // Never-resolving fetch: the usage panel must show a spinner rather than
    // mounting UsageView (which would fire a request and flash "no usage data
    // yet" before the first-run CTA replaces it).
    serversSpy.mockReturnValue(new Promise(() => {}))
    const { wrapper } = await mountDashboard('/')

    expect(wrapper.find('[data-test="dashboard-usage-pending"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="usage-view-stub"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dashboard-usage-first-run"]').exists()).toBe(false)
  })

  it('honours the last tab clicked when two clicks land before navigation settles', async () => {
    const { wrapper, router } = await mountDashboard('/')

    // No awaits between the clicks: the Overview navigation is still in flight
    // when Usage is clicked, so the second click must not be swallowed by a
    // comparison against the not-yet-updated current route.
    wrapper.find('[data-test="dashboard-tab-overview"]').trigger('click')
    wrapper.find('[data-test="dashboard-tab-usage"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/usage')
    expect(wrapper.find('[data-test="dashboard-usage-panel"]').isVisible()).toBe(true)
    expect(wrapper.find('[data-test="dashboard-overview-panel"]').isVisible()).toBe(false)
  })

  it('ends on the last tab clicked after a burst that revisits the same panel', async () => {
    // Overview → Usage → Overview → Usage in one burst. Guards the end state
    // only; the marker-ownership rule that makes this hold under a slow async
    // router guard is enforced by construction (navSeq in Dashboard.vue), which
    // vue-router's abort ordering cannot be faithfully staged from here.
    const { wrapper, router } = await mountDashboard('/')

    wrapper.find('[data-test="dashboard-tab-overview"]').trigger('click')
    wrapper.find('[data-test="dashboard-tab-usage"]').trigger('click')
    wrapper.find('[data-test="dashboard-tab-overview"]').trigger('click')
    wrapper.find('[data-test="dashboard-tab-usage"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/usage')
    expect(wrapper.find('[data-test="dashboard-usage-panel"]').isVisible()).toBe(true)
  })

  it('fetches the server list exactly once on mount', async () => {
    // Two unsequenced fetches made the first-run gate depend on which response
    // landed last (one could succeed with zero servers while the other failed).
    await mountDashboard('/')
    expect(serversSpy).toHaveBeenCalledTimes(1)
  })

  it('keeps the CTA available when an unrelated fetch reports an error', async () => {
    // `loading.error` is shared: App.vue fetches concurrently on mount and
    // silent background refreshes write the field too (and never clear it on
    // success). A failure that is not ours must not suppress the CTA.
    const { wrapper } = await mountDashboard('/')
    expect(wrapper.find('[data-test="dashboard-usage-first-run"]').exists()).toBe(true)

    const store = useServersStore()
    store.loading.error = 'transient background failure'
    await flushPromises()

    expect(wrapper.find('[data-test="dashboard-usage-first-run"]').exists()).toBe(true)
  })

  it('shows the CTA once a later refresh succeeds after a failed initial fetch', async () => {
    serversSpy.mockResolvedValue({ success: false, error: 'boom' })
    const { wrapper } = await mountDashboard('/')

    // Initial fetch failed: no CTA (we do not know the server count), and the
    // usage panel is shown rather than an endless spinner.
    expect(wrapper.find('[data-test="dashboard-usage-first-run"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dashboard-usage-pending"]').exists()).toBe(false)

    // A background refresh then succeeds and confirms there are no servers.
    serversSpy.mockResolvedValue({ success: true, data: { servers: [] } })
    await useServersStore().fetchServers(true)
    await flushPromises()

    expect(wrapper.find('[data-test="dashboard-usage-first-run"]').exists()).toBe(true)
  })
})
