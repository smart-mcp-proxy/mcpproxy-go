import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { scanAllTooltip } from '@/views/security/deepScanState'

// "Scan All Servers" was unreachable on exactly the installs that had never
// scanned anything: it only rendered when the overview reported an installed
// scanner (the always-on in-process baseline was not counted, so a fresh
// install reported zero), and it was disabled without Docker — the same false
// gate already fixed for the per-server Scan Now button (spec 088 FR-016).
//
// Every fixture here is that default install: zero scanners, no Docker.

let overviewPayload: Record<string, unknown>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getSecurityOverview: vi.fn(() => ok(overviewPayload)),
      listScanners: vi.fn(() => ok([])),
      listScanHistory: vi.fn(() => ok({ scans: [], total: 0 })),
      getQueueProgress: vi.fn(() => ok({ status: 'idle' })),
      getConfig: vi.fn(() => ok({ docker_isolation: { enabled: false }, security: { deep_scan: { enabled: false } } })),
      getServers: vi.fn(() => ok({ servers: [] })),
      scanAll: vi.fn(() => ok({ status: 'running', total: 1, completed: 0, running: 1 })),
      installScanner: vi.fn(() => ok({})),
      removeScanner: vi.fn(() => ok({})),
      configureScanner: vi.fn(() => ok({})),
      cancelAllScans: vi.fn(() => ok({})),
      patchConfig: vi.fn(() => ok({})),
      updateConfig: vi.fn(() => ok({})),
    },
  }
})

async function mountSecurity() {
  const api = (await import('@/services/api')).default
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
  await flushPromises()
  return { wrapper, api }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  // A fresh install as the backend used to report it: nothing persisted.
  overviewPayload = { scanners_enabled: 0, scanners_installed: 0, docker_available: false }
})

describe('Security page — Scan All is reachable on a default install', () => {
  it('renders the Scan All button with zero reported scanners', async () => {
    const { wrapper } = await mountSecurity()
    expect(wrapper.find('[data-test="scan-all-button"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('leaves it enabled when Docker is unavailable', async () => {
    const { wrapper } = await mountSecurity()
    expect(wrapper.find('[data-test="scan-all-button"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('starts a batch scan from the button on a default install', async () => {
    const { wrapper, api } = await mountSecurity()
    await wrapper.find('[data-test="scan-all-button"]').trigger('click')
    await flushPromises()
    expect(api.scanAll).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('explains Docker as a deep-scan-only requirement, not a scanning blocker', async () => {
    const { wrapper } = await mountSecurity()
    const tip = wrapper.find('[data-test="scan-all-button"]').element.parentElement?.getAttribute('data-tip') ?? ''
    expect(tip).toContain('deep scanners need Docker')
    expect(tip).toContain('baseline')
    expect(tip).not.toContain('Docker is required')

    const alert = wrapper.find('[data-test="docker-unavailable-alert"]')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toContain('baseline scan still runs')
    wrapper.unmount()
  })
})

describe('scanAllTooltip', () => {
  it('names deep scanners — not scanning itself — as what Docker gates', () => {
    expect(scanAllTooltip(false)).toBe('Optional deep scanners need Docker; the offline baseline scan is built in')
  })

  it('describes the baseline scan when Docker is present or unknown', () => {
    expect(scanAllTooltip(true)).toContain('offline baseline scan')
    expect(scanAllTooltip(undefined)).toContain('offline baseline scan')
    expect(scanAllTooltip(null)).toContain('offline baseline scan')
  })
})
