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

  // UX-04: triage controls for a queue dominated by disabled servers.
  describe('triage controls (UX-04)', () => {
    const rows = [
      { server: 'team-05-customer-data', kind: 'server_review', quarantined: true, enabled: false, tools_captured: 12 },
      { server: 'team-06-customer-data', kind: 'server_review', quarantined: true, enabled: false, tools_captured: 12 },
      { server: 'large-review', kind: 'server_review', quarantined: true, enabled: true, tools_captured: 180, since: '2026-10-01T00:00:00Z' },
      { server: 'alpha', kind: 'tool_review', quarantined: false, enabled: true, pending: 3, changed: 1, since: '2026-09-01T00:00:00Z' },
    ]
    async function open(path = '/review', data: any = { count: rows.length, servers: rows }) {
      ;(api.getReviewQueue as any).mockResolvedValue({ success: true, data })
      const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/review', component: Review }, { path: '/review/:server', component: Review }] })
      await router.push(path); await router.isReady()
      const wrapper = mount(Review, { global: { plugins: [router] } }); await flushPromises()
      return { wrapper, router }
    }
    const names = (w: any) => w.findAll('[data-test^="servers-review-row-"]').map((r: any) => r.attributes('data-test').replace('servers-review-row-', ''))

    it('defaults to active blockers, explains the counts, and offers All reviews', async () => {
      const { wrapper } = await open()
      expect(names(wrapper)).toEqual(['alpha', 'large-review'])
      expect(wrapper.get('[data-test="review-scope-text"]').text()).toContain('4 reviews: 2 active blockers, 2 on disabled servers')
      expect(wrapper.get('[data-test="review-view-active"]').text()).toContain('Active blockers (2)')
      expect(wrapper.get('[data-test="review-view-all"]').text()).toContain('All reviews (4)')
      await wrapper.get('[data-test="review-view-all"]').trigger('click')
      expect(names(wrapper)).toEqual(['alpha', 'large-review', 'team-05-customer-data', 'team-06-customer-data'])
    })

    it('marks disabled servers with a Disabled badge and never on enabled ones', async () => {
      const { wrapper } = await open()
      await wrapper.get('[data-test="review-view-all"]').trigger('click')
      expect(wrapper.get('[data-test="servers-review-row-team-05-customer-data"]').text()).toContain('Disabled')
      expect(wrapper.get('[data-test="servers-review-row-large-review"]').text()).not.toContain('Disabled')
    })

    it('falls back to All reviews when no row is an active blocker, and treats a missing enabled as enabled', async () => {
      const onlyDisabled = await open('/review', { count: 1, servers: [rows[0]] })
      expect(names(onlyDisabled.wrapper)).toEqual(['team-05-customer-data'])
      const legacy = await open('/review', { count: 1, servers: [{ server: 'old', kind: 'tool_review', quarantined: false, pending: 1 }] })
      expect(names(legacy.wrapper)).toEqual(['old'])
      expect(legacy.wrapper.get('[data-test="servers-review-row-old"]').text()).not.toContain('Disabled')
    })

    it('searches by server name across the whole queue and keeps an escape when nothing matches', async () => {
      const { wrapper } = await open()
      await wrapper.get('[data-test="review-search"]').setValue('team-05')
      expect(names(wrapper)).toEqual(['team-05-customer-data'])
      await wrapper.get('[data-test="review-search"]').setValue('zzz')
      expect(names(wrapper)).toEqual([])
      expect(wrapper.get('[data-test="review-no-match"]').text()).toContain('No reviews match')
    })

    it('sorts by name, impact and age', async () => {
      const { wrapper } = await open()
      await wrapper.get('[data-test="review-view-all"]').trigger('click')
      await wrapper.get('[data-test="review-sort"]').setValue('impact')
      expect(names(wrapper)[0]).toBe('large-review')
      await wrapper.get('[data-test="review-sort"]').setValue('age')
      expect(names(wrapper).slice(0, 2)).toEqual(['alpha', 'large-review'])
    })

    it('a ?server= deep link to a disabled server still lists it', async () => {
      const { wrapper } = await open('/review?server=team-06-customer-data')
      expect(names(wrapper)).toEqual(['team-06-customer-data'])
    })

    it('never hides the whole queue behind the Active view', async () => {
      const { wrapper } = await open()
      expect(wrapper.find('[data-test="review-view-all"]').exists()).toBe(true)
      expect(wrapper.get('[data-test="review-view-all"]').attributes('aria-pressed')).toBe('false')
      expect(wrapper.get('[data-test="review-view-active"]').attributes('aria-pressed')).toBe('true')
    })
  })
})
