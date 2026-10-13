import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Audit F15: on a cold start the Security page rendered
// "Scanners Installed 0 · Total Scans 0 · Active Scans 0 · Findings 0" while the
// overview request was still in flight — a security page asserting "0 findings"
// before it has looked. The tiles must skeleton until the first response lands.

let resolveOverview: (value: unknown) => void
let overviewPromise: Promise<unknown>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getSecurityOverview: vi.fn(() => overviewPromise),
      listScanners: vi.fn(() => ok([])),
      listScanHistory: vi.fn(() => ok({ scans: [], total: 0 })),
      getQueueProgress: vi.fn(() => ok({ status: 'idle' })),
      getConfig: vi.fn(() => ok({ docker_isolation: { enabled: false }, security: { deep_scan: { enabled: false } } })),
      getServers: vi.fn(() => ok({ servers: [] })),
      patchConfig: vi.fn(() => ok({})),
      updateConfig: vi.fn(() => ok({})),
    },
  }
})

async function mountSecurity() {
  const Security = (await import('@/views/Security.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/', component: { template: '<div/>' } },
      { path: '/settings', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', component: { template: '<div/>' } },
    ],
  })
  await router.push('/')
  await router.isReady()
  const wrapper = mount(Security, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  overviewPromise = new Promise((resolve) => {
    resolveOverview = resolve
  })
})

describe('Security overview tiles (audit F15)', () => {
  it('skeletons the tiles instead of claiming zeros while loading', async () => {
    const wrapper = await mountSecurity()

    const skeletons = wrapper.findAll('[data-test="overview-stat-skeleton"]')
    expect(skeletons.length).toBe(4)

    const stats = wrapper.get('[data-test="security-overview-stats"]')
    // Titles stay so the row keeps its shape; the numerals must not.
    expect(stats.text()).toContain('Findings')
    expect(stats.text()).not.toContain('0')
  })

  it('renders the real numbers once the overview lands', async () => {
    const wrapper = await mountSecurity()

    resolveOverview({
      success: true,
      data: {
        scanners_installed: 1,
        total_scans: 2,
        active_scans: 0,
        findings_by_severity: { total: 0, critical: 0, high: 0 },
        docker_available: true,
      },
    })
    await flushPromises()
    await flushPromises()

    expect(wrapper.findAll('[data-test="overview-stat-skeleton"]').length).toBe(0)
    const stats = wrapper.get('[data-test="security-overview-stats"]')
    expect(stats.text()).toContain('Scanners Installed')
    expect(stats.text()).toContain('1')
    expect(stats.text()).toContain('2')
  })
})
