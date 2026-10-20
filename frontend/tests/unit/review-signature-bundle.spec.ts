import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 101 FR-020 / #938: /security redirects to /review, so the Review page is
// where an operator must be able to see the signature bundle state and, when
// coverage is degraded, why scan-mode auto-approval is suspended.

const FULL = 'c0ffee012345' + 'd'.repeat(52)
const bundleRef = vi.hoisted(() => ({ current: null as unknown }))

vi.mock('@/services/api', () => ({
  default: { getReviewQueue: vi.fn(), listScanHistory: vi.fn() },
}))
vi.mock('@/composables/useSecurityScannerStatus', async () => {
  const { ref } = await import('vue')
  return {
    useSecurityScannerStatus: () => ({ signatureBundle: ref(bundleRef.current) }),
    refreshSecurityScannerStatus: vi.fn(),
  }
})

import Review from '@/views/Review.vue'
import api from '@/services/api'

const healthy = {
  source: 'file',
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

async function open() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/review', component: Review }, { path: '/review/:server', component: Review }],
  })
  await router.push('/review')
  await router.isReady()
  const wrapper = mount(Review, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  return wrapper
}

describe('Review page signature bundle (Spec 101 FR-020)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getReviewQueue as any).mockResolvedValue({ success: true, data: { count: 0, servers: [] } })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    bundleRef.current = healthy
  })

  it('renders a folded bundle section with identity and no alert when healthy', async () => {
    const wrapper = await open()
    const details = wrapper.get('[data-test="bundle-details"]')
    expect(details.attributes('open')).toBeUndefined()
    expect(wrapper.get('[data-test="bundle-summary-line"]').text()).toContain(FULL.slice(0, 12))
    const panel = wrapper.get('[data-test="bundle-status-panel"]')
    expect(panel.text()).toContain('42')
    expect(panel.text()).toContain('operator')
    expect(panel.text()).toContain('verified')
    expect(wrapper.find('[data-test="bundle-degraded-alert"]').exists()).toBe(false)
  })

  it('offers the full digest on demand', async () => {
    const wrapper = await open()
    expect(wrapper.find('[data-test="bundle-full-digest"]').exists()).toBe(false)
    await wrapper.get('[data-test="bundle-digest-toggle"]').trigger('click')
    expect(wrapper.get('[data-test="bundle-full-digest"]').text()).toBe(FULL)
  })

  it('raises a role=alert banner with the reason when coverage is degraded', async () => {
    bundleRef.current = { ...healthy, coverage_degraded: true, degraded_reason: 'unsigned_external_active' }
    const wrapper = await open()
    const alert = wrapper.get('[data-test="bundle-degraded-alert"]')
    expect(alert.attributes('role')).toBe('alert')
    expect(alert.text().toLowerCase()).toContain('unsigned')
    expect(alert.text().toLowerCase()).toContain('auto-approval')
  })

  it('renders nothing when the overview is unavailable (scoped session / older daemon)', async () => {
    bundleRef.current = null
    const wrapper = await open()
    expect(wrapper.find('[data-test="signature-bundle-status"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="bundle-degraded-alert"]').exists()).toBe(false)
  })
})
