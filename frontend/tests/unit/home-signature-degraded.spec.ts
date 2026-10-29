import { describe, it, expect, beforeEach, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 101 FR-020: a suspended scan-mode auto-approval must be visible on Home,
// with the plain-language reason and a link to /review. Nothing when healthy.

const bundleRef = vi.hoisted(() => ({ current: null as unknown }))

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const fakeEventSource = {
    onopen: null, onmessage: null, onerror: null,
    addEventListener() {}, removeEventListener() {}, close() {},
  }
  const base: Record<string, unknown> = {
    getAttention: ok({ count: 0, items: [] }),
    getServers: ok({ servers: [{ name: 'notes', enabled: true, connected: true, tool_count: 3 }] }),
    getActivitySummary: ok({ call_count: 5, blocked_count: 1, call_error_count: 2 }),
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

vi.mock('@/composables/useSecurityScannerStatus', async () => {
  const { ref } = await import('vue')
  return {
    refreshSecurityScannerStatus: vi.fn().mockResolvedValue(undefined),
    useSecurityScannerStatus: () => ({
      totalFindings: ref(0),
      totalScans: ref(0),
      loaded: ref(true),
      signatureBundle: ref(bundleRef.current),
    }),
  }
})

import Home from '@/views/Home.vue'

class FakeEventSource {
  close() {}
  addEventListener() {}
  onmessage: ((e: unknown) => void) | null = null
  onerror: ((e: unknown) => void) | null = null
}

async function mountHome() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: Home },
      { path: '/review', name: 'review', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
  router.push('/')
  await router.isReady()
  const wrapper = shallowMount(Home, {
    global: { plugins: [createPinia(), router], stubs: { RouterLink: false } },
  })
  await flushPromises()
  return wrapper
}

describe('Home degraded signature coverage (Spec 101 FR-020)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('shows a role=alert with the reason and a /review link when degraded', async () => {
    bundleRef.current = { runnable_rules: 5, coverage_degraded: true, degraded_reason: 'embedded_below_watermark' }
    const wrapper = await mountHome()
    const alert = wrapper.get('[data-test="home-signature-degraded"]')
    expect(alert.attributes('role')).toBe('alert')
    expect(alert.text().toLowerCase()).toContain('auto-approval is suspended')
    expect(alert.text()).toContain('embedded_below_watermark')
    expect(alert.text().toLowerCase()).toContain('older than a signed bundle')
    expect(wrapper.get('[data-test="home-signature-degraded-link"]').attributes('href')).toBe('/review')
  })

  it('shows nothing when coverage is not degraded', async () => {
    bundleRef.current = { runnable_rules: 5, coverage_degraded: false }
    const wrapper = await mountHome()
    expect(wrapper.find('[data-test="home-signature-degraded"]').exists()).toBe(false)
  })

  it('shows nothing when the overview is unavailable', async () => {
    bundleRef.current = null
    const wrapper = await mountHome()
    expect(wrapper.find('[data-test="home-signature-degraded"]').exists()).toBe(false)
  })
})
