import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

// Spec 069 B1 (T016): Dashboard carries an Overview↔Usage switcher. Overview
// state must survive a switch-back (SC-006 → rendered with v-show, never v-if),
// the Usage aggregate must be fetched lazily on first activation (SC-004, so
// the Overview first paint is never blocked) and re-fetched when the window
// selector (24h/7d/all) changes.

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

function mountDashboard() {
  return shallowMount(Dashboard, {
    global: {
      plugins: [createPinia()],
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

describe('Dashboard Overview↔Usage switcher', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    usageSpy.mockClear()
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('defaults to the Overview tab and does not fetch usage on first paint (SC-004)', async () => {
    const wrapper = mountDashboard()
    await flushPromises()

    const overview = wrapper.find('[data-test="dashboard-overview-panel"]')
    const usage = wrapper.find('[data-test="dashboard-usage-panel"]')
    expect(overview.exists()).toBe(true)
    expect(usage.exists()).toBe(true)
    // Overview visible, Usage hidden — both kept in the DOM (v-show), so the
    // Overview subtree is never torn down (SC-006).
    expect(overview.isVisible()).toBe(true)
    expect(usage.isVisible()).toBe(false)
    // Usage data is not fetched until the tab is opened.
    expect(usageSpy).not.toHaveBeenCalled()
  })

  it('switches to Usage, keeps Overview mounted, and lazily fetches the aggregate', async () => {
    const wrapper = mountDashboard()
    await flushPromises()

    await wrapper.find('[data-test="dashboard-tab-usage"]').trigger('click')
    await flushPromises()

    const overview = wrapper.find('[data-test="dashboard-overview-panel"]')
    const usage = wrapper.find('[data-test="dashboard-usage-panel"]')
    // Overview still in the DOM (state preserved) but hidden.
    expect(overview.exists()).toBe(true)
    expect(overview.isVisible()).toBe(false)
    expect(usage.isVisible()).toBe(true)
    // Default window is 24h on first load.
    expect(usageSpy).toHaveBeenCalledTimes(1)
    expect(usageSpy).toHaveBeenLastCalledWith(expect.objectContaining({ window: '24h' }))
  })

  it('re-fetches with the selected window when the window selector changes', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    await wrapper.find('[data-test="dashboard-tab-usage"]').trigger('click')
    await flushPromises()
    usageSpy.mockClear()

    await wrapper.find('[data-test="usage-window-7d"]').trigger('click')
    await flushPromises()

    expect(usageSpy).toHaveBeenCalledTimes(1)
    expect(usageSpy).toHaveBeenLastCalledWith(expect.objectContaining({ window: '7d' }))
  })

  it('switches back to Overview without re-fetching usage (cached, state preserved)', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    await wrapper.find('[data-test="dashboard-tab-usage"]').trigger('click')
    await flushPromises()
    usageSpy.mockClear()

    await wrapper.find('[data-test="dashboard-tab-overview"]').trigger('click')
    await flushPromises()

    const overview = wrapper.find('[data-test="dashboard-overview-panel"]')
    expect(overview.isVisible()).toBe(true)
    expect(usageSpy).not.toHaveBeenCalled()
  })
})
