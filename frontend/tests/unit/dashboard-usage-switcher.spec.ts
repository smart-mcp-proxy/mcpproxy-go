import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 069 B1 (T016): Dashboard carries a Usage↔Overview switcher. Both panels
// stay in the DOM (SC-006 → rendered with v-show, never v-if) so switching
// preserves their state, and the usage aggregate is re-fetched when the window
// selector (24h/7d/all) changes.
//
// Usage is now the default panel (analytics dashboard as the default landing
// page), so the aggregate is fetched on mount rather than on first activation;
// it stays code-split behind Suspense so the Dashboard shell paints first.

const usageSpy = vi.hoisted(() =>
  vi.fn().mockResolvedValue({
    success: true,
    data: { window: '24h', tokens_saved: 0, tokens_saved_percentage: 0, tools: [], timeline: [] },
  })
)

vi.mock('@/services/api', () => {
  // Null data keeps every guarded Overview block (`v-if="tokenSavingsData"`,
  // etc.) hidden so the un-mocked loaders don't render with undefined fields.
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  // The system store opens an SSE channel on mount via api.createEventSource();
  // hand it an inert EventSource-like object so mount() doesn't crash.
  const fakeEventSource = {
    onopen: null,
    onmessage: null,
    onerror: null,
    addEventListener() {},
    removeEventListener() {},
    close() {},
  }
  const base: Record<string, unknown> = {
    getActivityUsage: usageSpy,
    // One configured server, so the Dashboard renders the real usage panel
    // rather than the zero-servers first-run CTA.
    getServers: ok({ servers: [{ name: 'srv-a', enabled: true, connected: true, tool_count: 1 }] }),
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
// Dashboard imports Usage.vue lazily via defineAsyncComponent so the chart
// bundle stays out of first paint (SC-004). A dynamic import() never settles
// inside flushPromises (microtask-only) + Suspense, so the lazy panel would be
// stuck on its fallback spinner under test. Import the real Usage.vue eagerly
// here and swap it in as a synchronous stub for the async wrapper — this
// exercises the genuine Usage.vue switcher/fetch logic without the async race.
import UsageView from '@/views/Usage.vue'

// jsdom has no EventSource; the system store opens one on mount.
class FakeEventSource {
  close() {}
  addEventListener() {}
  onmessage: ((e: unknown) => void) | null = null
  onerror: ((e: unknown) => void) | null = null
}

// The switcher rewrites the URL, so the component needs a real router.
function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: Dashboard, meta: { dashboardView: 'usage' } },
      { path: '/usage', name: 'usage', component: Dashboard, meta: { dashboardView: 'usage' } },
      { path: '/overview', name: 'dashboard-overview', component: Dashboard, meta: { dashboardView: 'overview' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountDashboard(path = '/') {
  const router = makeRouter()
  router.push(path)
  await router.isReady()
  return shallowMount(Dashboard, {
    global: {
      plugins: [createPinia(), router],
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
        // Un-stub the <Suspense> wrapper that shallowMount would otherwise
        // replace with a stub (which swallows its children), and swap the lazy
        // async UsageView for the eagerly-imported real Usage.vue so the
        // switcher's fetch-on-activation + window-re-fetch logic actually runs.
        // Usage.vue's heavy chart grandchildren (CallHistogram/Bar etc.) stay
        // shallow-stubbed and never reach jsdom's missing canvas.
        Suspense: false,
        UsageView,
      },
    },
  })
}

describe('Dashboard Usage↔Overview switcher', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    usageSpy.mockClear()
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('opens on the Usage panel and fetches the aggregate (24h default)', async () => {
    const wrapper = await mountDashboard()
    await flushPromises()

    const overview = wrapper.find('[data-test="dashboard-overview-panel"]')
    const usage = wrapper.find('[data-test="dashboard-usage-panel"]')
    expect(overview.exists()).toBe(true)
    expect(usage.exists()).toBe(true)
    // Usage visible, Overview hidden — both kept in the DOM (v-show), so
    // neither subtree is torn down when switching (SC-006).
    expect(usage.isVisible()).toBe(true)
    expect(overview.isVisible()).toBe(false)
    expect(usageSpy).toHaveBeenCalledTimes(1)
    expect(usageSpy).toHaveBeenLastCalledWith(expect.objectContaining({ window: '24h' }))
  })

  it('switches to Overview and keeps the Usage panel mounted', async () => {
    const wrapper = await mountDashboard()
    await flushPromises()

    await wrapper.find('[data-test="dashboard-tab-overview"]').trigger('click')
    await flushPromises()

    const overview = wrapper.find('[data-test="dashboard-overview-panel"]')
    const usage = wrapper.find('[data-test="dashboard-usage-panel"]')
    expect(overview.isVisible()).toBe(true)
    // Usage still in the DOM (state preserved) but hidden.
    expect(usage.exists()).toBe(true)
    expect(usage.isVisible()).toBe(false)
  })

  it('re-fetches with the selected window when the window selector changes', async () => {
    const wrapper = await mountDashboard()
    await flushPromises()
    usageSpy.mockClear()

    await wrapper.find('[data-test="usage-window-7d"]').trigger('click')
    await flushPromises()

    expect(usageSpy).toHaveBeenCalledTimes(1)
    expect(usageSpy).toHaveBeenLastCalledWith(expect.objectContaining({ window: '7d' }))
  })

  it('switches back to Usage without re-fetching the aggregate (cached, state preserved)', async () => {
    const wrapper = await mountDashboard()
    await flushPromises()
    await wrapper.find('[data-test="dashboard-tab-overview"]').trigger('click')
    await flushPromises()
    usageSpy.mockClear()

    await wrapper.find('[data-test="dashboard-tab-usage"]').trigger('click')
    await flushPromises()

    const usage = wrapper.find('[data-test="dashboard-usage-panel"]')
    expect(usage.isVisible()).toBe(true)
    expect(usageSpy).not.toHaveBeenCalled()
  })
})
