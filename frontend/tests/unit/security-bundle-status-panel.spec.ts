import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 101 FR-020: the Security page answers "am I current and verified, or
// stuck on last-known-good, and why?". Only a state that needs action (degraded
// coverage) gets an alert; identity is shown with the full digest on demand.

const FULL = 'c0ffee012345' + 'd'.repeat(52)
let bundle: Record<string, unknown>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getSecurityOverview: vi.fn(() =>
        ok({
          scanners_installed: 1,
          total_scans: 0,
          active_scans: 0,
          findings_by_severity: { total: 0, critical: 0, high: 0 },
          docker_available: true,
          signature_bundle: bundle,
        }),
      ),
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
  bundle = {
    source: 'file',
    path: '/opt/tpa/b.json',
    runnable_rules: 12,
    fingerprint: FULL.slice(0, 12),
    full_digest: FULL,
    sequence: '42',
    authority: 'operator',
    trust_epoch: 3,
    signature_verified: true,
    staleness_days: 4,
    coverage_degraded: false,
  }
})

describe('Security bundle status panel (Spec 101)', () => {
  it('shows no alert when coverage is healthy, but shows identity', async () => {
    const wrapper = await mountSecurity()
    expect(wrapper.find('[data-test="bundle-degraded-alert"]').exists()).toBe(false)
    const panel = wrapper.get('[data-test="bundle-status-panel"]')
    expect(panel.text()).toContain('42')
    expect(panel.text()).toContain('operator')
    expect(panel.text()).toContain('verified')
    expect(panel.text()).toContain(FULL.slice(0, 12))
  })

  it('keeps the full digest out of the way until asked, then shows it in full', async () => {
    const wrapper = await mountSecurity()
    expect(wrapper.find('[data-test="bundle-full-digest"]').exists()).toBe(false)
    await wrapper.get('[data-test="bundle-digest-toggle"]').trigger('click')
    expect(wrapper.get('[data-test="bundle-full-digest"]').text()).toBe(FULL)
  })

  it('raises a role=alert banner naming the reason when degraded', async () => {
    bundle = { ...bundle, coverage_degraded: true, degraded_reason: 'unsigned_external_active' }
    const wrapper = await mountSecurity()
    const alert = wrapper.get('[data-test="bundle-degraded-alert"]')
    expect(alert.attributes('role')).toBe('alert')
    expect(alert.text().toLowerCase()).toContain('degraded')
    expect(alert.text().toLowerCase()).toContain('unsigned')
    expect(alert.text().toLowerCase()).toContain('auto-approval')
    expect(alert.text().toLowerCase()).toContain('review')
  })

  it('shows the last rejection with reason, detail and time, without an alert', async () => {
    bundle = {
      ...bundle,
      last_rejection: { reason: 'downgrade', detail: 'sequence 3 below watermark 5', at: '2026-10-19T10:00:00Z' },
    }
    const wrapper = await mountSecurity()
    const rej = wrapper.get('[data-test="bundle-last-rejection"]')
    expect(rej.text().toLowerCase()).toContain('downgrade')
    expect(rej.text()).toContain('sequence 3 below watermark 5')
    expect(rej.text()).toContain('2026')
    expect(wrapper.find('[data-test="bundle-degraded-alert"]').exists()).toBe(false)
  })

  it('renders no panel for an older daemon without a bundle descriptor', async () => {
    bundle = {}
    const wrapper = await mountSecurity()
    expect(wrapper.find('[data-test="bundle-status-panel"]').exists()).toBe(false)
  })
})
