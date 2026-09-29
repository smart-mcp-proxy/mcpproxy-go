import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import Review from '@/views/Review.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({ default: { getReviewQueue: vi.fn(), listScanHistory: vi.fn() } }))
describe('Review queue (T088)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.getReviewQueue as any).mockResolvedValue({ success: true, data: { count: 2, servers: [{ server: 'alpha', kind: 'tool_review', quarantined: false, pending: 1, changed: 1 }] } })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
  })
  it('lists servers and refreshes after review.changed', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/review', component: Review }, { path: '/review/:server', component: Review }] })
    await router.push('/review'); await router.isReady()
    const wrapper = mount(Review, { global: { plugins: [router] } }); await flushPromises()
    expect(wrapper.get('[data-test="servers-review-row-alpha"]').text()).toContain('1 new')
    expect(wrapper.get('[data-test="servers-review-link-alpha"]').attributes('href')).toContain('/review/alpha')
    window.dispatchEvent(new Event('mcpproxy:review-changed')); await flushPromises()
    expect(api.getReviewQueue).toHaveBeenCalledTimes(2)
  })

  it('filters the queue by the server query without turning it into a detail route', async () => {
    ;(api.getReviewQueue as any).mockResolvedValue({ success: true, data: { count: 2, servers: [
      { server: 'alpha', kind: 'tool_review', quarantined: false, pending: 1, changed: 0 },
      { server: 'bravo', kind: 'tool_review', quarantined: false, pending: 1, changed: 0 },
    ] } })
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/review', component: Review }, { path: '/review/:server', component: Review }] })
    await router.push('/review?server=alpha'); await router.isReady()
    const wrapper = mount(Review, { global: { plugins: [router] } }); await flushPromises()
    expect(wrapper.find('[data-test="servers-review-row-alpha"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="servers-review-row-bravo"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="review-screen"]').exists()).toBe(false)
  })
})
